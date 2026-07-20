// Package taskman is de taskmanager achter cmd/taskman: het clusteroverzicht
// van HOP als SURF-app. Sinds 20-07 een scene-app (P2): de boom reist één
// keer per scherm, updates zijn PATCHes en input komt semantisch terug
// ("rij 3 gekozen") — bytes op de draad waar het pixel-window KB's kostte.
//
// Vier schermen, elk een eigen boom (wisselen = Show, een boom is honderden
// bytes): AGENTS (de nodes), TASKS (de jobs), het jobdetail (de tasks per
// agent) en de logstaart van één task — de weg omlaag: cluster → job → task
// → stdout. De data komt binnen via SetData/SetDetail/LogLines; wat de app
// van de buitenwereld nodig heeft (fetches, logstromen) vraagt hij via de
// Refresh/OpenJob/OpenLog-hooks — cmd/taskman verbindt die aan hopapi.
package taskman

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/xinix00/hop-os-surf/app/hopapi"
	"github.com/xinix00/hop-os-surf/app/ui"
	"github.com/xinix00/hop-os-surf/stack/scene"
)

// Conn is het stukje scene.Conn dat de app gebruikt — als interface, zodat
// de host-tests met een fake draaien (geen display nodig).
type Conn interface {
	Show(*scene.Node) error
	SetText(n *scene.Node, s string)
	SetItems(n *scene.Node, items []string)
	AddItems(n *scene.Node, items []string)
	SetSel(n *scene.Node, sel int32)
}

// Schermen.
const (
	ScreenAgents = iota
	ScreenTasks
	ScreenDetail
	ScreenLogs
)

// taskRef koppelt een detail-rij aan zijn task ("" = kop/niet klikbaar).
type taskRef struct {
	agent, task string
}

// App is de taskmanager: data, schermstaat en de node-handles van het
// actieve scherm. Alle publieke methoden zijn goroutine-veilig (de scene-
// leeslus en de poller van cmd komen hier allebei binnen).
type App struct {
	// Hooks naar buiten; zetten vóór Start. Ze worden aangeroepen mét de
	// interne lock vast — niet terugroepen in de App (stuur een kanaal aan).
	Refresh  func()                           // vraag een verse snapshot
	OpenJob  func(job string)                 // detail geopend: haal JobStatus
	CloseJob func()                           // detail verlaten: poll mag stoppen
	OpenLog  func(agent, task, stream string) // logscherm: start deze staart
	CloseLog func()                           // logscherm verlaten: stop hem

	mu   sync.Mutex
	conn Conn

	status  hopapi.Status
	agents  []hopapi.Agent
	jobs    []hopapi.Job
	err     string
	fetched time.Time
	now     func() time.Time // injecteerbaar voor tests/previews

	screen    int
	detailJob string
	detail    *hopapi.JobStatus
	refs      []taskRef
	logAgent  string
	logTask   string
	logStream string

	// handles van het actieve scherm (elke show* vervangt ze)
	list    *scene.Node
	footer  *scene.Node
	cluster *scene.Node
}

// New maakt de app; Start toont het eerste scherm.
func New(conn Conn) *App {
	return &App{conn: conn, now: time.Now}
}

// Start toont het AGENTS-scherm (de eerste boom de lijn over).
func (a *App) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.showOverview(ScreenAgents)
}

// --- data-invoer --------------------------------------------------------------

// SetData neemt een verse cluster-snapshot over (gesorteerd, zodat de lijst
// stil ligt terwijl de server-maps van volgorde wisselen) en patcht het
// actieve scherm.
func (a *App) SetData(st hopapi.Status, agents []hopapi.Agent, jobs []hopapi.Job) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	a.status, a.agents, a.jobs = st, agents, jobs
	a.err, a.fetched = "", a.now()

	a.conn.SetText(a.cluster, a.clusterLine())
	switch a.screen {
	case ScreenAgents:
		a.conn.SetItems(a.list, a.agentRows())
	case ScreenTasks:
		a.conn.SetItems(a.list, a.jobRows())
	}
	a.patchFooter()
}

// SetDetail neemt de taskstatus van een job over; een verlaat antwoord voor
// een ander scherm of een andere job valt weg.
func (a *App) SetDetail(job string, js hopapi.JobStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.screen != ScreenDetail || job != a.detailJob {
		return
	}
	a.detail = &js
	rows, refs := a.detailRows(js)
	a.refs = refs
	a.conn.SetItems(a.list, rows)
	a.patchFooter()
}

// SetError meldt een mislukte fetch; de laatste goede data blijft staan.
func (a *App) SetError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.err = err.Error()
	a.patchFooter()
}

// LogLines plakt regels aan de logstaart (PropAdd: regel-lengte op de draad).
// De identificatie voorkomt dat een nakomende batch van een nét vervangen
// staart (andere task of stream-wissel) in de verkeerde lijst lekt.
func (a *App) LogLines(agent, task, stream string, lines []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.screen == ScreenLogs && agent == a.logAgent && task == a.logTask && stream == a.logStream {
		a.conn.AddItems(a.list, lines)
	}
}

