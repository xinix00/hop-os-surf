// Clock is de eerste GUI-demo-app (docs/gui-ontwerp.md §8, P1): een analoge
// klok die via SURF op een display-node tekent — vanaf elke node in het
// cluster. Kill de node waar hij draait en laat HOP hem elders herstarten:
// het window komt vanzelf terug (window.Present herverbindt en stuurt een vol
// frame). De jobspec-env SURF_ADDR wijst de display aan (host:poort).
package main

import (
	"fmt"
	"time"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"
	"github.com/xinix00/hop-os-surf/window"
	"github.com/xinix00/hop-os-surf/face"
	"github.com/xinix00/hop-os-surf/surf"
)

const size = 320

func main() {
	app := applib.Init()

	if _, err := appnet.Up(app); err != nil {
		app.Logf("net: %v", err)
		app.Exit(1)
	}
	addr := app.Env("SURF_ADDR")
	if addr == "" {
		app.Logf("clock: SURF_ADDR not set (want <display-node>:7878)")
		app.Exit(1)
	}

	// Herkomst in de titel: het cluster hoort zichtbaar te zijn in de chrome.
	name := fmt.Sprintf("clock @ slot %d", app.Slot)
	win, err := window.Open(addr, name, size, size, app.Logf)
	if err != nil {
		app.Logf("clock: open %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("clock: window open on %s", addr)

	// Input van de display (browser-KVM!): kliks loggen — het bewijs dat de
	// terugweg tot in een remote app reikt.
	go func() {
		for ev := range win.Events() {
			if ev.Kind == surf.InputButton && ev.Value == 1 {
				app.Logf("clock: click at %d,%d", ev.X, ev.Y)
			}
		}
	}()

	last := -1
	for {
		now := time.Now()
		if s := now.Second(); s != last {
			last = s
			face.Draw(win.Image(), now)
			if err := win.Present(); err != nil {
				app.Logf("clock: present: %v", err)
				app.Exit(1)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
