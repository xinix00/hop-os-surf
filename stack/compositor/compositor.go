// Package compositor is de software-fallback-compositor van de display-app
// (HopOS docs/gui-ontwerp.md §2/§5): zwevende windows met titelbalken en een
// taskbar, gecomponeerd naar een RGBA-beeld. De HVS/DPU-hardware-targets van
// P4 nemen later dezelfde surfaces over als planes; dit pakket blijft de
// fallback voor boards zonder scanout-compositor (en voor de headless
// screenshot-test).
//
// WM-model (20-07, "met windows :P" — Derek): een app krijgt de maat die hij
// in CREATE vraagt en hóudt die — geen tiling dat alles kleiner maakt bij elk
// nieuw window. Nieuwe windows cascaderen; de titelbalk sleept; klikken
// raist; de taskbar onderin heeft een startknop (de launcher naar voren) en
// een knop per window (klik = naar voren, nog eens = minimaliseren).
// CONFIGURE blijft de wet (de Wayland-les): de WM kent de maat toe — hij is
// alleen zo beleefd om de hint te honoreren zolang hij op het werkvlak past.
// Damage op een verouderde maat wordt stil gedropt.
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
	"time"

	"github.com/xinix00/hop-os-surf/stack/pixel"
)

// Het "HOP Slate"-thema (stack/pixel/theme.go): de chrome gebruikt het
// gedeelde palet — aliassen zodat de tekencode leesbaar blijft.
var (
	colContentPad  = pixel.ColSunk
	colTitleFocus  = pixel.ColAccentD
	colTitleIdle   = pixel.ColRaise
	colBorderFocus = pixel.ColAccent
	colBorderIdle  = pixel.ColLine
	colText        = pixel.ColText
	colTextDim     = pixel.ColDim
	colBar         = pixel.ColPanel // taskbar

	// De stoplichtjes (macOS-kleuren, rechts in de titelbalk): groen =
	// maximaliseer, geel = minimaliseer, rood = sluit (proces killen).
	// Zonder focus dimmen ze naar grijs — óók het macOS-gebaar.
	colLightMax   = color.RGBA{0x28, 0xC8, 0x40, 0xFF}
	colLightMin   = color.RGBA{0xFE, 0xBC, 0x2E, 0xFF}
	colLightClose = color.RGBA{0xFF, 0x5F, 0x57, 0xFF}
	colLightIdle  = pixel.ColLine
)

const margin = 8 // cascade-start en ademruimte langs de randen