// Key verwerkt een doorgestuurde toets: r/F5 ververst, Esc/Backspace gaat
// terug, Tab of 1/2 wisselt tussen de overzichten.
func (a *App) Key(code uint32, down bool) {
	if !down {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch code {
	case 82, 116: // r, F5
		if a.Refresh != nil {
			a.Refresh()
		}
	case 27, 8: // Esc, Backspace
		a.back()
	case 9: // Tab
		if a.screen == ScreenAgents {
			a.showOverview(ScreenTasks)
		} else if a.screen == ScreenTasks {
			a.showOverview(ScreenAgents)
		}
	case 49: // 1
		if a.screen == ScreenTasks {
			a.showOverview(ScreenAgents)
		}
	case 50: // 2
		if a.screen == ScreenAgents {
			a.showOverview(ScreenTasks)
		}
	}
}

// --- schermen -------------------------------------------------------------------

// header bouwt de kopregel: merk links, clusterfeiten rechts (als handle,
// zodat SetData hem patcht).
func (a *App) header() *scene.Node {
	a.cluster = scene.Label(0, a.clusterLine())
	return scene.Row(4,
		scene.Label(scene.StyleHeading, "hop taskman").Sized(180),
		a.cluster,
	).Sized(26)
}

// footerNode bouwt de voetregel-handle.
func (a *App) footerNode() *scene.Node {
	a.footer = scene.Label(0, "")
	return a.footer.Sized(16)
}

// showOverview toont AGENTS of TASKS: navigatieknoppen plus de lijst.
func (a *App) showOverview(screen int) error {
	a.stopLog()
	if a.detailJob != "" && a.CloseJob != nil {
		a.CloseJob()
	}
	a.screen = screen
	a.detailJob, a.detail, a.refs = "", nil, nil

	rows, onSel := a.agentRows(), func(int) { a.conn.SetSel(a.list, -1) }
	if screen == ScreenTasks {
		rows = a.jobRows()
		onSel = func(row int) {
			// vanaf de leeslus: de lock is al niet van ons — pak hem zelf
			a.mu.Lock()
			defer a.mu.Unlock()
			if row >= 0 && row < len(a.jobs) {
				a.openDetail(a.jobs[row].Name)
			}
		}
	}
	a.list = scene.List(rows, nil)
	a.list.OnSelect = onSel
	if screen == ScreenAgents {
		// selectie betekent hier niets: meteen weer wissen
		a.list.OnSelect = func(int) {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.conn.SetSel(a.list, -1)
		}
	}

	nav := func(label string, target int) *scene.Node {
		if (screen == ScreenAgents) == (target == ScreenAgents) {
			label = "[ " + label + " ]" // het open tabblad
		}
		return scene.Button(label, func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.screen != target {
				a.showOverview(target)
			}
		})
	}
	root := scene.Col(4,
		a.header(),
		scene.Row(4, nav("AGENTS", ScreenAgents), nav("TASKS", ScreenTasks)).Sized(28),
		a.list,
		a.footerNode(),
	)
	a.patchFooter()
	return a.conn.Show(root)
}

// openDetail wisselt naar het jobdetail en vraagt de buitenwereld om data.
func (a *App) openDetail(job string) {
	a.stopLog()
	a.screen = ScreenDetail
	a.detailJob, a.detail, a.refs = job, nil, nil

	a.list = scene.List([]string{"laden ..."}, nil)
	a.list.OnSelect = func(row int) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if row >= 0 && row < len(a.refs) && a.refs[row].task != "" {
			a.openLogs(a.refs[row].agent, a.refs[row].task, "stdout")
			return
		}
		a.conn.SetSel(a.list, -1) // kop- of laadregel: niets te kiezen
	}
	root := scene.Col(4,
		a.header(),
		scene.Row(4,
			a.backButton(),
			scene.Label(scene.StyleHeading, a.detailJob),
		).Sized(28),
		scene.Label(0, "klik een task voor zijn logs").Sized(14),
		a.list,
		a.footerNode(),
	)
	a.patchFooter()
	a.conn.Show(root)
	if a.OpenJob != nil {
		a.OpenJob(job)
	}
}

// openLogs wisselt naar de logstaart van één task.
func (a *App) openLogs(agent, task, stream string) {
	a.stopLog()
	a.screen = ScreenLogs
	a.logAgent, a.logTask, a.logStream = agent, task, stream

	a.list = scene.List(nil, nil)
	pick := func(s string) *scene.Node {
		label := s
		if s == stream {
			label = "[ " + s + " ]"
		}
		return scene.Button(label, func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.screen == ScreenLogs && a.logStream != s {
				a.openLogs(a.logAgent, a.logTask, s)
			}
		}).Sized(90)
	}
	root := scene.Col(4,
		a.header(),
		scene.Row(4,
			a.backButton(),
			scene.Label(scene.StyleHeading, task+" @ "+agent),
		).Sized(28),
		scene.Row(4, pick("stdout"), pick("stderr"), scene.Label(0, "")).Sized(24),
		a.list,
		a.footerNode(),
	)
	a.patchFooter()
	a.conn.Show(root)
	if a.OpenLog != nil {
		a.OpenLog(agent, task, stream)
	}
}

