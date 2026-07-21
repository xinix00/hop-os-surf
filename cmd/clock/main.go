// Clock is de eerste GUI-demo-app (docs/gui-ontwerp.md §8, P1): een analoge
// klok die via SURF op een display-node tekent — vanaf elke node in het
// cluster. Kill de node waar hij draait en laat HOP hem elders herstarten:
// het window komt vanzelf terug (window.Present herverbindt en stuurt een vol
// frame). De jobspec-env SURF_ADDR wijst de display aan (host:poort).
//
// De app zelf is clock.Drive — deze main is alleen het tamago-bootje
// (netstack, env, exit); de host-desktop (cmd/desktop) rijdt dezelfde Drive.
package main

import (
	"fmt"

	"github.com/xinix00/hop-os-surf/app/clock"
	"github.com/xinix00/hop-os-surf/stack/window"
	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"
)

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
	win, err := window.Open(addr, name, clock.Size, clock.Size, app.Logf)
	if err != nil {
		app.Logf("clock: open %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("clock: window open on %s", addr)

	app.Logf("clock: %v", clock.Drive(win, app.Logf))
	app.Exit(1)
}
