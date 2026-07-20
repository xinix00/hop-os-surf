// Taskman is de HOP-taskmanager — sinds 20-07 een scene-app (P2): de app
// stuurt bomen en PATCHes, de display rendert; en mét de weg helemaal
// omlaag: cluster → job → task → live logstaart (SSE), waar elke logregel
// één PATCH van regel-lengte is. HOP_ADDR mag naar elke agent wijzen (die
// proxyt /v1 door), HOP_KEY is de cluster-API-key (leeg = geen auth).
//
// Zelfde les als altijd: het net bevriest de app nooit — alle fetches en de
// logstaart draaien in workers; de scene-hooks zetten alleen verzoeken op
// kanalen (nooit blokkeren, laatste verzoek wint).
package main

import (
	"fmt"
	"strings"
	"time"

	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"

	"github.com/xinix00/hop-os-surf/app/hopapi"
	"github.com/xinix00/hop-os-surf/app/taskman"
	"github.com/xinix00/hop-os-surf/stack/scene"
)

// logReq identificeert één gewenste logstaart; de lege req betekent "stop".
type logReq struct{ agent, task, stream string }

// post zet v op c zonder ooit te blokkeren (vol: oudste eruit, verse erin) —
// de hooks draaien onder de app-lock, dus laatste-verzoek-wint is de wet.
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

func main() {
	app := applib.Init()

	if _, err := appnet.Up(app); err != nil {
		app.Logf("net: %v", err)
		app.Exit(1)
	}
	addr := app.Env("SURF_ADDR")
	if addr == "" {
		app.Logf("taskman: SURF_ADDR not set (want <display-node>:7878)")
		app.Exit(1)
	}
	hopAddr := app.Env("HOP_ADDR")
	if hopAddr == "" {
		app.Logf("taskman: HOP_ADDR not set (want <node>:8080)")
		app.Exit(1)
	}
	if !strings.Contains(hopAddr, "://") {
		hopAddr = "http://" + hopAddr
	}
	client := &hopapi.Client{Base: hopAddr, Key: app.Env("HOP_KEY")}

	conn := scene.Open(addr, fmt.Sprintf("taskman @ slot %d", app.Slot), 480, 360, app.Logf)
	a := taskman.New(conn)

	// De hooks vullen kanalen (lossy: laatste verzoek wint) — ze draaien
	// onder de app-lock en mogen dus nooit blokkeren of terugroepen.
	refreshC := make(chan struct{}, 1)
	detailC := make(chan string, 1)
	logC := make(chan logReq, 1)
	a.Refresh = func() { post(refreshC, struct{}{}) }
	a.OpenJob = func(job string) { post(detailC, job) }
	a.CloseJob = func() { post(detailC, "") }
	a.OpenLog = func(agent, task, stream string) { post(logC, logReq{agent, task, stream}) }
	a.CloseLog = func() { post(logC, logReq{}) }
	conn.OnKey = a.Key

	if err := a.Start(); err != nil {
		app.Logf("taskman: show %s: %v", addr, err)
		app.Exit(1)
	}
	app.Logf("taskman: scene open on %s, hop on %s", addr, hopAddr)

	// Fetch-worker: overzicht elke 3s (en op verzoek), het open jobdetail
	// pollt mee. SetData/SetDetail laten verlate antwoorden zelf vallen.
	go func() {
		fetch := func() {
			st, err := client.Status()
			if err == nil {
				var agents []hopapi.Agent
				var jobs []hopapi.Job
				if agents, err = client.Agents(); err == nil {
					jobs, err = client.Jobs()
				}
				if err == nil {
					a.SetData(st, agents, jobs)
					return
				}
			}
			a.SetError(err)
			app.Logf("taskman: fetch: %v", err)
		}
		detail := ""
		fetchDetail := func() {
			if detail == "" {
				return
			}
			if js, err := client.JobStatus(detail); err != nil {
				a.SetError(err)
			} else {
				a.SetDetail(detail, js)
			}
		}
		fetch()
		tick := time.NewTicker(3 * time.Second)
		for {
			select {
			case <-tick.C:
				fetch()
				fetchDetail()
			case <-refreshC:
				fetch()
				fetchDetail()
			case detail = <-detailC:
				fetchDetail()
			}
		}
	}()

	// Log-worker: beheert de éne open staart. Een nieuw verzoek sluit de
	// oude (zijn pomp stopt op het gesloten kanaal); LogLines' identificatie
	// vangt de laatste batch van een gesloten staart af.
	go func() {
		var cur *hopapi.LogStream
		for req := range logC {
			if cur != nil {
				cur.Close()
				cur = nil
			}
			if req.task == "" {
				continue
			}
			ls, err := client.Logs(req.agent, req.task, req.stream)
			if err != nil {
				a.SetError(err)
				app.Logf("taskman: logs %s/%s: %v", req.task, req.stream, err)
				continue
			}
			cur = ls
			go pump(a, req, ls)
		}
	}()

	select {} // volledig event-gedreven: leeslus + workers drijven de app
}

// pump schuift logregels naar de app, licht gebundeld: wat er al ligt gaat
// in één PATCH mee (een burst wordt zo tientallen bytes per regel, niet een
// bericht per regel op zijn eentje).
func pump(a *taskman.App, req logReq, ls *hopapi.LogStream) {
	for line := range ls.Lines {
		batch := []string{line}
	drain:
		for len(batch) < 32 {
			select {
			case l, ok := <-ls.Lines:
				if !ok {
					break drain
				}
				batch = append(batch, l)
			default:
				break drain
			}
		}
		a.LogLines(req.agent, req.task, req.stream, batch)
	}
}
