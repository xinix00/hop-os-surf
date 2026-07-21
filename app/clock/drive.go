package clock

import (
	"sync/atomic"
	"time"

	"github.com/xinix00/hop-os-surf/stack/surf"
	"github.com/xinix00/hop-os-surf/stack/window"
)

// Size is de vensterhint van de klok (vierkant).
const Size = 320

// Drive is de hele klok-app achter het window: tekent elke seconde de
// wijzers (en na een resize alles), logt kliks als bewijs van de input-
// terugweg. Keert terug wanneer het window sterft — de cmd-main beslist
// over exit, een host-desktop over opruimen.
func Drive(win *window.Window, logf func(string, ...any)) error {
	var resized atomic.Bool
	go func() {
		for ev := range win.Events() {
			switch {
			case ev.Kind == window.KindResize:
				resized.Store(true)
			case ev.Kind == surf.InputButton && ev.Value == 1:
				logf("clock: click at %d,%d", ev.X, ev.Y)
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
			Draw(img, now)
			var err error
			if full {
				err = win.Present()
			} else {
				// Alleen het wijzer-gebied de lijn over (ring en streepjes
				// veranderen nooit) — en met de stream-compressie erachter
				// is dat bijna niets.
				err = win.Present(HandsBox(img.Bounds()))
			}
			if err != nil {
				return err
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
