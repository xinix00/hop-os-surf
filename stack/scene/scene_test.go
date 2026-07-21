package scene

import (
	"image"
	"net"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/stack/surf"
)

// boom bouwt het test-dashboard: heading, twee values, meter, knoppenrij.
func boom() *Node {
	return Col(4,
		Label(StyleHeading, "test node").Sized(24),
		Row(2,
			Value("42", "MB"),
			Value("7", "s"),
		).Sized(48),
		Gauge(0, 100, 61, "%").Sized(20),
		Row(2,
			Button("min", nil),
			Button("plus", nil),
		).Sized(32),
		List([]string{"een", "twee", "drie"}, nil),
	)
}

func ids(root *Node) { // zoals Show ze toekent
	id := uint16(0)
	var walk func(*Node)
	walk = func(n *Node) {
		id++
		n.ID = id
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}

// TestRoundtrip: encode→decode geeft dezelfde boom terug.
func TestRoundtrip(t *testing.T) {
	root := boom()
	ids(root)
	wire := Encode(root)
	back, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	var check func(a, b *Node)
	check = func(a, b *Node) {
		if a.ID != b.ID || a.Kind != b.Kind || a.Text != b.Text || a.Style != b.Style ||
			a.Size != b.Size || a.Pad != b.Pad || a.Min != b.Min || a.Max != b.Max ||
			a.Val != b.Val || a.Unit != b.Unit || len(a.Items) != len(b.Items) ||
			len(a.Children) != len(b.Children) {
			t.Fatalf("node %d: %+v != %+v", a.ID, a, b)
		}
		for i := range a.Children {
			check(a.Children[i], b.Children[i])
		}
	}
	check(root, back)
	t.Logf("scene wire: %d bytes", len(wire))
}

// TestLayout: vaste maten en gewichten kloppen, pads gerespecteerd.
func TestLayout(t *testing.T) {
	root := boom()
	ids(root)
	Layout(root, 400, 300)
	head := root.Children[0]
	if head.Rect.Dy() != 24 {
		t.Fatalf("heading hoogte %d, wil 24", head.Rect.Dy())
	}
	vals := root.Children[1]
	a, b := vals.Children[0], vals.Children[1]
	if a.Rect.Dx() != b.Rect.Dx() && a.Rect.Dx()+1 != b.Rect.Dx() {
		t.Fatalf("values niet ~gelijk verdeeld: %d vs %d", a.Rect.Dx(), b.Rect.Dx())
	}
	if a.Rect.Max.X > b.Rect.Min.X {
		t.Fatal("values overlappen")
	}
	lijst := root.Children[4]
	if lijst.Rect.Max.Y > 300-4 {
		t.Fatalf("list steekt uit het venster: %v", lijst.Rect)
	}
}

// TestPatch: een PATCH verandert precies de bedoelde node.
func TestPatch(t *testing.T) {
	root := boom()
	ids(root)
	byID := Index(root)
	meter := root.Children[2]
	p := EncodePatch(meter.ID, []prop{{key: PropVal, typ: tI32, v: 88}})
	if len(p) > 12 {
		t.Fatalf("PATCH %d bytes — de belofte is ~tientallen", len(p))
	}
	n, err := ApplyPatch(byID, p)
	if err != nil {
		t.Fatal(err)
	}
	if n != meter || meter.Val != 88 {
		t.Fatalf("patch kwam niet aan: %+v", meter)
	}
	if _, err := ApplyPatch(byID, EncodePatch(999, nil)); err == nil {
		t.Fatal("patch op onbekende node moet falen")
	}
}

// TestHit: knoppen en list zijn raakbaar, labels niet.
func TestHit(t *testing.T) {
	root := boom()
	ids(root)
	Layout(root, 400, 300)
	knop := root.Children[3].Children[0]
	c := knop.Rect.Min.Add(knop.Rect.Size().Div(2))
	if HitAt(root, c.X, c.Y) != knop {
		t.Fatal("knop niet raakbaar op zijn middelpunt")
	}
	head := root.Children[0]
	hc := head.Rect.Min.Add(head.Rect.Size().Div(2))
	if h := HitAt(root, hc.X, hc.Y); h != nil {
		t.Fatalf("label is raakbaar: %+v", h)
	}
}

// TestRender: het volle beeld bevat panelpixels; RenderNode raakt alleen
// het eigen rect (de partiële-hertekening-eigenschap waar PATCH op leunt).
func TestRender(t *testing.T) {
	root := boom()
	ids(root)
	Layout(root, 400, 300)
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	Render(img, root)

	meter := root.Children[2]
	buiten := img.RGBAAt(meter.Rect.Min.X-1, meter.Rect.Min.Y) // net links van de meter
	meter.Val = 99
	RenderNode(img, meter)
	if img.RGBAAt(meter.Rect.Min.X-1, meter.Rect.Min.Y) != buiten {
		t.Fatal("RenderNode raakte pixels buiten zijn rect")
	}
	// De vulling moet nu verder reiken dan de helft.
	mid := meter.Rect.Min.Add(meter.Rect.Size().Div(2))
	if img.RGBAAt(mid.X, mid.Y) == colBG {
		t.Fatal("meter op 99% is halfweg niet gevuld")
	}
}

// TestForwardCompat: onbekende props en kinds mogen niet breken.
func TestForwardCompat(t *testing.T) {
	n := &Node{ID: 1, Kind: 200} // onbekende kind
	wire := Encode(n)
	if _, err := Decode(wire); err != nil {
		t.Fatalf("onbekende kind moet decoderen: %v", err)
	}
	// Handgebouwde node met een onbekende prop-key (77, type u8).
	raw := []byte{1, 0, 3, 1, 77, 3, 9, 0}
	back, err := Decode(raw)
	if err != nil || back.Kind != 3 {
		t.Fatalf("onbekende prop-key moet worden overgeslagen: %v", err)
	}
}

// TestPatchAdd: PropAdd plakt rijen achteraan, capt op listCap en volgt de
// staart als de kijker onderaan stond — de logstaart-semantiek (20-07).
func TestPatchAdd(t *testing.T) {
	l := List([]string{"a", "b"}, nil)
	l.ID = 1
	l.Rect = image.Rect(0, 0, 200, 2+3*listRowH) // 3 rijen zichtbaar
	byID := map[uint16]*Node{1: l}

	// append terwijl de kijker bovenaan staat (en alles past): staart volgen
	// is dan een no-op tot de lijst overloopt.
	if _, err := ApplyPatch(byID, EncodePatch(1, []prop{{key: PropAdd, typ: tStrList, list: []string{"c", "d"}}})); err != nil {
		t.Fatal(err)
	}
	if len(l.Items) != 4 || l.Items[3] != "d" {
		t.Fatalf("append → %v", l.Items)
	}
	if l.Scroll != l.maxScroll() {
		t.Fatalf("stond onderaan (alles paste) → hoort te volgen: scroll %d, want %d", l.Scroll, l.maxScroll())
	}

	// omhoog gescrolld: een append laat de kijker met rust
	l.Scroll = 0
	if _, err := ApplyPatch(byID, EncodePatch(1, []prop{{key: PropAdd, typ: tStrList, list: []string{"e"}}})); err != nil {
		t.Fatal(err)
	}
	if l.Scroll != 0 {
		t.Fatalf("omhoog gescrolld hoort te blijven staan: scroll %d", l.Scroll)
	}

	// cap: nooit meer dan listCap rijen, oudste eruit, Sel schuift mee
	l.Sel = 1
	big := make([]string, listCap)
	for i := range big {
		big[i] = "x"
	}
	l.addItems(big)
	if len(l.Items) != listCap {
		t.Fatalf("cap → %d rijen, want %d", len(l.Items), listCap)
	}
	if l.Sel != -1 {
		t.Fatalf("selectie viel boven de cap uit het venster → -1, kreeg %d", l.Sel)
	}
}

// TestPatchAddBlijftUitScene: PropAdd is PATCH-only — een geëncodeerde boom
// draagt de opgetelde Items als PropItems, nooit een add.
func TestPatchAddBlijftUitScene(t *testing.T) {
	l := List([]string{"a"}, nil)
	l.addItems([]string{"b"})
	back, err := Decode(Encode(l))
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Items) != 2 || back.Items[1] != "b" {
		t.Fatalf("roundtrip na addItems → %v", back.Items)
	}
}

