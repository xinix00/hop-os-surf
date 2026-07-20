package calc

import (
	"testing"

	"github.com/xinix00/hop-os-surf/stack/scene"
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

func TestKey(t *testing.T) {
	// Browser-keycodes: cijfer, numpad, enter, backspace.
	for code, want := range map[uint32]byte{55: '7', 103: '7', 13: '=', 8: 'b', 106: '*'} {
		if got := Key(code); got != want {
			t.Fatalf("Key(%d) = %q, want %q", code, got, want)
		}
	}
}

// TestTree: elke knop in de scene-boom drukt zijn éigen toets (de klassieke
// closure-valkuil), de =-rij bestaat, en Line toont de openstaande operator.
func TestTree(t *testing.T) {
	var c Calc
	root, display := Tree(func(k byte) { c.Press(k) })
	if display.Kind != scene.KindValue {
		t.Fatalf("display hoort een Value te zijn, kreeg kind %d", display.Kind)
	}

	byLabel := map[string]*scene.Node{}
	var walk func(n *scene.Node)
	walk = func(n *scene.Node) {
		if n.Kind == scene.KindButton {
			byLabel[n.Text] = n
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(root)
	if len(byLabel) != 17 {
		t.Fatalf("verwachtte 17 knoppen, kreeg %d", len(byLabel))
	}

	for _, s := range []string{"1", "2", "+", "3", "4", "="} {
		byLabel[s].OnClick()
	}
	if got := c.Display(); got != "46" { // 12+34, immediate execution
		t.Fatalf("12+34= via knoppen → %q, want 46", got)
	}
	byLabel["x"].OnClick() // × zet de operator
	if got := Line(&c); got != "x 46" {
		t.Fatalf("Line met openstaande operator → %q, want \"x 46\"", got)
	}

	// De boom moet de draad over kunnen (encode→decode zonder fout).
	if _, err := scene.Decode(scene.Encode(root)); err != nil {
		t.Fatal(err)
	}
}
