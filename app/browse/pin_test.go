package browse

import (
	"image"
	"image/color"
	"net/http"
	"strings"
	"testing"
)

// TestGepindeHeader: position:sticky/fixed + top:0 betekent "dit is mijn
// header, hou hem in beeld" — voorbij gescrold tekent View hem bovenin en
// vangt hij daar ook de kliks.
func TestGepindeHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
header { position: sticky; top: 0; background: #123456; color: #fff; }
</style></head><body>
<header><a href="/home">HOME</a></header>
<p>` + strings.Repeat("lorem ipsum dolor sit amet ", 200) + `</p>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)
	if !p.Pinned() || p.PinY1 <= p.PinY0 {
		t.Fatalf("header niet gepind: PinY0=%d PinY1=%d", p.PinY0, p.PinY1)
	}

	v := View{Page: p}
	img := image.NewRGBA(image.Rect(0, 0, 480, 360))
	v.ScrollBy(600, 360)
	if !v.pinnedNow() {
		t.Fatalf("voorbij de header gescrold maar niet gepind (scroll=%d)", v.Scroll)
	}
	v.Render(img)
	// De strook direct onder de adresbalk hoort de headerkleur te dragen.
	if got := img.RGBAAt(240, BarH+2); got != (color.RGBA{0x12, 0x34, 0x56, 0xFF}) {
		t.Fatalf("gepinde header niet bovenin getekend: %v", got)
	}
	// En de klik op de strook raakt de header-link, niet de tekst eronder.
	var link *Box
	for i := range p.Boxes {
		if p.Boxes[i].Href == "/home" {
			link = &p.Boxes[i]
			break
		}
	}
	if link == nil {
		t.Fatal("header-link niet gevonden")
	}
	y := BarH + (link.R.Min.Y - p.PinY0) + 1
	if got := v.Hit(link.R.Min.X+1, y, 360); got != "/home" {
		t.Fatalf("klik op gepinde header gaf %q, wil /home", got)
	}
	// Zonder scroll staat hij gewoon in de flow: niets dubbel getekend.
	v.Scroll = 0
	if v.pinnedNow() {
		t.Fatal("op scroll 0 hoort de header in de flow te staan")
	}
}

// TestCookiebarWeg: fixed onderaan geplakt (en zonder JS niet weg te
// klikken) hoort niet midden door de pagina te renderen — weg ermee.
func TestCookiebarWeg(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>
<p>gewone inhoud</p>
<div style="position:fixed;bottom:0;background:#000;color:#fff">Wij gebruiken cookies, akkoord?</div>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)
	if find(p, "cookies") != nil {
		t.Fatal("cookiebar (fixed+bottom) hoort weggelaten te worden")
	}
	if find(p, "gewone inhoud") == nil {
		t.Fatal("gewone inhoud hoort te blijven")
	}
}
