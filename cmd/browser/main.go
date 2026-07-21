// Browser is de derde GUI-app — en de eerste die het cluster met de
// buitenwereld verbindt: gost-dom haalt en parset de pagina (headless,
// pure Go — compileert gewoon onder tamago), browse/ layout hem op het
// 8x8-font, en het resultaat is een window als elk ander. Typen gaat in de
// adresbalk (Enter = laden), het wiel scrollt, links zijn klikbaar.
// SURF_HOME (optioneel) is de startpagina.
//
// De app zelf is browse.Drive — deze main is alleen het tamago-bootje
// (netstack, env, exit); de host-desktop (cmd/desktop) rijdt dezelfde Drive.
package main

import (
	"fmt"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"

	"github.com/xinix00/hop-os-surf/app/browse"
	"github.com/xinix00/hop-os-surf/stack/window"
)

func main() {
	app := applib.Init()

	if _, err := appnet.Up(app); err != nil {
		app.Logf("net: %v", err)
		app.Exit(1)
	}
	addr := app.Env("SURF_ADDR")
	if addr == "" {
		app.Logf("browser: SURF_ADDR not set (want <display-node>:7878)")
		app.Exit(1)
	}

	name := fmt.Sprintf("browser @ slot %d", app.Slot)
	win, err := window.Open(addr, name, 480, 360, app.Logf)
	if err != nil {
		app.Logf("browser: open %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("browser: window open on %s", addr)

	app.Logf("browser: %v", browse.Drive(win, app.Env("SURF_HOME"), app.Logf))
	app.Exit(1)
}
