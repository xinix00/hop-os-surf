// Package compositor is de software-fallback-compositor van de display-app
// (docs/gui-ontwerp.md §2/§5): surfaces in een tiling-grid met titelbalken,
// gecomponeerd naar een RGBA-beeld. De HVS/DPU-hardware-targets van P4 nemen
// later dezelfde surfaces over als planes; dit pakket blijft dan de fallback
// voor boards zonder scanout-compositor (en voor de headless screenshot-test).
//
// Pixelmodel: surfaces bufferen intern in RGBA-bytevolgorde (zoals
// image.RGBA); de draad levert XRGB8888 little-endian (B,G,R,X). De enige
// swap zit in Damage — compose is daarna pure rij-kopie.
package compositor

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"sync"
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
// frame kan nooit half zichtbaar worden).
type Surface struct {
	Name string
	W, H int

	mu        sync.Mutex
	back      []byte // RGBA-bytes, stride = W*4
	front     []byte
	presented bool // er is ooit een PRESENT geweest (anders: leeg vlak tonen)

	// screen is de content-rechthoek van de laatste compose (voor hit-test);
	// alleen onder Compositor.mu gelezen/geschreven.
	screen image.Rectangle
}

// Damage schrijft wire-pixels (XRGB8888) in de back-buffer van de surface.
func (s *Surface) Damage(x, y, w, h int, wire []byte) error {
	if x < 0 || y < 0 || w <= 0 || h <= 0 || x+w > s.W || y+h > s.H {
		return errors.New("compositor: damage outside surface")
	}
	if len(wire) != w*h*4 {
		return errors.New("compositor: damage pixel length mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for row := 0; row < h; row++ {
		src := wire[row*w*4 : (row+1)*w*4]
		dst := s.back[((y+row)*s.W+x)*4:]
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

// Compositor componeert alle surfaces in een grid naar één RGBA-beeld.
type Compositor struct {
	mu       sync.Mutex
	img      *image.RGBA
	surfaces []*Surface
	focus    *Surface
	dirty    bool
	gen      uint64

	curX, curY int
	curOn      bool
}

// New maakt een compositor voor een scherm van w×h pixels.
func New(w, h int) *Compositor {
	c := &Compositor{img: image.NewRGBA(image.Rect(0, 0, w, h)), dirty: true}
	return c
}

// Size geeft de schermmaat.
func (c *Compositor) Size() (w, h int) {
	b := c.img.Bounds()
	return b.Dx(), b.Dy()
}

// Add voegt een surface toe (nieuwe windows krijgen focus) en markeert het
// scherm vuil.
func (c *Compositor) Add(name string, w, h int) *Surface {
	s := &Surface{
		Name:  name,
		W:     w,
		H:     h,
		back:  make([]byte, w*h*4),
		front: make([]byte, w*h*4),
	}
	c.mu.Lock()
	c.surfaces = append(c.surfaces, s)
	c.focus = s
	c.dirty = true
	c.mu.Unlock()
	return s
}

// Remove haalt een surface weg (bv. bij een verbroken verbinding); de focus
// schuift naar het laatst toegevoegde window dat overblijft.
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

	bounds := c.img.Bounds()
	draw.Draw(c.img, bounds, image.NewUniform(colBG), image.Point{}, draw.Src)

	n := len(c.surfaces)
	if n == 0 {
		drawString(c.img, margin, margin, 1, colBorderIdle, "hopos display: no surfaces")
		return c.gen, true
	}

	// Tiling-grid (docs/gui-ontwerp.md §5): kolommen ~ vierkant.
	cols := 1
	for cols*cols < n {
		cols++
	}
	rows := (n + cols - 1) / cols

	scale := 1
	if bounds.Dy() >= 720 {
		scale = 2 // zelfde regel als de fb-console: echt scherm → 2×
	}
	titleH := 8*scale + 6

	cellW := (bounds.Dx() - (cols+1)*margin) / cols
	cellH := (bounds.Dy() - (rows+1)*margin) / rows

	for i, s := range c.surfaces {
		col, row := i%cols, i/cols
		x0 := margin + col*(cellW+margin)
		y0 := margin + row*(cellH+margin)
		win := image.Rect(x0, y0, x0+cellW, y0+cellH)

		title, border := colTitleIdle, colBorderIdle
		if s == c.focus {
			title, border = colTitleFocus, colBorderFocus
		}
		// Titelbalk + naam (het cluster zichtbaar in de chrome: de app zet
		// zijn herkomst in de naam, bv. "clock @ node-b").
		bar := image.Rect(win.Min.X, win.Min.Y, win.Max.X, win.Min.Y+titleH)
		draw.Draw(c.img, bar, image.NewUniform(title), image.Point{}, draw.Src)
		drawString(c.img, win.Min.X+4*scale, win.Min.Y+3, scale, colText, s.Name)

		// Content-vlak: surface op native maat, linksboven verankerd,
		// geclipt op de cel (geen schaling in v1 — dat doet de HVS straks
		// gratis). Ongebruikte content-ruimte is een donkerder vlak.
		content := image.Rect(win.Min.X, win.Min.Y+titleH, win.Max.X, win.Max.Y)
		draw.Draw(c.img, content, image.NewUniform(colContentPad), image.Point{}, draw.Src)
		s.mu.Lock()
		cw, ch := s.W, s.H
		if cw > content.Dx() {
			cw = content.Dx()
		}
		if ch > content.Dy() {
			ch = content.Dy()
		}
		if s.presented {
			for row := 0; row < ch; row++ {
				src := s.front[row*s.W*4 : row*s.W*4+cw*4]
				dstOff := c.img.PixOffset(content.Min.X, content.Min.Y+row)
				copy(c.img.Pix[dstOff:dstOff+cw*4], src)
			}
		}
		s.mu.Unlock()
		s.screen = image.Rect(content.Min.X, content.Min.Y, content.Min.X+cw, content.Min.Y+ch)

		rectOutline(c.img, win, border)
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

// rectOutline tekent een 1-px rand.
func rectOutline(img *image.RGBA, r image.Rectangle, col color.RGBA) {
	for x := r.Min.X; x < r.Max.X; x++ {
		img.SetRGBA(x, r.Min.Y, col)
		img.SetRGBA(x, r.Max.Y-1, col)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		img.SetRGBA(r.Min.X, y, col)
		img.SetRGBA(r.Max.X-1, y, col)
	}
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

// drawString tekent ASCII-tekst met het 8x8-font op pixelpositie (x,y).
func drawString(img *image.RGBA, x, y, scale int, col color.RGBA, s string) {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < 0x20 || ch >= 0x80 {
			ch = '?'
		}
		drawGlyph(img, x+i*8*scale, y, scale, col, ch)
	}
}

func drawGlyph(img *image.RGBA, x, y, scale int, col color.RGBA, ch byte) {
	for gy := 0; gy < 8; gy++ {
		bits := font8x8[ch][gy]
		for gx := 0; gx < 8; gx++ {
			if bits>>gx&1 == 0 {
				continue
			}
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					px, py := x+gx*scale+sx, y+gy*scale+sy
					if image.Pt(px, py).In(img.Bounds()) {
						img.SetRGBA(px, py, col)
					}
				}
			}
		}
	}
}
