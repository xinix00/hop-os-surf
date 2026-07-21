package compositor

import (
	"bytes"
	"image"
	"testing"
)

// wireFill maakt w×h wire-pixels (XRGB8888: B,G,R,X) in één kleur.
func wireFill(w, h int, r, g, b byte) []byte {
	pix := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		pix[i*4+0] = b
		pix[i*4+1] = g
		pix[i*4+2] = r
		pix[i*4+3] = 0
	}
	return pix
}

// fillPresent vult een surface volledig op zijn huidige WM-maat.
func fillPresent(c *Compositor, s *Surface, r, g, b byte) (w, h int) {
	w, h = s.Size()
	if err := s.Damage(0, 0, w, h, wireFill(w, h, r, g, b)); err != nil {
		panic(err)
	}
	c.Present(s)
	return w, h
}

// TestHintMaat: de kern van de zwevende WM (20-07) — een app krijgt de maat
// die hij vraagt en hóudt die; alleen wat niet past wordt geklemd.
func TestHintMaat(t *testing.T) {
	c := New(320, 200)
	var resizes []string
	c.OnResize(func(s *Surface, w, h int) {
		resizes = append(resizes, s.Name)
	})

	s1 := c.Add("one", 100, 80, false)
	c.Relayout()
	if w, h := s1.Size(); w != 100 || h != 80 {
		t.Fatalf("hint hoort gehonoreerd: %dx%d, want 100x80", w, h)
	}
	if len(resizes) != 1 || resizes[0] != "one" {
		t.Fatalf("resize-callbacks: %v", resizes)
	}

	// Tweede window erbij: het eerste blijft exact even groot (geen tiling
	// dat alles kleiner maakt), het tweede cascadeert een stukje verderop.
	resizes = nil
	s2 := c.Add("two", 100, 80, false)
	c.Relayout()
	if len(resizes) != 1 || resizes[0] != "two" {
		t.Fatalf("alleen het nieuwe window krijgt een maat: %v", resizes)
	}
	if w, h := s1.Size(); w != 100 || h != 80 {
		t.Fatalf("s1 hoort onaangeroerd: %dx%d", w, h)
	}
	if s2.win.Min == s1.win.Min {
		t.Fatal("cascade: het tweede window hoort verschoven te staan")
	}

	// Te groot voor het werkvlak: klemmen (en dat is de CONFIGURE-maat).
	s3 := c.Add("big", 9999, 9999, false)
	c.Relayout()
	if w, h := s3.Size(); w >= 320 || h >= 200-c.taskH {
		t.Fatalf("een reuze-hint hoort geklemd: %dx%d", w, h)
	}

	// Idempotent: nog een Relayout zonder wijzigingen → geen callbacks.
	resizes = nil
	c.Relayout()
	if len(resizes) != 0 {
		t.Fatalf("no-op relayout hoort geen callbacks te vuren: %v", resizes)
	}
}

