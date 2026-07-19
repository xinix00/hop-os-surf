package browse

import (
	"bytes"
	"crypto/x509"
	"image"
	"image/color"
	"image/png"
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
	href := v.Hit(link.R.Min.X+1, link.R.Min.Y+BarH+1, 10000)
	if href != "/twee" {
		t.Fatalf("Hit op de link gaf %q", href)
	}
	// Dezelfde klik met de statusbalk eroverheen raakt niets: de balk is
	// van de chrome, niet van de pagina.
	if got := v.Hit(link.R.Min.X+1, link.R.Min.Y+BarH+1, link.R.Min.Y+BarH+2); got != "" {
		t.Fatalf("klik op de statusbalk gaf een link: %q", got)
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
	if v.ScrollBy(big*2, 120); v.Scroll != big-(120-BarH-StatusH) {
		t.Fatalf("scroll niet op max geklemd: %d (hoogte %d)", v.Scroll, big)
	}
	v.Status, v.Err = "ok (12ms) http://example.com", false
	v.Render(img)
	// De statusbalk is chrome: de onderste strook hoort balk-kleur te zijn,
	// wat er ook aan content "onder" zit.
	if img.RGBAAt(3, 120-StatusH+1) != colBar {
		t.Fatalf("statusbalk niet getekend: %v", img.RGBAAt(3, 120-StatusH+1))
	}
	// Pagina langer dan de viewport → scrollindicator rechts; op max-scroll
	// zit de duim onderaan de baan.
	if got := img.RGBAAt(198, 120-StatusH-2); got != colScrThumb {
		t.Fatalf("scrollduim niet onderaan: %v", got)
	}
	if got := img.RGBAAt(198, BarH+1); got != colScrTrack {
		t.Fatalf("scrollbaan niet getekend bovenaan: %v", got)
	}
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

func TestAfbeeldingen(t *testing.T) {
	// Een 40x20 rood PNG'tje en een 1000px-brede banner, uit de handler.
	pngBytes := func(w, h int) []byte {
		m := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := range m.Pix {
			m.Pix[i] = 0xFF // effen; alpha ook
		}
		var buf bytes.Buffer
		png.Encode(&buf, m)
		return buf.Bytes()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>
<p>logo: <img src="/logo.png" alt="logo"> inline</p>
<p><a href="/twee"><img src="banner.png" alt="banner"></a></p>
<p><img src="/weg.png" alt="kapot"></p>
</body></html>`))
	})
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes(40, 20))
	})
	mux.HandleFunc("/banner.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes(1000, 100)) // breder dan de pagina → schalen
	})

	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)

	var logo, banner *Box
	for i := range p.Boxes {
		b := &p.Boxes[i]
		switch {
		case b.Img != nil && b.R.Dx() == 40:
			logo = b
		case b.Img != nil && b.R.Dx() > 40:
			banner = b
		}
	}
	if logo == nil {
		t.Fatalf("logo.png niet als afbeelding gelayout: %+v", p.Boxes)
	}
	if logo.R.Dy() != 20 {
		t.Fatalf("logo-maat klopt niet: %v", logo.R)
	}
	// Inline: "inline" staat erachter, op dezelfde regel als het logo.
	if txt := find(p, "inline"); txt == nil || txt.R.Min.Y != logo.R.Min.Y || txt.R.Min.X <= logo.R.Max.X {
		t.Fatalf("logo niet inline geplaatst: logo=%v tekst=%+v", logo.R, txt)
	}
	if banner == nil || banner.R.Dx() != 480-2*pad {
		t.Fatalf("banner niet passend geschaald: %+v", banner)
	}
	if banner.R.Dy() != 100*(480-2*pad)/1000 {
		t.Fatalf("banner niet proportioneel geschaald: %v", banner.R)
	}
	if banner.Href != "/twee" {
		t.Fatalf("banner in <a> niet klikbaar: %+v", banner)
	}
	if kapot := find(p, "[kapot]"); kapot == nil {
		t.Fatal("404-afbeelding viel niet terug op alt-tekst")
	}

	// En renderen: het pixelvlak van het logo hoort effen wit (0xFF) te zijn.
	v := View{Page: p}
	img := image.NewRGBA(image.Rect(0, 0, 480, 400))
	v.Render(img)
	at := img.RGBAAt(logo.R.Min.X+5, BarH+logo.R.Min.Y+5)
	if at.R != 0xFF || at.G != 0xFF || at.B != 0xFF {
		t.Fatalf("logo-pixels niet gerenderd: %v", at)
	}
}

func TestCSS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head>
<style>
/* commentaar hoort weg te vallen */
.rood { color: #c00; }
.balk { background-color: rgb(255, 235, 59); }
#weg, .cookie { display: none; }
h1 { text-align: center; }
p { color: green; font-weight: bold; }
p { color: navy; } /* zelfde specificiteit, later wint */
@media print { p { color: black } }
</style>
<link rel="stylesheet" href="/extra.css">
</head><body>
<h1>Kop</h1>
<p>alinea</p>
<span class="rood">waarschuwing</span>
<span class="balk">gemarkeerd</span>
<div id="weg">cookiebanner</div>
<div class="cookie">nog een banner</div>
<div hidden>attribuut-verborgen</div>
<b style="color: purple">inline-paars</b>
<span class="extern">uit-extern-blad</span>
</body></html>`))
	})
	mux.HandleFunc("/extra.css", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`.extern { color: teal; font-size: 24px; }`))
	})

	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)

	if b := find(p, "waarschuwing"); b == nil || b.Col != (color.RGBA{0xCC, 0x00, 0x00, 0xFF}) {
		t.Fatalf("class-kleur (#c00) niet toegepast: %+v", b)
	}
	if b := find(p, "gemarkeerd"); b == nil || !b.HasBG || b.BG != (color.RGBA{255, 235, 59, 0xFF}) {
		t.Fatalf("background-color (rgb) niet toegepast: %+v", b)
	}
	for _, weg := range []string{"cookiebanner", "nog een banner", "attribuut-verborgen"} {
		if find(p, weg) != nil {
			t.Fatalf("%q had verborgen moeten zijn", weg)
		}
	}
	if b := find(p, "alinea"); b == nil || b.Col != (color.RGBA{0x00, 0x00, 0x80, 0xFF}) || !b.Bold {
		t.Fatalf("p-regel (navy wint van green, bold): %+v", b)
	}
	if b := find(p, "inline-paars"); b == nil || b.Col != (color.RGBA{0x80, 0x00, 0x80, 0xFF}) || !b.Bold {
		t.Fatalf("inline style hoort te winnen, <b> hoort vet te blijven: %+v", b)
	}
	if b := find(p, "uit-extern-blad"); b == nil || b.Col != (color.RGBA{0x00, 0x80, 0x80, 0xFF}) || b.Scale != 3 {
		t.Fatalf("extern stylesheet (teal, 24px→schaal 3) niet toegepast: %+v", b)
	}
	kop := find(p, "Kop")
	if kop == nil || kop.R.Min.X <= pad+8 {
		t.Fatalf("h1 niet gecentreerd: %+v", kop)
	}
}

