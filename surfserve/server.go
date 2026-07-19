// Package surfserve is de netwerkkant van de display-app: SURF-sessies
// (docs/gui-ontwerp.md §3) plus het meetinstrument — /screen.png en de
// browser-KVM op /kvm (§6, trap 1). Los van main zodat het op de
// ontwikkelmachine integraal testbaar is (window ↔ server ↔ compositor).
package surfserve

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/xinix00/hop-os-surf/compositor"
	"github.com/xinix00/hop-os-surf/surf"
)

// Server bindt SURF-sessies aan één compositor.
type Server struct {
	comp *compositor.Compositor
	logf func(format string, args ...any)

	mu       sync.Mutex
	sessions map[*compositor.Surface]*session // surface → eigenaar (input-routering)

	pngMu   sync.Mutex
	pngGen  uint64
	pngData []byte
}

// New maakt een server rond een compositor; logf mag nil zijn.
func New(comp *compositor.Compositor, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{comp: comp, logf: logf, sessions: make(map[*compositor.Surface]*session)}
	// De WM beslist de maat: elke Relayout-wijziging wordt een CONFIGURE
	// naar de eigenaar van de surface (docs/gui-ontwerp.md §3).
	comp.OnResize(s.configure)
	return s
}

// configure stuurt de nieuwe WM-maat naar de app die de surface bezit.
func (s *Server) configure(sur *compositor.Surface, w, h int) {
	s.mu.Lock()
	sess := s.sessions[sur]
	s.mu.Unlock()
	if sess == nil {
		return
	}
	if sid, ok := sess.idOf(sur); ok {
		sess.send(surf.TypeConfigure, sid, surf.Configure{W: uint16(w), H: uint16(h)}.Encode())
	}
}

// session is één app-verbinding met zijn surfaces.
type session struct {
	srv  *Server
	conn net.Conn
	app  string

	writeMu sync.Mutex // INPUT/CONFIGURE delen de stream met elkaar

	mu       sync.Mutex // surfaces/damage: read-lus, relayout-callbacks én input-routering
	surfaces map[uint16]*compositor.Surface
	damage   map[uint16][]image.Rectangle // rects van dit frame, tot de PRESENT
}

// get geeft de surface bij een id (nil onbekend).
func (sess *session) get(id uint16) *compositor.Surface {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.surfaces[id]
}

// put registreert id → surface en geeft de eventuele oude terug.
func (sess *session) put(id uint16, sur *compositor.Surface) (old *compositor.Surface) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	old = sess.surfaces[id]
	sess.surfaces[id] = sur
	return old
}

// idOf zoekt het id van een surface (input/configure gaan per id terug).
func (sess *session) idOf(sur *compositor.Surface) (uint16, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for sid, cur := range sess.surfaces {
		if cur == sur {
			return sid, true
		}
	}
	return 0, false
}

// addDamage onthoudt een frame-rect tot de PRESENT (gemaximeerd: bij >32
// rects is één volle flip goedkoper dan de administratie).
func (sess *session) addDamage(id uint16, r image.Rectangle) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if acc, ok := sess.damage[id]; !ok || len(acc) <= 32 {
		sess.damage[id] = append(acc, r)
	}
}

// takeDamage geeft de opgebouwde rects van dit frame (nil = alles flippen).
func (sess *session) takeDamage(id uint16) []image.Rectangle {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	acc := sess.damage[id]
	delete(sess.damage, id)
	if len(acc) > 32 {
		return nil
	}
	return acc
}

// takeAll leegt de map (cleanup) en geeft alle surfaces terug.
func (sess *session) takeAll() []*compositor.Surface {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	all := make([]*compositor.Surface, 0, len(sess.surfaces))
	for _, sur := range sess.surfaces {
		all = append(all, sur)
	}
	sess.surfaces = make(map[uint16]*compositor.Surface)
	return all
}

