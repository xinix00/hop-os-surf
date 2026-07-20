// Calc is de tweede GUI-app — en sinds 20-07 een scene-app (P2): de boom
// met knoppen reist één keer, de display rendert en hit-test, en de app
// krijgt "knop 7 geklikt" plus rauwe toetsen terug. Elke toetsaanslag is
// daarmee één PATCH van het display-Value — bytes in plaats van de oude
// pixel-damage. Muisklikken bewijzen de EVENT-terugweg, het toetsenbord de
// key-doorvoer (web-KVM en straks USB-HID).
package main

import (
	"fmt"
	"sync"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"

	"github.com/xinix00/hop-os-surf/app/calc"
	"github.com/xinix00/hop-os-surf/stack/scene"
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

	conn := scene.Open(addr, fmt.Sprintf("calc @ slot %d", app.Slot), 240, 320, app.Logf)

	// press komt uit twee paden van de leeslus (EVENT en toetsen); de mutex
	// maakt de app onafhankelijk van dat detail.
	var mu sync.Mutex
	var c calc.Calc
	var display *scene.Node
	press := func(key byte) {
		mu.Lock()
		c.Press(key)
		line := calc.Line(&c)
		conn.SetText(display, line)
		mu.Unlock()
		app.Logf("calc: key %q -> %s", key, line)
	}

	root, disp := calc.Tree(press)
	display = disp
	conn.OnKey = func(code uint32, down bool) {
		if !down {
			return
		}
		if k := calc.Key(code); k != 0 {
			press(k)
		}
	}
	if err := conn.Show(root); err != nil {
		app.Logf("calc: show %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("calc: scene of %d bytes sent to %s", conn.BytesSent(), addr)

	select {} // volledig event-gedreven: de leeslus drijft de app
}
