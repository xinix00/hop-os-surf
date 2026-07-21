// Package scene is de retained-widget-laag van SURF (docs/gui-ontwerp.md §4,
// P2): een app stuurt één keer een binaire widget-boom (SCENE) en daarna
// alleen gerichte updates (PATCH); de display-node rendert, re-flowt bij
// CONFIGURE zonder de app te wekken, en stuurt semantische EVENT's terug
// ("#save clicked" in plaats van "klik op 412,88").
//
// De anti-HTML-clausule (§4) is hier afdwingbaar gemaakt: dit is géén taal
// maar een datastructuur — er is geen tekst-syntax en dus geen parser waar
// ooit expressies of scripting in kunnen groeien. De boom is dode data; alle
// logica blijft in de app (Derek: "scripting hébben we al — dat is de Go die
// erbij zit"). De widget-set v1 is compleet; uitbreiden is een
// ontwerpbeslissing, geen PR. Elke layout-wens buiten col/row: canvas.
//
// Draadvorm (little-endian, net als heel SURF):
//
//	node  = id u16 | kind u8 | nprops u8 | props | nchildren u8 | children
//	prop  = key u8 | type u8 | data
//	  type 1 str     = len u16 | bytes (UTF-8)
//	  type 2 i32     = 4 bytes
//	  type 3 u8      = 1 byte
//	  type 4 strlist = count u16 | count × (len u16 | bytes)
//
//	SCENE-payload = node (de wortel)         app→disp
//	PATCH-payload = id u16 | nprops u8 | props   app→disp (één node per bericht)
//	EVENT-payload = id u16 | kind u8 | value i32 disp→app (7 bytes)
//
// Onbekende prop-keys en node-kinds worden door een decoder overgeslagen
// (dezelfde forward-compatibiliteit als de berichttypes zelf): een nieuwere
// app blijft werken op een oudere display, die rendert voor een onbekende
// kind een lege rechthoek met de app-naam (§4-versioning).
package scene

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
)

// Widget-kinds (v1, compleet — zie de package-doc).
const (
	KindCol    = 1 // layout: kinderen onder elkaar
	KindRow    = 2 // layout: kinderen naast elkaar
	KindLabel  = 3 // tekst; PropStyle kiest normal/heading/mono
	KindValue  = 4 // het live-cijfer — PATCH-doelwit nummer één
	KindGauge  = 5 // instrumentenpaneel: waarde binnen Min..Max, met schaal
	KindBar    = 6 // idem, kaal (voortgang/vulgraad)
	KindButton = 7 // klikbaar → EVENT EvClick
	KindList   = 8 // rijen; klik → EVENT EvSelect, wiel scrollt display-side
	KindCanvas = 9 // het overdrukventiel: pixels via DAMAGE (v1: placeholder)
)

// Prop-keys. De encoding per key ligt hiermee vast (dossier §9, "PATCH-
// waarde-encoding vastpinnen"): PATCH gebruikt exact dezelfde prop-TLV.
const (
	PropText   = 1  // str    label/value/button-tekst
	PropStyle  = 2  // u8     label: 0 normal, 1 heading, 2 mono
	PropSize   = 3  // u16    vaste maat (px) langs de as van de ouder; 0 = gewicht
	PropWeight = 4  // u8     flex-gewicht in de restruimte (default 1)
	PropPad    = 5  // u8     ruimte (px) rond elk kind van deze container
	PropMin    = 6  // i32    ondergrens gauge/bar
	PropMax    = 7  // i32    bovengrens gauge/bar (default 100)
	PropVal    = 8  // i32    actuele waarde gauge/bar
	PropItems  = 9  // strlist rijen van een list
	PropSel    = 10 // i32    geselecteerde rij (-1 = geen)
	PropUnit   = 11 // str    eenheid-suffix ("°C", "MB/s") voor value/gauge/bar

	// PropAdd (20-07, de logstaart van taskman): rijen achteraan een list
	// bijplakken — PATCH-only, hij reist nooit in een SCENE (props() kent hem
	// niet; een herstuurde boom draagt de opgetelde Items als PropItems). De
	// display capt op listCap (oudste eruit) en volgt de staart als de lijst
	// al onderaan stond — zó wordt één logregel een PATCH van regel-lengte
	// plus een paar bytes, in plaats van de hele buffer opnieuw. Een oudere
	// display slaat de onbekende key over (geen nieuwe regels, geen crash).
	PropAdd = 12 // strlist
)

