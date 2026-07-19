// Package compositor is de software-fallback-compositor van de display-app
// (HopOS docs/gui-ontwerp.md §2/§5): surfaces in een tiling-grid met
// titelbalken, gecomponeerd naar een RGBA-beeld. De HVS/DPU-hardware-targets
// van P4 nemen later dezelfde surfaces over als planes; dit pakket blijft de
// fallback voor boards zonder scanout-compositor (en voor de headless
// screenshot-test).
//
// Maatvoering is WM-gestuurd (de Wayland-les): de app *vraagt* een maat
// (hint in CREATE), maar Relayout bepaalt wat elk window krijgt en meldt
// wijzigingen via OnResize — de server stuurt daar CONFIGURE op uit. Damage
// op een verouderde maat wordt stil gedropt: die app is gewoon nog onderweg
// naar de nieuwe maat.
//
// Pixelmodel: surfaces bufferen intern in RGBA-bytevolgorde (zoals
// image.RGBA); de draad levert XRGB8888 little-endian (B,G,R,X). De enige
// swap zit in Damage — compose is daarna pure rij-kopie.
package compositor

import (
	"errors"
	"image"
	"image/color"
	"sync"

	"github.com/xinix00/hop-os-surf/pixel"
)

// Instrumentenpaneel-kleuren (docs/gui-ontwerp.md §5): vlak, 1-px randen,
// geen gradients. De achtergrond is dezelfde als de fb-console — "beeld doet
// het" is nooit een zwart scherm.
var (
	colBG          = color.RGBA{0x10, 0x18, 0x28, 0xFF}
	colContentPad  = color.RGBA{0x0A, 0x10, 0x1C, 0xFF}
	colTitleFocus  = color.RGBA{0x2D, 0x6C, 0xDF, 0xFF}
	colTitleIdle   = color.RGBA{0x24, 0x30, 0x4A, 0xFF}
	colBorderFocus = color.RGBA{0x6E, 0xA8, 0xFF, 0xFF}
	colBorderIdle  = color.RGBA{0x3A, 0x4A, 0x6A, 0xFF}
	colText        = color.RGBA{0xF0, 0xF4, 0xFF, 0xFF}
)

const margin = 8 // pixels tussen/rond windows

// Surface is één window-inhoud: dubbel gebufferd (DAMAGE schrijft back,
// PRESENT flipt naar front — de compositor leest alleen front, dus een traag
// frame kan nooit half zichtbaar worden). De maat is van de WM: alleen
// resize (via Relayout) verandert hem.
type Surface struct {
	Name string

	mu        sync.Mutex
	w, h      int
	back      []byte // RGBA-bytes, stride = w*4
	front     []byte
	presented bool // er is ooit een PRESENT geweest (anders: leeg vlak tonen)

	// win/screen zijn de window- en content-rechthoek van de laatste
	// Relayout; alleen onder Compositor.mu gelezen/geschreven.
	win    image.Rectangle
	screen image.Rectangle
}

