// Package surf is het Hop Surface Protocol: de draadtaal tussen een app en de
// display-node (docs/gui-ontwerp.md §3). Eén TCP-stream, little-endian, elk
// bericht een 8-byte header + payload. De pixel-laag (DAMAGE/PRESENT) is v0;
// de scene-types (SCENE/PATCH/EVENT) zijn gereserveerd voor P2 en worden door
// een v0-ontvanger genegeerd (forward-compatibel by design).
//
// Dit pakket is bewust dependency-vrij (alleen stdlib): beide kanten — de
// display-app én elke GUI-app — importeren het, en het moet op de
// ontwikkelmachine testbaar blijven.
package surf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// Version is de protocolversie in HELLO.
	Version = 1
	// Port is de standaard SURF-poort op een display-node.
	Port = 7878
	// HeaderSize: type u8 | pad u8 | surface u16 | length u32.
	HeaderSize = 8
	// MaxPayload begrenst één bericht (ruim boven een 1080p-full-frame:
	// 1920·1080·4 ≈ 8,3 MB) zodat een kapotte header nooit de heap opblaast.
	MaxPayload = 16 << 20
	// TokenLen is de vaste HELLO-tokenlengte (HMAC-maat; verificatie is in
	// v0 een stub en hangt later aan de clustersleutels).
	TokenLen = 32
)

// Berichttypes (docs/gui-ontwerp.md §3).
const (
	TypeHello     = 1  // app→disp
	TypeCreate    = 2  // app→disp
	TypeDamage    = 3  // app→disp
	TypePresent   = 4  // app→disp
	TypeConfigure = 5  // disp→app
	TypeInput     = 6  // disp→app
	TypeClose     = 7  // beide
	TypeScene     = 8  // app→disp (P2): volledige widget-boom
	TypePatch     = 9  // app→disp (P2): gerichte update
	TypeEvent     = 10 // disp→app (P2): semantisch event
	TypePing      = 11 // app→disp: levensteken (lege payload). Een hard
	// gekilde slot stuurt nooit een FIN; zonder pings blijft zijn window
	// eeuwig staan (gemeten 19-07). Clients pingen elke ~10s, de display
	// ruimt sessies op die >30s niets sturen.
)

// FormatXRGB8888 is het enige v0-pixelformaat: 4 bytes per pixel,
// little-endian 0xXXRRGGBB → bytes B,G,R,X.
const FormatXRGB8888 = 1

// Input-kinds (INPUT.Kind). De browser-KVM, UART en straks de USB-HID-app
// leveren allemaal ditzelfde bericht — de bron is inwisselbaar.
const (
	InputKey    = 1 // Code = toetscode (JS keyCode in v0), Value = 1 down / 0 up
	InputMove   = 2 // X,Y = positie in schermpixels
	InputButton = 3 // Code = knop (0 = links), Value = 1 down / 0 up, X,Y = positie
	InputWheel  = 4 // Value = delta (positief = omlaag)
)

var (
	ErrTooLarge = errors.New("surf: payload larger than MaxPayload")
	ErrShort    = errors.New("surf: payload too short")
)

// Header is de vaste berichtkop.
type Header struct {
	Type    uint8
	Surface uint16
	Length  uint32
}

func putHeader(b []byte, typ uint8, surface uint16, length uint32) {
	b[0] = typ
	b[1] = 0
	binary.LittleEndian.PutUint16(b[2:], surface)
	binary.LittleEndian.PutUint32(b[4:], length)
}

// WriteMsg schrijft één compleet bericht.
func WriteMsg(w io.Writer, typ uint8, surface uint16, payload []byte) error {
	if len(payload) > MaxPayload {
		return ErrTooLarge
	}
	buf := make([]byte, HeaderSize+len(payload))
	putHeader(buf, typ, surface, uint32(len(payload)))
	copy(buf[HeaderSize:], payload)
	_, err := w.Write(buf)
	return err
}

