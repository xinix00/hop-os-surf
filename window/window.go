// Package window is de app-kant van SURF (docs/gui-ontwerp.md §3): teken in
// een image.RGBA, Present(), klaar. De verbinding herstelt zichzelf — valt
// de display-node weg (of failovert de app zelf en komt hij elders terug),
// dan herverbindt Present met HELLO+CREATE en een vol frame; het window
// verschijnt vanzelf opnieuw. Bewust niet aan applib gekoppeld: alleen
// stdlib + surf, zodat het op de ontwikkelmachine integraal testbaar is.
package window

import (
	"errors"
	"image"
	"net"
	"sync"
	"time"

	"github.com/xinix00/hop-os-surf/surf"
)

var errClosed = errors.New("window: session lost")

// Event is één input-event van de display (zie surf.Input*-kinds), of een
// resize (Kind == KindResize, X/Y = de nieuwe maat).
type Event struct {
	Kind  uint8
	Code  uint32
	Value int32
	X, Y  uint16
}

// KindResize: de WM heeft het window een nieuwe maat gegeven (Wayland-les:
// CREATE is een hint, CONFIGURE is de wet). Vraag Image() opnieuw op — die
// heeft dan de nieuwe maat — herteken en Present.
const KindResize uint8 = 255

// Window is één surface op een display-node.
type Window struct {
	addr, name string
	w, h       int
	img        *image.RGBA
	logf       func(string, ...any)

	mu           sync.Mutex
	conn         net.Conn
	dead         bool // leesgoroutine zag de verbinding sterven
	pendW, pendH int  // door de WM opgelegde maat (CONFIGURE), nog te verwerken
	frame        uint32
	scratch      []byte // wire-conversiebuffer (RGBA → XRGB8888)
	events       chan Event
}

// Open verbindt met een display-node (addr = host:poort, doorgaans uit de
// jobspec-env SURF_ADDR) en maakt daar een w×h-window aan. name is de
// windowtitel — zet er je herkomst in ("clock @ node-b"): het cluster hoort
// zichtbaar te zijn in de chrome. logf mag nil zijn.
func Open(addr, name string, w, h int, logf func(string, ...any)) (*Window, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	win := &Window{
		addr: addr, name: name, w: w, h: h,
		img:     image.NewRGBA(image.Rect(0, 0, w, h)),
		logf:    logf,
		scratch: make([]byte, w*h*4),
		events:  make(chan Event, 64),
	}
	if err := win.connect(); err != nil {
		return nil, err
	}
	return win, nil
}

// Image is het tekenvlak: teken erin en roep Present aan. Vraag hem elke
// frame opnieuw op en cache hem niet — na een CONFIGURE van de WM is dit
// een nieuw beeld op de nieuwe maat (de w/h van Open zijn slechts een hint).
func (win *Window) Image() *image.RGBA {
	win.mu.Lock()
	if win.pendW > 0 && (win.pendW != win.w || win.pendH != win.h) {
		win.w, win.h = win.pendW, win.pendH
		win.img = image.NewRGBA(image.Rect(0, 0, win.w, win.h))
		win.scratch = make([]byte, win.w*win.h*4)
	}
	win.mu.Unlock()
	return win.img
}

// Size geeft de actuele (WM-)maat van het window.
func (win *Window) Size() (w, h int) {
	win.mu.Lock()
	defer win.mu.Unlock()
	if win.pendW > 0 {
		return win.pendW, win.pendH
	}
	return win.w, win.h
}

// Events levert input van de display (toetsen/muis in lokale window-
// coördinaten). Bij een overvolle buffer vallen oude events weg — input is
// een verse-waarde-stroom, geen log.
func (win *Window) Events() <-chan Event { return win.events }

// Present maakt de huidige Image-inhoud atomisch zichtbaar. Zonder
// argumenten gaat het volle frame de lijn over; met rects alleen die
// rechthoeken (beeldcoördinaten) — de rest van de buffer is dan per
// definitie ongewijzigd sinds de vorige Present (dat is het contract:
// partieel presenteren betekent dat je alleen dáár getekend hebt). Bij een
// kapotte verbinding blokkeert Present tot de display terug is, en na een
// herverbinding gaat er altijd eerst een vol frame op (de display begon
// met een lege surface).
//
// Semantiek is at-most-once per aanroep: een breuk die pas ná de write
// zichtbaar wordt (TCP-buffer) kan één frame kosten, maar de leesgoroutine
// markeert de sessie dood en de eerstvolgende Present herverbindt — een app
// die blijft presenteren heelt zichzelf. Present is bedoeld voor één
// tekenende goroutine.
func (win *Window) Present(rects ...image.Rectangle) error {
	for first := true; ; first = false {
		r := rects
		if !first {
			r = nil // na een reconnect: altijd een vol frame
		}
		err := win.present(r)
		if err == nil {
			return nil
		}
		win.logf("window: display lost (%v), reconnecting", err)
		for {
			time.Sleep(500 * time.Millisecond)
			if err := win.connect(); err == nil {
				break
			}
		}
	}
}

