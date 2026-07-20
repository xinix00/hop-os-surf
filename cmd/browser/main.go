// Browser is de derde GUI-app — en de eerste die het cluster met de
// buitenwereld verbindt: gost-dom haalt en parset de pagina (headless,
// pure Go — compileert gewoon onder tamago), browse/ layout hem op het
// 8x8-font, en het resultaat is een window als elk ander. Typen gaat in de
// adresbalk (Enter = laden), het wiel scrollt, links zijn klikbaar.
// SURF_HOME (optioneel) is de startpagina.
package main

import (
	"fmt"
	"time"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"

	"github.com/xinix00/hop-os-surf/app/browse"
	"github.com/xinix00/hop-os-surf/stack/surf"
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

	sess := browse.NewSession()
	view := browse.View{}

	redraw := func() {
		img := win.Image() // elke frame opvragen: na een resize is hij nieuw
		view.Render(img)
		if err := win.Present(); err != nil {
			app.Logf("browser: present: %v", err)
			app.Exit(1)
		}
	}

	// Navigatie draait in een worker-goroutine: een trage of dooie site mag
	// de event-lus nooit bevriezen ("na de eerste site gebeurt er niets en
	// de input is stuk", Derek 19-07). De select hieronder blijft input
	// verwerken; het resultaat komt terug via navDone. sess wordt alleen
	// door de worker aangeraakt zolang navBusy staat — daarna weer door de
	// lus (URL/Layout). Eén navigatie tegelijk; kliks tijdens het laden
	// worden genegeerd.
	navDone := make(chan error, 1)
	navBusy := false
	var navT0 time.Time
	startNav := func(what string, load func() error) {
		if navBusy {
			return
		}
		navBusy = true
		navT0 = time.Now()
		app.Logf("browser: %s", what)
		view.Status, view.Err = what+" ...", false
		redrawStatus(win, &view, app.Logf)
		go func() { navDone <- load() }()
	}

	view.Page = sess.Layout(win.Image().Bounds().Dx())
	view.Addr = sess.URL()
	if home := app.Env("SURF_HOME"); home != "" {
		view.Addr = home
		startNav("go "+home, func() error { return sess.Go(home) })
	}
	redraw()

	var shift bool
	for {
		select {
		case err := <-navDone:
			navBusy = false
			ms := time.Since(navT0).Milliseconds()
			if err != nil {
				view.Status, view.Err = err.Error(), true
				app.Logf("browser: %v", err)
			} else {
				view.Scroll = 0
				view.Focus = 0 // veld-indexen horen bij de oude pagina
				view.Status, view.Err = fmt.Sprintf("ok (%dms) %s", ms, sess.URL()), false
			}
			view.Addr = sess.URL()
			view.Page = sess.Layout(win.Image().Bounds().Dx())
			redraw()

		case ev, ok := <-win.Events():
			if !ok {
				app.Exit(1)
			}
			switch {
			case ev.Kind == window.KindResize:
				// nieuwe breedte → nieuwe layout; scroll blijft zo goed als
				// mogelijk staan (ScrollBy(0) klemt hem op de nieuwe hoogte).
				// Tijdens een navigatie niet layouten (sess is van de worker).
				if !navBusy {
					view.Page = sess.Layout(int(ev.X))
				}
				view.ScrollBy(0, int(ev.Y))
				redraw()

			case ev.Kind == surf.InputWheel:
				_, h := win.Size()
				if view.ScrollBy(int(ev.Value)*24, h) {
					redraw()
				}

			case ev.Kind == surf.InputButton && ev.Value == 1:
				_, h := win.Size()
				// Velden eerst: een knop verstuurt het formulier, een
				// tekstveld pakt de focus (en daarmee de toetsen).
				if fi := view.HitField(int(ev.X), int(ev.Y), h); fi > 0 {
					f := &view.Page.Fields[fi-1]
					if f.Submit {
						startNav("submit", func() error { return sess.Submit(f) })
					} else if view.Focus != fi {
						view.Focus = fi
						redraw()
					}
					continue
				}
				if view.Focus != 0 { // klik naast de velden: focus terug naar de adresbalk
					view.Focus = 0
					redraw()
				}
				href := view.Hit(int(ev.X), int(ev.Y), h)
				if href == "" {
					continue
				}
				startNav("follow "+href, func() error { return sess.Follow(href) })

			case ev.Kind == surf.InputKey:
				if ev.Code == 16 { // shift bijhouden voor Rune
					shift = ev.Value == 1
					continue
				}
				if ev.Value != 1 {
					continue
				}
				// Een veld met focus krijgt de toetsen (Escape geeft ze
				// terug aan de adresbalk); spatie tikt daar een spatie in
				// plaats van te scrollen.
				if f := view.Focused(); f != nil && !navBusy {
					switch ev.Code {
					case 27: // Escape
						view.Focus = 0
						redraw()
					case 13: // Enter: verstuur het formulier
						startNav("submit", func() error { return sess.Submit(f) })
					case 8:
						sess.Type(f, 0, true)
						redraw()
					case 32:
						sess.Type(f, ' ', false)
						redraw()
					default:
						if r := browse.Rune(ev.Code, shift); r != 0 {
							sess.Type(f, r, false)
							redraw()
						} else {
							goto scroll // pijltjes/PgUp blijven scrollen
						}
					}
					continue
				}
			scroll:
				switch ev.Code {
				case 13: // Enter: laad wat er in de balk staat
					addr := view.Addr
					startNav("go "+addr, func() error { return sess.Go(addr) })
				case 8: // Backspace
					if view.Addr != "" {
						view.Addr = view.Addr[:len(view.Addr)-1]
						redrawBar(win, &view, app.Logf)
					}
				case 38, 40, 33, 34, 32, 36, 35: // scroll-toetsen
					_, h := win.Size()
					page := h - browse.BarH - browse.StatusH - 24 // met overlap
					var d int
					switch ev.Code {
					case 38: // pijl omhoog
						d = -24
					case 40: // pijl omlaag
						d = 24
					case 33: // PgUp
						d = -page
					case 34, 32: // PgDn; spatie doet wat hij in elke browser doet
						d = page
					case 36: // Home
						d = -view.Scroll
					case 35: // End
						d = view.Page.Height
					}
					if view.ScrollBy(d, h) {
						redraw()
					}
				default:
					if r := browse.Rune(ev.Code, shift); r != 0 {
						view.Addr += string(r)
						redrawBar(win, &view, app.Logf)
					}
				}
			}
		}
	}
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

// redrawStatus idem voor de statusbalk: het laden begint met één strook
// damage onderin, niet met een vol frame.
func redrawStatus(win *window.Window, view *browse.View, logf func(string, ...any)) {
	img := win.Image()
	view.RenderStatus(img)
	if err := win.Present(view.StatusRect(img)); err != nil {
		logf("browser: present: %v", err)
	}
}