func TestMenuOpEenRegel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
.menu { display: flex; }
ul.inl li { display: inline; }
</style></head><body>
<nav><a href="/a">Home</a><a href="/b">Docs</a><a href="/c">Over</a></nav>
<div class="menu"><div>Een</div><div>Twee</div><div>Drie</div></div>
<ul class="inl"><li>x</li><li>y</li></ul>
<p>gewone alinea eronder</p>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)

	sameLine := func(a, b string) {
		t.Helper()
		ba, bb := find(p, a), find(p, b)
		if ba == nil || bb == nil {
			t.Fatalf("%q of %q niet gevonden", a, b)
		}
		if ba == bb {
			return // tot één run samengevoegd: per definitie op één regel
		}
		if ba.R.Min.Y != bb.R.Min.Y {
			t.Fatalf("%q en %q horen op één regel: %+v vs %+v", a, b, ba, bb)
		}
		if bb.R.Min.X <= ba.R.Max.X {
			t.Fatalf("%q hoort rechts van %q (met lucht): %+v vs %+v", b, a, bb, ba)
		}
	}
	sameLine("Home", "Docs") // <nav>: UA-vooroordeel
	sameLine("Docs", "Over")
	sameLine("Een", "Twee") // display:flex
	sameLine("x", "y")      // li display:inline (en zonder streepjes)
	if find(p, "-") != nil {
		t.Fatal("inline li hoort geen '-'-bullet te krijgen")
	}
	menu := find(p, "Drie")
	onder := find(p, "gewone alinea eronder")
	if onder == nil || menu == nil || onder.R.Min.Y <= menu.R.Min.Y {
		t.Fatalf("de alinea hoort ónder het menu: %+v vs %+v", onder, menu)
	}
}

