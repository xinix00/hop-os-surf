package browse

import (
	"image"
	"net/http"
	"strings"
	"testing"
)

// testsite is een mini-web: twee pagina's met een relatieve link ertussen —
// via gost-doms WithHandler, dus zonder sockets.
func testsite() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>
<h1>Voorpagina</h1>
<p>Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod
tempor incididunt ut labore et dolore magna aliqua.</p>
<ul><li>eerste</li><li>tweede met <a href="/twee">een link</a></li></ul>
<hr>
<pre>  kolom1  kolom2</pre>
</body></html>`))
	})
	mux.HandleFunc("/twee", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><h2>Pagina twee</h2><p>Aangekomen.</p></body></html>`))
	})
	return mux
}

func find(p Page, text string) *Box {
	for i := range p.Boxes {
		if strings.Contains(p.Boxes[i].Text, text) {
			return &p.Boxes[i]
		}
	}
	return nil
}

func TestLayoutKetenEnNavigatie(t *testing.T) {
	s := NewSessionHandler(testsite())
	if err := s.Go("example.com"); err != nil { // kaal adres → http://
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)

	kop := find(p, "Voorpagina")
	if kop == nil || kop.Scale != 3 {
		t.Fatalf("h1 niet op schaal 3 gelayout: %+v", kop)
	}
	link := find(p, "een link")
	if link == nil || link.Href != "/twee" {
		t.Fatalf("link niet gevonden of zonder href: %+v", link)
	}
	li := find(p, "eerste")
	if li == nil || li.R.Min.X <= pad {
		t.Fatalf("lijstitem niet ingesprongen: %+v", li)
	}
	pre := find(p, "kolom1  kolom2")
	if pre == nil {
		t.Fatalf("pre-spaties niet behouden: %+v", p.Boxes)
	}
	if p.Height <= 0 {
		t.Fatalf("paginahoogte %d", p.Height)
	}
	// Alles moet binnen de paginabreedte blijven (pre uitgezonderd — die
	// mag uitlopen, maar deze regels passen).
	for _, b := range p.Boxes {
		if b.R.Max.X > 480 {
			t.Fatalf("box loopt uit het beeld: %+v", b)
		}
	}

	// Klik de link aan via de View (documentcoördinaten → windowklik).
	v := View{Page: p}
	href := v.Hit(link.R.Min.X+1, link.R.Min.Y+BarH+1)
	if href != "/twee" {
		t.Fatalf("Hit op de link gaf %q", href)
	}
	if err := s.Follow(href); err != nil { // relatief: gost-dom lost op
		t.Fatalf("Follow: %v", err)
	}
	if got := s.URL(); !strings.HasSuffix(got, "/twee") {
		t.Fatalf("na Follow op %q beland", got)
	}
	if find(s.Layout(480), "Aangekomen.") == nil {
		t.Fatal("pagina twee niet gelayout")
	}
}

func TestWoordwrap(t *testing.T) {
	s := NewSessionHandler(testsite())
	if err := s.Go("http://example.com/"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	smal := s.Layout(160)
	breed := s.Layout(480)
	if smal.Height <= breed.Height {
		t.Fatalf("smalle layout (%d) hoort hoger te zijn dan brede (%d)", smal.Height, breed.Height)
	}
	for _, b := range smal.Boxes {
		if !b.Rule && b.R.Max.X > 160 && b.Scale == 1 && !strings.Contains(b.Text, "kolom") {
			t.Fatalf("box niet gewrapt op 160px: %+v", b)
		}
	}
}

func TestRenderEnScroll(t *testing.T) {
	s := NewSessionHandler(testsite())
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	v := View{Addr: s.URL(), Page: s.Layout(200)}
	img := image.NewRGBA(image.Rect(0, 0, 200, 120))
	v.Render(img) // mag niet panicken, ook met boxes buiten beeld

	if !v.ScrollBy(48, 120) {
		t.Fatal("scroll omlaag moest kunnen")
	}
	if v.ScrollBy(-1000, 120); v.Scroll != 0 {
		t.Fatalf("scroll niet op 0 geklemd: %d", v.Scroll)
	}
	big := v.Page.Height // ver voorbij het einde → klem op max
	if v.ScrollBy(big*2, 120); v.Scroll != big-(120-BarH) {
		t.Fatalf("scroll niet op max geklemd: %d (hoogte %d)", v.Scroll, big)
	}
	v.Render(img)
}

func TestStartpagina(t *testing.T) {
	s := NewSession() // geen netwerk: de ingebouwde welkomstpagina
	p := s.Layout(320)
	if find(p, "surf") == nil {
		t.Fatal("welkomstpagina leeg")
	}
}

func TestRune(t *testing.T) {
	cases := []struct {
		code  uint32
		shift bool
		want  byte
	}{
		{'A', false, 'a'}, {'A', true, 'A'}, {'7', false, '7'},
		{186, true, ':'}, {191, false, '/'}, {190, false, '.'},
		{189, false, '-'}, {'3', true, '#'}, {99, false, '3'},
		{16, false, 0}, {13, false, 0},
	}
	for _, c := range cases {
		if got := Rune(c.code, c.shift); got != c.want {
			t.Errorf("Rune(%d, %v) = %q, wil %q", c.code, c.shift, got, c.want)
		}
	}
}

func TestAscii(t *testing.T) {
	cases := []struct{ in, want string }{
		{"gewoon", "gewoon"},
		{"em—dash", "em-dash"},
		{"‘quo’", "'quo'"},
		{"…", "..."},
		{"smörgås", "sm?rg?s"}, // één ? per teken, niet per byte
	}
	for _, c := range cases {
		if got := ascii(c.in); got != c.want {
			t.Errorf("ascii(%q) = %q, wil %q", c.in, got, c.want)
		}
	}
}
