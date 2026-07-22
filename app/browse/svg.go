// SVG: heel veel logo's (tweakers, NRC) en iconen zijn vectors —
// tdewolff/canvas parseert en rastert ze (echte gradients, strokes,
// transforms; puur Go, dus ook op tamago). Drie routes komen hier samen:
// inline <svg> in de pagina, <img src="*.svg"> en svg-iconen/
// achtergronden via de Session.
package browse

import (
	"bytes"
	"image"
	"image/draw"
	"strconv"
	"strings"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
	"golang.org/x/net/html"
)

// rasterSVG rastert een SVG naar precies w×h. SVG's uit het wilde web
// kunnen de parser laten struikelen (filters, CSS-in-SVG): elke fout of
// panic is gewoon "geen beeld" — de aanroeper valt dan stil terug.
func rasterSVG(data []byte, w, h int) (m *image.RGBA) {
	defer func() {
		if recover() != nil {
			m = nil
		}
	}()
	if w < 1 || h < 1 || w > imgMaxDim || h > imgMaxDim {
		return nil
	}
	// LET OP: canvas rekt een viewBox x/y-apart de width/height-attr-doos
	// in (negeert preserveAspectRatio) — een use-symbol met een andere
	// verhouding dan zijn host-doos (NRC's logo: 491:147 in 110x55) werd
	// zo uitgerekt. De attrs eraf: de viewBox is dé verhouding, en het
	// passen in de doos doet de letterbox hieronder.
	c, err := canvas.ParseSVG(bytes.NewReader(stripRootSize(data)))
	if err != nil || c.W <= 0 || c.H <= 0 {
		return nil
	}
	// preserveAspectRatio (default xMidYMid meet): een svg wordt pássend
	// gemaakt in zijn doos (uniforme schaal, gecentreerd op transparant),
	// niet uitgerekt — tenzij de bron expliciet "none" zegt.
	s := float64(w) / c.W
	if s2 := float64(h) / c.H; s2 < s {
		s = s2
	}
	img := rasterizer.Draw(c, canvas.DPMM(s), canvas.DefaultColorSpace)
	if img == nil || img.Bounds().Dx() < 1 || img.Bounds().Dy() < 1 {
		return nil
	}
	if bytes.Contains(bytes.ToLower(svgHead(data)), []byte(`preserveaspectratio="none"`)) {
		return scaleTo(img, w, h) // rekken is hier de bedoeling
	}
	if img.Bounds().Dx() == w && img.Bounds().Dy() == h {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	off := image.Pt((w-img.Bounds().Dx())/2, (h-img.Bounds().Dy())/2)
	draw.Draw(out, img.Bounds().Add(off), img, img.Bounds().Min, draw.Over)
	return out
}

// rasterSVGNatural rastert op de eigen maat: width/height uit de bron, of
// — met alléén een viewBox — de CSS default object size (max 300x150 op
// verhouding). Proportioneel gecapt op maxDim.
func rasterSVGNatural(data []byte, maxDim int) (m *image.RGBA) {
	defer func() {
		if recover() != nil {
			m = nil
		}
	}()
	c, err := canvas.ParseSVG(bytes.NewReader(data))
	if err != nil || c.W <= 0 || c.H <= 0 {
		return nil
	}
	// Met width/height-attributen zijn c.W/c.H die pixelmaten; met alleen
	// een viewBox een geschaalde lezing — dan telt enkel de verhouding.
	w, h := int(c.W+0.5), int(c.H+0.5)
	if w < 1 || h < 1 {
		return nil
	}
	if head := svgHead(data); !bytes.Contains(head, []byte("width")) || !bytes.Contains(head, []byte("height")) {
		w, h = defaultObjectSize(w, h, maxDim)
	}
	if w > maxDim {
		h, w = h*maxDim/w, maxDim
	}
	if h > maxDim {
		w, h = w*maxDim/h, maxDim
	}
	return rasterSVG(data, w, h)
}

// rasterSVGSheet rastert een sprite-vel van geneste <svg id>-sub-logo's
// (wikipedia's portal-sheet: 22 logo's onder elkaar): oksvg plakt zoiets
// tot één dekkende klodder, dus wij rasteren elk sub-svg apart en leggen
// ze op hun x/y in het vel — daarna knipt background-position er gewoon
// zijn plaatjes uit. nil = geen genest vel (de gewone route mag het doen).
func rasterSVGSheet(data []byte, maxDim int) *image.RGBA {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	root := findEl(doc, "svg")
	if root == nil {
		return nil
	}
	var subs []*html.Node
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "svg" {
			subs = append(subs, c)
		}
	}
	if len(subs) < 2 {
		return nil
	}
	w, h := svgFloat(root, "width"), svgFloat(root, "height")
	if w < 1 || h < 1 {
		w, h = svgViewBox(root)
	}
	if w < 1 || h < 1 || w > maxDim || h > maxDim {
		return nil
	}
	vel := image.NewRGBA(image.Rect(0, 0, w, h))
	for _, s := range subs {
		sw, sh := svgFloat(s, "width"), svgFloat(s, "height")
		if sw < 1 || sh < 1 {
			sw, sh = svgViewBox(s)
		}
		if sw < 1 || sh < 1 {
			continue
		}
		x, y := svgFloat(s, "x"), svgFloat(s, "y")
		var buf bytes.Buffer
		if html.Render(&buf, s) != nil {
			continue
		}
		if m := rasterSVG(buf.Bytes(), sw, sh); m != nil {
			draw.Draw(vel, image.Rect(x, y, x+sw, y+sh), m, image.Point{}, draw.Over)
		}
	}
	return vel
}