func TestComposeAndHitTest(t *testing.T) {
	c := New(320, 200)
	s1 := c.Add("one", 100, 80, false)
	s2 := c.Add("two", 100, 80, false) // laatst toegevoegd → focus, bovenop
	c.Relayout()

	if c.Focused() != s2 {
		t.Fatal("newest surface must have focus")
	}

	fillPresent(c, s1, 0xEE, 0x10, 0x10)
	// s2: wel damage, geen present — mag niet zichtbaar zijn.
	w2, h2 := s2.Size()
	if err := s2.Damage(0, 0, w2, h2, wireFill(w2, h2, 0x10, 0xEE, 0x10)); err != nil {
		t.Fatal(err)
	}

	gen1, changed := c.Compose()
	if !changed || gen1 == 0 {
		t.Fatalf("first compose must draw (gen=%d changed=%v)", gen1, changed)
	}
	if gen2, changed := c.Compose(); changed || gen2 != gen1 {
		t.Fatalf("idle compose must be a no-op (gen %d→%d)", gen1, gen2)
	}

	img, _ := c.Snapshot()
	if p := img.RGBAAt(s1.screen.Min.X+2, s1.screen.Min.Y+2); p.R != 0xEE {
		t.Fatalf("s1 content = %+v, want red", p)
	}
	if r := img.RGBAAt(s2.screen.Min.X+5, s2.screen.Min.Y+5); r.G == 0xEE {
		t.Fatalf("s2 shows un-presented damage: %+v", r)
	}
	c.Present(s2)
	c.Compose()
	img, _ = c.Snapshot()
	if r := img.RGBAAt(s2.screen.Min.X+5, s2.screen.Min.Y+5); r.G != 0xEE {
		t.Fatalf("s2 content after present = %+v, want green", r)
	}

	// Klik op s1-content (buiten s2): raist + focust, app-lokale coördinaten.
	s, lx, ly, ok := c.PointerDown(s1.screen.Min.X+2, s1.screen.Min.Y+2)
	if !ok || s != s1 || lx != 2 || ly != 2 {
		t.Fatalf("PointerDown: s=%v lx=%d ly=%d ok=%v", s, lx, ly, ok)
	}
	if c.Focused() != s1 || c.zstack[len(c.zstack)-1] != s1 {
		t.Fatal("klik hoort te raisen én te focussen")
	}
	// s1 ligt nu bovenop: het overlap-punt hoort bij s1 te horen.
	if s, _, _, ok := c.SurfaceAt(s2.screen.Min.X+5, s2.screen.Min.Y+5); !ok || s != s1 {
		t.Fatalf("z-order: overlap hoort naar het bovenste window (%v)", s)
	}
	if _, _, _, ok := c.SurfaceAt(0, 0); ok {
		t.Fatal("de rand van het scherm hoort leeg te zijn")
	}

	c.Remove(s1)
	c.Relayout()
	if c.Focused() != s2 {
		t.Fatal("focus must fall back to remaining surface")
	}
	c.Compose()
}

// TestSleep: de titelbalk sleept het window, geklemd op het werkvlak.
func TestSleep(t *testing.T) {
	c := New(320, 200)
	s := c.Add("one", 100, 80, false)
	c.Relayout()
	was := s.win

	grip := image.Pt(was.Min.X+20, was.Min.Y+4) // in de titelbalk
	if _, _, _, ok := c.PointerDown(grip.X, grip.Y); ok {
		t.Fatal("titelbalk-klik is voor de WM, niet voor de app")
	}
	c.PointerMove(grip.X+50, grip.Y+30)
	if got := s.win.Min; got != was.Min.Add(image.Pt(50, 30)) {
		t.Fatalf("sleep: win.Min = %v, want %v", got, was.Min.Add(image.Pt(50, 30)))
	}
	// Ver voorbij de rand: klemmen (titelbalk blijft pakbaar).
	c.PointerMove(9999, 9999)
	work := c.workLocked()
	if s.win.Max.X > work.Max.X || s.win.Max.Y > work.Max.Y {
		t.Fatalf("sleep hoort geklemd op het werkvlak: %v", s.win)
	}
	c.PointerUp(9999, 9999)
	after := s.win
	c.PointerMove(50, 50) // sleep is klaar: bewegen verplaatst niets meer
	if s.win != after {
		t.Fatal("na PointerUp hoort de sleep voorbij")
	}
}

// TestTaskbarEnMenu: taskbar-knoppen minimaliseren/herstellen, en de
// startknop klapt het RoleMenu-surface (de launcher) open en dicht.
func TestTaskbarEnMenu(t *testing.T) {
	c := New(320, 200)
	s := c.Add("one", 100, 80, false)
	m := c.Add("launcher @ 3", 100, 100, true)
	c.Relayout()

	if c.Focused() != s {
		t.Fatal("een menu hoort de focus niet te kapen")
	}
	if !m.minimized {
		t.Fatal("een menu begint dicht")
	}
	c.Compose()

	// Startknop: menu open (+focus), nog eens: dicht.
	start := c.startRectLocked()
	c.PointerDown(start.Min.X+2, start.Min.Y+2)
	if m.minimized || c.Focused() != m {
		t.Fatal("startknop hoort het menu te openen en te focussen")
	}
	// Het menu staat boven de startknop, tegen de taskbar aan.
	if m.win.Max.Y != c.workLocked().Max.Y {
		t.Fatalf("menu hoort op de taskbar te rusten: %v", m.win)
	}
	c.PointerUp(start.Min.X+2, start.Min.Y+2)
	c.PointerDown(start.Min.X+2, start.Min.Y+2)
	if !m.minimized {
		t.Fatal("startknop hoort het menu weer te sluiten")
	}

	// Open het menu en klik ernaast: dicht (het startmenu-gebaar).
	c.PointerUp(start.Min.X+2, start.Min.Y+2)
	c.PointerDown(start.Min.X+2, start.Min.Y+2)
	c.PointerUp(start.Min.X+2, start.Min.Y+2)
	c.PointerDown(310, 10)
	if !m.minimized {
		t.Fatal("klik naast het menu hoort het te sluiten")
	}

	// Taskbar-knop van s: gefocust → minimaliseren; nog eens → terug.
	task := c.taskRectLocked(0)
	if c.Focused() != s {
		t.Fatalf("focus hoort terug bij s te liggen")
	}
	c.PointerDown(task.Min.X+2, task.Min.Y+2)
	if !s.minimized || c.Focused() == s {
		t.Fatal("taskbar-klik op het gefocuste window hoort te minimaliseren")
	}
	if _, _, _, ok := c.SurfaceAt(s.screen.Min.X+5, s.screen.Min.Y+5); ok {
		t.Fatal("een geminimaliseerd window hoort geen hits te vangen")
	}
	c.PointerUp(task.Min.X+2, task.Min.Y+2)
	c.PointerDown(task.Min.X+2, task.Min.Y+2)
	if s.minimized || c.Focused() != s {
		t.Fatal("nog een taskbar-klik hoort te herstellen")
	}
}

