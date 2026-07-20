// Clock is de eerste GUI-demo-app (docs/gui-ontwerp.md §8, P1): een analoge
// klok die via SURF op een display-node tekent — vanaf elke node in het
// cluster. Kill de node waar hij draait en laat HOP hem elders herstarten:
// het window komt vanzelf terug (window.Present herverbindt en stuurt een vol
// frame). De jobspec-env SURF_ADDR wijst de display aan (host:poort).
package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/xinix00/hop-os-surf/app/clock"
	"github.com/xinix00/hop-os-surf/stack/surf"
	"github.com/xinix00/hop-os-surf/stack/window"
	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"
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
	// terugweg tot in een remote app reikt. Een resize van de WM forceert
	// een herteken (Image() heeft dan al de nieuwe maat).
	var resized atomic.Bool
	go func() {
		for ev := range win.Events() {
			switch {
			case ev.Kind == window.KindResize:
				resized.Store(true)
			case ev.Kind == surf.InputButton && ev.Value == 1:
				app.Logf("clock: click at %d,%d", ev.X, ev.Y)
			}
		}
	}()

	last := -1
	for {
		now := time.Now()
		res := resized.Swap(false)
		if s := now.Second(); s != last || res {
			full := last < 0 || res // eerste frame of net geresized: alles
			last = s
			img := win.Image()
			clock.Draw(img, now)
			var err error
			if full {
				err = win.Present()
			} else {
				// Alleen het wijzer-gebied de lijn over (ring en streepjes
				// veranderen nooit) — en met de stream-compressie erachter
				// is dat bijna niets.
				err = win.Present(clock.HandsBox(img.Bounds()))
			}
			if err != nil {
				app.Logf("clock: present: %v", err)
				app.Exit(1)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
