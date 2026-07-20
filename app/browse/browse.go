// Package browse is de browser achter cmd/browser: gost-dom levert de DOM
// (fetch + HTML-parsing, zonder rendering — het is een headless browser),
// dit pakket doet wat gost-dom bewust niet doet: layout en pixels. Eén
// flow-layout op het 8x8-font — blokken, woordwrap, koppen, links — genoeg
// om echte pagina's leesbaar te maken en links klikbaar. Los van main zodat
// de host-tests de hele keten (HTML → boxes → pixels → hit-test) kunnen
// draaien; net als calc/ is alleen de main tamago-only.
package browse

import (
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"

	"github.com/gost-dom/browser/dom"

	"github.com/xinix00/hop-os-surf/app/ui"
	"github.com/xinix00/hop-os-surf/stack/pixel"
)

// BarH is de hoogte van de adresbalk boven de pagina; StatusH die van de
// statusbalk eronder ("wat doet hij?" — laden, fouten, klaar).
const (
	BarH    = 20
	StatusH = 16
)

// Papier-look: pagina's zijn gemaakt voor zwart-op-wit; de chrome sluit aan
// bij het instrumentenpaneel van de rest van de desktop.
var (
	colBar      = color.RGBA{0x18, 0x22, 0x36, 0xFF}
	colBarTxt   = color.RGBA{0xF0, 0xF4, 0xFF, 0xFF}
	colPage     = color.RGBA{0xFC, 0xFC, 0xF8, 0xFF}
	colText     = color.RGBA{0x20, 0x20, 0x24, 0xFF}
	colBold     = color.RGBA{0x00, 0x00, 0x00, 0xFF}
	colLink     = color.RGBA{0x1A, 0x4F, 0xC4, 0xFF}
	colCode     = color.RGBA{0x6A, 0x2A, 0x8A, 0xFF}
	colRule     = color.RGBA{0xB0, 0xB0, 0xB8, 0xFF}
	colErrBar   = color.RGBA{0xFF, 0x8A, 0x7A, 0xFF} // fouttekst op de donkere statusbalk
	colScrTrack = color.RGBA{0xE4, 0xE4, 0xE0, 0xFF}
	colScrThumb = color.RGBA{0x8A, 0x96, 0xB0, 0xFF}
	colFieldBG  = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF} // invoerveld
	colBtnFace  = color.RGBA{0xE2, 0xE6, 0xEE, 0xFF} // knop
	colFocus    = color.RGBA{0x2D, 0x6C, 0xDF, 0xFF} // rand van het veld met focus
)

const (
	pad   = 6  // paginamarge
	lead  = 4  // interlinie
	inset = 16 // inspringing per lijst/quote-niveau
)

// Box is één gelayoute tekstrun, afbeelding of <hr>-lijn in document-
// coördinaten: (0,0) is de top van de pagina, los van scroll en adresbalk.
type Box struct {
	R      image.Rectangle
	Text   string
	Scale  int
	Col    color.RGBA
	Href   string      // niet-leeg: klikbaar (nog onopgeloste href uit de pagina)
	Rule   bool        // <hr>: R vullen i.p.v. Text tekenen
	Img    *image.RGBA // <img>: al geschaald naar R — teken i.p.v. Text
	Tile   *image.RGBA // background-image: herhaald over R (tegels — nooit een reuze-alloc)
	Bold   bool        // pseudo-vet (dubbelgetekend)
	BG     color.RGBA  // achtergrondvlak achter de run (of het blok)
	HasBG  bool
	Border color.RGBA // blokrand (kaarten, panelen)
	HasBrd bool
	Field  int // >0: invoerveld/knop — index+1 in Page.Fields
}

// Field is één formulierveld of -knop op de pagina. De waarde leeft in de
// Session (overleeft re-layouts); Submit=true is een knop.
type Field struct {
	R      image.Rectangle // klik-doel in documentcoördinaten
	Name   string
	Value  string
	Submit bool
	node   dom.Node // het <input>-element: sleutel voor Session.Type/Submit
}

// Page is het layout-resultaat voor één breedte; bij een resize opnieuw
// layouten (de WM bepaalt de maat, dus dit is de gewone gang van zaken).
type Page struct {
	Boxes  []Box
	Fields []Field
	Height int // documenthoogte in pixels (voor scroll-klemmen)
}

// style is de geërfde tekststijl tijdens de DOM-wandeling. CSS voedt
// dezelfde velden als de tag-defaults — de cascade ís deze struct.
type style struct {
	scale    int
	col      color.RGBA
	href     string
	indent   int
	pre      bool
	bold     bool // pseudo-vet: glyph dubbel getekend met 1px offset
	center   bool // text-align:center / <center>
	inline   bool // in een flex/inline-context: blokken breken hier niet
	blockify bool // direct kind van een grid/flex-kolom: word een blok (ook een <a>)
	bg       color.RGBA
	hasBG    bool
}

