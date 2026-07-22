package browse

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestScreenshotSites rendert échte sites (live netwerk) naar docs/ — per
// site twee schoten, elke testrun vers, zodat je zelf kunt confirmeren:
//
//	docs/browser-<naam>.png          mobiel venster (480x360)
//	docs/browser-<naam>-desktop.png  desktop (1280x700 — @media schakelt mee)
//
// SITE_LIST=a,b kiest andere sites; SITE_WIDTH=<px> rendert alléén die
// breedte; SITE_SHOTS=<dir> stuurt de schoten ergens anders heen en
// schrijft er dan ook de volledige pagina bij (voor layout-debugwerk).
// Geen netwerk → skip, de gate blijft offline groen.
func TestScreenshotSites(t *testing.T) {
	dir, full := "../../docs", false
	if v := os.Getenv("SITE_SHOTS"); v != "" {
		dir, full = v, true
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sites := []string{"tweakers.net", "nrc.nl", "nu.nl", "gethop.org/hop/", "wikipedia.org"}
	if v := os.Getenv("SITE_LIST"); v != "" {
		sites = strings.Split(v, ",")
	}
	// (breedte, hoogte, bestandssuffix) — standaard mobiel én desktop.
	type shot struct {
		w, h int
		sfx  string
	}
	shots := []shot{{480, 360, ""}, {1280, 700, "-desktop"}}
	if v := os.Getenv("SITE_WIDTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 200 {
			shots = []shot{{n, n * 3 / 4, ""}}
		}
	}
	gerenderd := 0
	for _, site := range sites {
		s := NewSession()
		if err := s.Go(site); err != nil {
			t.Logf("%s: %v (overgeslagen)", site, err)
			continue
		}
		gerenderd++
		naam := strings.ReplaceAll(site, "/", "_")
		if i := strings.IndexByte(naam, '.'); i > 0 {
			naam = naam[:i] // tweakers.net -> browser-tweakers.png
		}
		for _, sh := range shots {
			v := View{Addr: s.URL(), Page: s.Layout(sh.w), Status: "ok " + s.URL()}
			schrijf := func(bestand string, h int) {
				img := image.NewRGBA(image.Rect(0, 0, sh.w, h))
				v.Render(img)
				f, err := os.Create(filepath.Join(dir, bestand))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, img); err != nil {
					t.Fatal(err)
				}
			}
			schrijf("browser-"+naam+sh.sfx+".png", sh.h)
			if full {
				schrijf(strings.ReplaceAll(site, "/", "_")+sh.sfx+".png", 1800)
			}
			t.Logf("%s @ %dpx: hoogte %dpx, %d boxes -> %s/browser-%s%s.png",
				site, sh.w, v.Page.Height, len(v.Page.Boxes), dir, naam, sh.sfx)
		}
	}
	if gerenderd == 0 {
		t.Skip("geen enkele site bereikbaar: netwerk weg? (gate blijft groen)")
	}
}