// WriteDamage schrijft een DAMAGE-bericht zonder de pixels te kopiëren: kop +
// meta in één kleine write, de pixelrijen direct daarachter. pix is wire-
// formaat (XRGB8888), lengte moet exact d.W·d.H·4 zijn.
func WriteDamage(w io.Writer, surface uint16, d Damage, pix []byte) error {
	if len(pix) != int(d.W)*int(d.H)*4 {
		return fmt.Errorf("surf: damage %dx%d wants %d pixel bytes, got %d",
			d.W, d.H, int(d.W)*int(d.H)*4, len(pix))
	}
	if damageMetaSize+len(pix) > MaxPayload {
		return ErrTooLarge
	}
	head := make([]byte, HeaderSize+damageMetaSize)
	putHeader(head, TypeDamage, surface, uint32(damageMetaSize+len(pix)))
	d.encode(head[HeaderSize:])
	if _, err := w.Write(head); err != nil {
		return err
	}
	_, err := w.Write(pix)
	return err
}

// ReadMsg leest één bericht. buf wordt hergebruikt als hij groot genoeg is;
// de teruggegeven payload is alleen geldig tot de volgende ReadMsg met
// dezelfde buf.
func ReadMsg(r io.Reader, buf []byte) (Header, []byte, error) {
	var hb [HeaderSize]byte
	if _, err := io.ReadFull(r, hb[:]); err != nil {
		return Header{}, buf, err
	}
	h := Header{
		Type:    hb[0],
		Surface: binary.LittleEndian.Uint16(hb[2:]),
		Length:  binary.LittleEndian.Uint32(hb[4:]),
	}
	if h.Length > MaxPayload {
		return Header{}, buf, ErrTooLarge
	}
	if cap(buf) < int(h.Length) {
		buf = make([]byte, h.Length)
	}
	buf = buf[:h.Length]
	if _, err := io.ReadFull(r, buf); err != nil {
		return Header{}, buf, err
	}
	return h, buf, nil
}

// Hello: version u16 | nameLen u8 | name | token[32].
type Hello struct {
	Version uint16
	App     string
	Token   [TokenLen]byte
}

func (m Hello) Encode() []byte {
	name := m.App
	if len(name) > 255 {
		name = name[:255]
	}
	b := make([]byte, 2+1+len(name)+TokenLen)
	binary.LittleEndian.PutUint16(b, m.Version)
	b[2] = byte(len(name))
	copy(b[3:], name)
	copy(b[3+len(name):], m.Token[:])
	return b
}

func DecodeHello(p []byte) (Hello, error) {
	var m Hello
	if len(p) < 3 {
		return m, ErrShort
	}
	m.Version = binary.LittleEndian.Uint16(p)
	n := int(p[2])
	if len(p) < 3+n+TokenLen {
		return m, ErrShort
	}
	m.App = string(p[3 : 3+n])
	copy(m.Token[:], p[3+n:])
	return m, nil
}

// Create: w u16 | h u16 | format u8.
type Create struct {
	W, H   uint16
	Format uint8
	Role   uint8 // RoleWindow (default) of RoleMenu — wat de WM met dit
	// surface doet, verklaard door de app zelf (20-07): één byte dode data,
	// zodat de chrome nooit op windowtitels hoeft te raden.
}

// Surface-rollen (CREATE.Role).
const (
	RoleWindow = 0 // gewoon window: taskbar-knop, cascade-plaatsing
	RoleMenu   = 1 // startmenu (de launcher): verborgen tot de startknop,
	// boven de taskbar, geen eigen taskbar-knop, klik ernaast sluit
)

func (m Create) Encode() []byte {
	b := make([]byte, 6)
	binary.LittleEndian.PutUint16(b, m.W)
	binary.LittleEndian.PutUint16(b[2:], m.H)
	b[4] = m.Format
	b[5] = m.Role
	return b
}

// DecodeCreate accepteert ook de 5-byte vorm van vóór Role (20-07): een
// oudere app is gewoon een window — en een oudere display leest van een
// nieuwere app alleen de eerste 5 bytes. Beide kanten degraderen netjes.
func DecodeCreate(p []byte) (Create, error) {
	if len(p) < 5 {
		return Create{}, ErrShort
	}
	c := Create{
		W:      binary.LittleEndian.Uint16(p),
		H:      binary.LittleEndian.Uint16(p[2:]),
		Format: p[4],
	}
	if len(p) >= 6 {
		c.Role = p[5]
	}
	return c, nil
}

