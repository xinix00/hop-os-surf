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
	"strings"

	"github.com/gost-dom/browser/dom"

	"github.com/xinix00/hop-os-surf/pixel"
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
	colBar    = color.RGBA{0x18, 0x22, 0x36, 0xFF}
	colBarTxt = color.RGBA{0xF0, 0xF4, 0xFF, 0xFF}
	colPage   = color.RGBA{0xFC, 0xFC, 0xF8, 0xFF}
	colText   = color.RGBA{0x20, 0x20, 0x24, 0xFF}
	colBold   = color.RGBA{0x00, 0x00, 0x00, 0xFF}
	colLink   = color.RGBA{0x1A, 0x4F, 0xC4, 0xFF}
	colCode   = color.RGBA{0x6A, 0x2A, 0x8A, 0xFF}
	colRule   = color.RGBA{0xB0, 0xB0, 0xB8, 0xFF}
	colErrBar = color.RGBA{0xFF, 0x8A, 0x7A, 0xFF} // fouttekst op de donkere statusbalk
)

const (
	pad   = 6 // paginamarge
	lead  = 4 // interlinie
	inset = 16 // inspringing per lijst/quote-niveau
)

// Box is één gelayoute tekstrun (of een <hr>-lijn) in documentcoördinaten:
// (0,0) is de top van de pagina, los van scroll en adresbalk.
type Box struct {
	R     image.Rectangle
	Text  string
	Scale int
	Col   color.RGBA
	Href  string // niet-leeg: klikbaar (nog onopgeloste href uit de pagina)
	Rule  bool   // <hr>: R vullen i.p.v. Text tekenen
}

// Page is het layout-resultaat voor één breedte; bij een resize opnieuw
// layouten (de WM bepaalt de maat, dus dit is de gewone gang van zaken).
type Page struct {
	Boxes  []Box
	Height int // documenthoogte in pixels (voor scroll-klemmen)
}

// style is de geërfde tekststijl tijdens de DOM-wandeling.
type style struct {
	scale  int
	col    color.RGBA
	href   string
	indent int
	pre    bool
}

type layouter struct {
	width int
	x, y  int // x=0 betekent: nog niets op deze regel
	lineH int
	boxes []Box
	space bool // er hoort witruimte vóór het volgende woord
	gap   int  // opgespaarde blokmarge (collapsing): pas toe bij het volgende woord
}

// Layout wandelt de DOM onder body en vouwt hem tot boxes voor deze
// paginabreedte. Onbekende elementen erven gewoon door — een pagina met
// <article> of <custom-tag> blijft leesbaar.
func Layout(body dom.Node, width int) Page {
	l := &layouter{width: width}
	if body != nil {
		l.walk(body, style{scale: 1, col: colText})
	}
	l.breakLine()
	return Page{Boxes: merge(l.boxes), Height: l.y}
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
		alt, _ := el.GetAttribute("alt")
		if alt = strings.TrimSpace(alt); alt == "" {
			alt = "img"
		}
		l.word("["+alt+"]", style{scale: st.scale, col: colRule, href: st.href, indent: st.indent})
		l.space = true
		return
	}

	switch tag {
	case "h1":
		st.scale, st.col = 3, colBold
	case "h2":
		st.scale, st.col = 2, colBold
	case "h3":
		st.scale, st.col = 2, colBold
	case "h4", "h5", "h6", "b", "strong", "th":
		st.col = colBold
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
	}

	if blocks[tag] {
		l.blockGap(blockMargin(tag, st.scale))
	}
	if tag == "li" {
		l.word("-", st)
		l.space = true
	}
	for c := el.FirstChild(); c != nil; c = c.NextSibling() {
		l.walk(c, st)
	}
	if blocks[tag] {
		l.blockGap(blockMargin(tag, st.scale))
	}
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
	})
	l.x = x + ww
	if h := 8 * st.scale; h > l.lineH {
		l.lineH = h
	}
	l.space = false
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

// breakLine sluit de huidige regel af (no-op op een lege regel).
func (l *layouter) breakLine() {
	if l.x == 0 {
		return
	}
	l.y += l.lineH + lead
	l.x, l.lineH = 0, 0
	l.space = false
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
			if !p.Rule && !b.Rule &&
				p.Scale == b.Scale && p.Col == b.Col && p.Href == b.Href &&
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
		pixel.DrawString(img, b.Min.X+bx.R.Min.X, top, bx.Scale, bx.Col, bx.Text)
		if bx.Href != "" {
			pixel.Fill(img, image.Rect(b.Min.X+bx.R.Min.X, bot, b.Min.X+bx.R.Max.X, bot+1), bx.Col)
		}
	}
	v.RenderBar(img)
	v.RenderStatus(img)
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

// Rune vertaalt een web-KVM-keyCode (plus shift-stand) naar een teken voor
// de adresbalk; 0 = geen teken (Enter/Backspace/Shift gaan buitenom).
func Rune(code uint32, shift bool) byte {
	switch {
	case code >= 'A' && code <= 'Z':
		if shift {
			return byte(code)
		}
		return byte(code) + 'a' - 'A'
	case code >= '0' && code <= '9':
		if shift {
			// US-layout: shift-cijfers die in URL's voorkomen.
			switch code {
			case '3':
				return '#'
			case '5':
				return '%'
			case '7':
				return '&'
			}
			return 0
		}
		return byte(code)
	case code >= 96 && code <= 105: // numpad
		return byte(code-96) + '0'
	}
	switch code {
	case 186: // ; / :
		if shift {
			return ':'
		}
		return ';'
	case 187: // = / +
		if shift {
			return '+'
		}
		return '='
	case 189: // - / _
		if shift {
			return '_'
		}
		return '-'
	case 190, 110: // . (en numpad-punt)
		return '.'
	case 191: // / / ?
		if shift {
			return '?'
		}
		return '/'
	case 222: // ' / "
		return '\''
	}
	return 0
}
