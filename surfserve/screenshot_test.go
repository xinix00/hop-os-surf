package surfserve

import (
	"image"
	"image/color"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/calc"
	"github.com/xinix00/hop-os-surf/compositor"
	"github.com/xinix00/hop-os-surf/face"
	"github.com/xinix00/hop-os-surf/surf"
	"github.com/xinix00/hop-os-surf/window"
)

// TestScreenshotDemo is het meetinstrument als dev-tool: hij bouwt een echte
// demo-desktop (klok + telemetrie-blokjes, via de volledige window→SURF→
// compositor-keten) en schrijft /screen.png naar $SCREENSHOT_OUT. Zonder die
// env slaat hij over — in CI is dit een no-op.
//
//	SCREENSHOT_OUT=out/desktop-demo.png go test ./app/display/surfserve -run Screenshot
func TestScreenshotDemo(t *testing.T) {
	out := os.Getenv("SCREENSHOT_OUT")
	if out == "" {
		t.Skip("set SCREENSHOT_OUT=<file.png> to render the demo desktop")
	}

	comp := compositor.New(1280, 720)
	srv := New(comp, t.Logf)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.ServeSURF(l)
	web := httptest.NewServer(srv.Handler())
	defer web.Close()

	// Drie windows van drie "nodes": klok, telemetrie en de rekenmachine.
	clk, err := window.Open(l.Addr().String(), "clock @ node-a", 320, 320, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	tel, err := window.Open(l.Addr().String(), "sensors @ node-b", 420, 300, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	cw, err := window.Open(l.Addr().String(), "calc @ node-c", 240, 320, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	var cc calc.Calc
	for _, k := range []byte("12+34=") {
		cc.Press(k)
	}

	// Cursor erbij (de KVM-aanwijzer); hertekenen per poging zodat late
	// CONFIGUREs (WM-maten) altijd verwerkt worden — apps vullen hun cel.
	srv.Input(surf.Input{Kind: surf.InputMove, X: 700, Y: 400})
	eventually(t, "all windows composed at final sizes", func() bool {
		// Pas klaar als iedereen zijn definitieve WM-maat heeft (3 windows
		// op 1280 breed = 2 kolommen → cellen van ~630 px breed).
		for _, w := range []*window.Window{clk, tel, cw} {
			if ww, _ := w.Size(); ww < 500 {
				return false
			}
		}
		face.Draw(clk.Image(), time.Date(2026, 7, 19, 10, 8, 42, 0, time.UTC))
		if err := clk.Present(); err != nil {
			t.Fatal(err)
		}
		drawBars(tel.Image())
		if err := tel.Present(); err != nil {
			t.Fatal(err)
		}
		calc.Render(cw.Image(), &cc)
		if err := cw.Present(); err != nil {
			t.Fatal(err)
		}
		_, ok1 := findColor(t, web.URL, color.RGBA{0xFF, 0x6E, 0x50, 0xFF}) // secondewijzer
		_, ok2 := findColor(t, web.URL, color.RGBA{0x39, 0xB5, 0x6A, 0xFF}) // groene balk/=-knop
		return ok1 && ok2
	})
	resp, err := http.Get(web.URL + "/screen.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	t.Logf("demo desktop written to %s", out)
}

// drawBars tekent een simpel instrumentenpaneel: zes meetbalkjes.
func drawBars(img *image.RGBA) {
	bg := color.RGBA{0x18, 0x22, 0x36, 0xFF}
	green := color.RGBA{0x39, 0xB5, 0x6A, 0xFF}
	track := color.RGBA{0x24, 0x30, 0x4A, 0xFF}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetRGBA(x, y, bg)
		}
	}
	vals := []int{82, 34, 61, 12, 95, 48}
	for i, v := range vals {
		y0 := 24 + i*44
		for y := y0; y < y0+20; y++ {
			for x := 20; x < b.Dx()-20; x++ {
				col := track
				if (x-20)*100 < v*(b.Dx()-40) {
					col = green
				}
				img.SetRGBA(x, y, col)
			}
		}
	}
}
