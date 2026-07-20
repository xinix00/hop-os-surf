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
	"strings"
	"sync"
	"time"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/surf"
)

// orphanGrace: hoe lang een window zonder verbinding blijft staan. Ruim
// genoeg voor een HOP-her-plaatsing (download + start ≈ 10-15s); een app
// die dan nóg niet terug is, is echt weg.
const orphanGrace = 45 * time.Second

// orphanInfo is één geparkeerd window: de verbinding viel weg maar de app is
// niet aantoonbaar dood (geen CLOSE gezien). Het window blijft bevroren
// staan; een reconnect van dezelfde app adopteert het geruisloos.
type orphanInfo struct {
	app   string
	since time.Time
}

// Server bindt SURF-sessies aan één compositor.
type Server struct {
	comp *compositor.Compositor
	logf func(format string, args ...any)

	mu       sync.Mutex
	sessions map[*compositor.Surface]*session   // surface → eigenaar (input-routering)
	scenes   map[*compositor.Surface]*sceneView // scene-surfaces (display-side gerenderd, P2)
	orphans  map[*compositor.Surface]orphanInfo // windows zonder verbinding (Dereks wet 19-07: app leeft → window blijft)

	pngMu   sync.Mutex
	pngGen  uint64
	pngData []byte

	// pointer (optioneel): elke muispositie in schermcoördinaten — voor een
	// cursor die búíten de compositie om getekend wordt (fbblit-overlay op
	// het echte glas; de compose blijft cursorvrij, docs/gui-ontwerp.md §5).
	pointer func(x, y int)
}

// OnPointer registreert de cursor-volger (vóór het serveren zetten).
func (s *Server) OnPointer(f func(x, y int)) { s.pointer = f }

// New maakt een server rond een compositor; logf mag nil zijn.
func New(comp *compositor.Compositor, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{comp: comp, logf: logf,
		sessions: make(map[*compositor.Surface]*session),
		scenes:   make(map[*compositor.Surface]*sceneView),
		orphans:  make(map[*compositor.Surface]orphanInfo)}
	// De WM beslist de maat: elke Relayout-wijziging wordt een CONFIGURE
	// naar de eigenaar van de surface (docs/gui-ontwerp.md §3).
	comp.OnResize(s.configure)
	go s.reapOrphans()
	return s
}

// orphan parkeert een window waarvan de verbinding wegviel zonder CLOSE:
// het blijft bevroren staan (geen Remove, geen Relayout — niets verschuift)
// tot de app terugkomt of de grace verstrijkt.
func (s *Server) orphan(sur *compositor.Surface, app string) {
	s.mu.Lock()
	s.orphans[sur] = orphanInfo{app: app, since: time.Now()}
	s.mu.Unlock()
	s.logf("surf: %s: verbinding weg — window geparkeerd (%.0fs grace)", app, orphanGrace.Seconds())
}

// adopt zoekt een geparkeerd window voor een terugkerende app: exact op naam,
// anders op de naam-stam vóór " @ " (een her-plaatste app zit op een ander
// slot maar is hetzelfde window). nil = niets te adopteren.
func (s *Server) adopt(app string) *compositor.Surface {
	s.mu.Lock()
	defer s.mu.Unlock()
	var stemHit *compositor.Surface
	for sur, o := range s.orphans {
		if o.app == app {
			delete(s.orphans, sur)
			return sur
		}
		if stemHit == nil && nameStem(o.app) == nameStem(app) {
			stemHit = sur
		}
	}
	if stemHit != nil {
		delete(s.orphans, stemHit)
	}
	return stemHit
}

// isReset herkent een verbinding die door de peer is afgebroken (TCP-RST):
// het enige signaal dat "de app is dood" betekent. String-match omdat de
// fout door verschillende netstacks komt (gvisor op de node, syscalls in de
// host-tests) — beide zeggen "connection reset".
func isReset(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "reset")
}

// nameStem: "browser @ slot 6" → "browser" (de herkomst-conventie van Open).
func nameStem(s string) string {
	if i := strings.Index(s, " @ "); i > 0 {
		return s[:i]
	}
	return s
}

