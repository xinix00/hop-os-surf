// Browser is de derde GUI-app — en de eerste die het cluster met de
// buitenwereld verbindt: gost-dom haalt en parset de pagina (headless,
// pure Go — compileert gewoon onder tamago), browse/ layout hem op het
// 8x8-font, en het resultaat is een window als elk ander. Typen gaat in de
// adresbalk (Enter = laden), het wiel scrollt, links zijn klikbaar.
// SURF_HOME (optioneel) is de startpagina.
package main

import (
	"fmt"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"

	"github.com/xinix00/hop-os-surf/browse"
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

	sess := browse.NewSession()
	view := browse.View{}

	// navigate laadt een pagina en herlayout hem; bij een fout blijft de
	// oude pagina staan met de fout bovenin.
	navigate := func(load func() error) {
		view.Status = "laden..."
		img := win.Image()
		view.Render(img)
		win.Present()
		if err := load(); err != nil {
			view.Status = err.Error()
			app.Logf("browser: %v", err)
		} else {
			view.Status = ""
			view.Scroll = 0
		}
		view.Addr = sess.URL()
		view.Page = sess.Layout(win.Image().Bounds().Dx())
	}

	if home := app.Env("SURF_HOME"); home != "" {
		view.Addr = home
		navigate(func() error { return sess.Go(home) })
	} else {
		view.Page = sess.Layout(win.Image().Bounds().Dx())
		view.Addr = sess.URL()
	}

	redraw := func() {
		img := win.Image() // elke frame opvragen: na een resize is hij nieuw
		view.Render(img)
		if err := win.Present(); err != nil {
			app.Logf("browser: present: %v", err)
			app.Exit(1)
		}
	}
	redraw()

	var shift bool
	for ev := range win.Events() {
		switch {
		case ev.Kind == window.KindResize:
			// nieuwe breedte → nieuwe layout; scroll blijft zo goed als
			// mogelijk staan (ScrollBy(0) klemt hem op de nieuwe hoogte)
			view.Page = sess.Layout(int(ev.X))
			view.ScrollBy(0, int(ev.Y))
			redraw()

		case ev.Kind == surf.InputWheel:
			_, h := win.Size()
			if view.ScrollBy(int(ev.Value)*24, h) {
				redraw()
			}

		case ev.Kind == surf.InputButton && ev.Value == 1:
			href := view.Hit(int(ev.X), int(ev.Y))
			if href == "" {
				continue
			}
			app.Logf("browser: follow %s", href)
			navigate(func() error { return sess.Follow(href) })
			redraw()

		case ev.Kind == surf.InputKey:
			if ev.Code == 16 { // shift bijhouden voor Rune
				shift = ev.Value == 1
				continue
			}
			if ev.Value != 1 {
				continue
			}
			switch ev.Code {
			case 13: // Enter: laad wat er in de balk staat
				app.Logf("browser: go %s", view.Addr)
				navigate(func() error { return sess.Go(view.Addr) })
				redraw()
			case 8: // Backspace
				if view.Addr != "" {
					view.Addr = view.Addr[:len(view.Addr)-1]
					redrawBar(win, &view, app.Logf)
				}
			default:
				if r := browse.Rune(ev.Code, shift); r != 0 {
					view.Addr += string(r)
					redrawBar(win, &view, app.Logf)
				}
			}
		}
	}
	app.Exit(1)
}

// redrawBar hertekent alléén de adresbalk: tikken kost zo een strook van
// een paar KB per toets in plaats van een vol frame.
func redrawBar(win *window.Window, view *browse.View, logf func(string, ...any)) {
	img := win.Image()
	view.RenderBar(img)
	if err := win.Present(view.Bar(img)); err != nil {
		logf("browser: present: %v", err)
	}
}
