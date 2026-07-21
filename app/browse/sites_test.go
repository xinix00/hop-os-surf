package browse

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScreenshotSites rendert échte sites (live netwerk, mobile view) naar
// docs/browser-<naam>.png — één vensterschot (480x360) per site, elke
// testrun vers, zodat je gewoon kunt meekijken hoe herkenbaar het web is.
// SITE_LIST=a,b kiest andere sites; SITE_SHOTS=<dir> stuurt de schoten
// ergens anders heen en schrijft er dan ook de volledige pagina bij (voor
// layout-debugwerk). Geen netwerk → skip, de gate blijft offline groen.
func TestScreenshotSites(t *testing.T) {
	dir, full := "../../docs", false
	if v := os.Getenv("SITE_SHOTS"); v != "" {
		dir, full = v, true
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sites := []string{"tweakers.net", "nrc.nl", "nu.nl"}
	if v := os.Getenv("SITE_LIST"); v != "" {
		sites = strings.Split(v, ",")
	}
	gerenderd := 0
	for _, site := range sites {
		s := NewSession()
		if err := s.Go(site); err != nil {
			t.Logf("%s: %v (overgeslagen)", site, err)
			continue
		}
		gerenderd++
		v := View{Addr: s.URL(), Page: s.Layout(480), Status: "ok " + s.URL()}
		naam := strings.ReplaceAll(site, "/", "_")
		if i := strings.IndexByte(naam, '.'); i > 0 {
			naam = naam[:i] // tweakers.net -> browser-tweakers.png
		}
		schrijf := func(bestand string, h int) {
			img := image.NewRGBA(image.Rect(0, 0, 480, h))
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
		schrijf("browser-"+naam+".png", 360)
		if full {
			schrijf(strings.ReplaceAll(site, "/", "_")+".png", 1800)
		}
		t.Logf("%s: hoogte %dpx, %d boxes -> %s/browser-%s.png", site, v.Page.Height, len(v.Page.Boxes), dir, naam)
	}
	if gerenderd == 0 {
		t.Skip("geen enkele site bereikbaar: netwerk weg? (gate blijft groen)")
	}
}