// listCap begrenst een list na PropAdd: genoeg om in terug te scrollen, klein
// genoeg om nooit de display-heap te worden (een logstaart is een venster op
// de stroom, geen archief).
const listCap = 500

// Prop-types op de draad.
const (
	tStr     = 1
	tI32     = 2
	tU8      = 3
	tStrList = 4
)

// Widget-stijlen (PropStyle). Labels: normal/heading/mono. Knoppen:
// normal/primary/danger — de functionele kleurgroepen (calc: operatoren
// accent, C rood; zie render.go). Eén veld, de display kiest de kleuren.
const (
	StyleNormal  = 0
	StyleHeading = 1
	StyleMono    = 2
	StylePrimary = 3
	StyleDanger  = 4
)

// EVENT-kinds (disp→app).
const (
	EvClick  = 1 // Value: 1 (reserve voor dubbelklik e.d.)
	EvSelect = 2 // Value: rij-index
)

// Event is één semantisch event van de display naar de app.
type Event struct {
	ID    uint16
	Kind  uint8
	Value int32
}

// EncodeEvent maakt de 7-byte EVENT-payload.
func EncodeEvent(ev Event) []byte {
	b := make([]byte, 7)
	binary.LittleEndian.PutUint16(b, ev.ID)
	b[2] = ev.Kind
	binary.LittleEndian.PutUint32(b[3:], uint32(ev.Value))
	return b
}

// DecodeEvent leest een EVENT-payload.
func DecodeEvent(p []byte) (Event, error) {
	if len(p) < 7 {
		return Event{}, errShort
	}
	return Event{
		ID:    binary.LittleEndian.Uint16(p),
		Kind:  p[2],
		Value: int32(binary.LittleEndian.Uint32(p[3:])),
	}, nil
}

var errShort = errors.New("scene: payload too short")

// Node is één widget in de boom: pure data op de draad, plus de runtime-
// staat die de display-kant erbij houdt (layout-rect, hover, scroll) en de
// callbacks die de app-kant eraan hangt. Alleen de data reist.
type Node struct {
	ID       uint16
	Kind     uint8
	Text     string
	Style    uint8
	Size     uint16
	Weight   uint8
	Pad      uint8
	Min, Max int32
	Val      int32
	Items    []string
	Sel      int32
	Unit     string
	Children []*Node

	// Display-side runtime (niet op de draad).
	Rect    image.Rectangle // toegekend door Layout
	Hover   bool
	Pressed bool
	Scroll  int // list: eerste zichtbare rij

	// App-side callbacks (niet op de draad); zie client.go.
	OnClick  func()
	OnSelect func(row int)
}

// set past één gedecodeerde prop toe; onbekende keys zijn al overgeslagen.
func (n *Node) set(key uint8, s string, v int32, list []string) {
	switch key {
	case PropText:
		n.Text = s
	case PropStyle:
		n.Style = uint8(v)
	case PropSize:
		n.Size = uint16(v)
	case PropWeight:
		n.Weight = uint8(v)
	case PropPad:
		n.Pad = uint8(v)
	case PropMin:
		n.Min = v
	case PropMax:
		n.Max = v
	case PropVal:
		n.Val = v
	case PropItems:
		n.Items = list
	case PropSel:
		n.Sel = v
	case PropUnit:
		n.Unit = s
	case PropAdd:
		n.addItems(list)
	}
}

// addItems plakt rijen achteraan (PropAdd): cappen op listCap en de staart
// volgen als de kijker daar al stond — het gedrag van een logstaart. Sel en
// Scroll schuiven mee met wat er bovenaan afvalt.
func (n *Node) addItems(list []string) {
	atEnd := n.Scroll >= n.maxScroll()
	n.Items = append(n.Items, list...)
	if drop := len(n.Items) - listCap; drop > 0 {
		n.Items = append(n.Items[:0], n.Items[drop:]...)
		if n.Sel >= 0 {
			n.Sel -= int32(drop)
			if n.Sel < 0 {
				n.Sel = -1
			}
		}
		n.Scroll -= drop
		if n.Scroll < 0 {
			n.Scroll = 0
		}
	}
	if atEnd {
		n.Scroll = n.maxScroll()
	}
}

