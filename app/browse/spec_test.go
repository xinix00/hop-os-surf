package browse

// De levende specificatie van de layout-engine. Elke fixture in
// testdata/spec/ is één gedragsgebied: echt HTML+CSS met onderin een
// <script type="text/x-expect">-blok dat in gewone woorden zegt wat er op
// de pagina moet staan (woordenlijst: docs/browser-plan.md). *.todo.html
// zijn de gaten die nog open staan: ze renderen wél mee op het contactvel,
// maar hun verwachtingen tellen pas mee met SPEC_TODO=1 (rood = de klus).
// Het contactvel — docs/browser-spec.png — wordt elke run vers geschreven,
// volledig offline via de handler-transport.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const specW, specH = 480, 360

func TestSpec(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "spec", "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("geen spec-fixtures gevonden: %v", err)
	}
	// Op het vel: eerst wat we kunnen, dan de open todo's.
	sort.Slice(files, func(i, j int) bool {
		ti, tj := strings.Contains(files[i], ".todo."), strings.Contains(files[j], ".todo.")
		if ti != tj {
			return !ti
		}
		return files[i] < files[j]
	})
	runTodo := os.Getenv("SPEC_TODO") != ""
	type cel struct {
		naam string
		img  *image.RGBA
	}
	var cellen []cel
	for _, f := range files {
		naam := strings.TrimSuffix(filepath.Base(f), ".html")
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := NewSessionHandler(specMux())
		if err := s.Go("spec.local/" + filepath.Base(f)); err != nil {
			t.Fatalf("%s: Go: %v", naam, err)
		}
		img := image.NewRGBA(image.Rect(0, 0, specW, specH))
		v := View{Addr: s.URL(), Page: s.Layout(specW), Status: "spec " + naam}
		v.Render(img)
		cellen = append(cellen, cel{naam, img})
		if strings.HasSuffix(naam, ".todo") && !runTodo {
			continue
		}
		t.Run(naam, func(t *testing.T) {
			runExpect(t, s, expectBlock(t, string(src)))
		})
	}

	// Het contactvel: alle fixtures naast elkaar, elke run vers in docs/.
	const kols, marge, labelH = 3, 8, 16
	rijen := (len(cellen) + kols - 1) / kols
	sheet := image.NewRGBA(image.Rect(0, 0, marge+kols*(specW+marge), marge+rijen*(specH+labelH+marge)))
	draw.Draw(sheet, sheet.Bounds(), &image.Uniform{color.RGBA{0x10, 0x14, 0x1E, 0xFF}}, image.Point{}, draw.Src)
	for i, c := range cellen {
		x := marge + (i%kols)*(specW+marge)
		y := marge + (i/kols)*(specH+labelH+marge)
		draw.Draw(sheet, image.Rect(x, y, x+specW, y+labelH), &image.Uniform{colBar}, image.Point{}, draw.Src)
		drawTxt(sheet, x+4, y+4, 1, colBarTxt, c.naam)
		draw.Draw(sheet, image.Rect(x, y+labelH, x+specW, y+labelH+specH), c.img, image.Point{}, draw.Src)
	}
	out := filepath.Join("..", "..", "docs", "browser-spec.png")
	fo, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer fo.Close()
	if err := png.Encode(fo, sheet); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d fixtures -> %s", len(cellen), out)
}