// Surface is één window-inhoud: dubbel gebufferd (DAMAGE schrijft back,
// PRESENT flipt naar front — de compositor leest alleen front, dus een traag
// frame kan nooit half zichtbaar worden). De maat is van de WM: die honoreert
// de CREATE-hint (geklemd op het werkvlak) en verandert hem daarna niet meer.
type Surface struct {
	Name string

	mu        sync.Mutex
	w, h      int
	back      []byte // RGBA-bytes, stride = w*4
	front     []byte
	presented bool // er is ooit een PRESENT geweest (anders: leeg vlak tonen)

	// WM-staat; alleen onder Compositor.mu gelezen/geschreven.
	hintW, hintH int             // de CREATE-wens
	win          image.Rectangle // window incl. chrome; leeg = nog niet geplaatst
	screen       image.Rectangle // content-rechthoek op het scherm
	restore      image.Rectangle // win van vóór maximaliseren; leeg = normaal
	minimized    bool            // uit beeld (taskbar-knop, of een dicht menu)
	menu         bool            // CREATE.Role menu: het startmenu (de launcher)
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

// Compositor componeert alle surfaces als zwevende windows naar één RGBA-beeld.
type Compositor struct {
	mu       sync.Mutex
	img      *image.RGBA
	surfaces []*Surface // aanmaakvolgorde — de taskbar
	zstack   []*Surface // stapelvolgorde, achter → voor
	focus    *Surface
	dirty    bool
	gen      uint64
	titleH   int
	taskH    int
	scale    int
	face     pixel.Face // chrome-font (F16 op een echt scherm, F12 klein)
	clockStr string     // taskbar-klok (HH:MM), bijgehouden door clockLoop
	cascade  image.Point

	drag    *Surface    // window aan de muis (titelbalk-sleep)
	dragOff image.Point // muis − win.Min bij het oppakken

	// De resize-sleep (grip rechtsonder): tijdens het slepen beweegt alleen
	// het kader (rszRect) — de app hertekent pas bij het loslaten, één keer.
	// Op een framebuffer is dat het verschil tussen een strak kader en een
	// stotterende storm van CONFIGURE's (Derek 21-07).
	rsz     *Surface
	rszRect image.Rectangle

	onResize func(s *Surface, w, h int)
	onClose  func(s *Surface) // rood stoplichtje: de eigenaar mag hem killen

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
	face := pixel.F12
	if h >= 720 {
		scale = 2 // zelfde regel als de fb-console: echt scherm → groter
		face = pixel.F16
	}
	titleH := face.H + 6
	c := &Compositor{
		img:      image.NewRGBA(image.Rect(0, 0, w, h)),
		dirty:    true,
		scale:    scale,
		face:     face,
		clockStr: time.Now().Format("15:04"),
		titleH:   titleH,
		taskH:    titleH + 8,
		cascade:  image.Pt(margin*2, margin*2),
		frameLog: make(map[uint64][]image.Rectangle),
	}
	go c.clockLoop()
	return c
}

// clockLoop houdt de taskbar-klok bij: één kleine damage per minuutwissel —
// de goedkoopste "dit is een desktop"-vibe die er bestaat.
func (c *Compositor) clockLoop() {
	for range time.Tick(10 * time.Second) {
		now := time.Now().Format("15:04")
		c.mu.Lock()
		if now != c.clockStr {
			c.clockStr = now
			c.dirty = true
			c.addDamageLocked(c.clockRectLocked())
		}
		c.mu.Unlock()
	}
}

// clockRectLocked is het klok-vlakje rechts in de taskbar.
func (c *Compositor) clockRectLocked() image.Rectangle {
	bar := c.taskbarLocked()
	w := pixel.TextWidth(c.face, 1, "00:00") + 12
	return image.Rect(bar.Max.X-w, bar.Min.Y, bar.Max.X, bar.Max.Y)
}

// dropRect is win plus zijn slagschaduw (2px rechts+onder) — de rechthoek
// die vuil wordt wanneer een window verschijnt, verdwijnt of beweegt.
func dropRect(win image.Rectangle) image.Rectangle {
	return image.Rect(win.Min.X, win.Min.Y, win.Max.X+2, win.Max.Y+2)
}

// OnResize registreert de callback die elke WM-maatwijziging meldt (de
// server stuurt er CONFIGURE op uit). Wordt buiten de compositor-lock
// aangeroepen; zetten vóór de eerste Add.
func (c *Compositor) OnResize(f func(s *Surface, w, h int)) { c.onResize = f }

// OnClose registreert de callback voor het rode stoplichtje: sluiten is
// proces killen — de server stuurt CLOSE naar de app en ruimt het window op.
// Wordt buiten de compositor-lock aangeroepen; zetten vóór de eerste Add.
func (c *Compositor) OnClose(f func(s *Surface)) { c.onClose = f }

// lightRectsLocked zijn de drie stoplichtjes in de titelbalk van s, van
// binnen naar buiten: [0] maximaliseer (groen), [1] minimaliseer (geel),
// [2] sluit (rood, buitenste — het Windows-gebaar met de macOS-kleuren).
// Menu's (het startmenu) hebben er geen: een popup sluit je door te klikken.
func (c *Compositor) lightRectsLocked(s *Surface) [3]image.Rectangle {
	d := 8 + 2*(c.scale-1) // 8px klein scherm, 10px groot
	gap := d / 2
	y := s.win.Min.Y + (c.titleH-d)/2
	x := s.win.Max.X - 7 - d
	var r [3]image.Rectangle
	for i := 2; i >= 0; i-- {
		r[i] = image.Rect(x, y, x+d, y+d)
		x -= d + gap
	}
	return r
}

// Size geeft de schermmaat.
func (c *Compositor) Size() (w, h int) {
	b := c.img.Bounds()
	return b.Dx(), b.Dy()
}

// work is het vlak boven de taskbar waar windows leven (onder c.mu).
func (c *Compositor) workLocked() image.Rectangle {
	b := c.img.Bounds()
	return image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Max.Y-c.taskH)
}