type layouter struct {
	width  int
	x, y   int // x=0 betekent: nog niets op deze regel
	lineH  int
	boxes  []Box
	fields []Field
	space  bool // er hoort witruimte vóór het volgende woord
	gap    int  // opgespaarde blokmarge (collapsing): pas toe bij het volgende woord
	imgs   map[string]image.Image
	styles map[dom.Node]props
	edits  map[dom.Node]string // door de gebruiker ingetikte veldwaarden
	line0  int                 // index van de eerste box op de huidige regel (voor centreren)
	center bool                // deze regel centreren bij breakLine
}

// Layout wandelt de DOM onder body en vouwt hem tot boxes voor deze
// paginabreedte. Onbekende elementen erven gewoon door — een pagina met
// <article> of <custom-tag> blijft leesbaar.
func Layout(body dom.Node, width int) Page {
	return LayoutWithImages(body, width, nil)
}

// LayoutWithImages is Layout met de opgehaalde afbeeldingen, gesleuteld op
// het rauwe src-attribuut (Session lost de URL's op en haalt ze binnen —
// layout blijft puur en synchroon). Een <img> zonder plaatje valt terug op
// zijn alt-tekst.
func LayoutWithImages(body dom.Node, width int, imgs map[string]image.Image) Page {
	return layoutStyled(body, width, imgs, nil, nil)
}

// layoutStyled is de volledige variant: mét de computed CSS-props uit
// Session.loadStyles en de ingetikte veldwaarden. Inline style=""-
// attributen werken altijd, ook zonder die map.
func layoutStyled(body dom.Node, width int, imgs map[string]image.Image, styles map[dom.Node]props, edits map[dom.Node]string) Page {
	l := &layouter{width: width, imgs: imgs, styles: styles, edits: edits}
	if body != nil {
		l.walk(body, style{scale: 1, col: colText})
	}
	l.breakLine()
	return Page{Boxes: merge(l.boxes), Fields: l.fields, Height: l.y}
}

// blocks: elementen die een eigen regel (en marge) afdwingen.
var blocks = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"div": true, "dl": true, "dt": true, "dd": true, "fieldset": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"li": true, "main": true, "nav": true, "ol": true, "p": true,
	"pre": true, "section": true, "table": true, "tr": true, "ul": true,
}

// skip: elementen zonder zichtbare inhoud.
var skip = map[string]bool{
	"script": true, "style": true, "head": true, "title": true,
	"meta": true, "link": true, "noscript": true, "template": true,
	"svg": true, "iframe": true, "object": true, "select": true,
}

func (l *layouter) walk(n dom.Node, st style) {
	switch n.NodeType() {
	case dom.NodeTypeText:
		txt, _ := n.NodeValue()
		if st.pre {
			l.preText(txt, st)
			return
		}
		if len(txt) > 0 && isSpace(txt[0]) {
			l.space = true
		}
		words := strings.Fields(txt)
		for _, w := range words {
			l.word(w, st)
			l.space = true
		}
		if len(words) > 0 && !isSpace(txt[len(txt)-1]) {
			l.space = false
		}
	case dom.NodeTypeElement:
		el, ok := n.(dom.Element)
		if !ok {
			return
		}
		l.element(el, st)
	case dom.NodeTypeDocument, dom.NodeTypeDocumentFragment:
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			l.walk(c, st)
		}
	}
}