// ServeSURF draait de accept-lus tot de listener sluit.
func (s *Server) ServeSURF(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	sess := &session{
		srv: s, conn: conn,
		surfaces: make(map[uint16]*compositor.Surface),
		damage:   make(map[uint16][]image.Rectangle),
	}
	defer sess.cleanup()

	var buf []byte
	var h surf.Header
	var err error

	// Eerste bericht moet HELLO zijn. Tokenverificatie is de v0-stub:
	// aangenomen en gelogd; de echte check hangt straks aan de clustersleutels.
	h, buf, err = surf.ReadMsg(conn, buf)
	if err != nil || h.Type != surf.TypeHello {
		s.logf("surf: %s: expected HELLO, got type %d (%v)", conn.RemoteAddr(), h.Type, err)
		return
	}
	hello, err := surf.DecodeHello(buf)
	if err != nil || hello.Version != surf.Version {
		s.logf("surf: %s: bad HELLO (version %d, %v)", conn.RemoteAddr(), hello.Version, err)
		return
	}
	sess.app = hello.App
	s.logf("surf: %s connected (%s)", hello.App, conn.RemoteAddr())

	for {
		h, buf, err = surf.ReadMsg(conn, buf)
		if err != nil {
			s.logf("surf: %s disconnected (%v)", sess.app, err)
			return
		}
		switch h.Type {
		case surf.TypeCreate:
			c, err := surf.DecodeCreate(buf)
			if err != nil || c.Format != surf.FormatXRGB8888 || c.W == 0 || c.H == 0 {
				s.logf("surf: %s: bad CREATE (%v)", sess.app, err)
				return
			}
			// V0 heeft geen titelveld in CREATE: de HELLO-appnaam is de
			// windowtitel (de app zet daar zelf zijn herkomst in). De
			// CREATE-maat is een hint — Relayout kent de echte maat toe en
			// de OnResize-callback stuurt de CONFIGURE; registratie moet
			// dus vóór de Relayout.
			sur := s.comp.Add(sess.app, int(c.W), int(c.H))
			if old := sess.put(h.Surface, sur); old != nil {
				// Her-CREATE van hetzelfde id: oude weg, wees voorspelbaar.
				s.comp.Remove(old)
				s.unregister(old)
			}
			s.register(sur, sess)
			s.comp.Relayout()
		case surf.TypeDamage:
			d, pix, err := surf.DecodeDamage(buf)
			sur := sess.get(h.Surface)
			if err != nil || sur == nil {
				s.logf("surf: %s: bad DAMAGE (%v)", sess.app, err)
				return
			}
			if err := sur.Damage(int(d.X), int(d.Y), int(d.W), int(d.H), pix); err != nil {
				s.logf("surf: %s: %v", sess.app, err)
				return
			}
			sess.addDamage(h.Surface, image.Rect(int(d.X), int(d.Y), int(d.X)+int(d.W), int(d.Y)+int(d.H)))
		case surf.TypePresent:
			if sur := sess.get(h.Surface); sur != nil {
				// Alleen de gedamagede rects flippen — partiële presents
				// (hover, klok-wijzers) blijven zo klein tot in de stream.
				s.comp.PresentRects(sur, sess.takeDamage(h.Surface))
			}
		case surf.TypeClose:
			return
		default:
			// Onbekende types (o.a. de gereserveerde scene-laag) negeren:
			// nieuwere apps blijven werken op een oudere display.
		}
	}
}

func (s *Server) register(sur *compositor.Surface, sess *session) {
	s.mu.Lock()
	s.sessions[sur] = sess
	s.mu.Unlock()
}

func (s *Server) unregister(sur *compositor.Surface) {
	s.mu.Lock()
	delete(s.sessions, sur)
	s.mu.Unlock()
}

func (sess *session) cleanup() {
	all := sess.takeAll()
	for _, sur := range all {
		sess.srv.comp.Remove(sur)
		sess.srv.unregister(sur)
	}
	if len(all) > 0 {
		sess.srv.comp.Relayout() // de rest krijgt de vrijgekomen ruimte
	}
	sess.conn.Close()
}

// send schrijft één bericht naar de app; een schrijffout sluit de verbinding
// (de leeslus ruimt daarna op).
func (sess *session) send(typ uint8, surface uint16, payload []byte) {
	sess.writeMu.Lock()
	defer sess.writeMu.Unlock()
	if err := surf.WriteMsg(sess.conn, typ, surface, payload); err != nil {
		sess.conn.Close()
	}
}

// Input routeert één input-event: bewegingen sturen de cursor en gaan (net
// als knoppen) naar het window ónder de aanwijzer; toetsen en wiel naar de
// focus. Een knop-down verlegt eerst de focus (klik = focus, §5).
func (s *Server) Input(ev surf.Input) {
	var sur *compositor.Surface
	var lx, ly int
	var ok bool

	switch ev.Kind {
	case surf.InputMove:
		// Geen SetCursor: de cursor wordt niet gecomponeerd (browser heeft
		// z'n eigen; op de Pi straks een HVS-plane) — een move kost het
		// scherm dus niets, alleen de app onder de aanwijzer hoort hem.
		sur, lx, ly, ok = s.comp.SurfaceAt(int(ev.X), int(ev.Y))
	case surf.InputButton:
		if ev.Value != 0 {
			sur, lx, ly, ok = s.comp.ClickAt(int(ev.X), int(ev.Y))
		} else {
			sur, lx, ly, ok = s.comp.SurfaceAt(int(ev.X), int(ev.Y))
		}
	default: // toetsen, wiel → focus
		sur = s.comp.Focused()
		ok = sur != nil
	}
	if !ok {
		return
	}

	s.mu.Lock()
	sess := s.sessions[sur]
	s.mu.Unlock()
	if sess == nil {
		return
	}
	// Lokale coördinaten voor positie-events; het surface-id van de eigenaar.
	if ev.Kind == surf.InputMove || ev.Kind == surf.InputButton {
		ev.X, ev.Y = uint16(lx), uint16(ly)
	}
	id, _ := sess.idOf(sur)
	sess.send(surf.TypeInput, id, ev.Encode())
}

