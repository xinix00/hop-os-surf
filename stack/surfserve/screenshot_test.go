package surfserve

import (
	"image/color"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/app/calc"
	"github.com/xinix00/hop-os-surf/app/clock"
	"github.com/xinix00/hop-os-surf/app/hopapi"
	"github.com/xinix00/hop-os-surf/app/launcher"
	"github.com/xinix00/hop-os-surf/app/taskman"
	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/scene"
	"github.com/xinix00/hop-os-surf/stack/surf"
	"github.com/xinix00/hop-os-surf/stack/window"
)

// TestScreenshotDemo is het meetinstrument als dev-tool: hij bouwt een echte
// demo-desktop (klok + telemetrie-blokjes, via de volledige window→SURF→
// compositor-keten) en schrijft /screen.png naar $SCREENSHOT_OUT. Zonder die
// env slaat hij over — in CI is dit een no-op.
//
//	SCREENSHOT_OUT=out/desktop-demo.png go test ./stack/surfserve -run Screenshot
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

	// Vier windows van vier "nodes": de klok als pixel-app (P1), en taskman,
	// launcher en calc als échte scene-apps (P2) — dezelfde keten als op het
	// device: SCENE de lijn over, display-side gerenderd.
	clk, err := window.Open(l.Addr().String(), "clock @ node-a", 320, 320, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	tconn := scene.Open(l.Addr().String(), "taskman @ node-b", 480, 360, t.Logf)
	defer tconn.Close()
	demoTaskman(t, tconn)
	lconn := scene.Open(l.Addr().String(), "launcher @ node-a", 480, 360, t.Logf)
	defer lconn.Close()
	demoLauncher(t, lconn)
	cconn := scene.Open(l.Addr().String(), "calc @ node-c", 240, 320, t.Logf)
	defer cconn.Close()
	var cc calc.Calc
	for _, k := range []byte("12+34=") {
		cc.Press(k)
	}
	croot, cdisp := calc.Tree(func(byte) {})
	cdisp.Text = calc.Line(&cc)
	if err := cconn.Show(croot); err != nil {
		t.Fatal(err)
	}

	// Cursor erbij (de KVM-aanwijzer); hertekenen per poging zodat late
	// CONFIGUREs (WM-maten) altijd verwerkt worden — apps vullen hun cel.
	srv.Input(surf.Input{Kind: surf.InputMove, X: 700, Y: 400})
	demoNow := time.Date(2026, 7, 19, 10, 8, 42, 0, time.UTC)
	eventually(t, "all windows composed at final sizes", func() bool {
		// De klok is de enige pixel-app: die moet zijn definitieve WM-maat
		// hebben en hertekenen (scene-surfaces re-flowt de display zelf).
		if ww, _ := clk.Size(); ww < 500 {
			return false
		}
		clock.Draw(clk.Image(), demoNow)
		if err := clk.Present(); err != nil {
			t.Fatal(err)
		}
		_, ok1 := findColor(t, web.URL, color.RGBA{0xFF, 0x6E, 0x50, 0xFF}) // secondewijzer
		_, ok2 := findColor(t, web.URL, color.RGBA{0x23, 0x2D, 0x46, 0xFF}) // scene-knoppen
		_, ok3 := findColor(t, web.URL, color.RGBA{0x3A, 0x46, 0x63, 0xFF}) // scene-list-rand
		return ok1 && ok2 && ok3
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

// demoTaskman toont een gevulde taskmanager over een echte scene-verbinding:
// drie nodes, vier jobs — dezelfde weg als op een device.
func demoTaskman(t *testing.T, conn *scene.Conn) {
	now := time.Now()
	a := taskman.New(conn)
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	a.SetData(
		hopapi.Status{ClusterName: "dev", Agents: 3, Jobs: 4, TotalPlaced: 5,
			Placed: map[string]int{"web": 2, "redis": 1, "worker": 1, "clock": 1}},
		[]hopapi.Agent{
			{ID: "node-a", Endpoint: "http://10.0.0.1:8080", Version: "v0.3.1", LastSeen: now.Add(-2 * time.Second)},
			{ID: "node-b", Endpoint: "http://10.0.0.2:8080", Version: "v0.3.1", LastSeen: now.Add(-18 * time.Second)},
			{ID: "node-c", Endpoint: "http://10.0.0.3:8080", Version: "v0.3.0", LastSeen: now.Add(-2 * time.Minute)},
		},
		[]hopapi.Job{
			{Name: "web", Command: "./server", Count: 2},
			{Name: "redis", Image: "redis:7", Command: "redis-server"},
			{Name: "worker", Command: "./worker", Count: 2},
			{Name: "clock", Driver: "hop", Count: 2},
		})
}

// demoLauncher toont een gevulde launcher: de catalogus uit docs/config.md,
// met twee al-draaiende apps.
func demoLauncher(t *testing.T, conn *scene.Conn) {
	apps, err := launcher.ParseCatalog(`[
		{"name":"clock","driver":"hop"},{"name":"calc","driver":"hop"},
		{"name":"browser","driver":"hop"},{"name":"taskman","driver":"hop"}]`)
	if err != nil {
		t.Fatal(err)
	}
	m := launcher.NewMenu(conn, apps)
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	m.SetData(hopapi.Status{ClusterName: "dev", Agents: 3, Jobs: 4, TotalPlaced: 5,
		Placed: map[string]int{"clock": 1, "taskman": 1}})
}