// stripRootSize haalt de width/height-attributen van de wortel-<svg> af
// als er een viewBox is: de aanroeper bepaalt de doelmaat al, en de
// viewBox draagt de verhouding — zo kan de parser niets uitrekken.
func stripRootSize(data []byte) []byte {
	i := bytes.Index(data, []byte("<svg"))
	if i < 0 {
		return data
	}
	end := bytes.IndexByte(data[i:], '>')
	if end < 0 {
		return data
	}
	head := data[i : i+end]
	if !bytes.Contains(bytes.ToLower(head), []byte("viewbox")) {
		return data // zonder viewBox zíjn de attrs de enige maat
	}
	stripped := dropAttr(dropAttr(head, "width"), "height")
	if len(stripped) == len(head) {
		return data
	}
	out := make([]byte, 0, len(data)-len(head)+len(stripped))
	out = append(out, data[:i]...)
	out = append(out, stripped...)
	return append(out, data[i+end:]...)
}

// dropAttr verwijdert één attribuut (naam="waarde") uit een tag-kop —
// grens-bewust: style="width:…" en stroke-width blijven staan.
func dropAttr(head []byte, name string) []byte {
	low := bytes.ToLower(head)
	n := []byte(name)
	for i := 0; ; {
		j := bytes.Index(low[i:], n)
		if j < 0 {
			return head
		}
		j += i
		if j == 0 || !isSpace(low[j-1]) {
			i = j + 1
			continue
		}
		k := j + len(n)
		for k < len(low) && isSpace(low[k]) {
			k++
		}
		if k >= len(low) || low[k] != '=' {
			i = j + 1
			continue
		}
		k++
		for k < len(low) && isSpace(low[k]) {
			k++
		}
		if k < len(low) && (low[k] == '"' || low[k] == '\'') {
			q := low[k]
			m := bytes.IndexByte(low[k+1:], q)
			if m < 0 {
				return head
			}
			k += 1 + m + 1
		} else {
			for k < len(low) && !isSpace(low[k]) && low[k] != '>' {
				k++
			}
		}
		head = append(head[:j-1:j-1], head[k:]...)
		low = bytes.ToLower(head)
		i = j - 1
	}
}

// svgHead: de openings-tag van het svg-element (voor attribuut-detectie).
func svgHead(data []byte) []byte {
	i := bytes.Index(data, []byte("<svg"))
	if i < 0 {
		return nil
	}
	rest := data[i:]
	if j := bytes.IndexByte(rest, '>'); j >= 0 {
		return rest[:j]
	}
	return rest
}

// defaultObjectSize: de CSS-regel voor vervangen inhoud met alleen een
// verhouding — de grootste rechthoek met die verhouding binnen 300x150,
// verder gecapt op de beschikbare breedte.
func defaultObjectSize(rw, rh, avail int) (int, int) {
	w := 300
	h := rh * w / rw
	if h > 150 {
		h = 150
		w = rw * h / rh
	}
	if w > avail && avail > 0 {
		h, w = h*avail/w, avail
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// looksLikeSVG: is deze (afbeeldings)respons een SVG? Het content-type of
// gewoon de bron zelf zegt het.
func looksLikeSVG(ct string, data []byte) bool {
	if strings.Contains(ct, "svg") {
		return true
	}
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	return bytes.Contains(head, []byte("<svg"))
}

// svgFloat leest een svg-maatattribuut ("24", "1.5em", "32px"; procenten
// tellen niet — daar is geen basis voor).
func svgFloat(el *html.Node, name string) int {
	v, ok := attr(el, name)
	if !ok {
		return 0
	}
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	if strings.HasSuffix(v, "%") {
		return 0
	}
	if strings.HasSuffix(v, "em") {
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "em"), 64); err == nil && f > 0 {
			return int(f * 16)
		}
		return 0
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
		return int(f)
	}
	return 0
}