// Size geeft de huidige (WM-)maat van de surface.
func (s *Surface) Size() (w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

// Damage schrijft wire-pixels (XRGB8888) in de back-buffer. Een rechthoek
// die niet (meer) binnen de surface past wordt stil gedropt — dat is een
// frame op een verouderde maat, geen fout (de CONFIGURE is al onderweg).
// Een pixellengte die niet bij de rechthoek past is wél corruptie.
func (s *Surface) Damage(x, y, w, h int, wire []byte) error {
	if w <= 0 || h <= 0 || len(wire) != w*h*4 {
		return errors.New("compositor: damage pixel length mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if x < 0 || y < 0 || x+w > s.w || y+h > s.h {
		return nil // verouderde maat: droppen
	}
	for row := 0; row < h; row++ {
		src := wire[row*w*4 : (row+1)*w*4]
		dst := s.back[((y+row)*s.w+x)*4:]
		for i := 0; i < w; i++ {
			// XRGB little-endian → RGBA: B,G,R,X → R,G,B,A.
			dst[i*4+0] = src[i*4+2]
			dst[i*4+1] = src[i*4+1]
			dst[i*4+2] = src[i*4+0]
			dst[i*4+3] = 0xFF
		}
	}
	return nil
}

// resize legt een nieuwe WM-maat op; de overlap van het oude beeld blijft
// staan (geen zwart gat terwijl de app naar de nieuwe maat onderweg is).
func (s *Surface) resize(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w == s.w && h == s.h {
		return
	}
	back := make([]byte, w*h*4)
	front := make([]byte, w*h*4)
	// Nieuw gebied vooraf vullen met het content-vlak (opaak!): zero-bytes
	// zijn alpha-0 en dat wordt "transparant wit" in de PNG van /screen.png.
	for i := 0; i < w*h; i++ {
		back[i*4+0], back[i*4+1], back[i*4+2], back[i*4+3] = colContentPad.R, colContentPad.G, colContentPad.B, 0xFF
	}
	copy(front, back)
	cw, ch := s.w, s.h
	if w < cw {
		cw = w
	}
	if h < ch {
		ch = h
	}
	for row := 0; row < ch; row++ {
		copy(front[row*w*4:row*w*4+cw*4], s.front[row*s.w*4:row*s.w*4+cw*4])
		copy(back[row*w*4:row*w*4+cw*4], s.back[row*s.w*4:row*s.w*4+cw*4])
	}
	s.w, s.h, s.back, s.front = w, h, back, front
}

// Compositor componeert alle surfaces in een grid naar één RGBA-beeld.
type Compositor struct {
	mu       sync.Mutex
	img      *image.RGBA
	surfaces []*Surface
	focus    *Surface
	dirty    bool
	gen      uint64
	titleH   int
	scale    int

	onResize func(s *Surface, w, h int)

	// Damage-administratie voor kijker-streams (/stream): welke rechthoeken
	// veranderden er per compose-generatie. pending verzamelt tussen twee
	// composes; frameLog bewaart de laatste ~128 generaties zodat een
	// bijlopende kijker alleen de verschillen hoeft te halen.
	pending  []image.Rectangle
	frameLog map[uint64][]image.Rectangle
	minGen   uint64
}

// New maakt een compositor voor een scherm van w×h pixels.
func New(w, h int) *Compositor {
	scale := 1
	if h >= 720 {
		scale = 2 // zelfde regel als de fb-console: echt scherm → 2×
	}
	return &Compositor{
		img:      image.NewRGBA(image.Rect(0, 0, w, h)),
		dirty:    true,
		scale:    scale,
		titleH:   8*scale + 6,
		frameLog: make(map[uint64][]image.Rectangle),
	}
}

// OnResize registreert de callback die elke WM-maatwijziging meldt (de
// server stuurt er CONFIGURE op uit). Wordt buiten de compositor-lock
// aangeroepen; zetten vóór de eerste Add.
func (c *Compositor) OnResize(f func(s *Surface, w, h int)) { c.onResize = f }

// Size geeft de schermmaat.
func (c *Compositor) Size() (w, h int) {
	b := c.img.Bounds()
	return b.Dx(), b.Dy()
}

// Add voegt een surface toe (nieuwe windows krijgen focus). hintW/hintH is
// de wens van de app — v1 negeert hem (het grid beslist); hij bestaat voor
// latere aspect/schaal-keuzes. Roep daarna Relayout aan (ná registratie van
// de eigenaar) om de echte maat toe te kennen.
func (c *Compositor) Add(name string, hintW, hintH int) *Surface {
	_ = hintW
	_ = hintH
	s := &Surface{Name: name}
	c.mu.Lock()
	c.surfaces = append(c.surfaces, s)
	c.focus = s
	c.dirty = true
	c.addDamageLocked(c.img.Bounds())
	c.mu.Unlock()
	return s
}

// Remove haalt een surface weg (bv. bij een verbroken verbinding); de focus
// schuift naar het laatst toegevoegde window dat overblijft. Roep daarna
// Relayout aan om de rest de vrijgekomen ruimte te geven.
func (c *Compositor) Remove(s *Surface) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, cur := range c.surfaces {
		if cur == s {
			c.surfaces = append(c.surfaces[:i], c.surfaces[i+1:]...)
			break
		}
	}
	if c.focus == s {
		c.focus = nil
		if n := len(c.surfaces); n > 0 {
			c.focus = c.surfaces[n-1]
		}
	}
	c.dirty = true
	c.addDamageLocked(c.img.Bounds())
}

// Relayout verdeelt het scherm (tiling-grid, kolommen ~ vierkant), legt elke
// surface zijn content-maat op en meldt wijzigingen via OnResize — buiten de
// lock, want de callback schrijft doorgaans naar een verbinding.
func (c *Compositor) Relayout() {
	type change struct {
		s    *Surface
		w, h int
	}
	var changes []change

	c.mu.Lock()
	if n := len(c.surfaces); n > 0 {
		bounds := c.img.Bounds()
		cols := 1
		for cols*cols < n {
			cols++
		}
		rows := (n + cols - 1) / cols
		cellW := (bounds.Dx() - (cols+1)*margin) / cols
		cellH := (bounds.Dy() - (rows+1)*margin) / rows

		for i, s := range c.surfaces {
			col, row := i%cols, i/cols
			x0 := margin + col*(cellW+margin)
			y0 := margin + row*(cellH+margin)
			win := image.Rect(x0, y0, x0+cellW, y0+cellH)
			content := image.Rect(win.Min.X+1, win.Min.Y+c.titleH, win.Max.X-1, win.Max.Y-1)
			if content.Dx() < 8 || content.Dy() < 8 {
				content.Max = content.Min.Add(image.Pt(8, 8)) // vloer: nooit 0×0
			}
			s.win, s.screen = win, content
			if w, h := s.Size(); w != content.Dx() || h != content.Dy() {
				s.resize(content.Dx(), content.Dy())
				changes = append(changes, change{s, content.Dx(), content.Dy()})
			}
		}
	}
	c.dirty = true
	if len(changes) > 0 {
		c.addDamageLocked(c.img.Bounds())
	}
	f := c.onResize
	c.mu.Unlock()

	if f != nil {
		for _, ch := range changes {
			f(ch.s, ch.w, ch.h)
		}
	}
}

// Present flipt back→front voor deze surface en markeert het scherm vuil.
func (c *Compositor) Present(s *Surface) { c.PresentRects(s, nil) }

// PresentRects flipt alleen de gegeven rechthoeken (surface-lokaal; nil =
// alles) — het app-side verlengstuk van de damage-stream: een hover-wissel
// flipt twee knopjes, geen 1,7MB window.
func (c *Compositor) PresentRects(s *Surface, rects []image.Rectangle) {
	s.mu.Lock()
	sb := image.Rect(0, 0, s.w, s.h)
	if len(rects) == 0 {
		rects = []image.Rectangle{sb}
	}
	clipped := rects[:0]
	for _, r := range rects {
		r = r.Intersect(sb)
		if r.Empty() {
			continue
		}
		w := r.Dx() * 4
		for y := r.Min.Y; y < r.Max.Y; y++ {
			off := (y*s.w + r.Min.X) * 4
			copy(s.front[off:off+w], s.back[off:off+w])
		}
		clipped = append(clipped, r)
	}
	s.presented = true
	s.mu.Unlock()

	c.mu.Lock()
	c.dirty = true
	for _, r := range clipped {
		c.addDamageLocked(r.Add(s.screen.Min).Intersect(s.screen))
	}
	c.mu.Unlock()
}

// addDamageLocked noteert een veranderde schermrechthoek (onder c.mu).
func (c *Compositor) addDamageLocked(r image.Rectangle) {
	r = r.Intersect(c.img.Bounds())
	if !r.Empty() {
		c.pending = append(c.pending, r)
	}
}

// SurfaceAt zoekt de surface onder schermpunt (x,y) en geeft de lokale
// surface-coördinaten terug.
func (c *Compositor) SurfaceAt(x, y int) (s *Surface, lx, ly int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := image.Pt(x, y)
	for _, cur := range c.surfaces {
		if p.In(cur.screen) {
			return cur, x - cur.screen.Min.X, y - cur.screen.Min.Y, true
		}
	}
	return nil, 0, 0, false
}

// ClickAt zet de focus op het window onder (x,y) — de klik zelf routeert de
// server daarna naar diezelfde surface.
func (c *Compositor) ClickAt(x, y int) (s *Surface, lx, ly int, ok bool) {
	s, lx, ly, ok = c.SurfaceAt(x, y)
	if ok {
		c.mu.Lock()
		if c.focus != s {
			if c.focus != nil {
				c.addDamageLocked(c.focus.win)
			}
			c.focus = s
			c.addDamageLocked(s.win)
			c.dirty = true
		}
		c.mu.Unlock()
	}
	return s, lx, ly, ok
}

// Focused geeft het window met toetsenbord-focus (nil zonder windows).
func (c *Compositor) Focused() *Surface {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focus
}

// Compose hertekent het scherm als er iets veranderd is en geeft het
// generatienummer terug (verhoogt alleen bij een echte hertekening — de
// PNG-cache van de server hangt eraan).
func (c *Compositor) Compose() (gen uint64, changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.composeLocked()
}

// RenderTo componeert (indien vuil) en geeft f het interne beeld, onder de
// lock — voor blit-targets zoals de firmware-framebuffer die geen 3,7MB-kopie
// per tik willen. f draait alleen bij een echte hertekening en mag img niet
// bewaren.
func (c *Compositor) RenderTo(f func(img *image.RGBA)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, changed := c.composeLocked(); changed {
		f(c.img)
	}
}

func (c *Compositor) composeLocked() (gen uint64, changed bool) {
	if !c.dirty {
		return c.gen, false
	}
	c.dirty = false
	c.gen++
	clips := c.pending
	c.frameLog[c.gen] = clips
	c.pending = nil
	if c.minGen == 0 {
		c.minGen = c.gen
	}
	for c.gen-c.minGen > 128 {
		delete(c.frameLog, c.minGen)
		c.minGen++
	}

	// Partieel waar het kan (de klap op een geëmuleerde/kleine core: een
	// hover-wissel hertekent twee knopjes, niet 3,7MB scherm), volledig bij
	// de eerste keer of wanneer een clip het hele scherm dekt.
	full := c.gen == 1 || len(clips) == 0
	for _, r := range clips {
		if r == c.img.Bounds() {
			full = true
			break
		}
	}
	if full {
		clips = []image.Rectangle{c.img.Bounds()}
	}
	for _, clip := range clips {
		c.redrawRegionLocked(clip)
	}
	if len(c.surfaces) == 0 {
		pixel.DrawString(c.img, margin, margin, 1, colBorderIdle, "hopos display: no surfaces")
	}
	return c.gen, true
}

// redrawRegionLocked hertekent één schermregio deterministisch: achtergrond,
// dan van elk snijdend window de geraakte delen. Titelbalk en rand worden
// bij een snijding integraal hertekend (goedkoop en scheelt clip-logica in
// de tekst-glyphs — dezelfde pixels opnieuw schrijven is onschadelijk).
func (c *Compositor) redrawRegionLocked(clip image.Rectangle) {
	clip = clip.Intersect(c.img.Bounds())
	if clip.Empty() {
		return
	}
	pixel.Fill(c.img, clip, colBG)

	for _, s := range c.surfaces {
		if !s.win.Overlaps(clip) {
			continue
		}
		title, border := colTitleIdle, colBorderIdle
		if s == c.focus {
			title, border = colTitleFocus, colBorderFocus
		}
		bar := image.Rect(s.win.Min.X, s.win.Min.Y, s.win.Max.X, s.win.Min.Y+c.titleH)
		if bar.Overlaps(clip) {
			// Titelbalk + naam (het cluster zichtbaar in de chrome).
			pixel.Fill(c.img, bar, title)
			pixel.DrawString(c.img, s.win.Min.X+4*c.scale, s.win.Min.Y+3, c.scale, colText, s.Name)
		}

		if content := s.screen.Intersect(clip); !content.Empty() {
			s.mu.Lock()
			if !s.presented {
				pixel.Fill(c.img, content, colContentPad)
			} else {
				// Surface-lokale bron van dit schermdeel; defensief clippen
				// op de surfacemaat (resize-overgang).
				lx := content.Min.X - s.screen.Min.X
				ly := content.Min.Y - s.screen.Min.Y
				cw, ch := content.Dx(), content.Dy()
				if lx+cw > s.w {
					cw = s.w - lx
				}
				if ly+ch > s.h {
					ch = s.h - ly
				}
				for row := 0; row < ch; row++ {
					src := s.front[((ly+row)*s.w+lx)*4:]
					dstOff := c.img.PixOffset(content.Min.X, content.Min.Y+row)
					copy(c.img.Pix[dstOff:dstOff+cw*4], src[:cw*4])
				}
			}
			s.mu.Unlock()
		}

		pixel.Outline(c.img, s.win, border)
	}
}

// Snapshot geeft een kopie van het gecomponeerde beeld (voor de PNG-encoder:
// die mag niet lezen terwijl een volgende Compose tekent) plus de generatie.
func (c *Compositor) Snapshot() (*image.RGBA, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := image.NewRGBA(c.img.Bounds())
	copy(cp.Pix, c.img.Pix)
	return cp, c.gen
}

// De muiscursor wordt hier bewust NIET gecomponeerd (besluit Derek 19-07):
// de browser-KVM heeft z'n eigen cursor gratis, en op een echt scherm wordt
// het straks een eigen HVS-plane (docs/gui-ontwerp.md §5) — nooit pixels in
// de compositie. Bijvangst: muisbewegingen maken het scherm niet vuil.

// FrameSince componeert (indien nodig) en pakt alles wat er sinds generatie
// since veranderde in één wire-frame voor een kijker-stream (/stream):
//
//	u32 payloadLen | u16 nRects | nRects × (x,y,w,h u16) | RGBA-pixels aaneen
//
// (little-endian; pixels rij-op-rij per rect, zonder stride-opvulling — en
// RGBA is exact wat een browser-ImageData wil hebben, nul conversie). Een
// kijker die te ver achterloopt (of since 0) krijgt het volledige scherm.
// Geen wijzigingen → (nil, since).
func (c *Compositor) FrameSince(since uint64) (frame []byte, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.composeLocked()
	if c.gen == since {
		return nil, since
	}

	bounds := c.img.Bounds()
	var rects []image.Rectangle
	if since == 0 || since < c.minGen-1 {
		rects = []image.Rectangle{bounds}
	} else {
		for g := since + 1; g <= c.gen; g++ {
			rects = append(rects, c.frameLog[g]...)
		}
		// Veel of grote rects: één volledig frame is dan kleiner én simpeler.
		if len(rects) > 16 {
			rects = []image.Rectangle{bounds}
		}
	}

	size := 4 + 2
	for _, r := range rects {
		size += 8 + r.Dx()*r.Dy()*4
	}
	frame = make([]byte, size)
	le := func(off int, v uint32, n int) {
		for i := 0; i < n; i++ {
			frame[off+i] = byte(v >> (8 * i))
		}
	}
	le(0, uint32(size-4), 4)
	le(4, uint32(len(rects)), 2)
	off := 6
	for _, r := range rects {
		le(off, uint32(r.Min.X), 2)
		le(off+2, uint32(r.Min.Y), 2)
		le(off+4, uint32(r.Dx()), 2)
		le(off+6, uint32(r.Dy()), 2)
		off += 8
	}
	for _, r := range rects {
		w := r.Dx() * 4
		for y := r.Min.Y; y < r.Max.Y; y++ {
			src := c.img.PixOffset(r.Min.X, y)
			copy(frame[off:off+w], c.img.Pix[src:src+w])
			off += w
		}
	}
	return frame, c.gen
}
