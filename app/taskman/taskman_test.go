package taskman

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xinix00/hop-os-surf/app/hopapi"
	"github.com/xinix00/hop-os-surf/stack/scene"
)

var t0 = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

// fakeConn registreert wat de app de display zou sturen: de laatst getoonde
// boom en de patches per node — de host-tests zien zo het hele draadgedrag
// zonder display.
type fakeConn struct {
	root  *scene.Node
	shows int
}

func (f *fakeConn) Show(n *scene.Node) error { f.root, f.shows = n, f.shows+1; return nil }
func (f *fakeConn) SetText(n *scene.Node, s string) {
	if n != nil {
		n.Text = s
	}
}
func (f *fakeConn) SetItems(n *scene.Node, items []string) {
	if n != nil {
		n.Items = items
	}
}
func (f *fakeConn) AddItems(n *scene.Node, items []string) {
	if n != nil {
		n.Items = append(n.Items, items...)
	}
}
func (f *fakeConn) SetSel(n *scene.Node, sel int32) {
	if n != nil {
		n.Sel = sel
	}
}

func demo(t *testing.T) (*App, *fakeConn) {
	f := &fakeConn{}
	a := New(f)
	a.now = func() time.Time { return t0 }
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	a.SetData(
		hopapi.Status{ClusterName: "dev", Agents: 3, Jobs: 4, TotalPlaced: 5,
			Placed: map[string]int{"web": 2, "redis": 1, "worker": 1, "clock": 1}},
		[]hopapi.Agent{
			{ID: "node-a", Endpoint: "http://10.0.0.1:8080", Version: "v0.3.1", LastSeen: t0.Add(-2 * time.Second)},
			{ID: "node-b", Endpoint: "http://10.0.0.2:8080", Version: "v0.3.1", LastSeen: t0.Add(-18 * time.Second)},
			{ID: "node-c", Endpoint: "http://10.0.0.3:8080", Version: "v0.3.0", LastSeen: t0.Add(-2 * time.Minute)},
		},
		[]hopapi.Job{
			{Name: "web", Command: "./server", Count: 2},
			{Name: "redis", Image: "redis:7", Command: "redis-server"},
			{Name: "worker", Command: "./worker", Count: 2},
			{Name: "clock", Driver: "hop", Count: 2},
		})
	return a, f
}

func demoDetail() hopapi.JobStatus {
	return hopapi.JobStatus{
		Agents: []hopapi.Agent{
			{ID: "node-a", Endpoint: "http://10.0.0.1:8080"},
			{ID: "node-b", Endpoint: "http://10.0.0.2:8080"},
		},
		TasksByAgent: map[string][]hopapi.Task{
			"node-a": {{ID: "web-1", State: "running", Pid: 3120, StartedAt: t0.Add(-90 * time.Minute), CPUPercent: 34, MemPercent: 61}},
			"node-b": {{ID: "web-2", State: "running", Pid: 811, StartedAt: t0.Add(-42 * time.Second), RestartCount: 3, CPUPercent: 78, MemPercent: 12}},
		},
	}
}

