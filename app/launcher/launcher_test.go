package launcher

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/app/hopapi"
	"github.com/xinix00/hop-os-surf/stack/scene"
)

var t0 = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

type fakeConn struct{ root *scene.Node }

func (f *fakeConn) Show(n *scene.Node) error { f.root = n; return nil }
func (f *fakeConn) SetText(n *scene.Node, s string) {
	if n != nil {
		n.Text = s
	}
}

// catalogus zoals HopOS hem levert: hopos.apps[]-regels, {{host}} al vervangen.
const demoCatalog = `[
	{"name":"clock","driver":"hop","artifacts":[{"url":"http://10.0.0.5/clock.elf"}],"env":{"SURF_ADDR":"10.0.0.7:7878"}},
	{"name":"calc","driver":"hop","artifacts":[{"url":"http://10.0.0.5/calc.elf"}]},
	{"name":"browser","driver":"hop","artifacts":[{"url":"http://10.0.0.5/browser.elf"}]},
	{"name":"taskman","driver":"hop","artifacts":[{"url":"http://10.0.0.5/taskman.elf"}]},
	{"name":"redis","image":"redis:7","command":"redis-server"}
]`

func demo(t *testing.T) (*Menu, *fakeConn) {
	apps, err := ParseCatalog(demoCatalog)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeConn{}
	m := NewMenu(f, apps)
	m.now = func() time.Time { return t0 }
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	m.SetData(hopapi.Status{ClusterName: "dev", Agents: 1, Jobs: 2,
		Placed: map[string]int{"clock": 1, "taskman": 1}})
	return m, f
}

func TestParseCatalog(t *testing.T) {
	apps, err := ParseCatalog(demoCatalog)
	if err != nil || len(apps) != 5 {
		t.Fatalf("ParseCatalog → %d apps, %v", len(apps), err)
	}
	if apps[0].Name != "clock" || apps[0].Driver != "hop" {
		t.Errorf("app 0 → %+v", apps[0])
	}
	if apps[4].Driver != "docker" { // image zonder driver = docker
		t.Errorf("redis hoort docker te zijn: %+v", apps[4])
	}
	if !strings.Contains(string(apps[0].Spec), "SURF_ADDR") {
		t.Error("de rauwe spec hoort onaangeroerd bewaard te blijven")
	}

	if _, err := ParseCatalog("geen json"); err == nil {
		t.Error("rommel hoort een fout te geven")
	}
	if apps, err := ParseCatalog(`[{"driver":"hop"},{"name":"ok"}]`); err == nil || len(apps) != 1 {
		t.Errorf("naamloze regel hoort overgeslagen én gemeld: %d apps, %v", len(apps), err)
	}
}

// TestStart: een klik start — óók op een draaiende app (meerdere instanties
// mogen; stoppen doe je met het rode stoplichtje). Eén start tegelijk, en de
// eerstvolgende status maakt hem af.
func TestStart(t *testing.T) {
	m, _ := demo(t)
	var started []string
	m.OnStart = func(a App) { started = append(started, a.Name) }

	// clock draait al (geplaatst): klik = gewoon nóg een start
	m.buttons[0].OnClick()
	if len(started) != 1 || started[0] != "clock" || m.busy != "clock" {
		t.Fatalf("klik op draaiende hoort te starten: started=%v busy=%q", started, m.busy)
	}
	if !strings.HasPrefix(m.footer.Text, "start clock") {
		t.Fatalf("voetregel: %q", m.footer.Text)
	}
	// tweede klik tijdens een lopende actie doet niets
	m.buttons[1].OnClick()
	if len(started) != 1 {
		t.Fatal("één start tegelijk")
	}
	// de eerstvolgende status klaart busy; twee instanties tonen een teller
	m.SetData(hopapi.Status{ClusterName: "dev", Placed: map[string]int{"clock": 2, "taskman": 1}})
	if m.busy != "" {
		t.Fatalf("busy hoort geklaard na een verse status: %q", m.busy)
	}
	if m.buttons[0].Text != "* clock x2" {
		t.Fatalf("knoptekst bij 2 instanties: %q", m.buttons[0].Text)
	}

	// en na het sluiten van beide windows is de knop weer kaal
	m.SetData(hopapi.Status{ClusterName: "dev", Placed: map[string]int{"taskman": 1}})
	if m.buttons[0].Text != "clock" {
		t.Fatalf("knoptekst na sluiten: %q", m.buttons[0].Text)
	}
}

func TestFoutEnRefresh(t *testing.T) {
	m, _ := demo(t)
	m.busy = "calc"
	m.SetError(errors.New("hop: apply: connection refused"))
	if !strings.HasPrefix(m.footer.Text, "FOUT:") || m.busy != "" {
		t.Fatalf("fout hoort busy te klaren en in de voetregel: %q", m.footer.Text)
	}
	refreshed := false
	m.Refresh = func() { refreshed = true }
	m.Key(82, true)
	if !refreshed {
		t.Fatal("r hoort Refresh te vragen")
	}
}

// TestBoomReist: de menuboom encodeert en decodeert schoon, ook leeg.
func TestBoomReist(t *testing.T) {
	m, f := demo(t)
	_ = m
	ids := uint16(0)
	var walk func(n *scene.Node)
	walk = func(n *scene.Node) {
		ids++
		n.ID = ids
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(f.root)
	if _, err := scene.Decode(scene.Encode(f.root)); err != nil {
		t.Fatal(err)
	}

	leeg := NewMenu(&fakeConn{}, nil)
	if err := leeg.Start(); err != nil {
		t.Fatal(err)
	}
}
