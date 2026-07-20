package surfserve

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/surf"
	"github.com/xinix00/hop-os-surf/stack/window"
)

// recListener onthoudt geaccepteerde verbindingen zodat de test er één kan
// doorknippen — de host-versie van "node kwijt".
type recListener struct {
	net.Listener
	conns chan net.Conn
}

func (l *recListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.conns <- c
	}
	return c, err
}

// eventually pollt tot ok of de deadline — de keten is asynchroon (leesgoroutine,
// compose-lazy), dus asserts zijn convergentie-checks.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

// TestEndToEnd: de hele P1-keten op de ontwikkelmachine.
func TestEndToEnd(t *testing.T) {
	comp := compositor.New(320, 200)
	srv := New(comp, t.Logf)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recListener{Listener: l, conns: make(chan net.Conn, 4)}
	go srv.ServeSURF(rec)

	web := httptest.NewServer(srv.Handler())
	defer web.Close()

	// App-kant: window openen, rood vullen, presenteren.
	win, err := window.Open(l.Addr().String(), "itest @ node-x", 60, 40, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Close()
	red := color.RGBA{0xEE, 0x10, 0x10, 0xFF}
	draw.Draw(win.Image(), win.Image().Bounds(), image.NewUniform(red), image.Point{}, draw.Src)
	if err := win.Present(); err != nil {
		t.Fatal(err)
	}

	// Het meetinstrument ziet het window: /screen.png bevat rood.
	var redAt image.Point
	eventually(t, "red window visible in /screen.png", func() bool {
		p, ok := findColor(t, web.URL, red)
		redAt = p
		return ok
	})

	// Browser-KVM-terugweg: klik op het rode vlak → InputButton bij de app,
	// in lokale window-coördinaten.
	postInput(t, web.URL, fmt.Sprintf(`{"k":"btn","c":0,"v":1,"x":%d,"y":%d}`, redAt.X, redAt.Y))
	eventually(t, "button event reaches app", func() bool {
		select {
		case ev := <-win.Events():
			if ev.Kind != surf.InputButton || ev.Value != 1 {
				return false
			}
			if int(ev.X) >= 60 || int(ev.Y) >= 40 {
				t.Fatalf("button coords not window-local: %d,%d", ev.X, ev.Y)
			}
			return true
		default:
			return false
		}
	})

	// Toets → focus-window (dat is dit window: klik heeft focus gezet).
	postInput(t, web.URL, `{"k":"key","c":65,"v":1}`)
	eventually(t, "key event reaches app", func() bool {
		select {
		case ev := <-win.Events():
			return ev.Kind == surf.InputKey && ev.Code == 65 && ev.Value == 1
		default:
			return false
		}
	})

	// Failover: knip de sessie door → het window blijft gewoon staan
	// (Dereks wet 19-07: verbinding kwijt ≠ app dood — parkeren, niet
	// verwijderen; alleen CLOSE of de 45s-grace ruimt op). Het laatste
	// frame blijft bevroren zichtbaar.
	conn := <-rec.conns
	conn.Close()
	time.Sleep(300 * time.Millisecond) // de display mag het window NIET weghalen
	if _, ok := findColor(t, web.URL, red); !ok {
		t.Fatal("window verdween bij verbindingsverlies — moest geparkeerd blijven")
	}
	// Een app die blijft presenteren (zoals elke echte GUI-app) heelt
	// zichzelf: de leesgoroutine markeert de sessie dood, de eerstvolgende
	// Present herverbindt met HELLO+CREATE+vol frame en adopteert zijn
	// geparkeerde window (geen relayout, geen tweede window). Eén frame mag
	// verloren gaan (at-most-once), het wíndow niet.
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			if err := win.Present(); err != nil {
				done <- err
				return
			}
			select {
			case <-stop:
				done <- nil
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
	eventually(t, "window back after reconnect", func() bool {
		_, ok := findColor(t, web.URL, red)
		return ok
	})
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// Bewust afscheid (CLOSE) ruimt wél meteen op — het onderscheid met de
	// geparkeerde verbinding-kwijt-tak hierboven.
	win.Close()
	eventually(t, "window weg na CLOSE", func() bool {
		_, ok := findColor(t, web.URL, red)
		return !ok
	})
}

// findColor haalt /screen.png op en zoekt een pixel in exact deze kleur.
func findColor(t *testing.T, base string, want color.RGBA) (image.Point, bool) {
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
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(bl>>8) == want.B {
				return image.Pt(x, y), true
			}
		}
	}
	return image.Point{}, false
}

func postInput(t *testing.T, base, body string) {
	t.Helper()
	resp, err := http.Post(base+"/input", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /input: %s", resp.Status)
	}
}

// TestKVMPage: de KVM-pagina wordt geserveerd en bevat de event-bedrading.
func TestKVMPage(t *testing.T) {
	srv := New(compositor.New(64, 64), t.Logf)
	web := httptest.NewServer(srv.Handler())
	defer web.Close()
	resp, err := http.Get(web.URL + "/kvm")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/screen.png", "/input", "keydown", "mousedown"} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("kvm page misses %q", want)
		}
	}
}