// svgRenderable: kan deze inline <svg> überhaupt een beeld worden — heeft
// hij een maat (viewBox of width+height) én eigen tekenwerk? Een svg die
// alleen een <use>-referentie is (het sprite-patroon: NRC's logo wijst
// naar een symbol elders in het document) kunnen we niet rasteren — dan
// is hij ook geen "inhoud", en mag het logo-slot hem blijven vervangen.
func svgRenderable(el *html.Node) bool {
	if !svgHasGraphic(el) {
		return false
	}
	if w, h := svgViewBox(el); w > 0 && h > 0 {
		return true
	}
	return svgFloat(el, "width") > 0 && svgFloat(el, "height") > 0
}

// svgHasGraphic: staat er echt tekenwerk in (paths, vormen), of alleen
// verwijzingen? Defs en symbolen tellen niet — die renderen per spec
// alléén via een <use> (anders zou een sprite-vel zelf een beeld worden).
func svgHasGraphic(el *html.Node) bool {
	found := false
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "defs", "symbol":
				return
			case "path", "rect", "circle", "ellipse", "polygon", "polyline", "line", "text", "image":
				found = true
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for c := el.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	return found
}

// svgViewBox geeft de viewBox-maat (0,0 zonder bruikbare viewBox).
func svgViewBox(el *html.Node) (int, int) {
	v, ok := attr(el, "viewbox")
	if !ok {
		v, ok = attr(el, "viewBox")
	}
	if !ok {
		return 0, 0
	}
	f := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' })
	if len(f) != 4 {
		return 0, 0
	}
	w, err1 := strconv.ParseFloat(f[2], 64)
	h, err2 := strconv.ParseFloat(f[3], 64)
	if err1 != nil || err2 != nil || w < 1 || h < 1 {
		return 0, 0
	}
	return int(w), int(h)
}

// inlineSVG rastert een inline <svg> op zijn plek in de flow: maat uit de
// CSS, anders de attributen, anders de viewBox (gecapt — een logo hoort
// in de regel te passen). Gerasterd op doelmaat: scherp, geen naschalen.
func (l *layouter) inlineSVG(el *html.Node, st style) {
	if l.svgN >= 24 {
		return // budget: geen icoontjes-lawine op bare metal
	}
	if !svgHasGraphic(el) {
		return // alleen <use>-referenties: daar valt niets te rasteren
	}
	cp := l.propsOf(el)
	if cp["display"] == "none" || cp["visibility"] == "hidden" || cp[srProp] == "1" {
		return // een verborgen sprite-vel (of icoon) is geen beeld
	}
	avail := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
	if avail < 8 {
		return
	}
	w, h := 0, 0
	if v, ok := cssLenPct(cp["width"], avail); ok && v > 0 {
		w = v
	} else if v := svgFloat(el, "width"); v > 0 {
		w = v
	}
	if v, ok := cssLen(cp["height"]); ok && v > 0 {
		h = v
	} else if v := svgFloat(el, "height"); v > 0 {
		h = v
	}
	vbW, vbH := svgViewBox(el)
	switch {
	case w > 0 && h > 0:
	case w > 0 && vbW > 0:
		h = vbH * w / vbW
	case h > 0 && vbH > 0:
		w = vbW * h / vbH
	case vbW > 0:
		// Alleen een viewBox: dat is een coördinatenstelsel, geen maat.
		// De CSS default object size geldt: de grootste rechthoek met deze
		// verhouding binnen 300x150 (en hij moet op de regel passen).
		w, h = defaultObjectSize(vbW, vbH, avail)
	default:
		return // geen enkele maat te bekennen
	}
	if w < 4 || h < 4 {
		return
	}
	var buf bytes.Buffer
	if html.Render(&buf, el) != nil {
		return
	}
	data := buf.Bytes()
	// fill/stroke: currentColor — "de kleur van hier": de computed color
	// van de svg zelf, anders de geërfde cascade-kleur. Alomtegenwoordig op
	// iconen; zonder invulling rastert oksvg ze zwart of helemaal niet.
	if bytes.Contains(data, []byte("currentColor")) || bytes.Contains(data, []byte("currentcolor")) {
		col := st.col
		if c, ok := cssColor(cp["color"]); ok {
			col = c
		}
		hex := []byte(hexCSS(col))
		data = bytes.ReplaceAll(data, []byte("currentColor"), hex)
		data = bytes.ReplaceAll(data, []byte("currentcolor"), hex)
	}
	m := rasterSVG(data, w, h)
	if m == nil {
		return
	}
	l.svgN++
	l.imageSized(m, w, h, st, false)
}