// reapOrphans ruimt geparkeerde windows op waarvan de app niet terugkwam.
func (s *Server) reapOrphans() {
	for range time.Tick(5 * time.Second) {
		var expired []*compositor.Surface
		s.mu.Lock()
		for sur, o := range s.orphans {
			if time.Since(o.since) > orphanGrace {
				delete(s.orphans, sur)
				expired = append(expired, sur)
				s.logf("surf: %s kwam niet terug — window opgeruimd", o.app)
			}
		}
		s.mu.Unlock()
		for _, sur := range expired {
			s.comp.Remove(sur)
		}
		if len(expired) > 0 {
			s.comp.Relayout()
		}
	}
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
	// Scene-surfaces re-flowen display-side (§4-winst 1): de app hoeft met
	// zijn CONFIGURE niets te doen. Veilig: Relayout roept ons ná zijn
	// unlock aan.
	if v := s.sceneOf(sur); v != nil {
		v.reflow()
	}
}

// session is één app-verbinding met zijn surfaces.
type session struct {
	srv  *Server
	conn net.Conn
	app  string

	writeMu sync.Mutex // INPUT/CONFIGURE delen de stream met elkaar

	// Input loopt via een eigen wachtrij + pomp-goroutine: een app die even
	// niet leest (de browser rendert x seconden bij een scroll — Dereks
	// vondst 19-07) liet de directe send anders op zijn 5s-deadline de hele
	// verbinding sluiten → window weg + zelfheling = "wegschieten en
	// terugschieten". Input is lossy by design: vol = droppen, nooit killen.
	inputQ chan inMsg
	done   chan struct{} // gesloten in cleanup: de pomp stopt
	bye    bool          // app zei CLOSE: écht weg (alleen gezet in de read-lus)

	mu       sync.Mutex // surfaces/damage: read-lus, relayout-callbacks én input-routering
	surfaces map[uint16]*compositor.Surface
	damage   map[uint16][]image.Rectangle // rects van dit frame, tot de PRESENT
}

// inMsg is één gequeued input-event voor surface id.
type inMsg struct {
	id uint16
	ev surf.Input
}

// inputPump schrijft gequeuede input naar de app. Eén schrijver: een trage
// app blokkeert alleen deze goroutine; de wachtrij vangt de burst en de
// enqueue-kant dropt bij vol. send's eigen 5s-deadline blijft het vangnet
// voor een écht dode verbinding.
func (sess *session) inputPump() {
	for {
		select {
		case m := <-sess.inputQ:
			sess.send(surf.TypeInput, m.id, m.ev.Encode())
		case <-sess.done:
			return
		}
	}
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
		inputQ:   make(chan inMsg, 128),
		done:     make(chan struct{}),
	}
	defer sess.cleanup()
	go sess.inputPump()

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
		// Liveness: clients pingen elke ~10s (surf.TypePing). Een hard
		// gekilde slot stuurt nooit een FIN — zonder deze deadline blijft
		// zijn window eeuwig in de compositor staan (gemeten 19-07).
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		h, buf, err = surf.ReadMsg(conn, buf)
		if err != nil {
			// RST = de peer is dood: de switch stuurt hem bij slot-dood, de
			// app-stack bij een (nette) exit — opruimen. Al het andere (EOF
			// van een zelfhelende client, onze eigen deadline, een net-stall)
			// kan een levende app zijn: parkeren (Dereks wet).
			if isReset(err) {
				sess.bye = true
				s.logf("surf: %s: reset — app dood, window opruimen", sess.app)
			} else {
				s.logf("surf: %s disconnected (%v)", sess.app, err)
			}
			return
		}
		switch h.Type {
		case surf.TypeCreate:
			c, err := surf.DecodeCreate(buf)
			if err != nil || c.Format != surf.FormatXRGB8888 || c.W == 0 || c.H == 0 {
				s.logf("surf: %s: bad CREATE (%v)", sess.app, err)
				return
			}
			// Eerst kijken of er een geparkeerd window van deze app staat
			// (verbinding was weg, app niet): dan adopteren — het window
			// stond er nog precies zo, dus géén relayout: niets verschuift.
			sur := s.adopt(sess.app)
			fresh := sur == nil
			if fresh {
				// V0 heeft geen titelveld in CREATE: de HELLO-appnaam is de
				// windowtitel (de app zet daar zelf zijn herkomst in). De
				// CREATE-maat is een hint — Relayout kent de echte maat toe
				// en de OnResize-callback stuurt de CONFIGURE; registratie
				// moet dus vóór de Relayout.
				sur = s.comp.Add(sess.app, int(c.W), int(c.H))
			}
			if old := sess.put(h.Surface, sur); old != nil {
				// Her-CREATE van hetzelfde id: oude weg, wees voorspelbaar.
				s.comp.Remove(old)
				s.unregister(old)
			}
			s.register(sur, sess)
			if fresh {
				s.comp.Relayout()
			} else {
				// De maat is ongewijzigd, maar de app rendert op zijn
				// CREATE-hint tot iemand het zegt: zeg het.
				w, hh := sur.Size()
				sess.send(surf.TypeConfigure, h.Surface,
					surf.Configure{W: uint16(w), H: uint16(hh)}.Encode())
				s.logf("surf: %s terug — window geadopteerd", sess.app)
			}
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
		case surf.TypeScene:
			s.handleScene(sess, h.Surface, buf)
		case surf.TypePatch:
			s.handlePatch(sess, h.Surface, buf)
		case surf.TypeClose:
			sess.bye = true // bewust afscheid: window mag écht weg
			return
		default:
			// Onbekende types negeren: nieuwere apps blijven werken op een
			// oudere display.
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
	delete(s.scenes, sur)
	s.mu.Unlock()
}

