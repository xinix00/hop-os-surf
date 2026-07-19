package compositor

import (
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

func TestComposeAndHitTest(t *testing.T) {
	c := New(320, 200)

	s1 := c.Add("one", 60, 40)
	s2 := c.Add("two", 60, 40) // laatst toegevoegd → focus

	if c.Focused() != s2 {
		t.Fatal("newest surface must have focus")
	}

	// Vol rood naar s1, presenteren; s2 krijgt damage maar (nog) geen present.
	if err := s1.Damage(0, 0, 60, 40, wireFill(60, 40, 0xEE, 0x10, 0x10)); err != nil {
		t.Fatal(err)
	}
	c.Present(s1)
	if err := s2.Damage(0, 0, 60, 40, wireFill(60, 40, 0x10, 0xEE, 0x10)); err != nil {
		t.Fatal(err)
	}

	gen1, changed := c.Compose()
	if !changed || gen1 == 0 {
		t.Fatalf("first compose must draw (gen=%d changed=%v)", gen1, changed)
	}
	// Zonder wijzigingen geen hertekening (de PNG-cache leunt hierop).
	if gen2, changed := c.Compose(); changed || gen2 != gen1 {
		t.Fatalf("idle compose must be a no-op (gen %d→%d)", gen1, gen2)
	}

	// s1-content staat rood op het scherm (XRGB→RGBA-swap bewezen)...
	img, _ := c.Snapshot()
	p := img.RGBAAt(s1.screen.Min.X+5, s1.screen.Min.Y+5)
	if p.R != 0xEE || p.G != 0x10 || p.B != 0x10 {
		t.Fatalf("s1 content pixel = %+v, want red", p)
	}
	// ...maar s2 is zonder PRESENT nog leeg (double buffering).
	q := img.RGBAAt(s2.screen.Min.X+5, s2.screen.Min.Y+5)
	if q.G == 0xEE {
		t.Fatalf("s2 shows un-presented damage: %+v", q)
	}
	c.Present(s2)
	img, _ = mustCompose(t, c)
	if q := img.RGBAAt(s2.screen.Min.X+5, s2.screen.Min.Y+5); q.G != 0xEE {
		t.Fatalf("s2 content after present = %+v, want green", q)
	}

	// Hit-test + focuswissel: klik midden in s1.
	cx, cy := s1.screen.Min.X+10, s1.screen.Min.Y+7
	s, lx, ly, ok := c.ClickAt(cx, cy)
	if !ok || s != s1 || lx != 10 || ly != 7 {
		t.Fatalf("ClickAt: s=%v lx=%d ly=%d ok=%v", s, lx, ly, ok)
	}
	if c.Focused() != s1 {
		t.Fatal("click must move focus")
	}
	if _, _, _, ok := c.SurfaceAt(0, 0); ok {
		t.Fatal("margin must not hit-test to a surface")
	}

	// Remove: focus schuift door, compose blijft werken.
	c.Remove(s1)
	if c.Focused() != s2 {
		t.Fatal("focus must fall back to remaining surface")
	}
	mustCompose(t, c)
}

func TestDamageGuards(t *testing.T) {
	c := New(100, 100)
	s := c.Add("x", 10, 10)
	if err := s.Damage(5, 5, 10, 10, wireFill(10, 10, 1, 2, 3)); err == nil {
		t.Fatal("damage outside surface must error")
	}
	if err := s.Damage(0, 0, 10, 10, make([]byte, 7)); err == nil {
		t.Fatal("wrong pixel length must error")
	}
}

// TestCursorDirty: cursorbeweging maakt het scherm vuil, stilstand niet.
func TestCursorDirty(t *testing.T) {
	c := New(100, 100)
	mustCompose(t, c)
	c.SetCursor(10, 10)
	if _, changed := c.Compose(); !changed {
		t.Fatal("cursor move must redraw")
	}
	c.SetCursor(10, 10)
	if _, changed := c.Compose(); changed {
		t.Fatal("same cursor position must not redraw")
	}
}

func mustCompose(t *testing.T, c *Compositor) (*image.RGBA, uint64) {
	t.Helper()
	c.Compose()
	return c.Snapshot()
}