// TestShowHergebruiktVerbinding: Show mag per schermwissel — één verbinding,
// elke volgende Show is alleen een nieuw SCENE-bericht (de storm van 20-07:
// elke Show startte een eigen lees/ping-lus die zelf ging herverbinden, en
// elke klik in taskman opende zo een nieuw window).
func TestShowHergebruiktVerbinding(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	accepts := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepts <- c
		}
	}()

	c := Open(l.Addr().String(), "test", 100, 100, t.Logf)
	if err := c.Show(Col(2, Label(0, "een"))); err != nil {
		t.Fatal(err)
	}
	conn := <-accepts

	types := make(chan uint8, 8)
	go func() {
		var buf []byte
		for {
			h, p, err := surf.ReadMsg(conn, buf)
			if err != nil {
				return
			}
			buf = p
			types <- h.Type
		}
	}()
	want := func(what string, typ uint8) {
		select {
		case got := <-types:
			if got != typ {
				t.Fatalf("%s: bericht %d, want %d", what, got, typ)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: geen bericht", what)
		}
	}
	want("hello", surf.TypeHello)
	want("create", surf.TypeCreate)
	want("scene", surf.TypeScene)

	if err := c.Show(Col(2, Label(0, "twee"))); err != nil {
		t.Fatal(err)
	}
	want("herShow = alleen SCENE", surf.TypeScene)

	select {
	case <-accepts:
		t.Fatal("een tweede Show hoort géén tweede verbinding te openen")
	case <-time.After(300 * time.Millisecond):
	}

	// Close is definitief: de heel-lus mag niet na een seconde herverbinden
	// (een host-proces zoals cmd/desktop leeft na een app-stop gewoon door).
	c.Close()
	select {
	case <-accepts:
		t.Fatal("na Close hoort de heel-lus nooit meer te verbinden")
	case <-time.After(1500 * time.Millisecond):
	}
}
