package browse

import (
	"fmt"
	"time"

	"github.com/xinix00/hop-os-surf/stack/surf"
	"github.com/xinix00/hop-os-surf/stack/window"
)

// Drive is de hele browser-app achter het window: sessie, layout en de
// event-lus (typen, klikken, scrollen, navigeren in een worker). home is de
// startpagina ("" = leeg). Keert terug wanneer het window sterft of een
// Present faalt — de cmd-main beslist over exit, een host-desktop over
// opruimen.
func Drive(win *window.Window, home string, logf func(string, ...any)) error {
	sess := NewSession()
	view := View{}

	var presentErr error
	redraw := func() {
		img := win.Image() // elke frame opvragen: na een resize is hij nieuw
		view.Render(img)
		if err := win.Present(); err != nil {
			presentErr = err
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
		logf("browser: %s", what)
		view.Status, view.Err = what+" ...", false
		redrawStatus(win, &view, logf)
		go func() { navDone <- load() }()
	}

	view.Page = sess.Layout(win.Image().Bounds().Dx())
	view.Addr = sess.URL()
	if home != "" {
		view.Addr = home
		startNav("go "+home, func() error { return sess.Go(home) })
	}
	redraw()

	var shift bool
	for presentErr == nil {
		select {
		case err := <-navDone:
			navBusy = false
			ms := time.Since(navT0).Milliseconds()
			if err != nil {
				view.Status, view.Err = err.Error(), true
				logf("browser: %v", err)
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
				return fmt.Errorf("browser: window closed")
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
						if r := Rune(ev.Code, shift); r != 0 {
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
						redrawBar(win, &view, logf)
					}
				case 38, 40, 33, 34, 32, 36, 35: // scroll-toetsen
					_, h := win.Size()
					page := h - BarH - StatusH - 24 // met overlap
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
					if r := Rune(ev.Code, shift); r != 0 {
						view.Addr += string(r)
						redrawBar(win, &view, logf)
					}
				}
			}
		}
	}
	return presentErr
}

// redrawBar hertekent alléén de adresbalk: tikken kost zo een strook van
// een paar KB per toets in plaats van een vol frame.
func redrawBar(win *window.Window, view *View, logf func(string, ...any)) {
	img := win.Image()
	view.RenderBar(img)
	if err := win.Present(view.Bar(img)); err != nil {
		logf("browser: present: %v", err)
	}
}

// redrawStatus idem voor de statusbalk: het laden begint met één strook
// damage onderin, niet met een vol frame.
func redrawStatus(win *window.Window, view *View, logf func(string, ...any)) {
	img := win.Image()
	view.RenderStatus(img)
	if err := win.Present(view.StatusRect(img)); err != nil {
		logf("browser: present: %v", err)
	}
}
