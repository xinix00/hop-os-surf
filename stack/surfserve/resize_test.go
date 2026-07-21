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
// een hint, CONFIGURE is de wet — de zwevende WM (20-07) honoreert de hint
// en klemt alleen wat niet op het werkvlak past. De app die op de
// toegekende maat hertekent vult zijn window exact.
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

	// Window 1: de hint wordt gehonoreerd — en blijft staan.
	win1, err := window.Open(l.Addr().String(), "one @ node-a", 100, 80, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win1.Close()
	waitResize(t, win1)
	if w, h := win1.Size(); w != 100 || h != 80 {
		t.Fatalf("hint hoort gehonoreerd: %dx%d, want 100x80", w, h)
	}
	fill(win1)
	// De present wordt asynchroon door de sessie verwerkt: pollen. Het
	// resize-hoekje (drie diagonale streepjes chrome, 21-07) snoept een paar
	// hoekpixels van de content — vandaar de kleine marge.
	eventually(t, "hint-sized fill visible", func() bool {
		n := countColor(t, web.URL, red)
		return n >= 100*80-40 && n <= 100*80
	})

	// Window 2 erbij: win1 verandert NIET van maat (het hele punt).
	win2, err := window.Open(l.Addr().String(), "two @ node-b", 60, 40, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win2.Close()
	waitResize(t, win2)
	if w, h := win1.Size(); w != 100 || h != 80 {
		t.Fatalf("win1 hoort onaangeroerd na een tweede window: %dx%d", w, h)
	}

	// Een reuze-hint wordt geklemd op het werkvlak: dáár is CONFIGURE de wet.
	win3, err := window.Open(l.Addr().String(), "big @ node-c", 999, 999, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win3.Close()
	waitResize(t, win3)
	if w, h := win3.Size(); w >= 320 || h >= 200 {
		t.Fatalf("reuze-hint hoort geklemd: %dx%d", w, h)
	}
	fill(win3) // hertekenen op de geklemde maat: geen stale damage meer
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
