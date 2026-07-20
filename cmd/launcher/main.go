// Launcher is het startmenu — sinds 20-07 een scene-app (P2): de catalogus
// reist één keer als boom, een klik komt semantisch terug en een status-
// wissel is een PATCH van de knoptekst. De catalogus komt uit de boot-config
// (hopos.apps[]) via de HOPOS_APPS-env, met {{host}} al vervangen door het
// node-IP; klik = start (POST /v1/jobs), klik op een draaiende = stop
// (DELETE). Zet display + launcher in hopos.init[] en de node boot een
// bruikbare desktop (zie hop-os docs/config.md).
package main

import (
	"fmt"
	"strings"
	"time"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"

	"github.com/xinix00/hop-os-surf/app/hopapi"
	"github.com/xinix00/hop-os-surf/app/launcher"
	"github.com/xinix00/hop-os-surf/stack/scene"
)

// action is één start of stop voor de worker (stop = Spec nil).
type action struct {
	name string
	spec []byte
}

func main() {
	app := applib.Init()

	if _, err := appnet.Up(app); err != nil {
		app.Logf("net: %v", err)
		app.Exit(1)
	}
	addr := app.Env("SURF_ADDR")
	if addr == "" {
		app.Logf("launcher: SURF_ADDR not set (want <display-node>:7878)")
		app.Exit(1)
	}
	hopAddr := app.Env("HOP_ADDR")
	if hopAddr == "" {
		app.Logf("launcher: HOP_ADDR not set (want <node>:8080)")
		app.Exit(1)
	}
	if !strings.Contains(hopAddr, "://") {
		hopAddr = "http://" + hopAddr
	}
	client := &hopapi.Client{Base: hopAddr, Key: app.Env("HOP_KEY")}

	apps, err := launcher.ParseCatalog(app.Env("HOPOS_APPS"))
	if err != nil {
		// Niet fataal: een lege of halve catalogus is zichtbaar in het window,
		// en de log zegt waarom — beter dan een crashloop.
		app.Logf("launcher: %v", err)
	}
	app.Logf("launcher: %d app(s) in de catalogus", len(apps))

	conn := scene.Open(addr, fmt.Sprintf("launcher @ slot %d", app.Slot), 480, 360, app.Logf)
	m := launcher.NewMenu(conn, apps)

	// Hooks vullen kanalen (nooit blokkeren onder de menu-lock); de worker
	// voert uit en haalt daarna meteen de status — de knop toont het gevolg.
	refreshC := make(chan struct{}, 1)
	actC := make(chan action, 1)
	m.Refresh = func() { post(refreshC, struct{}{}) }
	m.OnStart = func(a launcher.App) { post(actC, action{name: a.Name, spec: a.Spec}) }
	m.OnStop = func(name string) { post(actC, action{name: name}) }
	conn.OnKey = m.Key

	if err := m.Start(); err != nil {
		app.Logf("launcher: show %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("launcher: scene open on %s, hop on %s", addr, hopAddr)

	fetch := func() {
		if st, err := client.Status(); err != nil {
			m.SetError(err)
			app.Logf("launcher: fetch: %v", err)
		} else {
			m.SetData(st)
		}
	}
	go func() {
		fetch()
		tick := time.NewTicker(3 * time.Second)
		for {
			select {
			case <-tick.C:
				fetch()
			case <-refreshC:
				fetch()
			case act := <-actC:
				var err error
				if act.spec != nil {
					err = client.Apply(act.spec)
					app.Logf("launcher: start %s: %v", act.name, err)
				} else {
					err = client.Delete(act.name)
					app.Logf("launcher: stop %s: %v", act.name, err)
				}
				if err != nil {
					m.SetError(err)
				} else {
					fetch()
				}
			}
		}
	}()

	select {} // volledig event-gedreven: leeslus + worker drijven de app
}

// post zet v op c zonder ooit te blokkeren (vol: oudste eruit, verse erin).
func post[T any](c chan T, v T) {
	for {
		select {
		case c <- v:
			return
		default:
			select {
			case <-c:
			default:
			}
		}
	}
}
