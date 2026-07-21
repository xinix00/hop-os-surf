package taskman

import (
	"errors"
	"time"

	"github.com/xinix00/hop-os-surf/app/hopapi"
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

// Drive is de hele taskman-app achter de scene-verbinding: hooks, de
// fetch-worker (overzicht + open jobdetail) en de log-worker (de éne open
// staart). Fout alleen als de eerste Show faalt; daarna is de app volledig
// event-gedreven en blokkeert Drive voorgoed — de cmd-main én de
// host-desktop (cmd/desktop) rijden allebei deze functie.
func Drive(conn *scene.Conn, client *hopapi.Client, logf func(string, ...any)) error {
	a := New(conn)

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
	closed := make(chan struct{})
	conn.OnClose = func() { close(closed) }

	if err := a.Start(); err != nil {
		return err
	}

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
			logf("taskman: fetch: %v", err)
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
				logf("taskman: logs %s/%s: %v", req.task, req.stream, err)
				continue
			}
			cur = ls
			go pump(a, req, ls)
		}
	}()

	// Volledig event-gedreven: leeslus + workers drijven de app — tot de WM
	// ons sluit (het rode stoplichtje): sluiten is proces killen.
	<-closed
	return errors.New("taskman: window gesloten door de WM")
}

// pump schuift logregels naar de app, licht gebundeld: wat er al ligt gaat
// in één PATCH mee (een burst wordt zo tientallen bytes per regel, niet een
// bericht per regel op zijn eentje).
func pump(a *App, req logReq, ls *hopapi.LogStream) {
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