// TestClusterNaarLogs loopt de hele weg omlaag: agents → tasks → jobdetail →
// logstaart, en Esc weer terug omhoog.
func TestClusterNaarLogs(t *testing.T) {
	a, f := demo(t)
	if a.screen != ScreenAgents || len(a.list.Items) != 3 {
		t.Fatalf("start: scherm %d, %d agents", a.screen, len(a.list.Items))
	}
	if !strings.HasPrefix(a.list.Items[0], "+ node-a") || !strings.HasPrefix(a.list.Items[2], "x node-c") {
		t.Fatalf("agent-markering: %q / %q", a.list.Items[0], a.list.Items[2])
	}

	// Tab → TASKS (gesorteerd: clock, redis, web, worker)
	a.Key(9, true)
	if a.screen != ScreenTasks || len(a.list.Items) != 4 {
		t.Fatalf("na tab: scherm %d, %d jobs", a.screen, len(a.list.Items))
	}
	if !strings.HasPrefix(a.list.Items[2], "+ web") {
		t.Fatalf("jobrij: %q", a.list.Items[2])
	}

	// klik op web → detail + OpenJob-hook
	var opened string
	a.OpenJob = func(job string) { opened = job }
	f.root = nil
	a.list.OnSelect(2)
	if a.screen != ScreenDetail || opened != "web" || f.root == nil {
		t.Fatalf("detail: scherm %d, opened %q", a.screen, opened)
	}
	a.SetDetail("andere-job", demoDetail()) // verlaat antwoord: valt weg
	if a.detail != nil {
		t.Fatal("detail van een andere job hoort genegeerd")
	}
	a.SetDetail("web", demoDetail())
	rows := a.list.Items
	if len(rows) != 7 { // feiten, 2×(lege+kop+task)
		t.Fatalf("detailrijen: %d (%q)", len(rows), rows)
	}
	if !strings.Contains(rows[3], "web-1") || !strings.Contains(rows[3], "pid 3120") {
		t.Fatalf("taskrij: %q", rows[3])
	}

	// klik op web-2 (laatste rij) → logs + OpenLog-hook
	var gotAgent, gotTask, gotStream string
	a.OpenLog = func(agent, task, stream string) { gotAgent, gotTask, gotStream = agent, task, stream }
	a.list.OnSelect(6)
	if a.screen != ScreenLogs || gotAgent != "node-b" || gotTask != "web-2" || gotStream != "stdout" {
		t.Fatalf("logs: scherm %d, %s/%s/%s", a.screen, gotAgent, gotTask, gotStream)
	}
	a.LogLines("node-b", "web-2", "stdout", []string{"regel 1", "regel 2"})
	if len(a.list.Items) != 2 || a.list.Items[1] != "regel 2" {
		t.Fatalf("logstaart: %v", a.list.Items)
	}

	// Esc: logs → detail (met verse OpenJob), nog eens: detail → tasks
	closed := false
	a.CloseLog = func() { closed = true }
	opened = ""
	a.Key(27, true)
	if a.screen != ScreenDetail || !closed || opened != "web" {
		t.Fatalf("terug uit logs: scherm %d, closed %v, opened %q", a.screen, closed, opened)
	}
	a.Key(27, true)
	if a.screen != ScreenTasks {
		t.Fatalf("terug uit detail: scherm %d", a.screen)
	}
}

// TestFoutEnRefresh: een fout komt in de voetregel (data blijft), r ververst.
func TestFoutEnRefresh(t *testing.T) {
	a, _ := demo(t)
	a.SetError(errors.New("hop: /v1/status: connection refused"))
	if !strings.HasPrefix(a.footer.Text, "FOUT:") || len(a.agents) != 3 {
		t.Fatalf("fout hoort in de voetregel mét behoud van data: %q", a.footer.Text)
	}
	refreshed := false
	a.Refresh = func() { refreshed = true }
	a.Key(82, true)
	if !refreshed {
		t.Fatal("r hoort Refresh te vragen")
	}
}

// TestBomenReizen: elke schermboom encodeert en decodeert schoon.
func TestBomenReizen(t *testing.T) {
	a, f := demo(t)
	assign := func(n *scene.Node) { // zoals Show ids toekent
		id := uint16(0)
		var walk func(*scene.Node)
		walk = func(n *scene.Node) {
			id++
			n.ID = id
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(n)
	}
	check := func(when string) {
		assign(f.root)
		if _, err := scene.Decode(scene.Encode(f.root)); err != nil {
			t.Fatalf("%s: %v", when, err)
		}
	}
	check("agents")
	a.Key(9, true)
	check("tasks")
	a.list.OnSelect(2)
	a.SetDetail("web", demoDetail())
	check("detail")
	a.list.OnSelect(3)
	a.LogLines("node-b", "web-2", "stdout", []string{"hallo"})
	check("logs")
}
