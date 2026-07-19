package browse

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"testing"
)

// TestScreenshotDemo rendert de demo-pagina van de browser (menu, kleuren,
// vet, centreren, afbeelding — de hele "simpele renderer") naar
// $SCREENSHOT_OUT. Zonder die env slaat hij over — in CI is dit een no-op.
//
//	SCREENSHOT_OUT=$PWD/docs/browser-demo.png go test ./browse -run Screenshot
func TestScreenshotDemo(t *testing.T) {
	out := os.Getenv("SCREENSHOT_OUT")
	if out == "" {
		t.Skip("set SCREENSHOT_OUT=<file.png> to render the browser demo")
	}

	logo := image.NewRGBA(image.Rect(0, 0, 48, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			// Een herkenbaar blokpatroon in hop-kleuren.
			c := color.RGBA{0x2D, 0x6C, 0xDF, 0xFF}
			if (x/8+y/8)%2 == 0 {
				c = color.RGBA{0x39, 0xB5, 0x6A, 0xFF}
			}
			logo.SetRGBA(x, y, c)
		}
	}
	var logoPNG bytes.Buffer
	png.Encode(&logoPNG, logo)

	mux := http.NewServeMux()
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Write(logoPNG.Bytes())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><style>
nav a { font-weight: bold; }
h1 { text-align: center; color: #1a4fc4; }
.warn { color: #c00; font-weight: bold; }
.tip  { background-color: #d8f0dc; }
.badge { background-color: #1a4fc4; color: white; }
.verborgen { display: none; }
</style></head><body>
<nav><a href="/">home</a><a href="/docs">docs</a><a href="/status">status</a><a href="/kvm">kvm</a></nav>
<hr>
<h1>surf: de simpele renderer</h1>
<p><img src="/logo.png" alt="logo"> Dit is <b>CSS</b> op een 8x8-font:
<span class="warn">waarschuwingen in rood</span>,
<span class="tip">tips met een achtergrond</span> en
<span class="badge"> badges </span> &mdash; en een
<mark>gemarkeerde</mark> passage.</p>
<div class="verborgen">Deze cookiebanner zie je dus niet.</div>
<ul><li>menu's via <code>display:flex</code> en <code>&lt;nav&gt;</code></li>
<li>kleuren uit stylesheets, inline styles en <code>bgcolor</code></li>
<li><b>pseudo-vet</b>, gecentreerde koppen, <code>display:none</code></li></ul>
<hr>
<p><center>-- <a href="/docs">meer weten?</a> --</center></p>
</body></html>`))
	})

	s := NewSessionHandler(mux)
	if err := s.Go("hop.local"); err != nil {
		t.Fatalf("Go: %v", err)
	}
	v := View{Addr: s.URL(), Page: s.Layout(480), Status: "ok (3ms) http://hop.local/"}
	img := image.NewRGBA(image.Rect(0, 0, 480, 360))
	v.Render(img)

	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("browser-demo geschreven naar %s", out)
}