// maxScroll is de hoogste eerste-rij zodat de laatste rij nog in beeld is;
// 0 zolang de node geen rect heeft (app-kant: daar is scroll display-zaak).
func (n *Node) maxScroll() int {
	vis := (n.Rect.Dy() - 2) / listRowH
	if vis <= 0 {
		return 0
	}
	m := len(n.Items) - vis
	if m < 0 {
		return 0
	}
	return m
}

// prop is één te encoderen key/waarde-paar; de builder-API en Patch bouwen
// hiermee dezelfde bytes (dat ís het vastpinnen van de PATCH-encoding).
type prop struct {
	key  uint8
	typ  uint8
	s    string
	v    int32
	list []string
}

func appendProp(b []byte, p prop) []byte {
	b = append(b, p.key, p.typ)
	switch p.typ {
	case tStr:
		b = binary.LittleEndian.AppendUint16(b, uint16(len(p.s)))
		b = append(b, p.s...)
	case tI32:
		b = binary.LittleEndian.AppendUint32(b, uint32(p.v))
	case tU8:
		b = append(b, uint8(p.v))
	case tStrList:
		b = binary.LittleEndian.AppendUint16(b, uint16(len(p.list)))
		for _, s := range p.list {
			b = binary.LittleEndian.AppendUint16(b, uint16(len(s)))
			b = append(b, s...)
		}
	}
	return b
}

// props verzamelt de niet-default props van een node (alleen wat afwijkt
// reist — een kale col is 5 bytes).
func (n *Node) props() []prop {
	var ps []prop
	if n.Text != "" {
		ps = append(ps, prop{key: PropText, typ: tStr, s: n.Text})
	}
	if n.Style != 0 {
		ps = append(ps, prop{key: PropStyle, typ: tU8, v: int32(n.Style)})
	}
	if n.Size != 0 {
		ps = append(ps, prop{key: PropSize, typ: tI32, v: int32(n.Size)})
	}
	if n.Weight != 0 {
		ps = append(ps, prop{key: PropWeight, typ: tU8, v: int32(n.Weight)})
	}
	if n.Pad != 0 {
		ps = append(ps, prop{key: PropPad, typ: tU8, v: int32(n.Pad)})
	}
	if n.Min != 0 {
		ps = append(ps, prop{key: PropMin, typ: tI32, v: n.Min})
	}
	if n.Max != 0 {
		ps = append(ps, prop{key: PropMax, typ: tI32, v: n.Max})
	}
	if n.Val != 0 {
		ps = append(ps, prop{key: PropVal, typ: tI32, v: n.Val})
	}
	if len(n.Items) != 0 {
		ps = append(ps, prop{key: PropItems, typ: tStrList, list: n.Items})
	}
	if n.Sel != 0 {
		ps = append(ps, prop{key: PropSel, typ: tI32, v: n.Sel})
	}
	if n.Unit != "" {
		ps = append(ps, prop{key: PropUnit, typ: tStr, s: n.Unit})
	}
	return ps
}

// Encode maakt de SCENE-payload van de boom onder n (n = de wortel).
func Encode(n *Node) []byte { return appendNode(nil, n) }

func appendNode(b []byte, n *Node) []byte {
	b = binary.LittleEndian.AppendUint16(b, n.ID)
	b = append(b, n.Kind)
	ps := n.props()
	b = append(b, uint8(len(ps)))
	for _, p := range ps {
		b = appendProp(b, p)
	}
	b = append(b, uint8(len(n.Children)))
	for _, c := range n.Children {
		b = appendNode(b, c)
	}
	return b
}

// Decode leest een SCENE-payload terug tot een boom.
func Decode(p []byte) (*Node, error) {
	n, rest, err := decodeNode(p, 0)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("scene: %d trailing bytes", len(rest))
	}
	return n, nil
}

