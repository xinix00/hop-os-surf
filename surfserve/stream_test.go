package surfserve

import (
	"bufio"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/compositor"
	"github.com/xinix00/hop-os-surf/surf"
	"github.com/xinix00/hop-os-surf/window"
)

// readFrame leest één stream-frame (u32 len | payload) met een deadline.
func readFrame(t *testing.T, rd *bufio.Reader) []byte {
	t.Helper()
	var lenb [4]byte
	if _, err := ioReadFull(rd, lenb[:]); err != nil {
		t.Fatalf("frame length: %v", err)
	}
	n := binary.LittleEndian.Uint32(lenb[:])
	p := make([]byte, n)
	if _, err := ioReadFull(rd, p); err != nil {
		t.Fatalf("frame body: %v", err)
	}
	return p
}

func ioReadFull(rd *bufio.Reader, p []byte) (int, error) {
	n := 0
	for n < len(p) {
		k, err := rd.Read(p[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// parseFrame geeft de rects en het pixelblok van een frame-payload.
func parseFrame(t *testing.T, p []byte) []image.Rectangle {
	t.Helper()
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
	return rects
}

// TestStream: kijkers krijgen eerst het volle scherm en daarna alleen de
// veranderde rechthoeken — idle is nul bytes.
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
	rd := bufio.NewReader(resp.Body)

	// Frame 1: het volledige scherm.
	rects := parseFrame(t, readFrame(t, rd))
	if len(rects) != 1 || rects[0] != image.Rect(0, 0, 320, 200) {
		t.Fatalf("first frame must be full screen, got %v", rects)
	}

	// App verbindt en presenteert: het volgende frame is (veel) kleiner dan
	// het scherm maar dekt het window.
	win, err := window.Open(l.Addr().String(), "w @ n", 60, 40, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Close()
	red := color.RGBA{0xEE, 0x10, 0x10, 0xFF}
	deadline := time.Now().Add(5 * time.Second)
	for {
		img := win.Image()
		draw.Draw(img, img.Bounds(), image.NewUniform(red), image.Point{}, draw.Src)
		if err := win.Present(); err != nil {
			t.Fatal(err)
		}
		rects = parseFrame(t, readFrame(t, rd))
		if len(rects) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no damage frame")
		}
	}

	// Cursorbeweging: een klein damage-frame (niet het volle scherm).
	srv.Input(surf.Input{Kind: surf.InputMove, X: 100, Y: 100})
	rects = parseFrame(t, readFrame(t, rd))
	total := 0
	for _, r := range rects {
		total += r.Dx() * r.Dy()
	}
	if total >= 320*200/2 {
		t.Fatalf("cursor move produced %dpx damage, want small rects: %v", total, rects)
	}
}