func (l *layouter) element(el dom.Element, st style) {
	tag := strings.ToLower(el.TagName())
	if skip[tag] {
		return
	}
	if _, hidden := el.GetAttribute("hidden"); hidden {
		return
	}
	// Computed props (uit de stylesheets) + inline style="" (wint altijd).
	cp := l.styles[dom.Node(el)]
	if inline, ok := el.GetAttribute("style"); ok && inline != "" {
		if d := parseDecls(inline); d != nil {
			m := props{}
			for k, v := range cp {
				m[k] = v
			}
			for k, v := range d {
				m[k] = v
			}
			cp = m
		}
	}
	// display:none is de waardevolste property van allemaal: cookiebanners,
	// dichtgeklapte menu's en ander verborgen vuil verdwijnen echt.
	if cp["display"] == "none" || cp["visibility"] == "hidden" {
		return
	}
	if st.inline {
		// In een menu-context (flex/nav) hoort lucht tussen de items, ook
		// als de bron geen witruimte heeft ("</a><a>") — flex-gap, arm.
		l.space = true
	}
	switch tag {
	case "br":
		l.breakLine()
		return
	case "hr":
		l.breakLine()
		l.blockGap(lead)
		l.flushGap()
		l.boxes = append(l.boxes, Box{
			R: image.Rect(pad, l.y, l.width-pad, l.y+1), Col: colRule, Rule: true,
		})
		l.y++
		l.blockGap(lead)
		return
	case "img":
		if src, _ := el.GetAttribute("src"); l.imgs[src] != nil {
			l.image(l.imgs[src], st)
			return
		}
		alt, _ := el.GetAttribute("alt")
		if alt = strings.TrimSpace(alt); alt == "" {
			alt = "img"
		}
		l.word("["+alt+"]", style{scale: st.scale, col: colRule, href: st.href, indent: st.indent})
		l.space = true
		return
	case "input":
		l.input(el, st)
		return
	case "button":
		label := strings.TrimSpace(ascii(el.TextContent()))
		if label == "" {
			label, _ = el.GetAttribute("value")
		}
		l.widget(el, label, true, st)
		return
	case "textarea":
		val := el.TextContent()
		if v, ok := l.edits[dom.Node(el)]; ok {
			val = v
		}
		l.widget(el, val, false, st)
		return
	}

	switch tag {
	case "h1":
		st.scale, st.col, st.bold = 3, colBold, true
	case "h2":
		st.scale, st.col, st.bold = 2, colBold, true
	case "h3":
		st.scale, st.col, st.bold = 2, colBold, true
	case "h4", "h5", "h6", "b", "strong", "th":
		st.col, st.bold = colBold, true
	case "a":
		if href, ok := el.GetAttribute("href"); ok && href != "" {
			st.href, st.col = href, colLink
		}
	case "code", "kbd", "samp":
		st.col = colCode
	case "pre":
		st.pre, st.col = true, colCode
	case "ul", "ol", "blockquote", "dd":
		st.indent += inset
	case "center":
		st.center = true
	case "mark":
		st.bg, st.hasBG = namedColors["gold"], true
	case "font": // oud web: <font color="...">
		if v, ok := el.GetAttribute("color"); ok {
			if c, ok := cssColor(strings.ToLower(v)); ok {
				st.col = c
			}
		}
	}
	// Ook oud web: bgcolor-attribuut (tabellen, body's van vroeger).
	if v, ok := el.GetAttribute("bgcolor"); ok {
		if c, ok := cssColor(strings.ToLower(v)); ok {
			st.bg, st.hasBG = c, true
		}
	}

	// CSS over de tag-defaults heen (auteur wint van onze "UA-stylesheet").
	if v, ok := cp["color"]; ok {
		if c, ok := cssColor(v); ok {
			st.col = c
		}
	}
	if v, ok := cp["background-color"]; ok {
		if c, ok := cssColor(v); ok {
			st.bg, st.hasBG = c, true
		}
	}
	if v, ok := cp["font-weight"]; ok {
		if b, known := boldWeight(v); known {
			st.bold = b
		}
	}
	if v, ok := cp["font-size"]; ok {
		st.scale = fontScale(v, st.scale)
	}
	if v, ok := cp["text-align"]; ok {
		st.center = v == "center"
	}

	// "Divs goed zetten": display:inline(-block) haalt een element uit de
	// blok-flow, en de kinderen van een flex/grid-container komen náást
	// elkaar in plaats van onder elkaar — precies genoeg voor menu's,
	// zonder een echte layout-engine te worden.
	isBlock := (blocks[tag] || st.blockify) && !st.inline
	switch cp["display"] {
	case "inline", "inline-block", "inline-flex":
		isBlock = false
	case "block", "list-item":
		isBlock = !st.inline
	}
	// childInline: krijgen de kínderen een inline-context (menu), en
	// childBlockify: worden ze juist blokken (kaarten)? Flex-rij = menu;
	// grid en flex-kolom = blokken onder elkaar — gethops .doors-kaarten
	// stapelen dan net als bij hun eigen mobiele breakpoint, en een
	// <a class=door> wordt daarbij geblokkificeerd zoals in echte CSS.
	childInline := st.inline
	childBlockify := false
	switch cp["display"] {
	case "flex", "inline-flex":
		if fd := cp["flex-direction"]; fd == "column" || fd == "column-reverse" {
			childBlockify = true
		} else {
			childInline = true
		}
	case "grid":
		childBlockify = true
	}
	if tag == "nav" {
		// UA-vooroordeel: een <nav> ís vrijwel altijd een menu — leg hem
		// plat, ook zonder stylesheet (die staat vol properties die wij
		// toch niet dragen).
		childInline = true
	}

	// Een blok dat inline gezet is (flex-kind, display:inline-li) krijgt
	// lucht om zich heen in plaats van een regelbreuk.
	inlined := blocks[tag] && !isBlock

	// Border: uit de CSS (border/border-color); "none"/"0" is uit.
	var brdCol color.RGBA
	hasBrd := false
	if v, ok := cp["border"]; ok {
		brdCol, hasBrd = cssBorder(v)
	}
	if v, ok := cp["border-color"]; ok {
		if c, ok := cssColor(v); ok {
			brdCol, hasBrd = c, true
		}
	}

	// Blok-achtergrond en/of -rand: één vlak (of tegelpatroon) achter het
	// hele blok — body-achtergrond wordt zo vanzelf de paginakleur. Het
	// vlak gaat als placeholder de boxlijst in (paint-volgorde: onder de
	// inhoud) en krijgt zijn rechthoek als de blokhoogte bekend is. Een
	// gekaderd blok (kaart) krijgt binnenmarge, anders plakt de tekst aan
	// de rand.
	bgIdx := -1
	var bgY0, bgX0 int
	if tile := l.imgs[cssURL(cp["background-image"])]; (isBlock || tag == "body") && (st.hasBG || tile != nil || hasBrd) {
		l.breakLine()
		l.flushGap()
		bgIdx = len(l.boxes)
		box := Box{BG: st.bg, HasBG: st.hasBG, Border: brdCol, HasBrd: hasBrd}
		if tile != nil {
			w, h := tile.Bounds().Dx(), tile.Bounds().Dy()
			if w > 0 && h > 0 && w <= imgMaxDim && h <= imgMaxDim {
				box.Tile = scaleTo(tile, w, h) // één RGBA-tegel, nooit een reuze-alloc
			}
		}
		l.boxes = append(l.boxes, box)
		bgY0 = l.y
		bgX0 = pad + st.indent - 2
		if tag == "body" {
			bgX0 = 0
		} else {
			l.y += 4 // binnenmarge boven
			st.indent += 6
		}
		st.hasBG = false // de kinderen liggen al óp het vlak: geen run-vulling meer nodig
	}

	childSt := st
	childSt.inline = childInline
	childSt.blockify = childBlockify

	if isBlock {
		l.blockGap(blockMargin(tag, st.scale))
	}
	if tag == "li" && isBlock {
		l.word("-", st)
		l.space = true
	}
	if inlined {
		l.space = true
	}
	for c := el.FirstChild(); c != nil; c = c.NextSibling() {
		l.walk(c, childSt)
	}
	if inlined {
		l.space = true
	}
	if isBlock {
		l.blockGap(blockMargin(tag, st.scale))
	}
	if bgIdx >= 0 {
		l.breakLine()
		x1 := l.width - pad + 2
		if tag == "body" {
			x1 = l.width
		} else {
			l.y += 4 // binnenmarge onder
		}
		// Verticaal exact de blokgrenzen: de binnenmarge zit er al in, en
		// ±2 zou aangrenzende kaarten laten overlappen.
		l.boxes[bgIdx].R = image.Rect(bgX0, bgY0, x1, l.y)
	}
}