// backButton: één stap terug in cluster → job → task → logs.
func (a *App) backButton() *scene.Node {
	return scene.Button("< terug", func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.back()
	}).Sized(90)
}

func (a *App) back() {
	switch a.screen {
	case ScreenLogs:
		job := a.detailJob
		a.openDetail(job)
	case ScreenDetail:
		a.showOverview(ScreenTasks)
	}
}

// stopLog meldt een lopende staart af (scherm verlaten).
func (a *App) stopLog() {
	if a.screen == ScreenLogs && a.CloseLog != nil {
		a.CloseLog()
	}
	a.logAgent, a.logTask, a.logStream = "", "", ""
}

// --- rijen en regels ------------------------------------------------------------

// mark is de tekst-statusstip van dit ASCII-font: + goed, ! let op, x fout.
func mark(state int) string { return [3]string{"+", "!", "x"}[state] }

func (a *App) clusterLine() string {
	if a.status.ClusterName == "" {
		return "geen verbinding"
	}
	s := fmt.Sprintf("%s  %da %dj %dt", a.status.ClusterName,
		a.status.Agents, a.status.Jobs, a.status.TotalPlaced)
	if a.status.Settling {
		s += " settling"
	}
	return s
}

func (a *App) agentRows() []string {
	rows := make([]string, len(a.agents))
	for i, ag := range a.agents {
		age := a.now().Sub(ag.LastSeen)
		st := 0
		switch {
		case age > 30*time.Second:
			st = 2
		case age > 10*time.Second:
			st = 1
		}
		rows[i] = fmt.Sprintf("%s %-14s %-8s %-24s %s",
			mark(st), ag.ID, ag.Version, ag.Endpoint, ui.Ago(age))
	}
	return rows
}

func (a *App) jobRows() []string {
	rows := make([]string, len(a.jobs))
	for i, j := range a.jobs {
		want := j.Count
		if want == 0 {
			want = 1
		}
		placed := a.status.Placed[j.Name]
		st := 0
		switch {
		case placed == 0:
			st = 2
		case placed < want:
			st = 1
		}
		rows[i] = fmt.Sprintf("%s %-14s %-7s %d/%d",
			mark(st), j.Name, driverOf(j), placed, want)
	}
	return rows
}

// detailRows vlakt het jobdetail tot lijst-rijen plus hun taskRefs: eerst de
// jobfeiten, dan per agent een kop met zijn tasks eronder.
func (a *App) detailRows(js hopapi.JobStatus) ([]string, []taskRef) {
	var rows []string
	var refs []taskRef
	push := func(s string, r taskRef) { rows = append(rows, s); refs = append(refs, r) }

	if job := a.jobByName(a.detailJob); job != nil {
		what := job.Command
		if job.Image != "" {
			what = job.Image + "  " + job.Command
		}
		push(fmt.Sprintf("driver %-7s %s", driverOf(*job), what), taskRef{})
	}

	agents := make([]string, 0, len(js.TasksByAgent))
	for id := range js.TasksByAgent {
		agents = append(agents, id)
	}
	sort.Strings(agents)
	endpoint := make(map[string]string, len(js.Agents))
	for _, ag := range js.Agents {
		endpoint[ag.ID] = ag.Endpoint
	}
	for _, id := range agents {
		tasks := js.TasksByAgent[id]
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
		push("", taskRef{})
		push(fmt.Sprintf("@ %s  %s", id, endpoint[id]), taskRef{})
		for _, t := range tasks {
			up := ui.Ago(a.now().Sub(t.StartedAt))
			if t.State != "running" {
				up = t.State
			}
			st := 0
			switch t.State {
			case "running":
			case "stopping":
				st = 1
			default:
				st = 2
			}
			push(fmt.Sprintf("  %s %-12s pid %-6d r%-2d cpu %3.0f%% mem %3.0f%%  %s",
				mark(st), t.ID, t.Pid, t.RestartCount, t.CPUPercent, t.MemPercent, up), taskRef{agent: id, task: t.ID})
		}
	}
	if len(agents) == 0 {
		push("geen tasks", taskRef{})
	}
	return rows, refs
}

func (a *App) jobByName(name string) *hopapi.Job {
	for i := range a.jobs {
		if a.jobs[i].Name == name {
			return &a.jobs[i]
		}
	}
	return nil
}

// patchFooter zet de voetregel: fout, of versheid plus de toetsenhulp.
func (a *App) patchFooter() {
	var s string
	switch {
	case a.err != "":
		s = "FOUT: " + a.err
	case a.fetched.IsZero():
		s = "verbinden ..."
	default:
		s = "bijgewerkt " + ui.Ago(a.now().Sub(a.fetched)) + " geleden"
	}
	switch a.screen {
	case ScreenAgents, ScreenTasks:
		s += "  --  tab: wissel  r: ververs"
	default:
		s += "  --  esc: terug  r: ververs"
	}
	a.conn.SetText(a.footer, s)
}

func driverOf(j hopapi.Job) string {
	if j.Driver != "" {
		return j.Driver
	}
	if j.Image != "" {
		return "docker"
	}
	return "exec"
}
