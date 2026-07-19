// Calc is de tweede GUI-demo-app — en de eerste échte interactieve: een
// rekenmachine die ergens in het cluster draait terwijl jij hem bedient
// vanuit de browser-KVM van de display-node (of straks een USB-toetsenbord).
// Muisklikken komen binnen als window-lokale InputButton-events, toetsen als
// browser-keycodes; calc.Hit/calc.Key vertalen beide naar dezelfde Press.
package main

import (
	"fmt"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"

	"github.com/xinix00/hop-os-surf/calc"
	"github.com/xinix00/hop-os-surf/surf"
	"github.com/xinix00/hop-os-surf/window"
)

func main() {
	app := applib.Init()

	if _, err := appnet.Up(app); err != nil {
		app.Logf("net: %v", err)
		app.Exit(1)
	}
	addr := app.Env("SURF_ADDR")
	if addr == "" {
		app.Logf("calc: SURF_ADDR not set (want <display-node>:7878)")
		app.Exit(1)
	}

	name := fmt.Sprintf("calc @ slot %d", app.Slot)
	win, err := window.Open(addr, name, 240, 320, app.Logf)
	if err != nil {
		app.Logf("calc: open %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("calc: window open on %s", addr)

	var c calc.Calc
	var hover byte
	redraw := func() {
		img := win.Image() // elke frame opvragen: na een resize is hij nieuw
		calc.Render(img, &c, hover)
		if err := win.Present(); err != nil {
			app.Logf("calc: present: %v", err)
			app.Exit(1)
		}
	}
	redraw()

	// De hele app is event-gedreven: geen ticker, geen polling — precies
	// het "idle mag geen CPU kosten"-principe. Hover herrendert alleen bij
	// het wisselen van knop (de damage-stream laat het live zien).
	for ev := range win.Events() {
		var key byte
		switch {
		case ev.Kind == window.KindResize:
			// herteken op de nieuwe maat (key blijft 0)
		case ev.Kind == surf.InputMove:
			h := calc.Hit(win.Image().Bounds(), int(ev.X), int(ev.Y))
			if h == hover {
				continue
			}
			hover = h
		case ev.Kind == surf.InputButton && ev.Value == 1:
			key = calc.Hit(win.Image().Bounds(), int(ev.X), int(ev.Y))
		case ev.Kind == surf.InputKey && ev.Value == 1:
			key = calc.Key(ev.Code)
		default:
			continue
		}
		if key != 0 {
			c.Press(key)
			app.Logf("calc: key %q -> %s", key, c.Display())
		}
		redraw()
	}
	app.Exit(1)
}