// input legt één <input> in de flow; hidden doet niet mee, knoppen en
// tekstvelden worden widgets, checkbox/radio (v0) een kaal vinkje.
func (l *layouter) input(el dom.Element, st style) {
	typ, _ := el.GetAttribute("type")
	typ = strings.ToLower(strings.TrimSpace(typ))
	val, _ := el.GetAttribute("value")
	if v, ok := l.edits[dom.Node(el)]; ok {
		val = v
	}
	switch typ {
	case "hidden":
		return
	case "submit", "button", "reset":
		if val == "" {
			val = "OK"
		}
		l.widget(el, val, true, st)
	case "checkbox", "radio":
		mark := "[ ]"
		if _, ok := el.GetAttribute("checked"); ok {
			mark = "[x]"
		}
		l.word(mark, st) // tonen wel, togglen (nog) niet
		l.space = true
	default: // text, search, email, url, ...
		l.widget(el, val, false, st)
	}
}

// widget plaatst een invoerveld of knop als box in de flow en registreert
// hem als Field (het klik/tik-doel). Veldbreedte volgt het size-attribuut
// (default 20 tekens), knopbreedte het label.
func (l *layouter) widget(el dom.Element, val string, submit bool, st style) {
	l.flushGap()
	chars := 20
	if submit {
		chars = len(val) + 2
	} else if v, ok := el.GetAttribute("size"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			chars = n
		}
	}
	w := chars*8*st.scale + 8
	if max := l.width - 2*pad - st.indent; w > max {
		w = max
	}
	h := 8*st.scale + 8
	sp := 0
	if l.space && l.x > 0 {
		sp = 8 * st.scale
	}
	if l.x > 0 && l.x+sp+w > l.width-pad {
		l.breakLine()
		sp = 0
	}
	if l.x == 0 {
		l.x = pad + st.indent
	}
	x := l.x + sp
	r := image.Rect(x, l.y, x+w, l.y+h)
	name, _ := el.GetAttribute("name")
	l.fields = append(l.fields, Field{R: r, Name: name, Value: val, Submit: submit, node: dom.Node(el)})
	l.boxes = append(l.boxes, Box{R: r, Scale: st.scale, Field: len(l.fields)})
	l.x = x + w
	if h > l.lineH {
		l.lineH = h
	}
	l.space = false
	l.center = l.center || st.center
}