// --- <use>-symbolen ------------------------------------------------------------

// resolveUses lost svg <use>-referenties op vóór de layout: het symbool
// — document-intern (NRC's logo) of uit een externe sprite-sheet
// (tweakers' icons-symbol.svg, 217 symbolen) — wordt op de plek van de
// <use> ingelijmd en de omhullende <svg> erft zijn viewBox. Daarna is het
// gewoon een inline svg die de bestaande route rastert. Draait één keer
// per navigatie, in de nav-goroutine (de sheet-fetch mag blokkeren).
func (s *Session) resolveUses() {
	if s.doc == nil {
		return
	}
	var uses []*html.Node
	eachEl(s.doc, func(n *html.Node) {
		if n.Data == "use" && len(uses) < 64 {
			uses = append(uses, n)
		}
	})
	sheets := map[string]*html.Node{}
	fetched := 0
	for _, u := range uses {
		href, ok := attr(u, "href")
		if !ok {
			href, ok = attr(u, "xlink:href")
		}
		if !ok {
			continue
		}
		ref, id, found := strings.Cut(href, "#")
		if !found || id == "" {
			continue
		}
		root := s.doc
		if ref != "" {
			var seen bool
			if root, seen = sheets[ref]; !seen {
				root = nil
				// budget: een paar sheets per pagina; fetchText cap 256KB
				// (tweakers' sheet is 191KB — hij past).
				if fetched < 4 {
					fetched++
					if txt := s.fetchText(ref); strings.Contains(txt, "<svg") {
						if d, err := html.Parse(strings.NewReader(txt)); err == nil {
							root = d
						}
					}
				}
				sheets[ref] = root
			}
		}
		if target := findByID(root, id); target != nil {
			injectSymbol(u, target)
		}
	}
}

// findByID: het element met dit id, waar ook in de boom (nil = niet daar).
func findByID(root *html.Node, id string) *html.Node {
	if root == nil {
		return nil
	}
	var res *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if res != nil {
			return
		}
		if n.Type == html.ElementNode {
			if v, ok := attr(n, "id"); ok && v == id {
				res = n
				return
			}
		}
		for c := n.FirstChild; c != nil && res == nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return res
}

// injectSymbol vervangt de <use> door (een kloon van) zijn doel: van een
// <symbol>/<svg> de kínderen (die wikkels renderen zelf niet), van een
// los element (<g id>, <path id>) het element zelf. De omhullende <svg>
// zonder eigen viewBox krijgt die van het symbool — het symbool ís het
// coördinatenstelsel.
func injectSymbol(use, target *html.Node) {
	parent := use.Parent
	if parent == nil {
		return
	}
	if target.Data == "symbol" || target.Data == "svg" {
		for c := target.FirstChild; c != nil; c = c.NextSibling {
			parent.InsertBefore(cloneNode(c), use)
		}
	} else {
		parent.InsertBefore(cloneNode(target), use)
	}
	svg := parent
	for svg != nil && !(svg.Type == html.ElementNode && svg.Data == "svg") {
		svg = svg.Parent
	}
	parent.RemoveChild(use)
	if svg == nil {
		return
	}
	if w, h := svgViewBox(svg); w == 0 || h == 0 {
		if v, ok := attr(target, "viewbox"); ok {
			svg.Attr = append(svg.Attr, html.Attribute{Key: "viewBox", Val: v})
		} else if v, ok := attr(target, "viewBox"); ok {
			svg.Attr = append(svg.Attr, html.Attribute{Key: "viewBox", Val: v})
		}
	}
}

// cloneNode: een diepe kopie — de sheet blijft heel, elke <use> krijgt
// zijn eigen exemplaar.
func cloneNode(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, Data: n.Data, DataAtom: n.DataAtom, Namespace: n.Namespace}
	c.Attr = append([]html.Attribute(nil), n.Attr...)
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		c.AppendChild(cloneNode(k))
	}
	return c
}