// Damage: frame u32 | x u16 | y u16 | w u16 | h u16, gevolgd door w·h·4
// pixelbytes. De pixels beschrijven exact de rechthoek, rij-op-rij zonder
// stride-opvulling.
type Damage struct {
	Frame      uint32
	X, Y, W, H uint16
}

const damageMetaSize = 12

func (m Damage) encode(b []byte) {
	binary.LittleEndian.PutUint32(b, m.Frame)
	binary.LittleEndian.PutUint16(b[4:], m.X)
	binary.LittleEndian.PutUint16(b[6:], m.Y)
	binary.LittleEndian.PutUint16(b[8:], m.W)
	binary.LittleEndian.PutUint16(b[10:], m.H)
}

// DecodeDamage splitst een DAMAGE-payload in meta + pixelbytes en valideert
// dat de pixellengte bij de rechthoek past.
func DecodeDamage(p []byte) (Damage, []byte, error) {
	if len(p) < damageMetaSize {
		return Damage{}, nil, ErrShort
	}
	m := Damage{
		Frame: binary.LittleEndian.Uint32(p),
		X:     binary.LittleEndian.Uint16(p[4:]),
		Y:     binary.LittleEndian.Uint16(p[6:]),
		W:     binary.LittleEndian.Uint16(p[8:]),
		H:     binary.LittleEndian.Uint16(p[10:]),
	}
	pix := p[damageMetaSize:]
	if len(pix) != int(m.W)*int(m.H)*4 {
		return Damage{}, nil, fmt.Errorf("surf: damage %dx%d wants %d pixel bytes, got %d",
			m.W, m.H, int(m.W)*int(m.H)*4, len(pix))
	}
	return m, pix, nil
}

// Present: frame u32 — alle DAMAGE van dit frame wordt atomisch zichtbaar.
type Present struct {
	Frame uint32
}

func (m Present) Encode() []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, m.Frame)
	return b
}

func DecodePresent(p []byte) (Present, error) {
	if len(p) < 4 {
		return Present{}, ErrShort
	}
	return Present{Frame: binary.LittleEndian.Uint32(p)}, nil
}

// Configure: w u16 | h u16 — de display vertelt de app zijn windowmaat.
type Configure struct {
	W, H uint16
}

func (m Configure) Encode() []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint16(b, m.W)
	binary.LittleEndian.PutUint16(b[2:], m.H)
	return b
}

func DecodeConfigure(p []byte) (Configure, error) {
	if len(p) < 4 {
		return Configure{}, ErrShort
	}
	return Configure{
		W: binary.LittleEndian.Uint16(p),
		H: binary.LittleEndian.Uint16(p[2:]),
	}, nil
}

// Input: kind u8 | code u32 | value s32 | x u16 | y u16 (13 bytes, gepakt).
type Input struct {
	Kind  uint8
	Code  uint32
	Value int32
	X, Y  uint16
}

func (m Input) Encode() []byte {
	b := make([]byte, 13)
	b[0] = m.Kind
	binary.LittleEndian.PutUint32(b[1:], m.Code)
	binary.LittleEndian.PutUint32(b[5:], uint32(m.Value))
	binary.LittleEndian.PutUint16(b[9:], m.X)
	binary.LittleEndian.PutUint16(b[11:], m.Y)
	return b
}

func DecodeInput(p []byte) (Input, error) {
	if len(p) < 13 {
		return Input{}, ErrShort
	}
	return Input{
		Kind:  p[0],
		Code:  binary.LittleEndian.Uint32(p[1:]),
		Value: int32(binary.LittleEndian.Uint32(p[5:])),
		X:     binary.LittleEndian.Uint16(p[9:]),
		Y:     binary.LittleEndian.Uint16(p[11:]),
	}, nil
}