// specMux serveert de fixtures plus een paar gegenereerde plaatjes —
// alles lokaal, dus de poort blijft zonder netwerk groen.
func specMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(filepath.Join("testdata", "spec"))))
	pix := func(w, h int, c color.RGBA) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		var buf bytes.Buffer
		png.Encode(&buf, img)
		return buf.Bytes()
	}
	// banden: drie verticale kleurbanden — wie hem plet ziet rood en blauw,
	// wie hem bijsnijdt (object-fit: cover) alleen de groene middenband.
	banden := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			c := color.RGBA{0xCC, 0x22, 0x22, 0xFF}
			switch {
			case x >= 120:
				c = color.RGBA{0x22, 0x44, 0xCC, 0xFF}
			case x >= 80:
				c = color.RGBA{0x22, 0xAA, 0x44, 0xFF}
			}
			banden.SetRGBA(x, y, c)
		}
	}
	var bbuf bytes.Buffer
	png.Encode(&bbuf, banden)
	for pad, b := range map[string][]byte{
		"/pix/rood.png":   pix(64, 48, color.RGBA{0xCC, 0x22, 0x22, 0xFF}),
		"/pix/blauw.png":  pix(48, 48, color.RGBA{0x22, 0x44, 0xCC, 0xFF}),
		"/pix/foto.png":   pix(160, 100, color.RGBA{0x44, 0x88, 0x66, 0xFF}),
		"/pix/banden.png": bbuf.Bytes(),
		"/pix/vorm.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="60" height="40" viewBox="0 0 60 40">` +
			`<rect width="60" height="40" fill="#cc2200"/><circle cx="45" cy="20" r="10" fill="#0022cc"/></svg>`),
		"/pix/breed.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1000 200">` +
			`<rect width="1000" height="200" fill="#22aa44"/></svg>`),
		"/pix/sprite.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="60" height="80" viewBox="0 0 60 80">` +
			`<rect width="60" height="40" fill="#cc2200"/><rect y="40" width="60" height="40" fill="#22aa44"/></svg>`),
		// symbolen: een externe sprite-sheet zoals tweakers' icons-symbol.svg —
		// alleen <symbol>-definities, niets dat uit zichzelf rendert.
		"/pix/symbolen.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg">` +
			`<symbol id="logo-vol" viewBox="0 0 120 24"><rect width="120" height="24" fill="#ff6600"/></symbol>` +
			`<symbol id="zoek" viewBox="0 0 20 20"><circle cx="10" cy="10" r="8" fill="#2244cc"/></symbol></svg>`),
		// nest: een genest sprite-vel zoals wikipedia's portal-sheet — de
		// sub-svg's hebben elk hun eigen viewBox en een y-offset in het vel.
		"/pix/nest.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="60" height="80" viewBox="0 0 60 80">` +
			`<svg id="boven" width="60" height="40" viewBox="0 0 60 40"><rect width="60" height="40" fill="#cc2200"/></svg>` +
			`<svg id="onder" width="60" height="40" y="40" viewBox="0 0 120 80"><rect width="120" height="80" fill="#22aa44"/></svg></svg>`),
		// import-*: de @import-keten — a importeert b (relatief) en een
		// media-gebonden derde; stijl hangt aan een <style>-blok.
		"/pix/import-a.css": []byte(`@import "import-b.css";
@import url(/pix/import-breed.css) (min-width: 900px);
.ge-a { color: #cc2200 }`),
		"/pix/import-b.css":     []byte(`.ge-b { color: #2244cc }`),
		"/pix/import-breed.css": []byte(`.ge-breed { color: #cc2200 }`),
		"/pix/import-stijl.css": []byte(`.stijl { color: #6a2a8a }`),
		// twk-symbolen: het tweakers-vel voor de kop-fixture — wordmark op
		// hun echte verhouding (139:38) plus een paar fa-iconen.
		"/pix/twk-symbolen.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg">` +
			`<symbol id="twk-logo-full" viewBox="0 0 139 38"><rect width="139" height="38" fill="#ffffff"/></symbol>` +
			`<symbol id="fa-bars" viewBox="0 0 448 512"><rect y="64" width="448" height="64" fill="#ffffff"/><rect y="224" width="448" height="64" fill="#ffffff"/><rect y="384" width="448" height="64" fill="#ffffff"/></symbol>` +
			`<symbol id="fa-magnifying-glass" viewBox="0 0 512 512"><circle cx="208" cy="208" r="160" fill="#ffffff"/></symbol>` +
			`<symbol id="fa-right-to-bracket" viewBox="0 0 512 512"><rect x="64" y="96" width="384" height="320" fill="#ffffff"/></symbol>` +
			`<symbol id="fa-gear" viewBox="0 0 512 512"><circle cx="256" cy="256" r="180" fill="#ffffff"/></symbol></svg>`),
	} {
		body := b
		mux.HandleFunc(pad, func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	}
	return mux
}

// expectBlock haalt het x-expect-blok uit de ruwe fixture-bytes.
func expectBlock(t *testing.T, src string) string {
	const open = `<script type="text/x-expect">`
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatal("fixture zonder x-expect-blok")
	}
	rest := src[i+len(open):]
	j := strings.Index(rest, "</script>")
	if j < 0 {
		t.Fatal("x-expect-blok niet gesloten")
	}
	return rest[:j]
}

// runExpect voert de verwachtingsregels uit. "breedte N" schakelt de
// layoutbreedte (default 480) — layouts worden per breedte gecachet.
func runExpect(t *testing.T, s *Session, blok string) {
	pages := map[int]Page{}
	w := specW
	for ln, line := range strings.Split(blok, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		toks := specTokens(line)
		if toks[0] == "breedte" {
			n, err := strconv.Atoi(toks[1])
			if err != nil || n < 200 {
				t.Fatalf("regel %d: onbruikbare breedte %q", ln+1, line)
			}
			w = n
			continue
		}
		p, ok := pages[w]
		if !ok {
			p = s.Layout(w)
			pages[w] = p
		}
		if err := specCheck(p, w, toks); err != nil {
			t.Errorf("regel %d (%s): %v", ln+1, line, err)
		}
	}
}

// specTokens splitst een regel in kale woorden en "tekst tussen
// aanhalingstekens" (die mag spaties bevatten).
func specTokens(line string) []string {
	var out []string
	for i := 0; i < len(line); {
		switch {
		case line[i] == ' ' || line[i] == '\t':
			i++
		case line[i] == '"':
			j := strings.IndexByte(line[i+1:], '"')
			if j < 0 {
				out = append(out, line[i+1:])
				return out
			}
			out = append(out, line[i+1:i+1+j])
			i += j + 2
		default:
			j := i
			for j < len(line) && line[j] != ' ' && line[j] != '\t' {
				j++
			}
			out = append(out, line[i:j])
			i = j
		}
	}
	return out
}

func findIdx(p Page, text string) int {
	for i := range p.Boxes {
		if strings.Contains(p.Boxes[i].Text, text) {
			return i
		}
	}
	return -1
}

func boxCenter(r image.Rectangle) image.Point {
	return image.Pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
}

// vlakOm zoekt het kleinste kleur/rand-vlak waar deze tekst op ligt —
// "de kaart onder de tekst".
func vlakOm(p Page, text string) *Box {
	tb := find(p, text)
	if tb == nil {
		return nil
	}
	c := boxCenter(tb.R)
	var best *Box
	for i := range p.Boxes {
		b := &p.Boxes[i]
		if b.Text != "" || (!b.HasBG && !b.HasBrd) {
			continue
		}
		if !c.In(b.R) {
			continue
		}
		if best == nil || b.R.Dx()*b.R.Dy() < best.R.Dx()*best.R.Dy() {
			best = b
		}
	}
	return best
}

// specCheck voert één verwachting uit tegen de gelayoute pagina.
func specCheck(p Page, w int, toks []string) error {
	arg := func(i int) string {
		if i < len(toks) {
			return toks[i]
		}
		return ""
	}
	box := func(i int) (*Box, error) {
		if b := find(p, arg(i)); b != nil {
			return b, nil
		}
		return nil, fmt.Errorf("%q staat niet op de pagina", arg(i))
	}
	vlak := func(i int) (*Box, error) {
		if v := vlakOm(p, arg(i)); v != nil {
			return v, nil
		}
		return nil, fmt.Errorf("geen vlak onder %q", arg(i))
	}
	num := func(i int) int { n, _ := strconv.Atoi(arg(i)); return n }
	col := func(i int) (color.RGBA, error) {
		c, ok := cssColor(arg(i))
		if !ok {
			return c, fmt.Errorf("%q is geen kleur", arg(i))
		}
		return c, nil
	}
	twee := func() (a, b *Box, err error) {
		if a, err = box(1); err != nil {
			return
		}
		b, err = box(2)
		return
	}
	// doos: het vlak om de tekst als dat er is, anders de tekstbox zelf —
	// voor verticaal meten aan kaarten én kale tekstregels.
	doos := func(i int) (*Box, error) {
		if v := vlakOm(p, arg(i)); v != nil {
			return v, nil
		}
		return box(i)
	}
	yOverlap := func(a, b *Box) bool { return a.R.Min.Y < b.R.Max.Y && b.R.Min.Y < a.R.Max.Y }

	switch toks[0] {
	case "zichtbaar":
		_, err := box(1)
		return err
	case "verborgen":
		if b := find(p, arg(1)); b != nil {
			return fmt.Errorf("%q staat op de pagina: %v", arg(1), b.R)
		}
	case "onder":
		a, b, err := twee()
		if err != nil {
			return err
		}
		if a.R.Min.Y < b.R.Max.Y {
			return fmt.Errorf("hoort eronder: %v boven %v", a.R, b.R)
		}
	case "rechtsvan":
		a, b, err := twee()
		if err != nil {
			return err
		}
		if a.R.Min.X < b.R.Max.X || !yOverlap(a, b) {
			return fmt.Errorf("hoort rechts ervan op dezelfde regel: %v vs %v", a.R, b.R)
		}
	case "overlapt":
		a, b, err := twee()
		if err != nil {
			return err
		}
		if !yOverlap(a, b) {
			return fmt.Errorf("y-bereiken raken elkaar niet: %v vs %v", a.R, b.R)
		}
	case "bovenop":
		ia, ib := findIdx(p, arg(1)), findIdx(p, arg(2))
		if ia < 0 || ib < 0 {
			return fmt.Errorf("%q of %q staat niet op de pagina", arg(1), arg(2))
		}
		if ia <= ib {
			return fmt.Errorf("hoort later (bovenop) geschilderd: index %d <= %d", ia, ib)
		}
	case "kleur":
		b, err := box(1)
		if err != nil {
			return err
		}
		c, err := col(2)
		if err != nil {
			return err
		}
		if b.Col != c {
			return fmt.Errorf("kleur %v, wil %v", b.Col, c)
		}
	case "achtergrond":
		b, err := box(1)
		if err != nil {
			return err
		}
		c, err := col(2)
		if err != nil {
			return err
		}
		if b.HasBG && b.BG == c {
			return nil
		}
		cen := boxCenter(b.R)
		for i := range p.Boxes {
			pb := &p.Boxes[i]
			if pb.HasBG && pb.BG == c && cen.In(pb.R) {
				return nil
			}
		}
		return fmt.Errorf("geen vlak in %v onder %q", c, arg(1))
	case "rand":
		b, err := box(1)
		if err != nil {
			return err
		}
		cen := boxCenter(b.R)
		for i := range p.Boxes {
			pb := &p.Boxes[i]
			if pb.HasBrd && cen.In(pb.R) {
				return nil
			}
		}
		return fmt.Errorf("geen rand-vlak onder %q", arg(1))
	case "randkleur":
		// het vlak onder de tekst heeft een rand in precies deze kleur
		b, err := box(1)
		if err != nil {
			return err
		}
		c, err := col(2)
		if err != nil {
			return err
		}
		cen := boxCenter(b.R)
		for i := range p.Boxes {
			pb := &p.Boxes[i]
			if pb.HasBrd && cen.In(pb.R) {
				if pb.Border == c {
					return nil
				}
				return fmt.Errorf("rand in %v, wil %v", pb.Border, c)
			}
		}
		return fmt.Errorf("geen rand-vlak onder %q", arg(1))
	case "vet":
		b, err := box(1)
		if err != nil {
			return err
		}
		if !b.Bold {
			return fmt.Errorf("niet vet")
		}
	case "nietvet":
		b, err := box(1)
		if err != nil {
			return err
		}
		if b.Bold {
			return fmt.Errorf("onbedoeld vet")
		}
	case "schaal":
		b, err := box(1)
		if err != nil {
			return err
		}
		if b.Scale != num(2) {
			return fmt.Errorf("schaal %d, wil %d", b.Scale, num(2))
		}
	case "link":
		b, err := box(1)
		if err != nil {
			return err
		}
		if b.Href != arg(2) {
			return fmt.Errorf("href %q, wil %q", b.Href, arg(2))
		}
	case "gecentreerd":
		b, err := box(1)
		if err != nil {
			return err
		}
		if mid := (b.R.Min.X + b.R.Max.X) / 2; absInt(mid-w/2) > 24 {
			return fmt.Errorf("midden op x=%d, paginamidden is %d", mid, w/2)
		}
	case "rechterrand":
		b, err := box(1)
		if err != nil {
			return err
		}
		if b.R.Max.X < w-pad-16 {
			return fmt.Errorf("eindigt op x=%d, rechterrand is %d", b.R.Max.X, w-pad)
		}
	case "ingesprongen":
		b, err := box(1)
		if err != nil {
			return err
		}
		if b.R.Min.X < pad+10 {
			return fmt.Errorf("begint op x=%d, dat is de kantlijn", b.R.Min.X)
		}
	case "vlakbreedte":
		v, err := vlak(1)
		if err != nil {
			return err
		}
		// ±8: vlakken liggen 2px binnen de blokgrenzen en cellen ronden af.
		if absInt(v.R.Dx()-num(2)) > 8 {
			return fmt.Errorf("vlak is %dpx breed, wil %d: %v", v.R.Dx(), num(2), v.R)
		}
	case "vlakhoogte":
		v, err := vlak(1)
		if err != nil {
			return err
		}
		if v.R.Dy() < num(2)-2 {
			return fmt.Errorf("vlak is %dpx hoog, wil minstens %d: %v", v.R.Dy(), num(2), v.R)
		}
	case "vlakjes":
		// minstens N losse gekleurde vlakjes van W×H (±2) zonder inhoud —
		// carrousel-stipjes, statuslampjes
		c, err := col(4)
		if err != nil {
			return err
		}
		n := 0
		for i := range p.Boxes {
			b := &p.Boxes[i]
			if b.HasBG && b.BG == c && b.Text == "" && b.Img == nil &&
				absInt(b.R.Dx()-num(2)) <= 2 && absInt(b.R.Dy()-num(3)) <= 2 {
				n++
			}
		}
		if n < num(1) {
			return fmt.Errorf("%d vlakjes van %dx%d in %v, wil minstens %d", n, num(2), num(3), c, num(1))
		}
	case "rond":
		v, err := vlak(1)
		if err != nil {
			return err
		}
		if v.Rad == 0 {
			return fmt.Errorf("vlak heeft geen hoekstraal: %v", v.R)
		}
	case "hmidden":
		// de tekstrun horizontaal in het midden van zijn eigen vlak —
		// text-align: center binnen een vak (wikipedia's talencirkel)
		a, err := box(1)
		if err != nil {
			return err
		}
		b, err := doos(2)
		if err != nil {
			return err
		}
		ca, cb := (a.R.Min.X+a.R.Max.X)/2, (b.R.Min.X+b.R.Max.X)/2
		if absInt(ca-cb) > 8 {
			return fmt.Errorf("horizontaal midden %d vs %d: %v vs %v", ca, cb, a.R, b.R)
		}
	case "geenrand":
		b, err := box(1)
		if err != nil {
			return err
		}
		cen := boxCenter(b.R)
		for i := range p.Boxes {
			pb := &p.Boxes[i]
			if pb.HasBrd && cen.In(pb.R) {
				return fmt.Errorf("onbedoeld een rand-vlak onder %q: %v", arg(1), pb.R)
			}
		}

	case "rondeplaat", "pasplaat":
		// de afbeelding van maat (1,2) heeft een doorzichtige hoek en een
		// dekkend midden — border-radius op een <img> (rondeplaat) of
		// letterboxing door preserveAspectRatio (pasplaat)
		for i := range p.Boxes {
			b := &p.Boxes[i]
			if b.Img != nil && absInt(b.R.Dx()-num(1)) <= 2 && absInt(b.R.Dy()-num(2)) <= 2 {
				if b.Img.RGBAAt(0, 0).A != 0 {
					return fmt.Errorf("hoekpixel is dekkend: %v", b.Img.RGBAAt(0, 0))
				}
				if b.Img.RGBAAt(num(1)/2, num(2)/2).A == 0 {
					return fmt.Errorf("middenpixel is doorzichtig")
				}
				return nil
			}
		}
		return fmt.Errorf("geen afbeelding van %dx%d op de pagina", num(1), num(2))
	case "minstensy":
		b, err := box(1)
		if err != nil {
			return err
		}
		if b.R.Min.Y < num(2) {
			return fmt.Errorf("begint op y=%d, hoort minstens %d (de marge erboven telt)", b.R.Min.Y, num(2))
		}
	case "vlakx":
		v, err := vlak(1)
		if err != nil {
			return err
		}
		if absInt(v.R.Min.X-num(2)) > 8 {
			return fmt.Errorf("vlak begint op x=%d, wil %d: %v", v.R.Min.X, num(2), v.R)
		}
	case "vlakmidden":
		v, err := vlak(1)
		if err != nil {
			return err
		}
		if mid := (v.R.Min.X + v.R.Max.X) / 2; absInt(mid-w/2) > 12 {
			return fmt.Errorf("vlakmidden op x=%d, paginamidden is %d", mid, w/2)
		}
	case "bredervlak":
		va, err := vlak(1)
		if err != nil {
			return err
		}
		vb, err := vlak(2)
		if err != nil {
			return err
		}
		if va.R.Dx() <= vb.R.Dx() {
			return fmt.Errorf("vlak %dpx hoort breder dan %dpx", va.R.Dx(), vb.R.Dx())
		}
	case "evenhoog":
		va, err := vlak(1)
		if err != nil {
			return err
		}
		vb, err := vlak(2)
		if err != nil {
			return err
		}
		if va.R.Dy() != vb.R.Dy() {
			return fmt.Errorf("vlakken %dpx en %dpx hoog, horen gelijk", va.R.Dy(), vb.R.Dy())
		}
	case "tegel":
		b, err := box(1)
		if err != nil {
			return err
		}
		cen := boxCenter(b.R)
		for i := range p.Boxes {
			pb := &p.Boxes[i]
			if (pb.Tile != nil || (pb.Img != nil && pb.Text == "")) && cen.In(pb.R) {
				return nil
			}
		}
		return fmt.Errorf("geen achtergrondafbeelding onder %q", arg(1))
	case "plaatmaat":
		for i := range p.Boxes {
			b := &p.Boxes[i]
			if b.Img != nil && absInt(b.R.Dx()-num(1)) <= 2 && absInt(b.R.Dy()-num(2)) <= 2 {
				return nil
			}
		}
		return fmt.Errorf("geen afbeelding van %dx%d op de pagina", num(1), num(2))
	case "plaatpunt":
		// de afbeelding van maat (1,2), gesampled op offset (3,4): kleur (5)
		// — Box.Img is al naar schermmaat geschaald, dus direct te prikken.
		c, err := col(5)
		if err != nil {
			return err
		}
		for i := range p.Boxes {
			b := &p.Boxes[i]
			if b.Img != nil && absInt(b.R.Dx()-num(1)) <= 2 && absInt(b.R.Dy()-num(2)) <= 2 {
				if got := b.Img.RGBAAt(num(3), num(4)); got != c {
					return fmt.Errorf("pixel (%d,%d) is %v, wil %v", num(3), num(4), got, c)
				}
				return nil
			}
		}
		return fmt.Errorf("geen afbeelding van %dx%d op de pagina", num(1), num(2))
	case "plaatmidden":
		for i := range p.Boxes {
			b := &p.Boxes[i]
			if b.Img != nil && absInt(b.R.Dx()-num(1)) <= 2 && absInt(b.R.Dy()-num(2)) <= 2 {
				if mid := (b.R.Min.X + b.R.Max.X) / 2; absInt(mid-w/2) > 24 {
					return fmt.Errorf("afbeelding-midden op x=%d, paginamidden is %d", mid, w/2)
				}
				return nil
			}
		}
		return fmt.Errorf("geen afbeelding van %dx%d op de pagina", num(1), num(2))
	case "plaatjes", "plaatjesprecies":
		n := 0
		for i := range p.Boxes {
			if p.Boxes[i].Img != nil {
				n++
			}
		}
		if n < num(1) || (toks[0] == "plaatjesprecies" && n != num(1)) {
			return fmt.Errorf("%d afbeeldingen, wil %s %d", n, toks[0], num(1))
		}
	case "gepind":
		if !p.Pinned() {
			return fmt.Errorf("geen gepinde header")
		}
	case "donker":
		if !p.HasBG || luma(p.BG) > 80 {
			return fmt.Errorf("paginacanvas niet donker: HasBG=%v BG=%v", p.HasBG, p.BG)
		}
	case "licht":
		if p.HasBG && luma(p.BG) < 128 {
			return fmt.Errorf("paginacanvas is donker: %v", p.BG)
		}
	case "veldkleur", "veldrond", "veldhoog", "veldkaal":
		// de stijl van een invoerveld/knop, gevonden op zijn name-attribuut
		var fb *Box
		for i := range p.Fields {
			if p.Fields[i].Name == arg(1) {
				for j := range p.Boxes {
					if p.Boxes[j].Field == i+1 {
						fb = &p.Boxes[j]
					}
				}
			}
		}
		if fb == nil {
			return fmt.Errorf("geen veld %q op de pagina", arg(1))
		}
		switch toks[0] {
		case "veldkleur":
			c, err := col(2)
			if err != nil {
				return err
			}
			if !fb.HasBG || fb.BG != c {
				return fmt.Errorf("veld heeft achtergrond %v/%v, wil %v", fb.HasBG, fb.BG, c)
			}
		case "veldrond":
			if fb.Rad == 0 {
				return fmt.Errorf("veld heeft geen hoekstraal")
			}
		case "veldhoog":
			if absInt(fb.R.Dy()-num(2)) > 2 {
				return fmt.Errorf("veld is %dpx hoog, wil %d", fb.R.Dy(), num(2))
			}
		case "veldkaal":
			if fb.HasBG || fb.HasBrd || fb.Rad != 0 {
				return fmt.Errorf("veld hoort de UA-default te dragen: %+v", fb)
			}
		}
	case "veldbreedte":
		for i := range p.Fields {
			if p.Fields[i].Name == arg(1) {
				if absInt(p.Fields[i].R.Dx()-num(2)) > 10 {
					return fmt.Errorf("veld %q is %dpx breed, wil %d", arg(1), p.Fields[i].R.Dx(), num(2))
				}
				return nil
			}
		}
		return fmt.Errorf("geen veld %q op de pagina", arg(1))
	case "vmidden":
		// de tekstrun zelf tegen het anker-vlak: "staat deze tekst verticaal
		// in het midden van dat blok?"
		a, err := box(1)
		if err != nil {
			return err
		}
		b, err := doos(2)
		if err != nil {
			return err
		}
		ca, cb := (a.R.Min.Y+a.R.Max.Y)/2, (b.R.Min.Y+b.R.Max.Y)/2
		if absInt(ca-cb) > 7 {
			return fmt.Errorf("verticaal midden %d vs %d: %v vs %v", ca, cb, a.R, b.R)
		}
	case "onderlijn":
		a, err := doos(1)
		if err != nil {
			return err
		}
		b, err := doos(2)
		if err != nil {
			return err
		}
		if absInt(a.R.Max.Y-b.R.Max.Y) > 6 {
			return fmt.Errorf("onderkanten %d vs %d horen gelijk", a.R.Max.Y, b.R.Max.Y)
		}
	case "omvat":
		a, err := doos(1)
		if err != nil {
			return err
		}
		b, err := doos(2)
		if err != nil {
			return err
		}
		if !b.R.In(a.R) {
			return fmt.Errorf("%v hoort binnen %v te liggen", b.R, a.R)
		}
	case "middenpaar":
		a, err := doos(1)
		if err != nil {
			return err
		}
		b, err := doos(2)
		if err != nil {
			return err
		}
		u := a.R.Union(b.R)
		if mid := (u.Min.X + u.Max.X) / 2; absInt(mid-w/2) > 12 {
			return fmt.Errorf("paar-midden op x=%d, paginamidden is %d", mid, w/2)
		}
	case "vlaklinks":
		v, err := vlak(1)
		if err != nil {
			return err
		}
		if v.R.Min.X > pad+6 {
			return fmt.Errorf("vlak begint op x=%d, de kantlijn is %d", v.R.Min.X, pad)
		}
	case "vlakrechts":
		v, err := vlak(1)
		if err != nil {
			return err
		}
		// ±18: een celvlak ligt een paar pixels binnen zijn kolomrand, en
		// in een gemeten (content-sized) cel komt daar de pad-marge bij.
		if v.R.Max.X < w-pad-18 {
			return fmt.Errorf("vlak eindigt op x=%d, rechterrand is %d", v.R.Max.X, w-pad)
		}
	case "ruimteonder":
		a, err := doos(1)
		if err != nil {
			return err
		}
		b, err := doos(2)
		if err != nil {
			return err
		}
		if d := a.R.Min.Y - b.R.Max.Y; d < num(3) {
			return fmt.Errorf("%dpx lucht tussen %v en %v, wil minstens %d", d, b.R, a.R, num(3))
		}
	case "streep":
		// een gekleurde streep (zijrand) aan of tegen het blok van de tekst
		b, err := doos(1)
		if err != nil {
			return err
		}
		c, err := col(2)
		if err != nil {
			return err
		}
		zone := b.R.Inset(-24) // ook een strook aan de overkant van de padding telt (blockquote: 16px + balk)
		for i := range p.Boxes {
			pb := &p.Boxes[i]
			if pb.Rule && pb.Col == c && pb.R.Overlaps(zone) {
				return nil
			}
		}
		return fmt.Errorf("geen streep in %v bij %q", c, arg(1))
	case "onderstreept":
		b, err := box(1)
		if err != nil {
			return err
		}
		if !b.Under {
			return fmt.Errorf("niet onderstreept")
		}
	case "doorgestreept":
		b, err := box(1)
		if err != nil {
			return err
		}
		if !b.Strike {
			return fmt.Errorf("niet doorgestreept")
		}
	case "lager":
		// a hangt lager dan b op (ruwweg) dezelfde regel — sub-offset
		a, b, err := twee()
		if err != nil {
			return err
		}
		if a.R.Min.Y <= b.R.Min.Y || !yOverlap(a, b) {
			return fmt.Errorf("hoort lager op de regel: %v vs %v", a.R, b.R)
		}
	case "hoger":
		a, b, err := twee()
		if err != nil {
			return err
		}
		if a.R.Min.Y >= b.R.Min.Y || !yOverlap(a, b) {
			return fmt.Errorf("hoort hoger op de regel: %v vs %v", a.R, b.R)
		}
	case "zelfdekolom":
		// beide beginnen op dezelfde x — kolomuitlijning (rowspan!)
		a, b, err := twee()
		if err != nil {
			return err
		}
		if absInt(a.R.Min.X-b.R.Min.X) > 8 {
			return fmt.Errorf("kolomstart %d vs %d", a.R.Min.X, b.R.Min.X)
		}
	case "regelafstand":
		// de verticale pitch tussen twee regelstarts, binnen [min, max] —
		// de line-height-knop
		a, b, err := twee()
		if err != nil {
			return err
		}
		if d := b.R.Min.Y - a.R.Min.Y; d < num(3) || d > num(4) {
			return fmt.Errorf("regelafstand %d, wil %d..%d", d, num(3), num(4))
		}
	default:
		return fmt.Errorf("onbekende verwachting %q", toks[0])
	}
	return nil
}
