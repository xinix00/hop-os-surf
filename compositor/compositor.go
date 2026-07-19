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
	colCursor      = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	colCursorRim   = color.RGBA{0x00, 0x00, 0x00, 0xFF}
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

	curX, curY int
	curOn      bool
}

// New maakt een compositor voor een scherm van w×h pixels.
func New(w, h int) *Compositor {
	scale := 1
	if h >= 720 {
		scale = 2 // zelfde regel als de fb-console: echt scherm → 2×
	}
	return &Compositor{
		img:    image.NewRGBA(image.Rect(0, 0, w, h)),
		dirty:  true,
		scale:  scale,
		titleH: 8*scale + 6,
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
	f := c.onResize
	c.mu.Unlock()

	if f != nil {
		for _, ch := range changes {
			f(ch.s, ch.w, ch.h)
		}
	}
}

// Present flipt back→front voor deze surface en markeert het scherm vuil.
func (c *Compositor) Present(s *Surface) {
	s.mu.Lock()
	copy(s.front, s.back)
	s.presented = true
	s.mu.Unlock()
	c.mu.Lock()
	c.dirty = true
	c.mu.Unlock()
}

// SetCursor beweegt de muiscursor (schermcoördinaten).
func (c *Compositor) SetCursor(x, y int) {
	c.mu.Lock()
	if !c.curOn || c.curX != x || c.curY != y {
		c.curOn, c.curX, c.curY = true, x, y
		c.dirty = true
	}
	c.mu.Unlock()
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
			c.focus = s
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

	pixel.Fill(c.img, c.img.Bounds(), colBG)
	if len(c.surfaces) == 0 {
		pixel.DrawString(c.img, margin, margin, 1, colBorderIdle, "hopos display: no surfaces")
		return c.gen, true
	}

	for _, s := range c.surfaces {
		title, border := colTitleIdle, colBorderIdle
		if s == c.focus {
			title, border = colTitleFocus, colBorderFocus
		}
		// Titelbalk + naam (het cluster zichtbaar in de chrome: de app zet
		// zijn herkomst in de naam, bv. "clock @ node-b").
		bar := image.Rect(s.win.Min.X, s.win.Min.Y, s.win.Max.X, s.win.Min.Y+c.titleH)
		pixel.Fill(c.img, bar, title)
		pixel.DrawString(c.img, s.win.Min.X+4*c.scale, s.win.Min.Y+3, c.scale, colText, s.Name)

		// Content: de surface heeft exact de content-maat (WM-gestuurd);
		// tijdens een resize-overgang clippen we defensief.
		s.mu.Lock()
		if !s.presented {
			pixel.Fill(c.img, s.screen, colContentPad)
		} else {
			cw, ch := s.w, s.h
			if cw > s.screen.Dx() {
				cw = s.screen.Dx()
			}
			if ch > s.screen.Dy() {
				ch = s.screen.Dy()
			}
			for row := 0; row < ch; row++ {
				src := s.front[row*s.w*4 : row*s.w*4+cw*4]
				dstOff := c.img.PixOffset(s.screen.Min.X, s.screen.Min.Y+row)
				copy(c.img.Pix[dstOff:dstOff+cw*4], src)
			}
		}
		s.mu.Unlock()

		pixel.Outline(c.img, s.win, border)
	}

	if c.curOn {
		drawCursor(c.img, c.curX, c.curY)
	}
	return c.gen, true
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

// drawCursor tekent een crosshair-cursor met donker randje (zichtbaar op
// licht én donker); de HVS maakt hier in P4 een hardware-plane van.
func drawCursor(img *image.RGBA, x, y int) {
	for d := -5; d <= 5; d++ {
		for _, p := range [][2]int{{x + d, y - 1}, {x + d, y + 1}, {x - 1, y + d}, {x + 1, y + d}} {
			if image.Pt(p[0], p[1]).In(img.Bounds()) {
				img.SetRGBA(p[0], p[1], colCursorRim)
			}
		}
	}
	for d := -4; d <= 4; d++ {
		if image.Pt(x+d, y).In(img.Bounds()) {
			img.SetRGBA(x+d, y, colCursor)
		}
		if image.Pt(x, y+d).In(img.Bounds()) {
			img.SetRGBA(x, y+d, colCursor)
		}
	}
}
