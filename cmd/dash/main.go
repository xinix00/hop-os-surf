// Dash is de P2-bewijs-app (docs/gui-ontwerp.md §8): een dashboard als
// SURF-scene. De boom reist één keer; daarna zijn alle updates PATCH-berichten
// van tientallen bytes — het dashboard meet en toont zijn eigen draadverkeer,
// zodat het bewijs ("bytes/s waar pixels KB/s waren") live op het scherm
// staat. De knoppen bewijzen de EVENT-terugweg: de display hit-test, de app
// krijgt alleen "#id clicked".
package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/xinix00/hop-os-surf/stack/scene"
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
		app.Logf("dash: SURF_ADDR not set (want <display-node>:7878)")
		app.Exit(1)
	}

	// De boom: kop, live-cijfers, meters, knoppen en een event-log.
	var counter int32
	uptime := scene.Value("0", "s")
	mem := scene.Value("0", "MB")
	count := scene.Value("0", "")
	load := scene.Gauge(0, 100, 0, "%")
	fill := scene.Bar(0, 100, 0)
	wire := scene.Value("0", "B/s")
	log := scene.List(nil, nil)

	c := scene.Open(addr, fmt.Sprintf("dash @ slot %d", app.Slot), 420, 420, app.Logf)

	logLine := func(s string) {
		items := append([]string{s}, log.Items...)
		if len(items) > 32 {
			items = items[:32]
		}
		c.SetItems(log, items)
	}
	add := func(d int32) func() {
		return func() {
			counter += d
			c.SetText(count, fmt.Sprintf("%d", counter))
			logLine(fmt.Sprintf("counter %+d -> %d", d, counter))
		}
	}

	root := scene.Col(6,
		scene.Label(scene.StyleHeading, "hop dash").Sized(28),
		scene.Row(4,
			scene.Col(2, scene.Label(0, "uptime").Sized(12), uptime),
			scene.Col(2, scene.Label(0, "go heap").Sized(12), mem),
			scene.Col(2, scene.Label(0, "counter").Sized(12), count),
		).Sized(72),
		scene.Label(0, "load (sine demo)").Sized(12),
		load.Sized(24),
		fill.Sized(10),
		scene.Row(4,
			scene.Button("- 1", add(-1)),
			scene.Button("+ 1", add(1)),
			scene.Button("reset", func() {
				counter = 0
				c.SetText(count, "0")
				logLine("counter reset")
			}),
		).Sized(36),
		scene.Row(4,
			scene.Col(2, scene.Label(0, "scene+patch wire").Sized(12), wire),
		).Sized(52),
		scene.Label(0, "events").Sized(12),
		log,
	)

	if err := c.Show(root); err != nil {
		app.Logf("dash: show %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("dash: scene of %d bytes sent to %s — patches from here on", c.BytesSent(), addr)

	// De meetlus: elke seconde een handvol PATCHes; het draadverkeer van de
	// afgelopen seconde staat als cijfer ín het dashboard.
	var ms runtime.MemStats
	start := time.Now()
	last := c.BytesSent()
	sine := []int32{50, 65, 79, 90, 97, 100, 97, 90, 79, 65, 50, 35, 21, 10, 3, 0, 3, 10, 21, 35}
	for i := 0; ; i++ {
		time.Sleep(time.Second)
		c.SetText(uptime, fmt.Sprintf("%d", int(time.Since(start).Seconds())))
		if i%2 == 0 {
			runtime.ReadMemStats(&ms)
			c.SetText(mem, fmt.Sprintf("%d", ms.HeapAlloc>>20))
		}
		c.SetVal(load, sine[i%len(sine)])
		c.SetVal(fill, int32((i*7)%101))
		d := c.BytesSent() - last
		last = c.BytesSent()
		c.SetText(wire, fmt.Sprintf("%d", d))
		if i%30 == 0 {
			app.Logf("dash: %d wire bytes last second (scene total %d)", d, c.BytesSent())
		}
	}
}