// TestStaleDamage: damage op een maat die de WM nooit toekende (de app
// rendert nog op zijn te grote hint) wordt stil gedropt; corruptie niet.
func TestStaleDamage(t *testing.T) {
	c := New(320, 200)
	s := c.Add("big", 999, 999, false)
	c.Relayout()
	w, h := s.Size()
	if w >= 999 {
		t.Fatal("hint hoort geklemd")
	}
	if err := s.Damage(0, 0, 999, 999, wireFill(999, 999, 1, 2, 3)); err != nil {
		t.Fatalf("stale damage must be dropped silently, got %v", err)
	}
	if err := s.Damage(0, 0, w, h, make([]byte, 7)); err == nil {
		t.Fatal("wrong pixel length must error")
	}
}

// TestPresentRects: partiële flip — alleen de gegeven rects worden zichtbaar.
func TestPresentRects(t *testing.T) {
	c := New(320, 200)
	s := c.Add("one", 100, 80, false)
	c.Relayout()
	w, h := s.Size()

	// Back-buffer volledig groen, maar presenteer alleen een blokje.
	if err := s.Damage(0, 0, w, h, wireFill(w, h, 0x10, 0xEE, 0x10)); err != nil {
		t.Fatal(err)
	}
	c.PresentRects(s, []image.Rectangle{image.Rect(4, 4, 12, 12)})
	c.Compose()
	img, _ := c.Snapshot()
	if p := img.RGBAAt(s.screen.Min.X+5, s.screen.Min.Y+5); p.G != 0xEE {
		t.Fatalf("presented rect must be visible, got %+v", p)
	}
	if p := img.RGBAAt(s.screen.Min.X+40, s.screen.Min.Y+40); p.G == 0xEE {
		t.Fatalf("un-presented area must stay hidden, got %+v", p)
	}
}

// frameRects decodeert de rects uit een FrameSince-frame.
func frameRects(frame []byte) []image.Rectangle {
	n := int(frame[4]) | int(frame[5])<<8
	rects := make([]image.Rectangle, 0, n)
	off := 6
	for i := 0; i < n; i++ {
		x := int(frame[off]) | int(frame[off+1])<<8
		y := int(frame[off+2]) | int(frame[off+3])<<8
		w := int(frame[off+4]) | int(frame[off+5])<<8
		h := int(frame[off+6]) | int(frame[off+7])<<8
		rects = append(rects, image.Rect(x, y, x+w, y+h))
		off += 8
	}
	return rects
}

