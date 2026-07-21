// Package launcher is het startmenu achter cmd/launcher: de app-catalogus
// uit de boot-config (hopos.apps[], door HopOS aangeleverd als HOPOS_APPS-env)
// als knoppengrid. Sinds 20-07 een scene-app (P2): de boom reist één keer,
// een klik komt terug als "#knop clicked" en een statuswissel is een PATCH
// van de knoptekst. Eén klik start een job (POST), een klik op een draaiende
// stopt hem (DELETE) — de toggle van Derek (19-07). Model en bomen zijn
// host-testbaar; cmd/launcher verbindt de hooks aan hopapi.
package launcher

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/xinix00/hop-os-surf/app/hopapi"
	"github.com/xinix00/hop-os-surf/app/ui"
	"github.com/xinix00/hop-os-surf/stack/scene"
)

// App is één catalogusregel: de naam (en driver, voor het label) uit de
// jobspec gepeuterd, plus de rauwe spec die bij een start de lijn over gaat.
type App struct {
	Name   string
	Driver string
	Spec   json.RawMessage
}

// ParseCatalog leest de HOPOS_APPS-env: een JSON-array van jobspecs (het
// formaat van hopos.apps[] — zie hop-os docs/config.md). Regels zonder naam
// worden overgeslagen; de fout meldt hoeveel.
func ParseCatalog(raw string) ([]App, error) {
	var specs []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, fmt.Errorf("launcher: HOPOS_APPS: %w", err)
	}
	var apps []App
	skipped := 0
	for _, spec := range specs {
		var peek struct {
			Name   string `json:"name"`
			Driver string `json:"driver"`
			Image  string `json:"image"`
		}
		if err := json.Unmarshal(spec, &peek); err != nil || peek.Name == "" {
			skipped++
			continue
		}
		driver := peek.Driver
		if driver == "" {
			driver = "exec"
			if peek.Image != "" {
				driver = "docker"
			}
		}
		apps = append(apps, App{Name: peek.Name, Driver: driver, Spec: spec})
	}
	if skipped > 0 {
		return apps, fmt.Errorf("launcher: %d catalogusregel(s) zonder naam overgeslagen", skipped)
	}
	return apps, nil
}

// Conn is het stukje scene.Conn dat het menu gebruikt (interface: host-tests
// draaien met een fake zonder display).
type Conn interface {
	Show(*scene.Node) error
	SetText(n *scene.Node, s string)
}

// Menu is de launcher: de catalogus als knoppen, met de clusterstatus als
// waarheid over wat er draait. Een klik start — altijd (meerdere van
// hetzelfde mag, Derek 21-07); stoppen doe je met het rode stoplichtje van
// het window zelf. Publieke methoden zijn goroutine-veilig.
type Menu struct {
	// Hooks naar buiten; zetten vóór Start. Aangeroepen mét de interne lock
	// vast — niet terugroepen in het Menu (stuur een kanaal aan).
	OnStart func(app App) // POST deze spec
	Refresh func()        // vraag een verse status

	mu   sync.Mutex
	conn Conn
	apps []App

	status  hopapi.Status
	err     string
	fetched time.Time
	busy    string // app waar een start voor loopt ("" = geen)
	now     func() time.Time

	buttons []*scene.Node
	footer  *scene.Node
	cluster *scene.Node
}

// NewMenu maakt het menu voor deze catalogus; Start toont hem.
func NewMenu(conn Conn, apps []App) *Menu {
	return &Menu{conn: conn, apps: apps, now: time.Now}
}

// Start bouwt en toont de boom: kop, het knoppengrid (drie per rij), voet.
func (m *Menu) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cluster = scene.Label(0, "geen verbinding")
	m.footer = scene.Label(0, "verbinden ...")
	m.buttons = make([]*scene.Node, len(m.apps))

	const perRow = 3
	var rows []*scene.Node
	for i := 0; i < len(m.apps); i += perRow {
		btns := make([]*scene.Node, 0, perRow)
		for j := i; j < i+perRow; j++ {
			if j >= len(m.apps) {
				btns = append(btns, scene.Label(0, "")) // vulling: rijen even breed
				continue
			}
			idx := j
			m.buttons[idx] = scene.Button(m.buttonText(m.apps[idx]), func() {
				m.mu.Lock()
				defer m.mu.Unlock()
				m.click(idx)
			})
			btns = append(btns, m.buttons[idx])
		}
		rows = append(rows, scene.Row(4, btns...).Sized(34))
	}

	kids := []*scene.Node{
		scene.Row(4,
			scene.Label(scene.StyleHeading, "hop launcher").Sized(200),
			m.cluster,
		).Sized(26),
	}
	if len(m.apps) == 0 {
		kids = append(kids, scene.Label(0, "lege catalogus (hopos.apps[])"))
	} else {
		kids = append(kids, rows...)
		kids = append(kids, scene.Label(0, "")) // restruimte onder het grid
	}
	kids = append(kids, m.footer.Sized(16))
	return m.conn.Show(scene.Col(4, kids...))
}

// click togglet app i: draait hij (geplaatst), dan stop; anders start.
func (m *Menu) click(i int) {
	if m.busy != "" || i < 0 || i >= len(m.apps) {
		return // één start tegelijk
	}
	a := m.apps[i]
	m.busy = a.Name
	if m.OnStart != nil {
		m.OnStart(a)
	}
	m.patch()
}

// SetData neemt een verse clusterstatus over; de eerstvolgende status na een
// start is de nieuwe werkelijkheid (de worker fetcht direct na de actie) —
// de knop is dan weer vrij.
func (m *Menu) SetData(st hopapi.Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status, m.err, m.fetched = st, "", m.now()
	m.busy = ""
	m.patch()
}

// SetError meldt een mislukte fetch of start/stop; de oude data blijft.
func (m *Menu) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err.Error()
	m.busy = ""
	m.patch()
}

// Key verwerkt een doorgestuurde toets: r of F5 ververst.
func (m *Menu) Key(code uint32, down bool) {
	if !down {
		return
	}
	if code == 82 || code == 116 {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.Refresh != nil {
			m.Refresh()
		}
	}
}

// patch werkt knopteksten, kop en voetregel bij (alleen wat wijzigt reist —
// SetText dedupt).
func (m *Menu) patch() {
	for i, b := range m.buttons {
		m.conn.SetText(b, m.buttonText(m.apps[i]))
	}
	cl := "geen verbinding"
	if m.status.ClusterName != "" {
		cl = fmt.Sprintf("%s  %da %dj %dt", m.status.ClusterName,
			m.status.Agents, m.status.Jobs, m.status.TotalPlaced)
	}
	m.conn.SetText(m.cluster, cl)

	var s string
	switch {
	case m.err != "":
		s = "FOUT: " + m.err
	case m.busy != "":
		s = "start " + m.busy + " ..."
	case m.fetched.IsZero():
		s = "verbinden ..."
	default:
		s = "bijgewerkt " + ui.Ago(m.now().Sub(m.fetched)) + " geleden  --  klik: start  r: ververs"
	}
	m.conn.SetText(m.footer, s)
}

// buttonText is het knoplabel: draaiend krijgt een ster (met aantal bij
// meerdere instanties), bezig een "...".
func (m *Menu) buttonText(a App) string {
	switch {
	case m.busy == a.Name:
		return a.Name + " ..."
	case m.status.Placed[a.Name] > 1:
		return fmt.Sprintf("* %s x%d", a.Name, m.status.Placed[a.Name])
	case m.status.Placed[a.Name] > 0:
		return "* " + a.Name
	default:
		return a.Name
	}
}
