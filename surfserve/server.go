// Package surfserve is de netwerkkant van de display-app: SURF-sessies
// (docs/gui-ontwerp.md §3) plus het meetinstrument — /screen.png en de
// browser-KVM op /kvm (§6, trap 1). Los van main zodat het op de
// ontwikkelmachine integraal testbaar is (window ↔ server ↔ compositor).
package surfserve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"net"
	"net/http"
	"sync"

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
	return &Server{comp: comp, logf: logf, sessions: make(map[*compositor.Surface]*session)}
}

// session is één app-verbinding met zijn surfaces.
type session struct {
	srv  *Server
	conn net.Conn
	app  string

	writeMu  sync.Mutex // INPUT/CONFIGURE delen de stream met elkaar
	surfaces map[uint16]*compositor.Surface
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
	sess := &session{srv: s, conn: conn, surfaces: make(map[uint16]*compositor.Surface)}
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
			if old := sess.surfaces[h.Surface]; old != nil {
				// Her-CREATE van hetzelfde id (reconnectsemantiek binnen een
				// sessie bestaat niet, maar wees voorspelbaar): oude weg.
				s.comp.Remove(old)
				s.unregister(old)
			}
			// V0 heeft geen titelveld in CREATE: de HELLO-appnaam is de
			// windowtitel (de app zet daar zelf zijn herkomst in).
			sur := s.comp.Add(sess.app, int(c.W), int(c.H))
			sess.surfaces[h.Surface] = sur
			s.register(sur, sess)
			// CONFIGURE terug: v1 bevestigt de gevraagde maat (het grid
			// clipt; echte re-flow komt met de scene-laag).
			sess.send(surf.TypeConfigure, h.Surface, surf.Configure{W: c.W, H: c.H}.Encode())
		case surf.TypeDamage:
			d, pix, err := surf.DecodeDamage(buf)
			sur := sess.surfaces[h.Surface]
			if err != nil || sur == nil {
				s.logf("surf: %s: bad DAMAGE (%v)", sess.app, err)
				return
			}
			if err := sur.Damage(int(d.X), int(d.Y), int(d.W), int(d.H), pix); err != nil {
				s.logf("surf: %s: %v", sess.app, err)
				return
			}
		case surf.TypePresent:
			if sur := sess.surfaces[h.Surface]; sur != nil {
				s.comp.Present(sur)
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
	for _, sur := range sess.surfaces {
		sess.srv.comp.Remove(sur)
		sess.srv.unregister(sur)
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
		s.comp.SetCursor(int(ev.X), int(ev.Y))
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
	var id uint16
	for sid, cur := range sess.surfaces {
		if cur == sur {
			id = sid
			break
		}
	}
	sess.send(surf.TypeInput, id, ev.Encode())
}

// Handler is de HTTP-kant: /screen.png (het meetinstrument), /kvm (browser-
// KVM) en /input (de event-terugweg van de KVM-pagina).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/screen.png", s.serveScreen)
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
		if err := png.Encode(&buf, img); err != nil {
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

// inputMsg is het JSON-event van de KVM-pagina.
type inputMsg struct {
	K string `json:"k"` // "key" | "move" | "btn" | "wheel"
	C int    `json:"c"`
	V int    `json:"v"`
	X int    `json:"x"`
	Y int    `json:"y"`
}

func (s *Server) serveInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var m inputMsg
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
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
