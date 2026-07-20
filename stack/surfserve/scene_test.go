package surfserve

import (
	"image"
	"net"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/scene"
	"github.com/xinix00/hop-os-surf/stack/surf"
)

// TestSceneEndToEnd: een scene-app door de echte keten — SCENE renderen,
// PATCH hertekent, klik komt als EVENT terug, en de PATCH-bytes blijven
// tientallen (de §8-P2-belofte).
func TestSceneEndToEnd(t *testing.T) {
	comp := compositor.New(400, 300)
	srv := New(comp, t.Logf)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go srv.ServeSURF(l)

	clicked := make(chan struct{}, 1)
	meter := scene.Gauge(0, 100, 10, "%").Sized(40)
	knop := scene.Button("go", func() { clicked <- struct{}{} }).Sized(30)
	root := scene.Col(4,
		scene.Label(scene.StyleHeading, "e2e").Sized(20),
		meter,
		knop,
	)

	c := scene.Open(l.Addr().String(), "e2e", 200, 150, t.Logf)
	if err := c.Show(root); err != nil {
		t.Fatal(err)
	}
	base := c.BytesSent()

	// De scene moet op het scherm komen (compose en zoek een accentpixel —
	// de meter-vulling); eventually: de server verwerkt async.
	deadline := time.Now().Add(2 * time.Second)
	for {
		comp.Compose()
		img, _ := comp.Snapshot()
		if hasAccent(img) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scene niet gerenderd binnen 2s")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// PATCH: waarde omhoog — tientallen bytes, geen pixels.
	c.SetVal(meter, 90)
	if d := c.BytesSent() - base; d > 32 {
		t.Fatalf("PATCH kostte %d bytes op de draad — wil ≤32", d)
	}

	// Klik op de knop: schermcoördinaten van het widget-rect opzoeken en
	// het EVENT moet de callback bereiken.
	time.Sleep(50 * time.Millisecond) // patch verwerken (rect is stabiel)
	kr := knop.Rect                   // client-side rects zijn niet gelayout; zoek via de view
	_ = kr
	sx, sy, ok := findButton(srv, comp)
	if !ok {
		t.Fatal("geen scene-knop gevonden op het scherm")
	}
	srv.Input(surf.Input{Kind: surf.InputButton, Code: 0, Value: 1, X: uint16(sx), Y: uint16(sy)})
	srv.Input(surf.Input{Kind: surf.InputButton, Code: 0, Value: 0, X: uint16(sx), Y: uint16(sy)})
	select {
	case <-clicked:
	case <-time.After(2 * time.Second):
		t.Fatal("klik-EVENT bereikte de app niet")
	}
}

// hasAccent zoekt de accentkleur (meter-vulling) in het beeld.
func hasAccent(img *image.RGBA) bool {
	for y := 0; y < img.Bounds().Dy(); y += 3 {
		for x := 0; x < img.Bounds().Dx(); x += 3 {
			c := img.RGBAAt(x, y)
			if c.R == 0x3B && c.G == 0x82 && c.B == 0xF6 {
				return true
			}
		}
	}
	return false
}

// findButton zoekt (display-side) het middelpunt van de knop in
// schermcoördinaten: de enige geregistreerde scene-view + zijn boom.
func findButton(s *Server, comp *compositor.Compositor) (x, y int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sur, v := range s.scenes {
		var btn *scene.Node
		var walk func(*scene.Node)
		walk = func(n *scene.Node) {
			if n.Kind == scene.KindButton {
				btn = n
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		v.mu.Lock()
		walk(v.root)
		v.mu.Unlock()
		if btn == nil {
			return 0, 0, false
		}
		c := btn.Rect.Min.Add(btn.Rect.Size().Div(2))
		// Content-lokaal → scherm: via SurfaceAt-omkering is er niet; de
		// compositor legt content op win.Min+titel — benader door alle
		// schermpunten te proberen die op deze surface mappen.
		for sy := 0; sy < 300; sy++ {
			for sx := 0; sx < 400; sx++ {
				if hs, lx, ly, hok := comp.SurfaceAt(sx, sy); hok && hs == sur && lx == c.X && ly == c.Y {
					return sx, sy, true
				}
			}
		}
	}
	return 0, 0, false
}
