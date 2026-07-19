package surf

import (
	"bytes"
	"io"
	"testing"
)

// TestRoundtrip codeert elk berichttype, leest het terug via ReadMsg en
// vergelijkt veld-voor-veld — het draadformaat is het contract.
func TestRoundtrip(t *testing.T) {
	var buf bytes.Buffer

	hello := Hello{Version: Version, App: "clock @ node-a"}
	for i := range hello.Token {
		hello.Token[i] = byte(i)
	}
	if err := WriteMsg(&buf, TypeHello, 0, hello.Encode()); err != nil {
		t.Fatal(err)
	}
	create := Create{W: 320, H: 200, Format: FormatXRGB8888}
	if err := WriteMsg(&buf, TypeCreate, 1, create.Encode()); err != nil {
		t.Fatal(err)
	}
	dmg := Damage{Frame: 7, X: 10, Y: 20, W: 3, H: 2}
	pix := make([]byte, 3*2*4)
	for i := range pix {
		pix[i] = byte(0xA0 + i)
	}
	if err := WriteDamage(&buf, 1, dmg, pix); err != nil {
		t.Fatal(err)
	}
	if err := WriteMsg(&buf, TypePresent, 1, (Present{Frame: 7}).Encode()); err != nil {
		t.Fatal(err)
	}
	if err := WriteMsg(&buf, TypeConfigure, 1, (Configure{W: 640, H: 480}).Encode()); err != nil {
		t.Fatal(err)
	}
	in := Input{Kind: InputButton, Code: 0, Value: 1, X: 100, Y: 42}
	if err := WriteMsg(&buf, TypeInput, 1, in.Encode()); err != nil {
		t.Fatal(err)
	}

	var scratch []byte
	var h Header
	var p []byte
	var err error

	h, p, err = ReadMsg(&buf, scratch)
	if err != nil || h.Type != TypeHello {
		t.Fatalf("hello: %v type=%d", err, h.Type)
	}
	gotHello, err := DecodeHello(p)
	if err != nil || gotHello != hello {
		t.Fatalf("hello roundtrip: %+v vs %+v (%v)", gotHello, hello, err)
	}

	h, p, err = ReadMsg(&buf, p)
	if err != nil || h.Type != TypeCreate || h.Surface != 1 {
		t.Fatalf("create: %v %+v", err, h)
	}
	if got, _ := DecodeCreate(p); got != create {
		t.Fatalf("create roundtrip: %+v", got)
	}

	h, p, err = ReadMsg(&buf, p)
	if err != nil || h.Type != TypeDamage {
		t.Fatalf("damage: %v %+v", err, h)
	}
	gotDmg, gotPix, err := DecodeDamage(p)
	if err != nil || gotDmg != dmg || !bytes.Equal(gotPix, pix) {
		t.Fatalf("damage roundtrip: %+v (%v)", gotDmg, err)
	}

	h, p, err = ReadMsg(&buf, p)
	if got, _ := DecodePresent(p); err != nil || h.Type != TypePresent || got.Frame != 7 {
		t.Fatalf("present: %v %+v", err, h)
	}

	h, p, err = ReadMsg(&buf, p)
	if got, _ := DecodeConfigure(p); err != nil || h.Type != TypeConfigure || got.W != 640 || got.H != 480 {
		t.Fatalf("configure: %v %+v", err, h)
	}

	h, p, err = ReadMsg(&buf, p)
	if got, _ := DecodeInput(p); err != nil || h.Type != TypeInput || got != in {
		t.Fatalf("input: %v %+v", err, h)
	}
}

// TestGuards: kapotte input mag nooit een panic of grote allocatie worden.
func TestGuards(t *testing.T) {
	// Length groter dan MaxPayload → ErrTooLarge, geen allocatie.
	raw := []byte{TypeDamage, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0x7F}
	if _, _, err := ReadMsg(bytes.NewReader(raw), nil); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	// Afgekapte header/payload → nette fout.
	if _, _, err := ReadMsg(bytes.NewReader([]byte{1, 2, 3}), nil); err == nil {
		t.Fatal("truncated header must error")
	}
	full := new(bytes.Buffer)
	if err := WriteMsg(full, TypePresent, 1, (Present{Frame: 1}).Encode()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadMsg(bytes.NewReader(full.Bytes()[:HeaderSize+2]), nil); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated payload: %v", err)
	}
	// Damage met pixellengte die niet bij de rechthoek past.
	bad := (Damage{W: 2, H: 2}) // 16 bytes verwacht
	var meta [damageMetaSize]byte
	bad.encode(meta[:])
	if _, _, err := DecodeDamage(append(meta[:], make([]byte, 8)...)); err == nil {
		t.Fatal("mismatched damage size must error")
	}
	// WriteDamage weigert een verkeerde pixellengte aan de zendkant.
	if err := WriteDamage(io.Discard, 1, bad, make([]byte, 8)); err == nil {
		t.Fatal("WriteDamage must reject wrong pixel length")
	}
	// Hello met te lange naam wordt afgekapt, niet corrupt.
	long := Hello{Version: 1, App: string(make([]byte, 300))}
	if got, err := DecodeHello(long.Encode()); err != nil || len(got.App) != 255 {
		t.Fatalf("long name: %v len=%d", err, len(got.App))
	}
}
