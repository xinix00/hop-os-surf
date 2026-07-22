package browse

import (
	"image"
	"net/http"
	"testing"
)

// TestBreedteSchakelt: @media wordt tegen de framebreedte geëvalueerd —
// hetzelfde document is op 480 de mobiele site en op 1024 de desktop.
func TestBreedteSchakelt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
.desktopmenu { display: none; }
@media (min-width: 900px) {
  .desktopmenu { display: block; }
  .mobielmenu { display: none; }
}
</style></head><body>
<div class="mobielmenu">hamburger</div>
<div class="desktopmenu">alle rubrieken</div>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	mob := s.Layout(480)
	if find(mob, "hamburger") == nil || find(mob, "alle rubrieken") != nil {
		t.Fatalf("op 480 hoort alleen het mobiele menu te staan")
	}
	desk := s.Layout(1024)
	if find(desk, "alle rubrieken") == nil || find(desk, "hamburger") != nil {
		t.Fatalf("op 1024 hoort alleen het desktopmenu te staan")
	}
}

// TestDonkerePagina: een site die zichzelf donker verklaart (op html of
// body) krijgt een donker paginacanvas — ook onder de content — en de
// tekst klapt leesbaar licht (contrastbewaking).
func TestDonkerePagina(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
html { background: #1a1a1a; color: #f4f4f4; }
</style></head><body><p>witte tekst op zwart</p></body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)
	if !p.HasBG || luma(p.BG) > 80 {
		t.Fatalf("paginacanvas niet donker: HasBG=%v BG=%v", p.HasBG, p.BG)
	}
	txt := find(p, "witte tekst")
	if txt == nil || luma(txt.Col) < 128 {
		t.Fatalf("tekst op donker canvas niet licht: %+v", txt)
	}
	// En het canvas onder de content is écht donker (View.Render vult ermee).
	v := View{Page: p}
	img := image.NewRGBA(image.Rect(0, 0, 480, 360))
	v.Render(img)
	if got := img.RGBAAt(240, 340); luma(got) > 80 {
		t.Fatalf("canvas onder de content niet donker: %v", got)
	}
}

// TestFlexBasis: rijen wrappen (flex-wrap), en reverse draait de volgorde.
func TestFlexBasis(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
.rij { display: flex; flex-wrap: wrap; }
.omgekeerd { display: flex; flex-direction: column-reverse; }
.kaart { background: #eee; }
</style></head><body>
<div class="rij">
<div class="kaart">een</div><div class="kaart">twee</div><div class="kaart">drie</div>
<div class="kaart">vier</div><div class="kaart">vijf</div><div class="kaart">zes</div>
</div>
<div class="omgekeerd"><div>onderste</div><div>bovenste</div></div>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)
	een, twee, vier := find(p, "een"), find(p, "twee"), find(p, "vier")
	if een == nil || twee == nil || vier == nil {
		t.Fatal("kaarten niet gevonden")
	}
	if een.R.Min.Y != twee.R.Min.Y || twee.R.Min.X <= een.R.Max.X {
		t.Fatalf("kaarten horen naast elkaar: %v vs %v", een.R, twee.R)
	}
	if vier.R.Min.Y <= een.R.Min.Y {
		t.Fatalf("flex-wrap: vierde kaart hoort op een volgende rij: %v vs %v", vier.R, een.R)
	}
	boven, onder := find(p, "bovenste"), find(p, "onderste")
	if boven == nil || onder == nil || boven.R.Min.Y >= onder.R.Min.Y {
		t.Fatalf("column-reverse hoort de volgorde om te draaien: boven=%v onder=%v", boven, onder)
	}
}

// TestFlexAutoMarge: margin-left:auto duwt een rij-item naar rechts, en
// kaartvlakken in één rij worden even hoog (align-items: stretch).
func TestFlexAutoMarge(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
header { display: flex; }
nav { margin-left: auto; }
.rij { display: flex; flex-direction: row; }
.kaart { background: #eee; border: 1px solid #999; }
</style></head><body>
<header><span>logo</span><nav><a href="/x">menu</a></nav></header>
<div class="rij">
<div class="kaart">kort</div>
<div class="kaart">deze kaart heeft veel meer tekst en wordt dus hoger dan de korte</div>
</div>
</body></html>`))
	})
	s := NewSessionHandler(mux)
	if err := s.Go("example.com"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	p := s.Layout(480)
	menu := find(p, "menu")
	if menu == nil || menu.R.Max.X < 480-pad-40 {
		t.Fatalf("margin-left:auto hoort het menu rechts te zetten: %+v", menu)
	}
	var vlakken []*Box
	for i := range p.Boxes {
		if p.Boxes[i].HasBrd {
			vlakken = append(vlakken, &p.Boxes[i])
		}
	}
	if len(vlakken) != 2 {
		t.Fatalf("wil 2 kaartvlakken, kreeg %d", len(vlakken))
	}
	if vlakken[0].R.Dy() != vlakken[1].R.Dy() {
		t.Fatalf("kaarten in één rij horen even hoog (stretch): %v vs %v", vlakken[0].R, vlakken[1].R)
	}
}