// maxDepth begrenst de recursie: een kapot/kwaadaardig bericht mag de
// display-stack niet opblazen. 32 diep is voor een dashboard al absurd.
const maxDepth = 32

func decodeNode(p []byte, depth int) (*Node, []byte, error) {
	if depth > maxDepth {
		return nil, nil, errors.New("scene: tree too deep")
	}
	if len(p) < 5 {
		return nil, nil, errShort
	}
	n := &Node{
		ID:   binary.LittleEndian.Uint16(p),
		Kind: p[2],
		Sel:  0,
	}
	nprops := int(p[3])
	p = p[4:]
	var err error
	for i := 0; i < nprops; i++ {
		if p, err = decodeProp(n, p); err != nil {
			return nil, nil, err
		}
	}
	if len(p) < 1 {
		return nil, nil, errShort
	}
	nkids := int(p[0])
	p = p[1:]
	for i := 0; i < nkids; i++ {
		var c *Node
		if c, p, err = decodeNode(p, depth+1); err != nil {
			return nil, nil, err
		}
		n.Children = append(n.Children, c)
	}
	return n, p, nil
}

// decodeProp leest één prop en past hem op n toe; onbekende keys worden op
// type-lengte overgeslagen (forward-compatibel).
func decodeProp(n *Node, p []byte) ([]byte, error) {
	if len(p) < 2 {
		return nil, errShort
	}
	key, typ := p[0], p[1]
	p = p[2:]
	var s string
	var v int32
	var list []string
	switch typ {
	case tStr:
		if len(p) < 2 {
			return nil, errShort
		}
		l := int(binary.LittleEndian.Uint16(p))
		if len(p) < 2+l {
			return nil, errShort
		}
		s = string(p[2 : 2+l])
		p = p[2+l:]
	case tI32:
		if len(p) < 4 {
			return nil, errShort
		}
		v = int32(binary.LittleEndian.Uint32(p))
		p = p[4:]
	case tU8:
		if len(p) < 1 {
			return nil, errShort
		}
		v = int32(p[0])
		p = p[1:]
	case tStrList:
		if len(p) < 2 {
			return nil, errShort
		}
		cnt := int(binary.LittleEndian.Uint16(p))
		p = p[2:]
		for i := 0; i < cnt; i++ {
			if len(p) < 2 {
				return nil, errShort
			}
			l := int(binary.LittleEndian.Uint16(p))
			if len(p) < 2+l {
				return nil, errShort
			}
			list = append(list, string(p[2:2+l]))
			p = p[2+l:]
		}
	default:
		return nil, fmt.Errorf("scene: unknown prop type %d", typ)
	}
	n.set(key, s, v, list)
	return p, nil
}

// EncodePatch maakt een PATCH-payload voor node id met de gegeven props —
// de app-kant gebruikt dit via de Set*-helpers in client.go.
func EncodePatch(id uint16, ps []prop) []byte {
	b := binary.LittleEndian.AppendUint16(nil, id)
	b = append(b, uint8(len(ps)))
	for _, p := range ps {
		b = appendProp(b, p)
	}
	return b
}

// ApplyPatch past een PATCH-payload toe op de boom (display-kant); de
// geraakte node komt terug zodat de renderer alleen dát rect hoeft te doen.
func ApplyPatch(byID map[uint16]*Node, p []byte) (*Node, error) {
	if len(p) < 3 {
		return nil, errShort
	}
	id := binary.LittleEndian.Uint16(p)
	n := byID[id]
	if n == nil {
		return nil, fmt.Errorf("scene: PATCH for unknown node %d", id)
	}
	nprops := int(p[2])
	p = p[3:]
	var err error
	for i := 0; i < nprops; i++ {
		if p, err = decodeProp(n, p); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// Index bouwt de id→node-kaart van een boom (voor ApplyPatch en events).
func Index(root *Node) map[uint16]*Node {
	m := make(map[uint16]*Node)
	var walk func(*Node)
	walk = func(n *Node) {
		m[n.ID] = n
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return m
}
