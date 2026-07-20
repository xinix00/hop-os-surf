// Client: de app-kant van de scene-laag — een Go-builder-API (géén taal,
// §4) en een zelfherstellende verbinding met dezelfde failover-semantiek als
// window/: bij een reconnect stuurt de app gewoon zijn boom opnieuw (een
// paar honderd bytes — §4-winst 4). Set* past de lokale boom aan én stuurt
// de PATCH, dus de hersteld-gestuurde boom is altijd actueel.
package scene

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xinix00/hop-os-surf/stack/surf"
)

// ---- Builders (ids kent Show toe; houd de *Node vast om te patchen) ----

// Col stapelt kinderen verticaal; pad is de ruimte rond elk kind.
func Col(pad int, kids ...*Node) *Node {
	return &Node{Kind: KindCol, Pad: uint8(pad), Children: kids}
}

// Row zet kinderen naast elkaar.
func Row(pad int, kids ...*Node) *Node {
	return &Node{Kind: KindRow, Pad: uint8(pad), Children: kids}
}

// Label is stilstaande tekst.
func Label(style uint8, text string) *Node {
	return &Node{Kind: KindLabel, Style: style, Text: text}
}

// Value is het live-cijfer (PATCH-doelwit nummer één).
func Value(text, unit string) *Node {
	return &Node{Kind: KindValue, Text: text, Unit: unit}
}

// Gauge is de instrumentenmeter met schaalstrepen en waarde-tekst.
func Gauge(min, max, val int32, unit string) *Node {
	return &Node{Kind: KindGauge, Min: min, Max: max, Val: val, Unit: unit}
}

// Bar is de kale vulgraadmeter.
func Bar(min, max, val int32) *Node {
	return &Node{Kind: KindBar, Min: min, Max: max, Val: val}
}

// Button vuurt OnClick (display stuurt EVENT EvClick).
func Button(text string, onClick func()) *Node {
	return &Node{Kind: KindButton, Text: text, OnClick: onClick}
}

// List toont rijen; OnSelect krijgt de aangeklikte index.
func List(items []string, onSelect func(int)) *Node {
	return &Node{Kind: KindList, Items: items, Sel: -1, OnSelect: onSelect}
}

// Canvas reserveert een pixel-rechthoek (v1: placeholder op de display).
func Canvas() *Node { return &Node{Kind: KindCanvas} }

// Sized geeft de node een vaste maat (px) langs de as van zijn ouder.
func (n *Node) Sized(px int) *Node { n.Size = uint16(px); return n }

// Weighted geeft de node een flex-gewicht in de restruimte.
func (n *Node) Weighted(w int) *Node { n.Weight = uint8(w); return n }

// ---- Verbinding ----

// Conn is één scene-app-verbinding met een display-node.
type Conn struct {
	addr, name string
	hintW      int
	hintH      int
	logf       func(string, ...any)

	// OnKey (optioneel, zetten vóór Show) krijgt toetsen die de display
	// doorstuurt (web-KVM-keyCode, down = ingedrukt). Toetsen zijn geen
	// hit-testbare semantiek — welke toets wat betekent is app-logica, dus
	// die reizen rauw, net als bij een pixel-app. Wordt aangeroepen vanaf de
	// leesgoroutine, zonder locks: de app mag terug de Conn in.
	OnKey func(code uint32, down bool)

	mu   sync.Mutex // conn + root + byID
	conn net.Conn
	root *Node
	byID map[uint16]*Node

	sent atomic.Uint64 // PATCH/SCENE-bytes op de draad — het §8-meetpunt
}

// Open verbindt met een display-node; de boom volgt met Show.
func Open(addr, name string, hintW, hintH int, logf func(string, ...any)) *Conn {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Conn{addr: addr, name: name, hintW: hintW, hintH: hintH, logf: logf}
}

