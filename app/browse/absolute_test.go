package browse

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"testing"
)

// TestAbsoluteBadge: position:absolute gaat uit de flow, ankert op de
// gepositioneerde voorouder en wordt bovenop geschilderd (late paint).
func TestAbsoluteBadge(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
.kaart { position: relative; background: #eee; }
.badge { position: absolute; top: 4px; left: 8px; background: #c00; color: #fff; }
</style></head><body>
<div class="kaart"><p>de kaarttekst zelf</p><span class="badge">Video</span></div>
<p>daarna gewoon verder</p>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)
	kaart, badge, verder := find(p, "kaarttekst"), find(p, "Video"), find(p, "daarna")
	if kaart == nil || badge == nil || verder == nil {
		t.Fatal("kaart, badge of vervolgtekst niet gevonden")
	}
	// De badge ankert linksboven op de kaart, niet in de flow erná.
	if badge.R.Min.Y > kaart.R.Min.Y+16 {
		t.Fatalf("badge hoort bovenop de kaart: badge=%v kaart=%v", badge.R, kaart.R)
	}
	// En hij duwt de flow niet omlaag: de vervolgtekst staat direct onder de kaart.
	if verder.R.Min.Y-kaart.R.Max.Y > 60 {
		t.Fatalf("absolute element duwt de flow omlaag: verder=%v kaart=%v", verder.R, kaart.R)
	}
	// Late paint: de badge-box komt in de lijst ná de kaarttekst.
	iK, iB := -1, -1
	for i := range p.Boxes {
		switch {
		case p.Boxes[i].Text == "de kaarttekst zelf":
			iK = i
		case p.Boxes[i].Text == "Video":
			iB = i
		}
	}
	if iB < iK {
		t.Fatalf("badge hoort bovenop (later) geschilderd te worden: %d < %d", iB, iK)
	}
}

// TestLogoSlot: een voorpagina-link zonder renderbare inhoud (svg-logo)
// krijgt het site-icoon — het alt-tekst-principe.
func TestLogoSlot(t *testing.T) {
	icoon := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range icoon.Pix {
		icoon.Pix[i] = 0xFF
	}
	var buf bytes.Buffer
	png.Encode(&buf, icoon)
	mux := http.NewServeMux()
	mux.HandleFunc("/icoon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head>
<link rel="apple-touch-icon" href="/icoon.png">
</head><body>
<header><a href="/" aria-label="De Site"><svg><title>De Site</title></svg></a></header>
<p>inhoud</p>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)
	var logo *Box
	for i := range p.Boxes {
		if p.Boxes[i].Img != nil && p.Boxes[i].Href != "" {
			logo = &p.Boxes[i]
		}
	}
	if logo == nil || logo.R.Dx() != 28 {
		t.Fatalf("logo-slot niet gevuld met het site-icoon: %+v", logo)
	}
	if !isRootHref(logo.Href) {
		t.Fatalf("logo hoort naar de voorpagina te linken: %q", logo.Href)
	}
}