// cleanup sluit de sessie af. Dereks wet (19-07): een weggevallen verbinding
// betekent NIET dat het window weg mag — alleen een expliciete CLOSE (de app
// nam bewust afscheid) ruimt meteen op; anders wordt het window geparkeerd
// en wacht het bevroren op de terugkeer van de app (adopt) of de grace.
func (sess *session) cleanup() {
	all := sess.takeAll()
	for _, sur := range all {
		sess.srv.unregister(sur)
		if sess.bye {
			sess.srv.comp.Remove(sur)
		} else {
			sess.srv.orphan(sur, sess.app)
		}
	}
	if sess.bye && len(all) > 0 {
		sess.srv.comp.Relayout() // de rest krijgt de vrijgekomen ruimte
	}
	close(sess.done) // de input-pomp stopt
	sess.conn.Close()
}

// send schrijft één bericht naar de app; een schrijffout sluit de verbinding
// (de leeslus ruimt daarna op). De write-deadline is essentieel: een hard
// gekilde app (rolling update — gemeten op de Pi, 19-07) stuurt nooit een
// RST, zijn send-buffer staat vol en een blokkerende write hier hangt via de
// Relayout→configure-keten de héle display op. Liever de dooie verbinding
// sluiten dan het scherm bevriezen.
func (sess *session) send(typ uint8, surface uint16, payload []byte) {
	sess.writeMu.Lock()
	defer sess.writeMu.Unlock()
	sess.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := surf.WriteMsg(sess.conn, typ, surface, payload); err != nil {
		sess.conn.Close()
	}
	sess.conn.SetWriteDeadline(time.Time{})
}

// Input routeert één input-event: bewegingen sturen de cursor en gaan (net
// als knoppen) naar het window ónder de aanwijzer; toetsen en wiel naar de
// focus. Een knop-down verlegt eerst de focus (klik = focus, §5).
func (s *Server) Input(ev surf.Input) {
	// De glas-cursor volgt élke positie-drager (schermcoördinaten, vóór de
	// vertaling naar window-lokaal hieronder).
	if s.pointer != nil && (ev.Kind == surf.InputMove || ev.Kind == surf.InputButton) {
		s.pointer(int(ev.X), int(ev.Y))
	}

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
	// Scene-surface: input wordt display-side afgehandeld (hover, klik,
	// scroll) en alleen semantische EVENT's bereiken de app (§4-winst 2).
	if v := s.sceneOf(sur); v != nil {
		if ev.Kind == surf.InputWheel {
			// Wiel routeert op focus met schermcoördinaten; de scene wil
			// lokale — alleen zinvol als de aanwijzer echt boven dit window
			// hangt.
			wsur, wx, wy, wok := s.comp.SurfaceAt(int(ev.X), int(ev.Y))
			if !wok || wsur != sur {
				return
			}
			ev.X, ev.Y = uint16(wx), uint16(wy)
		}
		v.input(ev)
		return
	}
	id, _ := sess.idOf(sur)
	select {
	case sess.inputQ <- inMsg{id: id, ev: ev}:
	default:
		// De app leest even niet (rendert): droppen. Een gemiste move/wheel
		// is onzichtbaar; de verbinding sluiten was het echte kwaad.
	}
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
