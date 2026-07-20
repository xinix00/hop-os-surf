// Package hopapi is de leesbril op het HOP-cluster: een kleine client voor
// de /v1-API van de leader (elke agent proxyt die door, dus HOP_ADDR mag naar
// elke agent wijzen). Alleen stdlib, eigen JSON-types met precies de velden
// die we tonen — geen dependency op de hop-module, dus host-testbaar en
// tamago-compatibel.
//
// Auth is HOP's HMAC-schema (hop/pkg/httputil): X-Hop-Auth =
// hex(HMAC-SHA256(key, METHOD\nPATH\nhex(sha256(body)))). Lege key = geen
// auth (dev/standalone).
package hopapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Agent is één geregistreerde node (GET /v1/agents).
type Agent struct {
	ID       string    `json:"id"`
	Endpoint string    `json:"endpoint"`
	Version  string    `json:"version"`
	LastSeen time.Time `json:"last_seen"`
}

// Job is de gewenste staat van één job (GET /v1/jobs) — alleen de velden
// die de taskmanager toont.
type Job struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver,omitempty"` // "" = exec
	Image   string            `json:"image,omitempty"`
	Command string            `json:"command,omitempty"`
	Count   int               `json:"count,omitempty"` // 0 = 1
	Ports   map[string]int    `json:"ports,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`
}

// Task is één draaiende instantie (GET /v1/jobs/{name}/status).
type Task struct {
	ID           string         `json:"id"`
	JobName      string         `json:"job_name"`
	Driver       string         `json:"driver"`
	Ports        map[string]int `json:"ports"`
	Pid          int            `json:"pid"`
	State        string         `json:"state"` // running/stopping/failed/stopped
	StartedAt    time.Time      `json:"started_at"`
	RestartCount int            `json:"restart_count"`
	CPUPercent   float64        `json:"cpu_percent"`
	MemPercent   float64        `json:"mem_percent"`
}

// Status is het clusteroverzicht (GET /v1/status).
type Status struct {
	ClusterName string         `json:"cluster_name"`
	Agents      int            `json:"agents"`
	Jobs        int            `json:"jobs"`
	TotalPlaced int            `json:"total_placed"`
	Settling    bool           `json:"settling"`
	Placed      map[string]int `json:"placed"` // jobnaam → geplaatst aantal
}

// JobStatus is het antwoord van GET /v1/jobs/{name}/status: welke agents de
// job dragen en per agent-id de tasks.
type JobStatus struct {
	Agents       []Agent           `json:"agents"`
	TasksByAgent map[string][]Task `json:"tasks_by_agent"`
}

// Client praat met één HOP-endpoint. Base is "http://host:poort" (zonder
// slash op het eind), Key de cluster-API-key ("" = geen auth).
type Client struct {
	Base string
	Key  string
	HTTP *http.Client // nil = default met 10s-timeout
}

var defaultHTTP = &http.Client{Timeout: 10 * time.Second}

// Status haalt het clusteroverzicht op.
func (c *Client) Status() (Status, error) {
	var s Status
	err := c.get("/v1/status", &s)
	return s, err
}

// Agents haalt de geregistreerde agents op.
func (c *Client) Agents() ([]Agent, error) {
	var a []Agent
	err := c.get("/v1/agents", &a)
	return a, err
}

// Jobs haalt alle jobs op.
func (c *Client) Jobs() ([]Job, error) {
	var j []Job
	err := c.get("/v1/jobs", &j)
	return j, err
}

// JobStatus haalt de tasks van één job op.
func (c *Client) JobStatus(name string) (JobStatus, error) {
	var js JobStatus
	err := c.get("/v1/jobs/"+name+"/status", &js)
	return js, err
}

// Apply dient een jobspec in (POST /v1/jobs — upsert, zoals `hop apply`).
// spec is de rauwe JSON: de launcher stuurt zijn catalogusregels
// onaangeroerd door, dus elke jobspec die de API begrijpt werkt hier ook.
func (c *Client) Apply(spec []byte) error {
	req, err := http.NewRequest("POST", c.Base+"/v1/jobs", bytes.NewReader(spec))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("X-Hop-Auth", sign(c.Key, "POST", req.URL.Path, spec))
	}
	client := c.HTTP
	if client == nil {
		client = defaultHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("hop: apply: %w", ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("hop: apply: %s (%s)", resp.Status, b)
	}
	return nil
}

// Delete verwijdert een job (DELETE /v1/jobs/{name}) — de stop-knop van de
// launcher: HOP ruimt de tasks op en het window verdwijnt vanzelf.
func (c *Client) Delete(name string) error {
	req, err := http.NewRequest("DELETE", c.Base+"/v1/jobs/"+name, nil)
	if err != nil {
		return err
	}
	if c.Key != "" {
		req.Header.Set("X-Hop-Auth", sign(c.Key, "DELETE", req.URL.Path, nil))
	}
	client := c.HTTP
	if client == nil {
		client = defaultHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("hop: delete: %w", ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("hop: delete: %s (%s)", resp.Status, b)
	}
	return nil
}

// LogStream is één live logstaart. Lines levert de regels; het kanaal sluit
// als de stream eindigt (task weg, verbinding stuk). Close stopt de stream.
type LogStream struct {
	Lines  <-chan string
	cancel context.CancelFunc
}

// Close stopt de stream; Lines sluit daarna vanzelf.
func (s *LogStream) Close() { s.cancel() }

// logClient: zonder totale timeout — een logstaart is bedoeld om open te
// blijven; annuleren gaat via de context van de request (Close).
var logClient = &http.Client{}

// Logs opent de live logstaart van één task (SSE: regels `data: <regel>`).
// stream is "stdout" of "stderr"; agentID en taskID komen uit JobStatus.
func (c *Client) Logs(agentID, taskID, stream string) (*LogStream, error) {
	path := "/v1/agents/" + agentID + "/logs/" + taskID + "/" + stream
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", c.Base+path, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.Key != "" {
		req.Header.Set("X-Hop-Auth", sign(c.Key, "GET", req.URL.Path, nil))
	}
	client := c.HTTP
	if client == nil {
		client = logClient
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("hop: logs: %w", ue.Err)
		}
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("hop: logs: %s (%s)", resp.Status, b)
	}

	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 4096), 256*1024) // logregels kunnen fors zijn
		for sc.Scan() {
			line, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue // event-regels en de lege scheiders
			}
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &LogStream{Lines: ch, cancel: cancel}, nil
}

// get doet een gesigneerde GET en decodeert JSON in out.
func (c *Client) get(path string, out any) error {
	req, err := http.NewRequest("GET", c.Base+path, nil)
	if err != nil {
		return err
	}
	if c.Key != "" {
		req.Header.Set("X-Hop-Auth", sign(c.Key, "GET", req.URL.Path, nil))
	}
	client := c.HTTP
	if client == nil {
		client = defaultHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		// url.Error herhaalt de volledige URL ('Get "http://...": ...') —
		// op een statusregel is alleen de oorzaak interessant.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("hop: %s: %w", path, ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("hop: %s: %s (%s)", path, resp.Status, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// sign bouwt HOP's request-handtekening: HMAC over METHOD\nPATH\nbody-hash.
// Moet byte-voor-byte gelijk zijn aan hop/pkg/httputil.Sign.
func sign(key, method, path string, body []byte) string {
	sum := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(method + "\n" + path + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(mac.Sum(nil))
}