// Handler is de HTTP-kant: /screen.png (het meetinstrument), /kvm (browser-
// KVM) en /input (de event-terugweg van de KVM-pagina).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/screen.png", s.serveScreen)
	mux.HandleFunc("/stream", s.serveStream)
	mux.HandleFunc("/kvm", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, kvmPage)
	})
	mux.HandleFunc("/input", s.serveInput)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/kvm", http.StatusFound)
	})
	return mux
}

// serveScreen componeert (lazy — geen client, geen werk) en cachet de PNG
// per compositor-generatie: tien kijkers kosten één encode.
func (s *Server) serveScreen(w http.ResponseWriter, r *http.Request) {
	s.comp.Compose()
	img, gen := s.comp.Snapshot()

	s.pngMu.Lock()
	if gen != s.pngGen || s.pngData == nil {
		var buf bytes.Buffer
		// BestSpeed: op één (geëmuleerde) core is de default-compressie de
		// bottleneck van de hele input-lus — elke cursorbeweging maakt het
		// scherm vuil en dus een verse encode. Groter bestand, veel
		// snellere display-app; LAN vindt het prima.
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, img); err != nil {
			s.pngMu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.pngGen, s.pngData = gen, buf.Bytes()
	}
	data := s.pngData
	s.pngMu.Unlock()

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// serveStream pusht damage-frames (compositor.FrameSince-formaat) over één
// chunked HTTP-response — de browser tekent alleen de veranderde rechthoeken
// (putImageData) en er wordt geen PNG meer geëncodeerd voor kijkers. Idle
// scherm = nul bytes. 25 Hz polling op het generatienummer is display-side
// een mutex-check; de kijkers-kant van docs/gui-ontwerp.md §6.
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")

	// ?z=1: elk frame deflate-raw gecomprimeerd (browser: DecompressionStream
	// — lossless, geen JS-bibliotheek). Het vlakke instrumentenpaneel haalt
	// makkelijk 20-50×; BestSpeed houdt de display-core vrij. De framing
	// blijft gelijk: u32 len | (gecomprimeerde) payload.
	var zw *flate.Writer
	var zbuf bytes.Buffer
	if r.URL.Query().Get("z") == "1" {
		zw, _ = flate.NewWriter(&zbuf, flate.BestSpeed)
	}

	var gen uint64
	for {
		frame, next := s.comp.FrameSince(gen)
		if next != gen {
			if zw != nil {
				zbuf.Reset()
				zbuf.Write([]byte{0, 0, 0, 0})
				zw.Reset(&zbuf)
				zw.Write(frame[4:])
				if err := zw.Close(); err != nil { // Close: het eindblok, anders wacht de browser
					return
				}
				out := zbuf.Bytes()
				binary.LittleEndian.PutUint32(out, uint32(len(out)-4))
				frame = out
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			fl.Flush()
			gen = next
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(15 * time.Millisecond):
		}
	}
}

// inputMsg is het JSON-event van de KVM-pagina.
type inputMsg struct {
	K string `json:"k"` // "key" | "move" | "btn" | "wheel"
	C int    `json:"c"`
	V int    `json:"v"`
	X int    `json:"x"`
	Y int    `json:"y"`
}

// event vertaalt het JSON-event naar een surf.Input.
func (m inputMsg) event() (surf.Input, bool) {
	ev := surf.Input{Code: uint32(m.C), Value: int32(m.V), X: clampU16(m.X), Y: clampU16(m.Y)}
	switch m.K {
	case "key":
		ev.Kind = surf.InputKey
	case "move":
		ev.Kind = surf.InputMove
	case "btn":
		ev.Kind = surf.InputButton
	case "wheel":
		ev.Kind = surf.InputWheel
	default:
		return surf.Input{}, false
	}
	return ev, true
}

func (s *Server) serveInput(w http.ResponseWriter, r *http.Request) {
	if wsUpgrade(r) {
		s.serveInputWS(w, r) // de blijvende input-stream van de KVM-pagina
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var m inputMsg
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ev, ok := m.event()
	if !ok {
		http.Error(w, "unknown kind", http.StatusBadRequest)
		return
	}
	s.Input(ev)
	w.WriteHeader(http.StatusNoContent)
}

func clampU16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 0xFFFF {
		return 0xFFFF
	}
	return uint16(v)
}