// Add voegt een surface toe (nieuwe windows komen bovenop en krijgen focus).
// hintW/hintH is de maatwens van de app — de WM honoreert hem, geklemd op
// het werkvlak. menu (CREATE.Role) maakt dit het startmenu: verborgen tot de
// startknop, geen taskbar-knop, geen focus-kaping bij het verschijnen van de
// app. Roep daarna Relayout aan (ná registratie van de eigenaar) om de maat
// echt toe te kennen.
func (c *Compositor) Add(name string, hintW, hintH int, menu bool) *Surface {
	s := &Surface{Name: name, hintW: hintW, hintH: hintH, menu: menu, minimized: menu}
	c.mu.Lock()
	c.surfaces = append(c.surfaces, s)
	c.zstack = append(c.zstack, s)
	if !menu {
		c.focus = s
	}
	c.dirty = true
	c.addDamageLocked(c.img.Bounds())
	c.mu.Unlock()
	return s
}

// Remove haalt een surface weg (bv. bij een verbroken verbinding); de focus
// schuift naar het bovenste window dat overblijft.
func (c *Compositor) Remove(s *Surface) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.surfaces = without(c.surfaces, s)
	c.zstack = without(c.zstack, s)
	if c.drag == s {
		c.drag = nil
	}
	if c.focus == s {
		c.focus = c.topLocked()
	}
	c.dirty = true
	c.addDamageLocked(c.img.Bounds())
}

