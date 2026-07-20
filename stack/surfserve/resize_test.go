package surfserve

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/window"
)

// fetchScreen haalt /screen.png op en decodeert hem.
func fetchScreen(t *testing.T, base string) image.Image {
	t.Helper()
	resp, err := http.Get(base + "/screen.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	img, err := png.Decode(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestResizeFlow bewijst de WM-gestuurde maatvoering end-to-end: CREATE is
// een hint, CONFIGURE is de wet, en een app die op de nieuwe maat hertekent
// vult zijn cel exact — geen dooie randen.
func TestResizeFlow(t *testing.T) {
	comp := compositor.New(320, 200)
	srv := New(comp, t.Logf)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.ServeSURF(l)
	web := httptest.NewServer(srv.Handler())
	defer web.Close()

	red := color.RGBA{0xEE, 0x10, 0x10, 0xFF}
	fill := func(win *window.Window) {
		img := win.Image() // pakt de actuele WM-maat
		draw.Draw(img, img.Bounds(), image.NewUniform(red), image.Point{}, draw.Src)
		if err := win.Present(); err != nil {
			t.Fatal(err)
		}
	}

	// Window 1 met een kleine hint: de WM maakt hem (bijna) schermvullend.
	win1, err := window.Open(l.Addr().String(), "big @ node-a", 60, 40, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win1.Close()
	waitResize(t, win1)
	if w, _ := win1.Size(); w < 250 {
		t.Fatalf("single window got width %d, want near-fullscreen", w)
	}
	fill(win1)
	// De present wordt asynchroon door de sessie verwerkt: pollen.
	var full int
	eventually(t, "full-cell fill visible", func() bool {
		full = countColor(t, web.URL, red)
		return full >= 250*120
	})

	// Window 2 erbij: win1 krijgt een nieuwe (kleinere) maat en hertekent.
	win2, err := window.Open(l.Addr().String(), "half @ node-b", 60, 40, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win2.Close()
	waitResize(t, win1)
	if w, _ := win1.Size(); w > 320/2 {
		t.Fatalf("win1 width %d after second window, want ~half", w)
	}
	fill(win1)
	eventually(t, "win1 fills exactly its shrunken cell", func() bool {
		n := countColor(t, web.URL, red)
		return n > 100*100 && n < full*6/10
	})
}

// waitResize wacht op het eerstvolgende KindResize-event.
func waitResize(t *testing.T, win *window.Window) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-win.Events():
			if ev.Kind == window.KindResize {
				return
			}
		case <-deadline:
			t.Fatal("no resize event")
		}
	}
}

// countColor telt pixels met exact deze kleur in /screen.png.
func countColor(t *testing.T, base string, want color.RGBA) int {
	t.Helper()
	img := fetchScreen(t, base)
	b := img.Bounds()
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(bl>>8) == want.B {
				n++
			}
		}
	}
	return n
}