// TestKlikKostGeenFrame: interactie-damage is precies (de "5kb per klik" in
// de KVM-stream, 20-07). Een klik in het voorste, gefocuste window kost níks
// (raise is dan een no-op); een focuswissel kost het geraisde window plus
// chrome-strips en de taskbar — nooit het volle scherm.
func TestKlikKostGeenFrame(t *testing.T) {
	c := New(320, 200)
	s1 := c.Add("one", 100, 80, false)
	s2 := c.Add("two", 100, 80, false)
	c.Relayout()
	fillPresent(c, s1, 0xAA, 0, 0)
	fillPresent(c, s2, 0, 0xAA, 0)
	_, gen := c.FrameSince(0) // het volle eerste frame is geweest

	// Klik (down+up) midden in s2 — al bovenop, al focus: geen damage.
	p := s2.screen.Min.Add(s2.screen.Size().Div(2))
	c.PointerDown(p.X, p.Y)
	c.PointerUp(p.X, p.Y)
	if frame, ngen := c.FrameSince(gen); frame != nil {
		t.Fatalf("klik op het gefocuste window kost een frame: gen %d→%d, rects %v",
			gen, ngen, frameRects(frame))
	}

	// Focuswissel naar s1: wél een frame, maar nooit het volle scherm.
	q := s1.screen.Min.Add(image.Pt(4, 4)) // s2 overlapt het midden (cascade)
	c.PointerDown(q.X, q.Y)
	c.PointerUp(q.X, q.Y)
	frame, ngen := c.FrameSince(gen)
	if frame == nil {
		t.Fatal("focuswissel hoort zichtbaar te zijn")
	}
	gen = ngen
	area := 0
	for _, r := range frameRects(frame) {
		if r == c.img.Bounds() {
			t.Fatalf("focuswissel hoort geen vol scherm te sturen: %v", r)
		}
		area += r.Dx() * r.Dy()
	}
	if full := c.img.Bounds(); area >= full.Dx()*full.Dy() {
		t.Fatalf("focuswissel-damage (%dpx) hoort onder schermmaat te blijven", area)
	}

	// Taskbar: minimize + herstel — ook zonder vol scherm, en het herstelde
	// window komt volledig terug.
	tb := c.taskRectLocked(0).Min.Add(c.taskRectLocked(0).Size().Div(2))
	c.PointerDown(tb.X, tb.Y) // s1 gefocust → minimize
	frame, ngen = c.FrameSince(gen)
	if frame == nil {
		t.Fatal("minimize hoort zichtbaar te zijn")
	}
	gen = ngen
	for _, r := range frameRects(frame) {
		if r == c.img.Bounds() {
			t.Fatalf("minimize hoort geen vol scherm te sturen: %v", r)
		}
	}
	c.PointerDown(tb.X, tb.Y) // herstel
	frame, _ = c.FrameSince(gen)
	if frame == nil {
		t.Fatal("herstel hoort zichtbaar te zijn")
	}
	won := false
	for _, r := range frameRects(frame) {
		if s1.win.In(r) {
			won = true
		}
	}
	if !won {
		t.Fatalf("herstel hoort heel s1.win te dragen: %v", frameRects(frame))
	}
}

// TestStoplichten: de drie titelbalk-knoppen — groen maximaliseert (en
// herstelt, met CONFIGURE), geel minimaliseert, rood vuurt OnClose (sluiten
// is proces killen; het opruimen zelf is van de server). Menu's hebben geen
// stoplichtjes: daar begint gewoon de sleep.
func TestStoplichten(t *testing.T) {
	c := New(320, 200)
	var closed []string
	c.OnClose(func(s *Surface) { closed = append(closed, s.Name) })
	var resizes []string
	c.OnResize(func(s *Surface, w, h int) { resizes = append(resizes, s.Name) })

	s1 := c.Add("one", 100, 80, false)
	menu := c.Add("menu", 100, 80, true)
	c.Relayout()
	resizes = nil

	mid := func(r image.Rectangle) image.Point { return r.Min.Add(r.Size().Div(2)) }

	// Groen: maximaliseren → het volle werkvlak, mét resize-callback...
	p := mid(c.lightRectsLocked(s1)[0])
	c.PointerDown(p.X, p.Y)
	c.PointerUp(p.X, p.Y)
	if s1.win != c.workLocked() {
		t.Fatalf("maximaal hoort het werkvlak te zijn: %v != %v", s1.win, c.workLocked())
	}
	if len(resizes) != 1 || resizes[0] != "one" {
		t.Fatalf("maximaliseren hoort één resize te melden: %v", resizes)
	}
	// ...en nog eens groen herstelt de oude plek.
	p = mid(c.lightRectsLocked(s1)[0])
	c.PointerDown(p.X, p.Y)
	c.PointerUp(p.X, p.Y)
	if w, h := s1.Size(); w != 100 || h != 80 {
		t.Fatalf("herstellen hoort de hint terug te geven: %dx%d", w, h)
	}

	// Geel: minimaliseren.
	p = mid(c.lightRectsLocked(s1)[1])
	c.PointerDown(p.X, p.Y)
	c.PointerUp(p.X, p.Y)
	if !s1.minimized {
		t.Fatal("geel hoort te minimaliseren")
	}
	s1.minimized = false

	// Rood: OnClose vuurt; de klik is verder van niemand.
	p = mid(c.lightRectsLocked(s1)[2])
	if _, _, _, ok := c.PointerDown(p.X, p.Y); ok {
		t.Fatal("een stoplicht-klik hoort nooit bij de app te komen")
	}
	c.PointerUp(p.X, p.Y)
	if len(closed) != 1 || closed[0] != "one" {
		t.Fatalf("rood hoort OnClose te vuren: %v", closed)
	}

	// Menu: geen stoplichtjes — dezelfde plek is daar titelbalk (sleep).
	menu.minimized = false
	c.raiseLocked(menu)
	p = mid(c.lightRectsLocked(menu)[2])
	c.PointerDown(p.X, p.Y)
	if len(closed) != 1 {
		t.Fatalf("een menu hoort geen sluitknop te hebben: %v", closed)
	}
	if c.drag != menu {
		t.Fatal("op een menu-titelbalk hoort de sleep te beginnen")
	}
	c.PointerUp(p.X, p.Y)
}