func without(list []*Surface, s *Surface) []*Surface {
	for i, cur := range list {
		if cur == s {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// topLocked is het bovenste niet-geminimaliseerde window (nil = geen).
func (c *Compositor) topLocked() *Surface {
	for i := len(c.zstack) - 1; i >= 0; i-- {
		if !c.zstack[i].minimized {
			return c.zstack[i]
		}
	}
	return nil
}

// Relayout plaatst nieuwe windows (cascade, hint-maat geklemd op het
// werkvlak) en meldt maat-toekenningen via OnResize — buiten de lock, want
// de callback schrijft doorgaans naar een verbinding. Bestaande windows
// blijven waar ze staan: dat is het hele punt.
func (c *Compositor) Relayout() {
	type change struct {
		s    *Surface
		w, h int
	}
	var changes []change

	c.mu.Lock()
	work := c.workLocked()
	for _, s := range c.surfaces {
		if !s.win.Empty() {
			continue // al geplaatst: blijft staan
		}
		// Content-maat: de hint, geklemd op wat er past (en nooit 0×0).
		cw := clamp(s.hintW, 8, work.Dx()-2)
		ch := clamp(s.hintH, 8, work.Dy()-c.titleH-1-margin)
		winW, winH := cw+2, ch+c.titleH+1

		var pos image.Point
		if s.menu {
			// Het startmenu klapt boven de startknop uit — vaste plek.
			pos = image.Pt(4, work.Max.Y-winH)
		} else {
			// Cascade: elk nieuw window een titelbalk lager/rechts; past de
			// stap niet meer, dan terug naar de start.
			pos = c.cascade
			if pos.X+winW > work.Max.X || pos.Y+winH > work.Max.Y {
				pos = image.Pt(margin, margin)
				c.cascade = pos
			}
			c.cascade = c.cascade.Add(image.Pt(c.titleH+6, c.titleH+6))
		}

		s.win = image.Rect(pos.X, pos.Y, pos.X+winW, pos.Y+winH)
		s.screen = image.Rect(s.win.Min.X+1, s.win.Min.Y+c.titleH, s.win.Max.X-1, s.win.Max.Y-1)
		if w, h := s.Size(); w != cw || h != ch {
			s.resize(cw, ch)
			changes = append(changes, change{s, cw, ch})
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

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
	if !s.minimized {
		c.dirty = true
		for _, r := range clipped {
			c.addDamageLocked(r.Add(s.screen.Min).Intersect(s.screen))
		}
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

// --- input: de muis is van de WM tot hij van de app is -----------------------

// SurfaceAt is de pure vraag: welke content ligt (bovenop) onder schermpunt
// (x,y), met surface-lokale coördinaten. Geminimaliseerde windows tellen niet.
func (c *Compositor) SurfaceAt(x, y int) (s *Surface, lx, ly int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.contentAtLocked(x, y)
}

func (c *Compositor) contentAtLocked(x, y int) (s *Surface, lx, ly int, ok bool) {
	p := image.Pt(x, y)
	for i := len(c.zstack) - 1; i >= 0; i-- {
		cur := c.zstack[i]
		if cur.minimized {
			continue
		}
		if p.In(cur.screen) {
			return cur, x - cur.screen.Min.X, y - cur.screen.Min.Y, true
		}
		if p.In(cur.win) {
			return nil, 0, 0, false // chrome (titelbalk/rand) vangt de muis
		}
	}
	return nil, 0, 0, false
}

// gripSize is het resize-hoekje rechtsonder (hit-vlak én de streepjes).
const gripSize = 14

// gripRectLocked is het resize-hoekje van s.
func (c *Compositor) gripRectLocked(s *Surface) image.Rectangle {
	return image.Rect(s.win.Max.X-gripSize, s.win.Max.Y-gripSize, s.win.Max.X, s.win.Max.Y)
}

// outlineDamageLocked maakt alleen de vier randstroken van r vuil — het
// kader van een resize-sleep kost de kijker-stream zo bijna niets.
func (c *Compositor) outlineDamageLocked(r image.Rectangle) {
	c.addDamageLocked(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+2))
	c.addDamageLocked(image.Rect(r.Min.X, r.Max.Y-2, r.Max.X, r.Max.Y))
	c.addDamageLocked(image.Rect(r.Min.X, r.Min.Y, r.Min.X+2, r.Max.Y))
	c.addDamageLocked(image.Rect(r.Max.X-2, r.Min.Y, r.Max.X, r.Max.Y))
}

// resizeDragLocked trekt het kader naar de muis: rechtsonder volgt, de
// linkerbovenhoek staat vast, geklemd op een minimum en het werkvlak.
func (c *Compositor) resizeDragLocked(x, y int) {
	s := c.rsz
	work := c.workLocked()
	nx := clamp(x, s.win.Min.X+gripSize*4, work.Max.X)
	ny := clamp(y, s.win.Min.Y+c.titleH+gripSize*2, work.Max.Y)
	nr := image.Rect(s.win.Min.X, s.win.Min.Y, nx, ny)
	if nr == c.rszRect {
		return
	}
	c.outlineDamageLocked(c.rszRect)
	c.rszRect = nr
	c.outlineDamageLocked(nr)
	c.dirty = true
}

// maxToggleLocked wisselt s tussen maximaal (het volle werkvlak) en zijn
// oude plek. Geeft de nieuwe content-maat terug als die veranderde — de
// aanroeper vuurt buiten de lock de OnResize-callback (CONFIGURE).
func (c *Compositor) maxToggleLocked(s *Surface) (w, h int, resized bool) {
	c.addDamageLocked(dropRect(s.win))
	if s.restore.Empty() {
		s.restore = s.win
		s.win = c.workLocked()
	} else {
		s.win = s.restore
		s.restore = image.Rectangle{}
	}
	s.screen = image.Rect(s.win.Min.X+1, s.win.Min.Y+c.titleH, s.win.Max.X-1, s.win.Max.Y-1)
	c.addDamageLocked(dropRect(s.win))
	c.dirty = true
	cw, ch := s.screen.Dx(), s.screen.Dy()
	if ow, oh := s.Size(); ow != cw || oh != ch {
		s.resize(cw, ch)
		return cw, ch, true
	}
	return 0, 0, false
}

// PointerDown verwerkt een muis-down: taskbar-knoppen, stoplichtjes,
// titelbalk (raise + sleep), of content (raise + doorsturen). ok=true
// betekent: dit is voor de app, op surface-lokale coördinaten.
func (c *Compositor) PointerDown(x, y int) (s *Surface, lx, ly int, ok bool) {
	// De hooks (OnClose, OnResize) vuren buiten de lock — LIFO: de unlock-
	// defer staat ná deze en loopt dus eerst.
	var closing, resizing *Surface
	var rw, rh int
	defer func() {
		if resizing != nil && c.onResize != nil {
			c.onResize(resizing, rw, rh)
		}
		if closing != nil && c.onClose != nil {
			c.onClose(closing)
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	p := image.Pt(x, y)

	// Taskbar eerst: die ligt altijd bovenop.
	if p.Y >= c.workLocked().Max.Y {
		if p.In(c.startRectLocked()) {
			c.startToggleLocked()
			return nil, 0, 0, false
		}
		for i, sur := range c.taskItemsLocked() {
			if p.In(c.taskRectLocked(i)) {
				c.taskToggleLocked(sur)
				return nil, 0, 0, false
			}
		}
		return nil, 0, 0, false
	}

	// Een open startmenu sluit bij een klik ernaast (het startmenu-gebaar);
	// de klik zelf gaat daarna gewoon door naar wat eronder ligt.
	if m := c.menuLocked(); m != nil && !m.minimized && !p.In(m.win) {
		m.minimized = true
		if c.focus == m {
			c.focus = c.topLocked()
			if c.focus != nil {
				c.chromeDamageLocked(c.focus)
			}
		}
		c.dirty = true
		c.addDamageLocked(dropRect(m.win)) // alleen waar het menu lag komt iets bloot
	}

	for i := len(c.zstack) - 1; i >= 0; i-- {
		cur := c.zstack[i]
		if cur.minimized || !p.In(cur.win) {
			continue
		}
		c.raiseLocked(cur)
		bar := image.Rect(cur.win.Min.X, cur.win.Min.Y, cur.win.Max.X, cur.win.Min.Y+c.titleH)
		if p.In(bar) {
			if !cur.menu {
				lights := c.lightRectsLocked(cur)
				switch {
				case p.In(lights[2]): // rood: sluiten = proces killen
					closing = cur
					return nil, 0, 0, false
				case p.In(lights[1]): // geel: minimaliseren
					cur.minimized = true
					c.focus = c.topLocked()
					c.addDamageLocked(dropRect(cur.win))
					if c.focus != nil {
						c.chromeDamageLocked(c.focus)
					}
					c.addDamageLocked(c.taskbarLocked())
					c.dirty = true
					return nil, 0, 0, false
				case p.In(lights[0]): // groen: maximaliseren/herstellen
					if w, h, ok := c.maxToggleLocked(cur); ok {
						resizing, rw, rh = cur, w, h
					}
					return nil, 0, 0, false
				}
			}
			c.drag = cur // slepen begint; Move verplaatst, Up laat los
			c.dragOff = p.Sub(cur.win.Min)
			return nil, 0, 0, false
		}
		if p.In(c.gripRectLocked(cur)) && !cur.menu {
			c.rsz = cur // resize-sleep: alleen het kader beweegt, tot de Up
			c.rszRect = cur.win
			return nil, 0, 0, false
		}
		if p.In(cur.screen) {
			return cur, x - cur.screen.Min.X, y - cur.screen.Min.Y, true
		}
		return nil, 0, 0, false // de rand
	}
	return nil, 0, 0, false
}

// PointerMove verwerkt een muisbeweging: een lopende sleep verplaatst het
// window (of trekt het resize-kader), anders is het hover voor de app onder
// de aanwijzer.
func (c *Compositor) PointerMove(x, y int) (s *Surface, lx, ly int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rsz != nil {
		c.resizeDragLocked(x, y)
		return nil, 0, 0, false
	}
	if c.drag != nil {
		c.moveDragLocked(x, y)
		return nil, 0, 0, false
	}
	return c.contentAtLocked(x, y)
}

// PointerUp beëindigt een sleep — een resize-sleep past hier pas écht de
// maat toe (één CONFIGURE, één herteken: de app sliep tijdens het slepen) —
// of levert de button-up bij de app af.
func (c *Compositor) PointerUp(x, y int) (s *Surface, lx, ly int, ok bool) {
	var resizing *Surface
	var rw, rh int
	defer func() {
		if resizing != nil && c.onResize != nil {
			c.onResize(resizing, rw, rh)
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	if sur := c.rsz; sur != nil {
		c.resizeDragLocked(x, y)
		c.addDamageLocked(dropRect(sur.win))
		sur.win = c.rszRect
		sur.screen = image.Rect(sur.win.Min.X+1, sur.win.Min.Y+c.titleH, sur.win.Max.X-1, sur.win.Max.Y-1)
		sur.restore = image.Rectangle{} // een handmatige maat is niet "gemaximaliseerd"
		c.addDamageLocked(dropRect(sur.win))
		c.rsz, c.rszRect = nil, image.Rectangle{}
		c.dirty = true
		cw, ch := sur.screen.Dx(), sur.screen.Dy()
		if ow, oh := sur.Size(); ow != cw || oh != ch {
			sur.resize(cw, ch)
			resizing, rw, rh = sur, cw, ch
		}
		return nil, 0, 0, false
	}
	if c.drag != nil {
		c.moveDragLocked(x, y)
		c.drag = nil
		return nil, 0, 0, false
	}
	return c.contentAtLocked(x, y)
}

// moveDragLocked schuift het gesleepte window naar de muis, geklemd op het
// werkvlak (de titelbalk blijft altijd pakbaar).
func (c *Compositor) moveDragLocked(x, y int) {
	s := c.drag
	work := c.workLocked()
	sz := s.win.Size()
	nx := clamp(x-c.dragOff.X, work.Min.X, work.Max.X-sz.X)
	ny := clamp(y-c.dragOff.Y, work.Min.Y, work.Max.Y-sz.Y)
	d := image.Pt(nx, ny).Sub(s.win.Min)
	if d == (image.Point{}) {
		return
	}
	c.addDamageLocked(dropRect(s.win))
	s.win = s.win.Add(d)
	s.screen = s.screen.Add(d)
	c.addDamageLocked(dropRect(s.win))
	c.dirty = true
}

// raiseLocked brengt s naar voren en geeft hem focus. Damage naar wat er
// écht verandert: al voor én gefocust = niets (elke klik in de voorste app
// kostte anders window+taskbar — de "5kb per klik" in de KVM-stream, 20-07);
// alleen focuswissel = de chrome (titelbalk+rand verkleurt, de inhoud niet);
// echt naar voren = het volle window (er komt inhoud onder vandaan).
func (c *Compositor) raiseLocked(s *Surface) {
	top := len(c.zstack) > 0 && c.zstack[len(c.zstack)-1] == s
	if top && c.focus == s {
		return
	}
	if c.focus != s {
		if c.focus != nil {
			c.chromeDamageLocked(c.focus)
		}
		c.focus = s
	}
	if top {
		c.chromeDamageLocked(s)
	} else {
		c.zstack = append(without(c.zstack, s), s)
		c.addDamageLocked(dropRect(s.win))
	}
	c.addDamageLocked(c.taskbarLocked())
	c.dirty = true
}

// chromeDamageLocked maakt alleen de window-chrome vuil: titelbalk en de
// drie 1px-randen — vier smalle rects in plaats van het hele window in de
// kijker-stream wanneer enkel de focuskleur wisselt.
func (c *Compositor) chromeDamageLocked(s *Surface) {
	w := s.win
	c.addDamageLocked(image.Rect(w.Min.X, w.Min.Y, w.Max.X, w.Min.Y+c.titleH))
	c.addDamageLocked(image.Rect(w.Min.X, w.Min.Y+c.titleH, w.Min.X+1, w.Max.Y))
	c.addDamageLocked(image.Rect(w.Max.X-1, w.Min.Y+c.titleH, w.Max.X, w.Max.Y))
	c.addDamageLocked(image.Rect(w.Min.X, w.Max.Y-1, w.Max.X, w.Max.Y))
}

// taskToggleLocked is de taskbar-knop: naar voren halen — en als hij al
// voor én gefocust is, minimaliseren (het Windows-gebaar).
func (c *Compositor) taskToggleLocked(s *Surface) {
	switch {
	case s.minimized:
		s.minimized = false
		c.raiseLocked(s)
		// raiseLocked ziet hem al bovenop staan (minimize haalt hem niet uit
		// de zstack) — maar hij komt uit het niets terug: heel het window.
		c.addDamageLocked(dropRect(s.win))
	case c.focus == s:
		s.minimized = true
		c.focus = c.topLocked()
		c.addDamageLocked(dropRect(s.win)) // er komt inhoud onder vandaan
		if c.focus != nil {
			c.chromeDamageLocked(c.focus) // de nieuwe voorste verkleurt
		}
		c.addDamageLocked(c.taskbarLocked())
	default:
		c.raiseLocked(s)
	}
	c.dirty = true
}

// startToggleLocked is de startknop: het startmenu ís de app die zich met
// CREATE.Role menu meldde (de launcher) — open/dicht. Zonder menu: niets.
func (c *Compositor) startToggleLocked() {
	if m := c.menuLocked(); m != nil {
		c.taskToggleLocked(m)
	}
}

// menuLocked is het (laatst gemelde) startmenu-surface, nil zonder.
func (c *Compositor) menuLocked() *Surface {
	for i := len(c.surfaces) - 1; i >= 0; i-- {
		if c.surfaces[i].menu {
			return c.surfaces[i]
		}
	}
	return nil
}

// taskItemsLocked zijn de windows mét een taskbar-knop (menu's niet).
func (c *Compositor) taskItemsLocked() []*Surface {
	items := make([]*Surface, 0, len(c.surfaces))
	for _, s := range c.surfaces {
		if !s.menu {
			items = append(items, s)
		}
	}
	return items
}

// Focused geeft het window met toetsenbord-focus (nil zonder windows).
func (c *Compositor) Focused() *Surface {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focus
}

// --- taskbar-geometrie (onder c.mu) ------------------------------------------

func (c *Compositor) taskbarLocked() image.Rectangle {
	b := c.img.Bounds()
	return image.Rect(b.Min.X, b.Max.Y-c.taskH, b.Max.X, b.Max.Y)
}

func (c *Compositor) startRectLocked() image.Rectangle {
	bar := c.taskbarLocked()
	w := pixel.TextWidth(c.face, 1, "hop") + 16
	return image.Rect(bar.Min.X+4, bar.Min.Y+4, bar.Min.X+4+w, bar.Max.Y-4)
}

// taskRectLocked is de knop van taskItems[i]: vaste breedte, naast de start;
// rechts blijft de klok vrij.
func (c *Compositor) taskRectLocked(i int) image.Rectangle {
	bar := c.taskbarLocked()
	x0 := c.startRectLocked().Max.X + 6
	avail := c.clockRectLocked().Min.X - 4 - x0
	w := 120 * c.scale
	if n := len(c.taskItemsLocked()); n > 0 && w*n > avail {
		w = avail / n
	}
	return image.Rect(x0+i*w, bar.Min.Y+4, x0+(i+1)*w-4, bar.Max.Y-4)
}

// --- compose -------------------------------------------------------------------

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
		pixel.DrawText(c.img, margin, margin, c.face, 1, colTextDim, "hopos display: no surfaces")
	}
	return c.gen, true
}

// redrawRegionLocked hertekent één schermregio deterministisch: achtergrond,
// dan de windows van achter naar voor (de stapelvolgorde), dan de taskbar.
// Alle chrome tekent in sub (het clip-uitsnede-beeld): een titelbalk of rand
// die de clip maar half raakt mag daarbuiten níks aanraken — daar kan een
// hoger window al staan dat deze regio-pass niet hertekent (de spook-chrome
// over de gemaximaliseerde browser, Derek 21-07).
func (c *Compositor) redrawRegionLocked(clip image.Rectangle) {
	clip = clip.Intersect(c.img.Bounds())
	if clip.Empty() {
		return
	}
	sub, _ := c.img.SubImage(clip).(*image.RGBA)
	if sub == nil {
		return // lege clip-uitsnede: niets te tekenen
	}
	pixel.VGrad(c.img, clip, c.img.Bounds(), pixel.ColDesk0, pixel.ColDesk1)

	// Wordmark rechtsonder: bijna toon-op-toon, het bureaublad is nooit
	// "gewoon zwart".
	work := c.workLocked()
	wm := "hop//os"
	wmX := work.Max.X - pixel.TextWidth(pixel.F16, 2, wm) - margin*2
	wmY := work.Max.Y - 32 - margin
	pixel.DrawText(sub, wmX, wmY, pixel.F16, 2, pixel.ColLineDim, wm)

	for _, s := range c.zstack {
		if s.minimized || !dropRect(s.win).Overlaps(clip) {
			continue
		}
		// Slagschaduw eerst: verdonkert wat er tot nu toe onder ligt (bg +
		// lagere windows) — deterministisch, want de stapel tekent van achter
		// naar voor.
		pixel.Shade(c.img, image.Rect(s.win.Max.X, s.win.Min.Y+2, s.win.Max.X+2, s.win.Max.Y+2).Intersect(clip))
		pixel.Shade(c.img, image.Rect(s.win.Min.X+2, s.win.Max.Y, s.win.Max.X, s.win.Max.Y+2).Intersect(clip))
		if !s.win.Overlaps(clip) {
			continue
		}
		title, border, ttext, bevel := colTitleIdle, colBorderIdle, colTextDim, pixel.ColBevel
		if s == c.focus {
			title, border, ttext, bevel = colTitleFocus, colBorderFocus, colText, pixel.ColAccentL
		}
		bar := image.Rect(s.win.Min.X, s.win.Min.Y, s.win.Max.X, s.win.Min.Y+c.titleH)
		if bar.Overlaps(clip) {
			// Titelbalk: notched vulling, 1px lichte bovenrand (bevel), de
			// naam (het cluster zichtbaar in de chrome) en de stoplichtjes —
			// alles in sub: buiten de clip kan een hoger window staan.
			pixel.Fill(sub, image.Rect(bar.Min.X+1, bar.Min.Y, bar.Max.X-1, bar.Min.Y+1), title)
			pixel.Fill(sub, image.Rect(bar.Min.X, bar.Min.Y+1, bar.Max.X, bar.Max.Y), title)
			pixel.Fill(sub, image.Rect(bar.Min.X+2, bar.Min.Y+1, bar.Max.X-2, bar.Min.Y+2), bevel)
			nameW := bar.Dx() - 16
			if !s.menu {
				lights := c.lightRectsLocked(s)
				lMax, lMin, lClose := colLightMax, colLightMin, colLightClose
				if s != c.focus {
					lMax, lMin, lClose = colLightIdle, colLightIdle, colLightIdle
				}
				pixel.Disc(sub, lights[0], lMax)
				pixel.Disc(sub, lights[1], lMin)
				pixel.Disc(sub, lights[2], lClose)
				nameW = lights[0].Min.X - s.win.Min.X - 14
			}
			name := s.Name
			if max := nameW / c.face.W; len(name) > max && max > 1 {
				name = name[:max-1] + "."
			}
			pixel.DrawText(sub, s.win.Min.X+8, s.win.Min.Y+(c.titleH-c.face.H)/2+1, c.face, 1, ttext, name)
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

		pixel.OutlineNotched(sub, s.win, border)

		// Het resize-hoekje: drie diagonale streepjes in de randkleur.
		if !s.menu {
			m := s.win.Max
			for _, d := range [3]int{4, 8, 12} {
				for k := 0; k <= d; k++ {
					sub.SetRGBA(m.X-2-k, m.Y-2-(d-k), border)
				}
			}
		}
	}

	// Het resize-kader (rubber band) ligt op alles behalve de taskbar: de
	// enige beweging tijdens een resize-sleep — de app slaapt tot de Up.
	if c.rsz != nil && !c.rszRect.Empty() && c.rszRect.Overlaps(clip) {
		pixel.Outline(sub, c.rszRect, pixel.ColAccentL)
		pixel.Outline(sub, c.rszRect.Inset(1), pixel.ColAccentD)
	}

	if bar := c.taskbarLocked(); bar.Overlaps(clip) {
		c.drawTaskbarLocked()
	}
}

// drawTaskbarLocked tekent de taskbar: startknop, een pil per window en de
// klok. Gefocust = accent, geminimaliseerd = verzonken — één oogopslag.
func (c *Compositor) drawTaskbarLocked() {
	bar := c.taskbarLocked()
	pixel.Fill(c.img, bar, colBar)
	pixel.Fill(c.img, image.Rect(bar.Min.X, bar.Min.Y, bar.Max.X, bar.Min.Y+1), pixel.ColLine)
	pixel.Fill(c.img, image.Rect(bar.Min.X, bar.Min.Y+1, bar.Max.X, bar.Min.Y+2), pixel.ColSunk)

	start := c.startRectLocked()
	pixel.Card(c.img, start, pixel.ColAccentD, pixel.ColAccent)
	pixel.DrawTextCentered(c.img, start, c.face, 1, colText, "hop")

	ty := bar.Min.Y + (c.taskH-c.face.H)/2 + 1
	for i, s := range c.taskItemsLocked() {
		r := c.taskRectLocked(i)
		if r.Dx() <= 8 {
			break // voller dan vol: liever geen knoppen dan onklikbare
		}
		switch {
		case s.minimized:
			pixel.FillNotched(c.img, r, pixel.ColSunk)
			pixel.OutlineNotched(c.img, r, pixel.ColLineDim)
		case s == c.focus:
			pixel.Card(c.img, r, pixel.ColAccentD, pixel.ColAccent)
		default:
			pixel.Card(c.img, r, pixel.ColRaise, pixel.ColLine)
		}
		txt := colText
		if s.minimized {
			txt = colTextDim
		}
		name := s.Name
		if max := (r.Dx() - 12) / c.face.W; len(name) > max && max > 1 {
			name = name[:max-1] + "."
		}
		pixel.DrawText(c.img, r.Min.X+6, ty, c.face, 1, txt, name)
	}

	pixel.DrawText(c.img, c.clockRectLocked().Min.X+6, ty, c.face, 1, colTextDim, c.clockStr)
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
