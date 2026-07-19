package compositor

import (
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

func TestWMSizing(t *testing.T) {
	c := New(320, 200)
	var resizes []string
	c.OnResize(func(s *Surface, w, h int) {
		resizes = append(resizes, s.Name)
	})

	// Eén window: krijgt (vrijwel) het hele scherm, wat de hint ook was.
	s1 := c.Add("one", 60, 40)
	c.Relayout()
	w1, h1 := s1.Size()
	if w1 < 250 || h1 < 150 {
		t.Fatalf("single window must fill the screen, got %dx%d", w1, h1)
	}
	if len(resizes) != 1 || resizes[0] != "one" {
		t.Fatalf("resize callbacks: %v", resizes)
	}

	// Tweede window erbij: de WM verdeelt opnieuw, beide krijgen een resize.
	resizes = nil
	s2 := c.Add("two", 999, 999)
	c.Relayout()
	if len(resizes) != 2 {
		t.Fatalf("both windows must be resized, got %v", resizes)
	}
	nw1, _ := s1.Size()
	w2, h2 := s2.Size()
	if nw1 >= w1 {
		t.Fatalf("s1 must shrink when s2 arrives (%d → %d)", w1, nw1)
	}
	if w2 > 320/2 || h2 <= 0 {
		t.Fatalf("s2 got %dx%d, want ~half the screen width", w2, h2)
	}

	// Idempotent: nog een Relayout zonder wijzigingen → geen callbacks.
	resizes = nil
	c.Relayout()
	if len(resizes) != 0 {
		t.Fatalf("no-op relayout must not fire callbacks: %v", resizes)
	}
}

func TestComposeAndHitTest(t *testing.T) {
	c := New(320, 200)
	s1 := c.Add("one", 0, 0)
	s2 := c.Add("two", 0, 0) // laatst toegevoegd → focus
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
	// Het window vult nu z'n hele content-vlak: check midden én hoek.
	p := img.RGBAAt(s1.screen.Min.X+s1.screen.Dx()/2, s1.screen.Min.Y+s1.screen.Dy()/2)
	q := img.RGBAAt(s1.screen.Max.X-2, s1.screen.Max.Y-2)
	if p.R != 0xEE || q.R != 0xEE {
		t.Fatalf("s1 must fill its cell (mid %+v, corner %+v)", p, q)
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

	// Hit-test + focuswissel.
	s, lx, ly, ok := c.ClickAt(s1.screen.Min.X+10, s1.screen.Min.Y+7)
	if !ok || s != s1 || lx != 10 || ly != 7 {
		t.Fatalf("ClickAt: s=%v lx=%d ly=%d ok=%v", s, lx, ly, ok)
	}
	if c.Focused() != s1 {
		t.Fatal("click must move focus")
	}
	if _, _, _, ok := c.SurfaceAt(0, 0); ok {
		t.Fatal("margin must not hit-test to a surface")
	}

	c.Remove(s1)
	c.Relayout()
	if c.Focused() != s2 {
		t.Fatal("focus must fall back to remaining surface")
	}
	c.Compose()
}

// TestStaleDamage: na een resize wordt damage op de oude (grotere) maat stil
// gedropt, en blijft de oude inhoud (overlap) staan tot de app bijtrekt.
func TestStaleDamage(t *testing.T) {
	c := New(320, 200)
	s1 := c.Add("one", 0, 0)
	c.Relayout()
	w1, h1 := fillPresent(c, s1, 0xEE, 0x10, 0x10)

	// Tweede window → s1 krimpt.
	c.Add("two", 0, 0)
	c.Relayout()
	nw, nh := s1.Size()
	if nw >= w1 {
		t.Fatal("s1 must shrink")
	}
	// Damage op de oude maat: gedropt, geen fout.
	if err := s1.Damage(0, 0, w1, h1, wireFill(w1, h1, 1, 2, 3)); err != nil {
		t.Fatalf("stale damage must be dropped silently, got %v", err)
	}
	// De overlap van het oude beeld staat er nog (geen zwart gat).
	c.Compose()
	img, _ := c.Snapshot()
	if p := img.RGBAAt(s1.screen.Min.X+3, s1.screen.Min.Y+3); p.R != 0xEE {
		t.Fatalf("old content must survive resize, got %+v", p)
	}
	// Corruptie (pixellengte past niet bij de rechthoek) blijft wél een fout.
	if err := s1.Damage(0, 0, nw, nh, make([]byte, 7)); err == nil {
		t.Fatal("wrong pixel length must error")
	}
}

// TestCursorDirty: cursorbeweging maakt het scherm vuil, stilstand niet.
func TestCursorDirty(t *testing.T) {
	c := New(100, 100)
	c.Compose()
	c.SetCursor(10, 10)
	if _, changed := c.Compose(); !changed {
		t.Fatal("cursor move must redraw")
	}
	c.SetCursor(10, 10)
	if _, changed := c.Compose(); changed {
		t.Fatal("same cursor position must not redraw")
	}
}