// Show maakt dit de getoonde boom: ids toekennen, verbinden en versturen.
// De leeslus (EVENT-dispatch + zelfheling) start hier.
func (c *Conn) Show(root *Node) error {
	c.mu.Lock()
	c.root = root
	id := uint16(0)
	var walk func(*Node)
	walk = func(n *Node) {
		id++
		n.ID = id
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(root)
	c.byID = Index(root)
	c.mu.Unlock()

	// Ook de éérste verbinding is zelfherstellend (zelfde boot-race als
	// window.Open: display downloadt nog terwijl deze app al draait): een
	// mislukte dial is geen fout maar een kwestie van wachten — de leeslus
	// begint dan meteen in zijn heel-lus en blijft redialen.
	if err := c.connect(); err != nil {
		c.logf("scene: display %s nog niet bereikbaar (%v) — blijven proberen", c.addr, err)
	}
	go c.readLoop()
	go c.pingLoop()
	return nil
}

// pingLoop stuurt elke 10s een levensteken: de display ruimt sessies op die
// 30s zwijgen (een hard gekilde app stuurt nooit een FIN). Een schrijffout
// sluit de verbinding; de leeslus heelt daarna met een verse boom.
func (c *Conn) pingLoop() {
	for range time.Tick(10 * time.Second) {
		c.mu.Lock()
		conn := c.conn
		var err error
		if conn != nil {
			err = surf.WriteMsg(conn, surf.TypePing, 0, nil)
		}
		c.mu.Unlock()
		if err != nil && conn != nil {
			conn.Close() // de leeslus merkt het en herverbindt
		}
	}
}

// connect (her)opent de verbinding en stuurt HELLO+CREATE+SCENE.
func (c *Conn) connect() error {
	conn, err := net.Dial("tcp", c.addr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := surf.WriteMsg(conn, surf.TypeHello, 0,
		surf.Hello{Version: surf.Version, App: c.name}.Encode()); err != nil {
		conn.Close()
		return err
	}
	if err := surf.WriteMsg(conn, surf.TypeCreate, 1,
		surf.Create{W: uint16(c.hintW), H: uint16(c.hintH), Format: surf.FormatXRGB8888}.Encode()); err != nil {
		conn.Close()
		return err
	}
	payload := Encode(c.root)
	if err := surf.WriteMsg(conn, surf.TypeScene, 1, payload); err != nil {
		conn.Close()
		return err
	}
	c.sent.Add(uint64(surf.HeaderSize + len(payload)))
	c.conn = conn
	return nil
}

// readLoop leest EVENT's en heelt de verbinding (redial + boom hersturen).
func (c *Conn) readLoop() {
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		var buf []byte
		for conn != nil {
			h, p, err := surf.ReadMsg(conn, buf)
			if err != nil {
				break
			}
			buf = p
			if h.Type == surf.TypeInput {
				// Toetsen (de display stuurt alleen keys door; muis blijft
				// display-side): rauw naar de app.
				if in, err := surf.DecodeInput(p); err == nil && in.Kind == surf.InputKey && c.OnKey != nil {
					c.OnKey(in.Code, in.Value == 1)
				}
				continue
			}
			if h.Type != surf.TypeEvent {
				continue // CONFIGURE re-flowt de display zelf; niets te doen
			}
			ev, err := DecodeEvent(p)
			if err != nil {
				continue
			}
			c.mu.Lock()
			n := c.byID[ev.ID]
			c.mu.Unlock()
			if n == nil {
				continue
			}
			switch {
			case ev.Kind == EvClick && n.OnClick != nil:
				n.OnClick()
			case ev.Kind == EvSelect && n.OnSelect != nil:
				n.Sel = ev.Value // lokale spiegel: een herstuurde boom toont de selectie
				n.OnSelect(int(ev.Value))
			}
		}

		// Zelfheling: redial tot het lukt, dan de actuele boom opnieuw tonen.
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
		c.mu.Unlock()
		for {
			time.Sleep(time.Second)
			if err := c.connect(); err == nil {
				c.logf("scene: reconnected to %s", c.addr)
				break
			}
		}
	}
}

// patch stuurt props voor node n en telt de draadbytes.
func (c *Conn) patch(n *Node, ps []prop) {
	payload := EncodePatch(n.ID, ps)
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return // reconnect stuurt zo de hele (actuele) boom
	}
	if err := surf.WriteMsg(conn, surf.TypePatch, 1, payload); err != nil {
		conn.Close() // de leeslus merkt het en heelt
		return
	}
	c.sent.Add(uint64(surf.HeaderSize + len(payload)))
}

// SetText werkt tekst bij (value/label/button) — lokaal én op de display.
func (c *Conn) SetText(n *Node, s string) {
	if n.Text == s {
		return // niets veranderd: nul bytes op de draad
	}
	n.Text = s
	c.patch(n, []prop{{key: PropText, typ: tStr, s: s}})
}

// SetVal werkt de waarde van een gauge/bar bij.
func (c *Conn) SetVal(n *Node, v int32) {
	if n.Val == v {
		return
	}
	n.Val = v
	c.patch(n, []prop{{key: PropVal, typ: tI32, v: v}})
}

// SetItems vervangt de rijen van een list.
func (c *Conn) SetItems(n *Node, items []string) {
	n.Items = items
	c.patch(n, []prop{{key: PropItems, typ: tStrList, list: items}})
}

// AddItems plakt rijen achteraan een list (PropAdd): de logstaart-update —
// regel-lengte plus een paar bytes op de draad, hoe vol de buffer ook staat.
// De lokale spiegel capt mee (listCap), zodat een reconnect-Show dezelfde
// staart toont als de display had.
func (c *Conn) AddItems(n *Node, items []string) {
	if len(items) == 0 {
		return
	}
	n.addItems(items)
	c.patch(n, []prop{{key: PropAdd, typ: tStrList, list: items}})
}

// SetSel zet de selectie van een list (-1 = geen) — voor een app die zijn
// selectie programmatisch wist of herstelt.
func (c *Conn) SetSel(n *Node, sel int32) {
	if n.Sel == sel {
		return
	}
	n.Sel = sel
	c.patch(n, []prop{{key: PropSel, typ: tI32, v: sel}})
}

// BytesSent is het totaal aan SCENE+PATCH-draadbytes — het bewijs van §8-P2
// (bytes/s waar pixels KB/s waren).
func (c *Conn) BytesSent() uint64 { return c.sent.Load() }

// Close neemt bewust afscheid: CLOSE naar de display (die ruimt het window
// dan meteen op — zónder CLOSE parkeert hij het en wacht hij op ons) en de
// verbinding dicht. Voor de OnExit-hook van applib.
func (c *Conn) Close() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil // de leeslus gaat helen, maar de app is toch weg
	c.mu.Unlock()
	if conn != nil {
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		surf.WriteMsg(conn, surf.TypeClose, 1, nil)
		conn.Close()
	}
}