// blockMargin: koppen krijgen lucht naar rato van hun maat, lijstitems
// alleen een regelbreuk.
func blockMargin(tag string, scale int) int {
	switch tag {
	case "li", "dt", "dd", "tr":
		return 0
	default:
		return 3 * scale
	}
}

// ascii vouwt tekst naar het 8x8-font (ASCII): typografische tekens naar
// hun ASCII-neef, elk ander niet-ASCII-teken naar één '?' — zonder dit
// werd een em-dash drie '?'-en (één per UTF-8-byte).
func ascii(s string) string {
	i := 0
	for ; i < len(s); i++ {
		if s[i] >= 0x80 {
			break
		}
	}
	if i == len(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:i])
	for _, r := range s[i:] {
		switch r {
		case '–', '—', '−': // – — −
			b.WriteByte('-')
		case '‘', '’': // ‘ ’
			b.WriteByte('\'')
		case '“', '”': // “ ”
			b.WriteByte('"')
		case ' ':
			b.WriteByte(' ')
		case '•', '·': // • ·
			b.WriteByte('-')
		case '…': // …
			b.WriteString("...")
		case '×': // ×
			b.WriteByte('x')
		case '©': // ©
			b.WriteString("(c)")
		case '→': // →
			b.WriteString("->")
		default:
			if r < 0x80 {
				b.WriteRune(r)
			} else {
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}

// word plaatst één woord, met wrap op de paginabreedte.
func (l *layouter) word(w string, st style) {
	w = ascii(w)
	l.flushGap()
	ww := pixel.StringWidth(w, st.scale)
	sp := 0
	if l.space && l.x > 0 {
		sp = 8 * st.scale
	}
	if l.x > 0 && l.x+sp+ww > l.width-pad {
		l.breakLine()
		sp = 0
	}
	if l.x == 0 {
		l.x = pad + st.indent
	}
	x := l.x + sp
	l.boxes = append(l.boxes, Box{
		R:     image.Rect(x, l.y, x+ww, l.y+8*st.scale),
		Text:  w,
		Scale: st.scale,
		Col:   st.col,
		Href:  st.href,
		Bold:  st.bold,
		BG:    st.bg,
		HasBG: st.hasBG,
	})
	l.x = x + ww
	if h := 8 * st.scale; h > l.lineH {
		l.lineH = h
	}
	l.space = false
	l.center = l.center || st.center
}

// image plaatst een afbeelding in de flow, als een (groot) woord: past hij
// nog op de regel dan inline, anders op een nieuwe. Breder dan de pagina →
// proportioneel verkleind; het schalen gebeurt hier (één keer per layout),
// renderen is daarna een kale draw.Draw.
func (l *layouter) image(m image.Image, st style) {
	l.flushGap()
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	if w < 1 || h < 1 {
		return
	}
	maxW := l.width - 2*pad - st.indent
	if maxW < 8 {
		maxW = 8
	}
	if w > maxW {
		h = h * maxW / w
		if h < 1 {
			h = 1
		}
		w = maxW
	}
	sp := 0
	if l.space && l.x > 0 {
		sp = 8 * st.scale
	}
	if l.x > 0 && l.x+sp+w > l.width-pad {
		l.breakLine()
		sp = 0
	}
	if l.x == 0 {
		l.x = pad + st.indent
	}
	x := l.x + sp
	l.boxes = append(l.boxes, Box{
		R:    image.Rect(x, l.y, x+w, l.y+h),
		Href: st.href,
		Img:  scaleTo(m, w, h),
	})
	l.x = x + w
	if h > l.lineH {
		l.lineH = h
	}
	l.space = false
	l.center = l.center || st.center
}

// scaleTo schaalt src naar w×h met nearest-neighbor: geen extra dependency,
// en op het 8x8-font-scherm is zachte interpolatie toch niet te zien.
func scaleTo(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	for y := 0; y < h; y++ {
		sy := sb.Min.Y + y*sb.Dy()/h
		for x := 0; x < w; x++ {
			dst.Set(x, y, src.At(sb.Min.X+x*sb.Dx()/w, sy))
		}
	}
	return dst
}

// preText behoudt regels en spaties; te lange regels lopen het beeld uit
// (geen wrap — zo doet een terminal het ook).
func (l *layouter) preText(txt string, st style) {
	for i, line := range strings.Split(strings.ReplaceAll(ascii(txt), "\t", "    "), "\n") {
		if i > 0 {
			l.breakLine()
		}
		line = strings.TrimRight(line, " \r")
		if line == "" {
			continue
		}
		l.flushGap()
		if l.x == 0 {
			l.x = pad + st.indent
		}
		ww := pixel.StringWidth(line, st.scale)
		l.boxes = append(l.boxes, Box{
			R:     image.Rect(l.x, l.y, l.x+ww, l.y+8*st.scale),
			Text:  line,
			Scale: st.scale,
			Col:   st.col,
			Href:  st.href,
		})
		l.x += ww
		if h := 8 * st.scale; h > l.lineH {
			l.lineH = h
		}
	}
}

// breakLine sluit de huidige regel af (no-op op een lege regel) en
// centreert hem als er gecentreerde content op stond — centreren kán pas
// hier, als de regelbreedte bekend is.
func (l *layouter) breakLine() {
	if l.x == 0 {
		return
	}
	if l.center {
		if shift := (l.width - pad - l.x) / 2; shift > 0 {
			for i := l.line0; i < len(l.boxes); i++ {
				if l.boxes[i].R.Min.Y == l.y { // <hr> e.d. niet meeschuiven
					l.boxes[i].R = l.boxes[i].R.Add(image.Pt(shift, 0))
				}
			}
		}
	}
	l.y += l.lineH + lead
	l.x, l.lineH = 0, 0
	l.space = false
	l.line0 = len(l.boxes)
	l.center = false
}

// blockGap vraagt om verticale marge; opeenvolgende blokken delen de
// grootste (margin collapsing, het arme-mans-model).
func (l *layouter) blockGap(g int) {
	l.breakLine()
	if g > l.gap {
		l.gap = g
	}
}

func (l *layouter) flushGap() {
	if l.gap > 0 {
		if l.y > 0 { // geen marge boven het allereerste blok
			l.y += l.gap
		}
		l.gap = 0
	}
}

// merge plakt woorden die op dezelfde regel met dezelfde stijl precies één
// spatie uit elkaar staan aan elkaar: minder boxes, minder DrawString-werk.
func merge(in []Box) []Box {
	out := in[:0]
	for _, b := range in {
		if n := len(out); n > 0 {
			p := &out[n-1]
			if !p.Rule && !b.Rule && p.Img == nil && b.Img == nil &&
				p.Field == 0 && b.Field == 0 && p.Tile == nil && b.Tile == nil &&
				p.Scale == b.Scale && p.Col == b.Col && p.Href == b.Href &&
				p.Bold == b.Bold && p.HasBG == b.HasBG && p.BG == b.BG &&
				p.R.Min.Y == b.R.Min.Y && p.R.Max.X+8*p.Scale == b.R.Min.X {
				p.Text += " " + b.Text
				p.R.Max.X = b.R.Max.X
				continue
			}
		}
		out = append(out, b)
	}
	return out
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// --- view: chrome + scroll + hit-test --------------------------------------

// View is de zichtbare toestand van het browserwindow: adresbalk, de
// gelayoute pagina, de scrollpositie en de statusregel. main houdt er één
// bij en rendert hem na elk event.
type View struct {
	Addr   string // adresbalk-inhoud (bewerkt door toetsen)
	Status string // statusbalk onderin: "go …", een fout, "" = niets
	Err    bool   // Status is een fout — kleur hem als zodanig
	Page   Page
	Scroll int
	Focus  int // >0: Page.Fields[Focus-1] heeft de toetsen; 0 = de adresbalk
}

// Focused geeft het veld met focus, of nil.
func (v *View) Focused() *Field {
	if v.Focus > 0 && v.Focus <= len(v.Page.Fields) {
		return &v.Page.Fields[v.Focus-1]
	}
	return nil
}

// Render tekent adresbalk + pagina + statusbalk over het hele beeld. De
// balken gaan als laatste over de content heen — dat ís de clipping: op de
// statusbalk rendert nooit pagina-inhoud.
func (v *View) Render(img *image.RGBA) {
	b := img.Bounds()
	pixel.Fill(img, b, colPage)
	y0 := b.Min.Y + BarH
	for _, bx := range v.Page.Boxes {
		top := y0 + bx.R.Min.Y - v.Scroll
		bot := y0 + bx.R.Max.Y - v.Scroll
		if bot <= y0 || top >= b.Max.Y {
			continue
		}
		if bx.Rule {
			pixel.Fill(img, image.Rect(b.Min.X+bx.R.Min.X, top, b.Min.X+bx.R.Max.X, bot), bx.Col)
			continue
		}
		if bx.Img != nil {
			// Over, niet Src: PNG-transparantie hoort het paginawit te tonen.
			r := image.Rect(b.Min.X+bx.R.Min.X, top, b.Min.X+bx.R.Max.X, bot)
			draw.Draw(img, r, bx.Img, bx.Img.Bounds().Min, draw.Over)
			continue
		}
		if bx.Field > 0 {
			v.renderField(img, &bx, top, bot)
			continue
		}
		if bx.Tile != nil || bx.HasBrd {
			// Blok-achtergrond: kleur, dan tegelpatroon, dan de rand.
			r := image.Rect(b.Min.X+bx.R.Min.X, top, b.Min.X+bx.R.Max.X, bot)
			if bx.HasBG {
				pixel.Fill(img, r, bx.BG)
			}
			if bx.Tile != nil {
				tw, th := bx.Tile.Bounds().Dx(), bx.Tile.Bounds().Dy()
				for ty := r.Min.Y; ty < r.Max.Y; ty += th {
					for tx := r.Min.X; tx < r.Max.X; tx += tw {
						dst := image.Rect(tx, ty, tx+tw, ty+th).Intersect(r)
						draw.Draw(img, dst, bx.Tile, bx.Tile.Bounds().Min, draw.Over)
					}
				}
			}
			if bx.HasBrd {
				// Niet clippen naar het beeld: SetRGBA buiten beeld is al
				// een no-op, en clippen zou valse randen op de snijlijn
				// tekenen bij een half-zichtbaar blok.
				pixel.Outline(img, r, bx.Border)
			}
			continue
		}
		if bx.HasBG {
			// 1px lucht rondom: leest prettiger en dekt de spatie in een
			// samengevoegde run.
			pixel.Fill(img, image.Rect(b.Min.X+bx.R.Min.X-1, top-1, b.Min.X+bx.R.Max.X+1, bot+1), bx.BG)
		}
		pixel.DrawString(img, b.Min.X+bx.R.Min.X, top, bx.Scale, bx.Col, bx.Text)
		if bx.Bold {
			// Pseudo-vet: het 8x8-font heeft geen gewichten — dubbel
			// tekenen met 1px offset is er verrassend dichtbij.
			pixel.DrawString(img, b.Min.X+bx.R.Min.X+1, top, bx.Scale, bx.Col, bx.Text)
		}
		if bx.Href != "" {
			pixel.Fill(img, image.Rect(b.Min.X+bx.R.Min.X, bot, b.Min.X+bx.R.Max.X, bot+1), bx.Col)
		}
	}
	v.RenderBar(img)
	v.RenderStatus(img)
	v.renderScrollbar(img)
}

// renderScrollbar tekent een smalle positie-indicator aan de rechterrand —
// alleen als de pagina langer is dan de viewport. Geen klik-doel (v0),
// puur "waar ben ik": scrollen gaat met wiel of toetsen.
func (v *View) renderScrollbar(img *image.RGBA) {
	b := img.Bounds()
	viewH := b.Dy() - BarH - StatusH
	if v.Page.Height <= viewH || viewH < 16 {
		return
	}
	top, bot := b.Min.Y+BarH, b.Max.Y-StatusH
	thumbH := viewH * viewH / v.Page.Height
	if thumbH < 8 {
		thumbH = 8
	}
	y := top + (viewH-thumbH)*v.Scroll/(v.Page.Height-viewH)
	pixel.Fill(img, image.Rect(b.Max.X-4, top, b.Max.X, bot), colScrTrack)
	pixel.Fill(img, image.Rect(b.Max.X-4, y, b.Max.X, y+thumbH), colScrThumb)
}

// renderField tekent één invoerveld of knop (bx.Field is 1-based).
func (v *View) renderField(img *image.RGBA, bx *Box, top, bot int) {
	f := &v.Page.Fields[bx.Field-1]
	b := img.Bounds()
	r := image.Rect(b.Min.X+bx.R.Min.X, top, b.Min.X+bx.R.Max.X, bot)
	face, edge := colFieldBG, colRule
	if f.Submit {
		face = colBtnFace
	}
	if v.Focus == bx.Field {
		edge = colFocus
	}
	pixel.Fill(img, r, face)
	pixel.Outline(img, r, edge)
	txt := ascii(f.Value)
	if v.Focus == bx.Field && !f.Submit {
		txt += "_"
	}
	if max := (r.Dx() - 8) / (8 * bx.Scale); len(txt) > max && max > 0 {
		txt = txt[len(txt)-max:] // het einde in beeld: daar wordt getikt
	}
	if f.Submit {
		pixel.DrawStringCentered(img, r, bx.Scale, colText, txt)
	} else {
		pixel.DrawString(img, r.Min.X+4, r.Min.Y+(r.Dy()-8*bx.Scale)/2, bx.Scale, colText, txt)
	}
}

// RenderBar tekent alléén de adresbalk (voor het tik-pad: een strook van
// een paar KB damage per toets in plaats van een vol frame).
func (v *View) RenderBar(img *image.RGBA) {
	b := img.Bounds()
	bar := image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+BarH)
	pixel.Fill(img, bar, colBar)
	txt := v.Addr + "_"
	// Houd het einde in beeld: daar wordt getypt.
	if max := (b.Dx() - 2*pad) / 8; len(txt) > max && max > 0 {
		txt = txt[len(txt)-max:]
	}
	pixel.DrawString(img, b.Min.X+pad, b.Min.Y+(BarH-8)/2, 1, colBarTxt, txt)
}

// RenderStatus tekent alléén de statusbalk onderin (voor het laad-pad:
// partiële damage — de pagina eronder blijft staan).
func (v *View) RenderStatus(img *image.RGBA) {
	r := v.StatusRect(img)
	pixel.Fill(img, r, colBar)
	txt := v.Status
	if max := (r.Dx() - 2*pad) / 8; len(txt) > max && max > 0 {
		txt = txt[:max] // begin in beeld houden: daar staat wát hij doet
	}
	col := colBarTxt
	if v.Err {
		col = colErrBar
	}
	pixel.DrawString(img, r.Min.X+pad, r.Min.Y+(StatusH-8)/2, 1, col, txt)
}

// Bar is de adresbalk-rechthoek in beeldcoördinaten (voor partiële Present).
func (v *View) Bar(img *image.RGBA) image.Rectangle {
	b := img.Bounds()
	return image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+BarH)
}

// StatusRect is de statusbalk-rechthoek in beeldcoördinaten.
func (v *View) StatusRect(img *image.RGBA) image.Rectangle {
	b := img.Bounds()
	return image.Rect(b.Min.X, b.Max.Y-StatusH, b.Max.X, b.Max.Y)
}

// Hit vertaalt een klik (window-lokale coördinaten, viewH = windowhoogte)
// naar de href van de link eronder; "" als daar geen link is. Kliks op de
// adres- en statusbalk zijn nooit een link.
func (v *View) Hit(x, y, viewH int) string {
	if y < BarH || y >= viewH-StatusH {
		return ""
	}
	p := image.Pt(x, y-BarH+v.Scroll)
	for _, bx := range v.Page.Boxes {
		if bx.Href != "" && p.In(bx.R) {
			return bx.Href
		}
	}
	return ""
}

// HitField geeft het veld (1-based, voor View.Focus) onder een klik; 0 als
// daar geen veld is.
func (v *View) HitField(x, y, viewH int) int {
	if y < BarH || y >= viewH-StatusH {
		return 0
	}
	p := image.Pt(x, y-BarH+v.Scroll)
	for i := range v.Page.Fields {
		if p.In(v.Page.Fields[i].R) {
			return i + 1
		}
	}
	return 0
}

// ScrollBy verschuift en klemt de scrollpositie voor deze viewporthoogte;
// geeft terug of er iets veranderde (zo niet: niet hertekenen).
func (v *View) ScrollBy(delta, viewH int) bool {
	max := v.Page.Height - (viewH - BarH - StatusH)
	if max < 0 {
		max = 0
	}
	s := v.Scroll + delta
	if s < 0 {
		s = 0
	}
	if s > max {
		s = max
	}
	if s == v.Scroll {
		return false
	}
	v.Scroll = s
	return true
}

// --- toetsen ----------------------------------------------------------------

// Rune vertaalt een web-KVM-keyCode naar een teken voor de adresbalk.
// Woont sinds de basis-toolset in ui (elke typende app dezelfde vertaling);
// deze naam blijft omdat hij bij de adresbalk hoort.
func Rune(code uint32, shift bool) byte { return ui.Rune(code, shift) }