func (win *Window) present(rects []image.Rectangle) error {
	win.mu.Lock()
	conn, dead := win.conn, win.dead
	win.mu.Unlock()
	if dead {
		return errClosed
	}
	// img/scratch wisselen alleen in Image() (zelfde tekengoroutine als
	// Present — dat is het contract), dus hier consistent.
	img, scratch := win.img, win.scratch
	b := img.Bounds()
	if len(rects) == 0 {
		rects = []image.Rectangle{b}
	}
	win.frame++
	for _, r := range rects {
		r = r.Intersect(b)
		if r.Empty() {
			continue
		}
		// RGBA → wire (XRGB8888 little-endian), alleen de rect-rijen: de
		// enige kopie aan de zendkant.
		w, h := r.Dx(), r.Dy()
		i := 0
		for y := r.Min.Y; y < r.Max.Y; y++ {
			src := img.Pix[img.PixOffset(r.Min.X, y):]
			for x := 0; x < w; x++ {
				scratch[i*4+0] = src[x*4+2]
				scratch[i*4+1] = src[x*4+1]
				scratch[i*4+2] = src[x*4+0]
				scratch[i*4+3] = 0
				i++
			}
		}
		d := surf.Damage{
			Frame: win.frame,
			X:     uint16(r.Min.X - b.Min.X), Y: uint16(r.Min.Y - b.Min.Y),
			W: uint16(w), H: uint16(h),
		}
		if err := surf.WriteDamage(conn, 1, d, scratch[:w*h*4]); err != nil {
			return err
		}
	}
	return surf.WriteMsg(conn, surf.TypePresent, 1, surf.Present{Frame: win.frame}.Encode())
}

// connect zet (opnieuw) een sessie op: HELLO + CREATE, en de leesgoroutine
// voor input. Surface-id is altijd 1 — één window per Window.
func (win *Window) connect() error {
	conn, err := net.DialTimeout("tcp", win.addr, 5*time.Second)
	if err != nil {
		return err
	}
	// Token: v0-stub (nullen); verificatie hangt later aan de clustersleutels.
	hello := surf.Hello{Version: surf.Version, App: win.name}
	if err := surf.WriteMsg(conn, surf.TypeHello, 0, hello.Encode()); err != nil {
		conn.Close()
		return err
	}
	create := surf.Create{W: uint16(win.w), H: uint16(win.h), Format: surf.FormatXRGB8888}
	if err := surf.WriteMsg(conn, surf.TypeCreate, 1, create.Encode()); err != nil {
		conn.Close()
		return err
	}
	win.mu.Lock()
	if win.conn != nil {
		win.conn.Close() // oude leesgoroutine stopt op de leesfout
	}
	win.conn = conn
	win.dead = false
	win.mu.Unlock()
	go win.readLoop(conn)
	return nil
}

// markDead meldt dat conn stierf; alleen als het nog de actieve sessie is
// (een oudere leesgoroutine mag een verse verbinding niet doodverklaren).
func (win *Window) markDead(conn net.Conn) {
	win.mu.Lock()
	if win.conn == conn {
		win.dead = true
	}
	win.mu.Unlock()
}

// readLoop zet inkomende INPUT om in Events; CONFIGURE wordt in v1 bevestigd
// ontvangen maar genegeerd (vaste windowmaat), onbekende types ook
// (forward-compatibel: een nieuwere display mag meer sturen).
func (win *Window) readLoop(conn net.Conn) {
	var buf []byte
	var h surf.Header
	var err error
	defer win.markDead(conn)
	for {
		h, buf, err = surf.ReadMsg(conn, buf)
		if err != nil {
			return
		}
		switch h.Type {
		case surf.TypeConfigure:
			cfg, err := surf.DecodeConfigure(buf)
			if err != nil || cfg.W == 0 || cfg.H == 0 {
				continue
			}
			win.mu.Lock()
			win.pendW, win.pendH = int(cfg.W), int(cfg.H)
			win.mu.Unlock()
			win.deliver(Event{Kind: KindResize, X: cfg.W, Y: cfg.H})
		case surf.TypeInput:
			in, err := surf.DecodeInput(buf)
			if err != nil {
				continue
			}
			win.deliver(Event{Kind: in.Kind, Code: in.Code, Value: in.Value, X: in.X, Y: in.Y})
		case surf.TypeClose:
			conn.Close()
			return
		}
	}
}

// deliver zet een event in de buffer; bij een volle buffer valt het oudste
// weg — input is een verse-waarde-stroom, geen log.
func (win *Window) deliver(ev Event) {
	select {
	case win.events <- ev:
	default:
		select {
		case <-win.events:
		default:
		}
		select {
		case win.events <- ev:
		default:
		}
	}
}

// Close sluit de sessie netjes af.
func (win *Window) Close() error {
	win.mu.Lock()
	conn := win.conn
	win.dead = true
	win.mu.Unlock()
	if conn == nil {
		return nil
	}
	surf.WriteMsg(conn, surf.TypeClose, 1, nil)
	return conn.Close()
}
