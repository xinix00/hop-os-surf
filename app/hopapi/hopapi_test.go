package hopapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSigning legt het wire-formaat vast tegen hop/pkg/httputil.Sign:
// HMAC-SHA256 over "METHOD\nPATH\nhex(sha256(body))". De vector is met de
// echte httputil.Sign gegenereerd — verandert dit, dan praat de client niet
// meer met het cluster.
func TestSigning(t *testing.T) {
	got := sign("geheim", "GET", "/v1/agents", nil)
	want := "a97de85bb624b873458584108c5c9a5f44224a02507a0100bed2dad57ae80c01"
	if got != want {
		t.Errorf("sign → %s, want %s", got, want)
	}
	// mét body (Apply): de hash van de exacte bytes gaat mee in de string.
	got = sign("geheim", "POST", "/v1/jobs", []byte(`{"name":"clock","driver":"hop"}`))
	want = "8f4c90af0e69e67d1b8a0d1c38c4c98fc5caf1696da8ce939f92584890b0d692"
	if got != want {
		t.Errorf("sign(body) → %s, want %s", got, want)
	}
}

// TestApply: POST met body-handtekening tegen een nep-leader die hem naloopt.
func TestApply(t *testing.T) {
	const key = "sleutel"
	spec := []byte(`{"name":"clock","driver":"hop"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/v1/jobs" {
			t.Errorf("verwachtte POST /v1/jobs, kreeg %s %s", r.Method, r.URL.Path)
		}
		if string(body) != string(spec) {
			t.Errorf("body hoort onaangeroerd door te gaan: %s", body)
		}
		if got := r.Header.Get("X-Hop-Auth"); got != sign(key, "POST", "/v1/jobs", body) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	if err := (&Client{Base: srv.URL, Key: key}).Apply(spec); err != nil {
		t.Fatal(err)
	}
}

// TestApplyFout: een 409 (update in flight) wordt een leesbare fout.
func TestApplyFout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "job locked", http.StatusConflict)
	}))
	defer srv.Close()
	if err := (&Client{Base: srv.URL}).Apply([]byte(`{}`)); err == nil {
		t.Fatal("409 hoort een fout te geven")
	}
}

// TestClient dekt het hele rondje: gesigneerde GET's, JSON-decodes en de
// vier endpoints, tegen een nep-leader die de handtekening controleert.
func TestClient(t *testing.T) {
	const key = "sleutel"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Hop-Auth"); got != sign(key, r.Method, r.URL.Path, nil) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/status":
			json.NewEncoder(w).Encode(Status{ClusterName: "dev", Agents: 2, Jobs: 1, TotalPlaced: 3, Placed: map[string]int{"web": 3}})
		case "/v1/agents":
			json.NewEncoder(w).Encode([]Agent{{ID: "node-a", Endpoint: "http://10.0.0.1:4646", Version: "v0.1.0", LastSeen: time.Now()}})
		case "/v1/jobs":
			json.NewEncoder(w).Encode([]Job{{Name: "web", Command: "./web", Count: 3}})
		case "/v1/jobs/web/status":
			json.NewEncoder(w).Encode(JobStatus{
				Agents:       []Agent{{ID: "node-a"}},
				TasksByAgent: map[string][]Task{"node-a": {{ID: "web-1", State: "running", Pid: 42}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{Base: srv.URL, Key: key}
	st, err := c.Status()
	if err != nil || st.ClusterName != "dev" || st.Placed["web"] != 3 {
		t.Fatalf("Status → %+v, %v", st, err)
	}
	agents, err := c.Agents()
	if err != nil || len(agents) != 1 || agents[0].ID != "node-a" {
		t.Fatalf("Agents → %+v, %v", agents, err)
	}
	jobs, err := c.Jobs()
	if err != nil || len(jobs) != 1 || jobs[0].Name != "web" {
		t.Fatalf("Jobs → %+v, %v", jobs, err)
	}
	js, err := c.JobStatus("web")
	if err != nil || len(js.TasksByAgent["node-a"]) != 1 || js.TasksByAgent["node-a"][0].Pid != 42 {
		t.Fatalf("JobStatus → %+v, %v", js, err)
	}
}

// TestClientZonderKey: lege key = geen header (dev/standalone, net als HOP).
func TestClientZonderKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hop-Auth") != "" {
			t.Error("lege key hoort géén X-Hop-Auth te sturen")
		}
		json.NewEncoder(w).Encode([]Agent{})
	}))
	defer srv.Close()
	if _, err := (&Client{Base: srv.URL}).Agents(); err != nil {
		t.Fatal(err)
	}
}

// TestClientTransportFout: een dial-fout wordt compact gemeld — de oorzaak
// ("connection refused"), niet url.Error's herhaling van de hele URL. Op de
// statusregel van taskman moet dit in één oogopslag leesbaar zijn (Derek
// 19-07: de fout liep onleesbaar door de voetregel heen).
func TestClientTransportFout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // poort is nu dicht: gegarandeerd connection refused
	_, err := (&Client{Base: srv.URL}).Status()
	if err == nil {
		t.Fatal("dichte poort hoort een fout te geven")
	}
	if s := err.Error(); strings.Contains(s, "Get \"") || !strings.Contains(s, "/v1/status") {
		t.Errorf("fout hoort compact te zijn (pad + oorzaak, geen volle URL): %q", s)
	}
}

// TestLogs: de SSE-staart — gesigneerde GET, `data: `-regels als kanaal, en
// Close die de stream netjes afbreekt.
func TestLogs(t *testing.T) {
	const key = "sleutel"
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/node-a/logs/web-1/stdout" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Hop-Auth"); got != sign(key, "GET", r.URL.Path, nil) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: regel een\n\ndata: regel twee\n\n"))
		f.Flush()
		<-r.Context().Done() // stream blijft open tot de client Close doet
		close(blocked)
	}))
	defer srv.Close()

	c := &Client{Base: srv.URL, Key: key}
	ls, err := c.Logs("node-a", "web-1", "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-ls.Lines; got != "regel een" {
		t.Fatalf("regel 1 → %q", got)
	}
	if got := <-ls.Lines; got != "regel twee" {
		t.Fatalf("regel 2 → %q", got)
	}
	ls.Close()
	<-blocked // de server zag de context sterven: Close breekt echt af
	for range ls.Lines {
	} // en het kanaal sluit
}

// TestLogsFout: een 404 (task weg) is een fout bij het openen, geen leeg kanaal.
func TestLogsFout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer srv.Close()
	if _, err := (&Client{Base: srv.URL}).Logs("a", "t", "stdout"); err == nil {
		t.Fatal("404 hoort een fout te geven")
	}
}

// TestClientFout: een niet-200 wordt een leesbare fout, geen stille nil.
func TestClientFout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := (&Client{Base: srv.URL}).Agents(); err == nil {
		t.Fatal("401 hoort een fout te geven")
	}
}
