package surfserve

import (
	"bufio"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/surf"
	"github.com/xinix00/hop-os-surf/stack/window"
)

// collectFrames leest de stream continu en levert per frame de rects op een
// channel — de test kan dan met timeouts asserteren, ook op afwezigheid,
// zonder blokkerende reads in de testbody.
func collectFrames(body io.Reader) <-chan []image.Rectangle {
	ch := make(chan []image.Rectangle, 64)
	rd := bufio.NewReader(body)
	go func() {
		defer close(ch)
		for {
			var lenb [4]byte
			if _, err := io.ReadFull(rd, lenb[:]); err != nil {
				return
			}
			p := make([]byte, binary.LittleEndian.Uint32(lenb[:]))
			if _, err := io.ReadFull(rd, p); err != nil {
				return
			}
			n := int(binary.LittleEndian.Uint16(p))
			rects := make([]image.Rectangle, n)
			off := 2
			for i := 0; i < n; i++ {
				x := int(binary.LittleEndian.Uint16(p[off:]))
				y := int(binary.LittleEndian.Uint16(p[off+2:]))
				w := int(binary.LittleEndian.Uint16(p[off+4:]))
				h := int(binary.LittleEndian.Uint16(p[off+6:]))
				rects[i] = image.Rect(x, y, x+w, y+h)
				off += 8
			}
			ch <- rects
		}
	}()
	return ch
}

// next wacht op één frame (of faalt na de deadline).
func next(t *testing.T, ch <-chan []image.Rectangle, what string) []image.Rectangle {
	t.Helper()
	select {
	case rects, ok := <-ch:
		if !ok {
			t.Fatalf("%s: stream closed", what)
		}
		return rects
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: no frame", what)
		return nil
	}
}

// quiet telt frames tot het stil is (assert-afwezigheid met korte deadline).
func quiet(ch <-chan []image.Rectangle, d time.Duration) int {
	n := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return n
			}
			n++
		case <-time.After(d):
			return n
		}
	}
}

// TestStream: kijkers krijgen eerst het volle scherm, daarna alleen de
// veranderde rechthoeken; muisbewegingen componeren niets meer.
func TestStream(t *testing.T) {
	comp := compositor.New(320, 200)
	srv := New(comp, t.Logf)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.ServeSURF(l)
	web := httptest.NewServer(srv.Handler())
	defer web.Close()

	resp, err := http.Get(web.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	frames := collectFrames(resp.Body)

	// Frame 1: het volledige scherm.
	rects := next(t, frames, "first frame")
	if len(rects) != 1 || rects[0] != image.Rect(0, 0, 320, 200) {
		t.Fatalf("first frame must be full screen, got %v", rects)
	}

	// App verbindt en presenteert vol.
	win, err := window.Open(l.Addr().String(), "w @ n", 60, 40, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Close()
	red := color.RGBA{0xEE, 0x10, 0x10, 0xFF}
	img := win.Image()
	draw.Draw(img, img.Bounds(), image.NewUniform(red), image.Point{}, draw.Src)
	if err := win.Present(); err != nil {
		t.Fatal(err)
	}
	next(t, frames, "frame after present")
	quiet(frames, 300*time.Millisecond)

	// Partiële present: het frame draagt alléén dat blokje.
	win.Image().SetRGBA(3, 3, color.RGBA{0x10, 0x10, 0xEE, 0xFF})
	if err := win.Present(image.Rect(2, 2, 10, 10)); err != nil {
		t.Fatal(err)
	}
	rects = next(t, frames, "partial frame")
	total := 0
	for _, r := range rects {
		total += r.Dx() * r.Dy()
	}
	if total > 32*32 {
		t.Fatalf("partial present produced %dpx damage, want tiny: %v", total, rects)
	}
	quiet(frames, 200*time.Millisecond)

	// Muisbewegingen componeren niets meer (cursor = browser/plane).
	srv.Input(surf.Input{Kind: surf.InputMove, X: 100, Y: 100})
	if n := quiet(frames, 300*time.Millisecond); n != 0 {
		t.Fatalf("cursor move must not compose, got %d frames", n)
	}
}
