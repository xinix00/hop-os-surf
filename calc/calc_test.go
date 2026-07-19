package calc

import (
	"image"
	"testing"
)

func press(c *Calc, keys string) {
	for i := 0; i < len(keys); i++ {
		c.Press(keys[i])
	}
}

func TestRekenen(t *testing.T) {
	cases := []struct {
		keys string
		want string
	}{
		{"2+3=", "5"},
		{"2+3*4=", "20"}, // immediate execution, geen voorrang — zoals een zakrekenmachine
		{"10-4-3=", "3"},
		{"7/2=", "3.5"},
		{"9/0=", "err"},
		{"9/0=C2+2=", "4"}, // C herstelt na err
		{"12.5+0.5=", "13"},
		{"5+=", "10"},     // = zonder tweede operand herhaalt met acc
		{"123bb4=", "14"}, // backspace
		{"..5=", "0.5"},   // dubbele punt genegeerd
		{"2+3", "3"},      // display toont de lopende invoer
		{"2+3=+1=", "6"},  // doorrekenen op het resultaat
	}
	for _, tc := range cases {
		var c Calc
		press(&c, tc.keys)
		if got := c.Display(); got != tc.want {
			t.Errorf("%q → %q, want %q", tc.keys, got, tc.want)
		}
	}
}

func TestPendingOp(t *testing.T) {
	var c Calc
	press(&c, "12+")
	if c.Op() != '+' {
		t.Fatalf("Op() = %q, want +", c.Op())
	}
	press(&c, "34=")
	if c.Op() != 0 {
		t.Fatalf("Op() after = must clear, got %q", c.Op())
	}
}

func TestHitEnKey(t *testing.T) {
	b := image.Rect(0, 0, 240, 300)
	_, btns := layout(b)
	// Midden van de 7-knop (index 0) raakt '7'; de =-rij raakt '='.
	m := btns[0].Min.Add(btns[0].Size().Div(2))
	if got := Hit(b, m.X, m.Y); got != '7' {
		t.Fatalf("Hit(7-knop) = %q", got)
	}
	e := btns[16].Min.Add(btns[16].Size().Div(2))
	if got := Hit(b, e.X, e.Y); got != '=' {
		t.Fatalf("Hit(=-rij) = %q", got)
	}
	if got := Hit(b, 0, 0); got != 0 {
		t.Fatalf("Hit(padding) = %q, want 0", got)
	}
	// Browser-keycodes: cijfer, numpad, enter, backspace.
	for code, want := range map[uint32]byte{55: '7', 103: '7', 13: '=', 8: 'b', 106: '*'} {
		if got := Key(code); got != want {
			t.Fatalf("Key(%d) = %q, want %q", code, got, want)
		}
	}
	// Rendert zonder panic op kleine en grote maten.
	for _, r := range []image.Rectangle{image.Rect(0, 0, 60, 60), image.Rect(0, 0, 500, 700)} {
		var c Calc
		press(&c, "1+2=")
		Render(image.NewRGBA(r), &c, '5')
	}
}