func TestParseCSS(t *testing.T) {
	rules := parseCSS(`a { color: red; margin: 4px } /* junk */ b,i{font-weight:700}
@media screen { .x { color: blue } } .leeg { margin: 0 }`, 0)
	// a (color), b en i (font-weight); .leeg (alleen margin) en de
	// @media-inhoud vallen weg.
	if len(rules) != 3 {
		t.Fatalf("wil 3 regels, kreeg %d: %+v", len(rules), rules)
	}
	if rules[0].decls["color"] != "red" || rules[0].decls["margin"] != "" {
		t.Fatalf("a-regel klopt niet: %+v", rules[0])
	}
	if rules[1].sel != "b" || rules[2].sel != "i" {
		t.Fatalf("selector-groep niet gesplitst: %+v", rules[1:])
	}
	if b, _ := boldWeight(rules[1].decls["font-weight"]); !b {
		t.Fatalf("700 hoort vet te zijn")
	}
	if specificity("#a .b c") <= specificity(".b c") || specificity(".b c") <= specificity("c") {
		t.Fatal("specificiteit niet oplopend id > class > tag")
	}
}

func TestCSSKleur(t *testing.T) {
	cases := []struct {
		in   string
		want color.RGBA
		ok   bool
	}{
		{"#fff", color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}, true},
		{"#1a4fc4", color.RGBA{0x1A, 0x4F, 0xC4, 0xFF}, true},
		{"rgb(16, 32, 48)", color.RGBA{16, 32, 48, 0xFF}, true},
		{"rgba(16,32,48,0.5)", color.RGBA{16, 32, 48, 0xFF}, true},
		{"red", color.RGBA{0xFF, 0x00, 0x00, 0xFF}, true},
		{"transparent", color.RGBA{}, false},
		{"var(--x)", color.RGBA{}, false},
	}
	for _, c := range cases {
		got, ok := cssColor(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("cssColor(%q) = %v,%v; wil %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCACertBundel(t *testing.T) {
	// De meegebakken bundel moet parsen — op tamago is dit de héle
	// certificaatwinkel.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cacertPEM) {
		t.Fatal("cacert.pem bevat geen parseerbare certificaten")
	}
	if n := strings.Count(string(cacertPEM), "BEGIN CERTIFICATE"); n < 50 {
		t.Fatalf("verdacht weinig roots in de bundel: %d", n)
	}
}

func TestEchteFoutInStatus(t *testing.T) {
	// gost-dom vouwt elke niet-200 plat tot "Non-ok Response"; de proxy
	// hoort de échte fout door te geven.
	s := NewSession() // met netProxy — de fetch faalt op DNS, niet op een handler
	err := s.Go("http://xn--dit-bestaat-echt-niet-4ob.invalid/")
	if err == nil {
		t.Skip("onverwacht: .invalid resolvet hier")
	}
	if strings.Contains(err.Error(), "Non-ok") {
		t.Fatalf("kale gost-dom-fout niet vervangen: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("fout noemt de host niet: %v", err)
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