// TestPartialComposeEquivalence: een reeks partiële composes — inclusief
// raise, sleep en taskbar-geklik — eindigt in exact dezelfde pixels als één
// volledige hertekening: de eigenschap die partieel componeren veilig maakt.
func TestPartialComposeEquivalence(t *testing.T) {
	c := New(320, 200)
	s1 := c.Add("one", 100, 80, false)
	s2 := c.Add("two", 100, 80, false)
	c.Relayout()
	c.Compose() // eerste = vol

	fillPresent(c, s1, 0xEE, 0x10, 0x10)
	c.Compose() // partieel: window 1
	w2, h2 := s2.Size()
	if err := s2.Damage(0, 0, w2, h2, wireFill(w2, h2, 0x10, 0xEE, 0x10)); err != nil {
		t.Fatal(err)
	}
	c.PresentRects(s2, []image.Rectangle{image.Rect(3, 3, 30, 20)})
	c.Compose() // partieel: blokje in window 2
	c.PointerDown(s1.screen.Min.X+2, s1.screen.Min.Y+2)
	c.PointerUp(s1.screen.Min.X+2, s1.screen.Min.Y+2)
	c.Compose() // partieel: raise + focuswissel
	grip := image.Pt(s1.win.Min.X+10, s1.win.Min.Y+4)
	c.PointerDown(grip.X, grip.Y)
	c.PointerMove(grip.X+40, grip.Y+20)
	c.PointerUp(grip.X+40, grip.Y+20)
	c.Compose() // partieel: sleep
	task := c.taskRectLocked(1)
	c.PointerDown(task.Min.X+2, task.Min.Y+2) // s2 naar voren
	c.Compose()

	// Maximaliseer s2 (groen licht) en present dan een blokje dat op het
	// scherm half over de titelbalk van het bedolven s1 valt: chrome van een
	// lager window mag buiten zijn clip niets aanraken (de spook-chrome over
	// de gemaximaliseerde browser, 21-07).
	lp := c.lightRectsLocked(s2)[0].Min.Add(image.Pt(3, 3))
	c.PointerDown(lp.X, lp.Y)
	c.PointerUp(lp.X, lp.Y)
	c.Compose()
	w2, h2 = s2.Size()
	if err := s2.Damage(0, 0, w2, h2, wireFill(w2, h2, 0x20, 0x20, 0xEE)); err != nil {
		t.Fatal(err)
	}
	cut := image.Rect(s1.win.Min.X+6, s1.win.Min.Y+2, s1.win.Min.X+40, s1.win.Min.Y+6)
	c.PresentRects(s2, []image.Rectangle{cut.Sub(s2.screen.Min)})
	c.Compose()
	incr, _ := c.Snapshot()

	// Forceer een volledige hertekening van exact dezelfde toestand.
	c.mu.Lock()
	c.dirty = true
	c.pending = []image.Rectangle{c.img.Bounds()}
	c.mu.Unlock()
	c.Compose()
	full, _ := c.Snapshot()

	if !bytes.Equal(incr.Pix, full.Pix) {
		t.Fatal("incremental compose diverged from full redraw")
	}
}
