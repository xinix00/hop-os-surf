package browse

import (
	"image"
	"net/http"
	"testing"
)

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
