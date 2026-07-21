// Package browse is de browser achter cmd/browser: x/net/html levert de
// DOM (de WHATWG-parser), cascadia de selectors, de Session het netwerk —
// dit pakket doet de rest: layout en pixels. Eén flow-layout op het
// Spleen-font — blokken, woordwrap, koppen, links, floats, gepinde
// headers — genoeg om echte pagina's leesbaar te maken en links klikbaar.
// Los van main zodat de host-tests de hele keten (HTML → boxes → pixels →
// hit-test) kunnen draaien; net als calc/ is alleen de main tamago-only.
package browse

import (
	"image"
	"image/color"
	"image/draw"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/xinix00/hop-os-surf/app/ui"
	"github.com/xinix00/hop-os-surf/stack/pixel"
)

// BarH is de hoogte van de adresbalk boven de pagina; StatusH die van de
// statusbalk eronder ("wat doet hij?" — laden, fouten, klaar).
const (
	BarH    = 24
	StatusH = 18
)

// faceFor kiest het Spleen-font voor een layout-schaal: 1 = lopende tekst
// (6x12), 2 = koppen (8x16), 3+ = h1 (8x16 op 2x). charW/charH zijn de
// celmaten waarop de hele layout rekent (voorheen het 8x8-grid).
func faceFor(scale int) (pixel.Face, int) {
	switch {
	case scale <= 1:
		return pixel.F12, 1
	case scale == 2:
		return pixel.F16, 1
	default:
		return pixel.F16, 2
	}
}

func charW(scale int) int           { f, s := faceFor(scale); return f.W * s }
func charH(scale int) int           { f, s := faceFor(scale); return f.H * s }
func textW(t string, scale int) int { return len(t) * charW(scale) }

func drawTxt(img *image.RGBA, x, y, scale int, col color.RGBA, t string) {
	f, s := faceFor(scale)
	pixel.DrawText(img, x, y, f, s, col, t)
}

func drawTxtCentered(img *image.RGBA, r image.Rectangle, scale int, col color.RGBA, t string) {
	f, s := faceFor(scale)
	pixel.DrawTextCentered(img, r, f, s, col, t)
}

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
	Field  int  // >0: invoerveld/knop — index+1 in Page.Fields
	Pin    bool // hoort bij de gepinde header (zie Page.PinY0/PinY1)
}

// Field is één formulierveld of -knop op de pagina. De waarde leeft in de
// Session (overleeft re-layouts); Submit=true is een knop.
type Field struct {
	R      image.Rectangle // klik-doel in documentcoördinaten
	Name   string
	Value  string
	Submit bool
	node   *html.Node // het <input>-element: sleutel voor Session.Type/Submit
}

// Page is het layout-resultaat voor één breedte; bij een resize opnieuw
// layouten (de WM bepaalt de maat, dus dit is de gewone gang van zaken).
type Page struct {
	Boxes  []Box
	Fields []Field
	Height int        // documenthoogte in pixels (voor scroll-klemmen)
	BG     color.RGBA // paginacanvas (body-achtergrond); HasBG=false → papierwit
	HasBG  bool
	// De gepinde header (position:fixed/sticky aan de bovenrand): de boxes
	// met Pin=true beslaan documentregels [PinY0, PinY1); voorbij PinY0
	// gescrold tekent View ze bovenin, zoals de site het vraagt.
	PinY0, PinY1 int
}

// Pinned: is er een header om vast te houden?
func (p *Page) Pinned() bool { return p.PinY1 > p.PinY0 }

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
	rIndent  int  // inspringing vanaf rechts (marges/padding van blokken)
	bg       color.RGBA
	hasBG    bool
	on       color.RGBA // effectieve achtergrond ónder de tekst (contrastbewaking)
	hasOn    bool
}

// flt is één actieve float: een afbeelding aan de kant waar de tekst langs
// stroomt. w is de opgeëiste breedte (incl. marge), bot de onderkant in
// documentcoördinaten, depth de blokdiepte waarop hij ontstond (voor het
// impliciete clearen als dat blok sluit).
type flt struct {
	w, bot, depth int
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
	styles map[*html.Node]props
	edits  map[*html.Node]string // door de gebruiker ingetikte veldwaarden
	line0  int                   // index van de eerste box op de huidige regel (voor centreren)
	center bool                  // deze regel centreren bij breakLine
	fL, fR flt                   // actieve floats links en rechts
	depth  int                   // blokdiepte tijdens de wandeling

	pageBG    color.RGBA // body-achtergrond: het paginacanvas (Page.BG)
	hasPageBG bool
	pin       pinState

	origins  []image.Point // gepositioneerde voorouders (containing blocks)
	late     []Box         // absolute boxes: geschilderd ná de flow (erbovenop)
	absEl    *html.Node    // absolute() legt dit element zelf — geen recursie
	icon     image.Image   // site-icoon (apple-touch-icon) voor het logo-slot
	iconUsed bool          // één logo-slot per pagina: het eerste (de header)
}

// pinState volgt de header die gepind gaat worden: tussen beginPin en
// endPin krijgen nieuwe boxes Pin=true, en de y-range wordt Page.PinY0/1.
type pinState struct {
	active, done bool
	box0, y0, y1 int
}

// beginPin: dit element vraagt fixed/sticky aan de bovenrand — pinnen als
// het ook écht bovenin de pagina ligt (een modal halverwege is geen
// header). Eén header per pagina; de eerste wint.
func (l *layouter) beginPin(cp props) bool {
	if l.pin.active || l.pin.done {
		return false
	}
	if v, ok := cssLen(cp["top"]); cp["top"] != "" && (!ok || v > 8) {
		return false // niet tegen de bovenrand geplakt
	}
	l.breakLine()
	l.flushGap()
	if l.y > 300 {
		return false
	}
	l.pin = pinState{active: true, box0: len(l.boxes), y0: l.y}
	return true
}

// endPin sluit de header af; te hoog (een fixed paneel of modal) betekent:
// toch niet pinnen — dan scrollt hij gewoon mee.
func (l *layouter) endPin() {
	l.breakLine()
	h := l.y - l.pin.y0
	if h > 0 && h <= 120 && len(l.boxes) > l.pin.box0 {
		for i := l.pin.box0; i < len(l.boxes); i++ {
			l.boxes[i].Pin = true
		}
		l.pin.y1, l.pin.done = l.y, true
	}
	l.pin.active = false
}

// lineLeft/lineRight: de regelgrenzen op de huidige y, mét de actieve
// floats — tekst stroomt er vanzelf langs en valt eronder weer breeduit.
func (l *layouter) lineLeft(indent int) int {
	x := pad + indent
	if l.fL.w > 0 && l.y < l.fL.bot {
		x += l.fL.w
	}
	return x
}

func (l *layouter) lineRight(rIndent int) int {
	r := l.width - pad - rIndent
	if l.fR.w > 0 && l.y < l.fR.bot {
		r -= l.fR.w
	}
	return r
}

// Layout wandelt de DOM onder body en vouwt hem tot boxes voor deze
// paginabreedte. Onbekende elementen erven gewoon door — een pagina met
// <article> of <custom-tag> blijft leesbaar.
func Layout(body *html.Node, width int) Page {
	return LayoutWithImages(body, width, nil)
}

// LayoutWithImages is Layout met de opgehaalde afbeeldingen, gesleuteld op
// het rauwe src-attribuut (Session lost de URL's op en haalt ze binnen —
// layout blijft puur en synchroon). Een <img> zonder plaatje valt terug op
// zijn alt-tekst.
func LayoutWithImages(body *html.Node, width int, imgs map[string]image.Image) Page {
	return layoutStyled(body, width, imgs, nil, nil, nil)
}

// layoutStyled is de volledige variant: mét de computed CSS-props uit
// Session.stylesFor en de ingetikte veldwaarden. Inline style=""-
// attributen werken altijd, ook zonder die map.
func layoutStyled(body *html.Node, width int, imgs map[string]image.Image, styles map[*html.Node]props, edits map[*html.Node]string, icon image.Image) Page {
	l := &layouter{width: width, imgs: imgs, styles: styles, edits: edits, icon: icon}
	if body != nil {
		l.walk(body, style{scale: 1, col: colText})
	}
	l.breakLine()
	p := Page{Boxes: merge(l.boxes), Fields: l.fields, Height: l.y, BG: l.pageBG, HasBG: l.hasPageBG}
	// Absolute boxes schilderen bovenop de flow (paint-volgorde: laatst).
	for _, b := range l.late {
		p.Boxes = append(p.Boxes, b)
		if b.R.Max.Y > p.Height {
			p.Height = b.R.Max.Y
		}
	}
	if l.pin.done {
		p.PinY0, p.PinY1 = l.pin.y0, l.pin.y1
	}
	return p
}

// luma: waargenomen helderheid 0..255 (ITU-R BT.601) — kiest tekstkleuren
// bij de themakleur en bewaakt het contrast op gekleurde vlakken.
func luma(c color.RGBA) int {
	return (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// uaProps is onze user-agent-stylesheet, in dezelfde taal als de site-CSS:
// berekende waarden die ónder de author-props gemerged worden. Een h1 is
// dus geen speciaal geval — hij heeft alleen defaults, en de site wint.
var uaProps = map[string]props{
	"h1":         {"font-size": "32px", "font-weight": "bold", "color": "#000000"},
	"h2":         {"font-size": "24px", "font-weight": "bold", "color": "#000000"},
	"h3":         {"font-size": "20px", "font-weight": "bold", "color": "#000000"},
	"h4":         {"font-weight": "bold", "color": "#000000"},
	"h5":         {"font-weight": "bold", "color": "#000000"},
	"h6":         {"font-weight": "bold", "color": "#000000"},
	"b":          {"font-weight": "bold"},
	"strong":     {"font-weight": "bold"},
	"th":         {"font-weight": "bold"},
	"code":       {"color": "#6a2a8a"},
	"kbd":        {"color": "#6a2a8a"},
	"samp":       {"color": "#6a2a8a"},
	"pre":        {"color": "#6a2a8a", "white-space": "pre"},
	"mark":       {"background-color": "gold"},
	"center":     {"text-align": "center"},
	"ul":         {"padding-left": "16px"},
	"ol":         {"padding-left": "16px"},
	"blockquote": {"padding-left": "16px"},
	"dd":         {"padding-left": "16px"},
	"button":     {"background-color": "#e2e6ee"},
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

func (l *layouter) walk(n *html.Node, st style) {
	switch n.Type {
	case html.TextNode:
		txt := n.Data
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
	case html.ElementNode:
		l.element(n, st)
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			l.walk(c, st)
		}
	}
}

func (l *layouter) element(el *html.Node, st style) {
	tag := el.Data
	if skip[tag] {
		return
	}
	if _, hidden := attr(el, "hidden"); hidden {
		return
	}
	// aria-hidden="true" op structuurelementen: het dichtgeklapte JS-menu
	// (<nav class="full-menu">) en ad-panelen (<aside>) die visueel ook
	// niemand ziet. Bewust níet op content: nu.nl markeert zijn (zichtbare!)
	// teaserfoto's als decoratief — die willen we juist wel.
	if v, ok := attr(el, "aria-hidden"); ok && strings.TrimSpace(v) == "true" {
		switch tag {
		case "nav", "aside", "dialog", "menu":
			return
		}
	}
	// <dialog> zonder open is per spec display:none (cookiebanners!).
	if tag == "dialog" {
		if _, open := attr(el, "open"); !open {
			return
		}
	}
	// Computed props (uit de stylesheets) + inline style="" (wint altijd).
	cp := l.styles[el]
	if inline, ok := attr(el, "style"); ok && inline != "" {
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
	// Image replacement ("het logo-patroon"): een element met een
	// background-image op vaste maat, waarvan de tekst leeg is of expres
	// onzichtbaar gemaakt (text-indent:-9999px, sr-only) — dat élement ís
	// de afbeelding. Renderen als plaatje, de weggeschoven tekst vervalt.
	if m, w, h := l.bgReplacement(el, cp); m != nil {
		l.imageSized(m, w, h, st)
		return
	}
	// srProp is onze eigen vondst uit parseDecls: het sr-only-patroon
	// (1x1px, weggeknipt of buiten beeld) — verborgen zónder display:none.
	if cp[srProp] == "1" {
		return
	}
	// Onderin vastgeplakt (fixed + bottom, geen top): een cookiebar of
	// app-banner. Zonder JS is die niet weg te klikken en hij zou in de
	// flow midden door de pagina renderen — weg ermee.
	if cp["position"] == "fixed" && cp["top"] == "" && cp["bottom"] != "" {
		return
	}
	// position:absolute: uit de flow, op zijn coördinaten t.o.v. de
	// dichtstbijzijnde gepositioneerde voorouder, en bovenop geschilderd
	// (badges, labels, overlays). absEl bewaakt de recursie: absolute()
	// legt dit element daarbinnen als gewoon blok.
	// Alleen mét een anker (top/left/right/bottom) gaat een absolute echt
	// uit de flow — zonder coördinaten zou hij als overlay-junk over zijn
	// broers heen vallen, terwijl de flow-plek precies is waar hij hoort.
	if cp["position"] == "absolute" && el != l.absEl && !fillAbs(cp) &&
		(cp["top"] != "" || cp["left"] != "" || cp["right"] != "" || cp["bottom"] != "") {
		l.absolute(el, cp, st)
		return
	}
	// Elk gepositioneerd element is de containing block voor zijn
	// absolute nazaten.
	if p := cp["position"]; p == "relative" || p == "absolute" || p == "fixed" || p == "sticky" {
		l.origins = append(l.origins, image.Pt(pad+st.indent, l.y))
		defer func() { l.origins = l.origins[:len(l.origins)-1] }()
	}
	// Het logo-slot: een voorpagina-link zonder renderbare inhoud (het
	// logo is svg of een webcomponent) — het alt-tekst-principe, met het
	// site-eigen icoon als vulling. Zo staat het logo wáár de site hem
	// heeft staan, niet in een verzonnen balk.
	if tag == "a" && l.icon != nil && !l.iconUsed && l.emptyContent(el) {
		if href, ok := attr(el, "href"); ok && isRootHref(href) {
			l.iconUsed = true
			st.href = href
			l.imageSized(l.icon, 28, 28, st)
			l.space = true
			return
		}
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
		if src, _ := attr(el, "src"); l.imgs[src] != nil {
			fl := cp["float"]
			if fl != "left" && fl != "right" && st.inline && l.fL.w == 0 && l.fR.w == 0 {
				// Teaser-patroon: in een flex-rij gaat het (eerste) plaatje
				// naar links en stroomt de kop ernaast — zonder dit stapelt
				// alles onder elkaar en lijkt geen nieuwssite op zichzelf.
				fl = "left"
			}
			if fl == "left" || fl == "right" {
				l.floatImage(l.imgs[src], st, fl == "right")
			} else {
				l.image(l.imgs[src], st)
			}
			return
		}
		// alt="" betekent in HTML: decoratief — dan ook géén placeholder.
		// Zonder dit werd elk icoontje (svg, lazy geladen, mislukt) een
		// grijze "[img]" en verzoop de pagina in de ruis.
		alt, hasAlt := attr(el, "alt")
		if alt = strings.TrimSpace(alt); alt == "" {
			if hasAlt {
				return
			}
			alt = "img"
		}
		l.word("["+alt+"]", style{scale: st.scale, col: colRule, href: st.href, indent: st.indent})
		l.space = true
		return
	case "input":
		l.input(el, st)
		return
	case "textarea":
		val := textContent(el)
		if v, ok := l.edits[el]; ok {
			val = v
		}
		l.widget(el, val, false, st)
		return
	}

	// Bovenin vastgeplakt (fixed/sticky + top): de site zegt zelf "dit is
	// mijn header, hou hem in beeld" — pinnen dus (zie pinState).
	pinning := false
	if p := cp["position"]; p == "fixed" || p == "sticky" {
		pinning = l.beginPin(cp)
	}

	// De cascade: UA-defaults ónder de author-props. Een h1 is niet
	// speciaal — hij hééft alleen defaults (font-size: 32px), in dezelfde
	// taal als de site-CSS. Daarna gaat álles door dezelfde ene
	// toepassing van berekende waarden hieronder.
	if ua, ok := uaProps[tag]; ok {
		m := make(props, len(ua)+len(cp))
		for k, v := range ua {
			m[k] = v
		}
		for k, v := range cp {
			m[k] = v
		}
		cp = m
	}
	if tag == "a" {
		// :link is niet in props uit te drukken: alleen een <a> mét href
		// is een link (en krijgt de UA-linkkleur als de site niks zegt).
		if href, ok := attr(el, "href"); ok && href != "" {
			st.href = href
			if _, ok := cp["color"]; !ok {
				st.col = colLink
			}
		}
	}
	// Oud web: presentatie-attributen (géén CSS-pad).
	if tag == "font" {
		if v, ok := attr(el, "color"); ok {
			if c, ok := cssColor(strings.ToLower(v)); ok {
				st.col = c
			}
		}
	}
	if v, ok := attr(el, "bgcolor"); ok {
		if c, ok := cssColor(strings.ToLower(v)); ok {
			st.bg, st.hasBG = c, true
		}
	}

	// Dé toepassing van de berekende waarden — voor elk element dezelfde.
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
	if v, ok := cp["white-space"]; ok {
		st.pre = v == "pre"
	}

	// Onthoud waar de tekst óp komt te liggen: het bg-vlak hieronder zet
	// hasBG uit (de kinderen liggen al op het vlak), maar voor de
	// contrastbewaking in word() moet de kleur eronder bekend blijven.
	if st.hasBG {
		st.on, st.hasOn = st.bg, true
	}

	// "Divs goed zetten": display:inline(-block) haalt een element uit de
	// blok-flow, en de kinderen van een flex/grid-container komen náást
	// elkaar in plaats van onder elkaar — precies genoeg voor menu's,
	// zonder een echte layout-engine te worden.
	isBlock := (blocks[tag] || st.blockify) && !st.inline
	switch cp["display"] {
	case "inline", "inline-block", "inline-flex":
		isBlock = false
	case "block", "list-item", "flex", "grid":
		// flex en grid zíjn blok-niveau — ook op een <span> of een custom
		// element (tweakers' <twk-site-menu>): anders krijgt zo'n container
		// nooit zijn achtergrondvlak en behandelen we divs ongelijk.
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
			// Een kolom-flex herstélt de blok-context — ook midden in een
			// flex-rij (figure in een teaser): zijn kinderen stapelen, en
			// figcaption komt dus onder de foto, niet ernaast.
			childBlockify, childInline = true, false
		} else {
			childInline = true
		}
	case "grid":
		childBlockify, childInline = true, false
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

	// --- het boxmodel: marge, padding, breedte -----------------------------
	// Containers zijn containers — button of div, het maakt niet uit: de
	// CSS bepaalt de doos, wij rekenen hem uit.
	mar := cssEdgesOf(cp, "margin", 96)
	pd := cssEdgesOf(cp, "padding", 48)
	topGap, botGap := blockMargin(tag, st.scale), blockMargin(tag, st.scale)
	if mar.setV {
		topGap, botGap = mar.t, mar.b
	}
	if isBlock {
		st.indent += mar.l
		st.rIndent += mar.r
		// width/max-width: het blok smaller dan zijn ouder; margin:auto
		// centreert (de klassieke artikel-kolom).
		availW := l.width - 2*pad - st.indent - st.rIndent
		if availW > 64 {
			target := availW
			if v, ok := cssLenPct(cp["width"], availW); ok && v >= 64 && v < target {
				target = v
			}
			if v, ok := cssLenPct(cp["max-width"], availW); ok && v >= 64 && v < target {
				target = v
			}
			if target < availW {
				extra := availW - target
				if mar.autoL && mar.autoR {
					st.indent += extra / 2
					st.rIndent += extra - extra/2
				} else {
					st.rIndent += extra
				}
			}
		}
	}

	tile := l.imgs[cssURL(cp["background-image"])]
	decorated := (isBlock || tag == "body") && (st.hasBG || tile != nil || hasBrd)
	// Padding: bij een gedecoreerd blok kleurt hij mee (binnen het vlak);
	// zonder decoratie is het gewoon lucht. Een kaart zonder expliciete
	// padding krijgt de oude kaart-default, en de rand zelf telt ook mee.
	if decorated && tag != "body" && !pd.setV && !pd.setH {
		pd = edges{t: 4, r: 6, b: 4, l: 6, setV: true, setH: true}
	}
	if hasBrd {
		pd.t, pd.r, pd.b, pd.l = pd.t+1, pd.r+1, pd.b+1, pd.l+1
	}
	if !decorated && isBlock {
		topGap += pd.t
		botGap += pd.b
		st.indent += pd.l
		st.rIndent += pd.r
	}

	// De knop-link: een inline(-block) element mét doos-eigenschappen
	// (padding, marge, vlak of rand). Geen widget en geen uitzondering:
	// de inhoud wordt inline gelegd en daarna vouwt de doos eromheen —
	// tekst rendert nooit los van de div (of a, of span) waar hij in zit.
	// Alleen bij échte decoratie (vlak of rand): kale padding op menu-
	// links zou elke nav in dozen hakken.
	inlineBox := !isBlock && tag != "body" && (st.hasBG || hasBrd)
	ibIdx := -1
	var ibX0, ibY0 int
	if inlineBox {
		l.flushGap()
		if l.space && l.x > 0 {
			l.x += charW(st.scale)
		}
		if l.x == 0 {
			l.x = l.lineLeft(st.indent)
		}
		l.x += mar.l
		ibIdx = len(l.boxes)
		l.boxes = append(l.boxes, Box{BG: st.bg, HasBG: st.hasBG, Border: brdCol, HasBrd: hasBrd})
		ibX0, ibY0 = l.x, l.y
		l.x += pd.l
		l.space = false
		st.hasBG = false // de inhoud ligt al óp de doos
	}

	if isBlock {
		l.blockGap(topGap)
		l.depth++
	}
	// clear: onder de lopende floats beginnen (footer onder de foto).
	if v := cp["clear"]; v == "both" || v == "left" || v == "right" {
		l.clearFloats(-1)
	}

	// Blok-achtergrond en/of -rand: één vlak (of tegelpatroon) achter het
	// hele blok — body-achtergrond wordt zo vanzelf de paginakleur. Het
	// vlak gaat als placeholder de boxlijst in (paint-volgorde: onder de
	// inhoud) en krijgt zijn rechthoek als de blokhoogte bekend is.
	bgIdx := -1
	var bgY0, bgX0, bgX1 int
	var bgCover image.Image // background-size:cover → bij het sluiten beeldvullend schalen
	if decorated {
		l.breakLine()
		l.flushGap()
		bgIdx = len(l.boxes)
		box := Box{BG: st.bg, HasBG: st.hasBG, Border: brdCol, HasBrd: hasBrd}
		if tile != nil {
			w, h := tile.Bounds().Dx(), tile.Bounds().Dy()
			if w > 0 && h > 0 && w <= imgMaxDim && h <= imgMaxDim {
				box.Tile = scaleTo(tile, w, h) // één RGBA-tegel, nooit een reuze-alloc
				if cp["background-size"] == "cover" {
					bgCover = tile
				}
			}
		}
		l.boxes = append(l.boxes, box)
		bgY0 = l.y
		bgX0 = pad + st.indent - 2
		bgX1 = l.width - pad - st.rIndent + 2
		if tag == "body" {
			bgX0, bgX1 = 0, l.width
			if st.hasBG {
				// De body-kleur is het paginacanvas: ook onder de content en
				// in de marge — een donkere site is dan echt donker.
				l.pageBG, l.hasPageBG = st.bg, true
			}
		} else {
			l.y += pd.t
			st.indent += pd.l
			st.rIndent += pd.r
		}
		st.hasBG = false // de kinderen liggen al óp het vlak: geen run-vulling meer nodig
	}

	// <button> is een container als elke andere — de site-CSS bepaalt de
	// look; alleen het UA-default-knopvlak (als er niets gezet is) en het
	// klikdoel zijn van ons.
	fieldStart := -1
	if tag == "button" {
		l.space = true
		fieldStart = len(l.boxes)
	}

	childSt := st
	childSt.inline = childInline
	childSt.blockify = childBlockify

	if tag == "li" && isBlock && !st.blockify {
		// In een grid/flex-cel is een <li> een kaart, geen lijstitem.
		l.word("-", st)
		l.space = true
	}
	if inlined {
		l.space = true
	}
	// Kolommen: een flex-rij met blok-kinderen (foto naast kop), een grid
	// met begrijpelijke tracks, of een tabel — elke cel een eigen
	// sub-layout naast elkaar. Lukt dat niet (of is de rij uit balans),
	// dan stapelen de cellen als blokken; zonder plan de gewone flow.
	if rows, colW, gap := l.columnPlan(el, cp, st, tag); rows != nil {
		for _, row := range rows {
			if !l.columns(row, colW, gap, st) {
				cst := childSt
				cst.inline, cst.blockify = false, true
				for _, cell := range row {
					l.walk(cell, cst)
				}
			}
		}
	} else if cp["flex-direction"] == "column-reverse" {
		// flex kent volgorde: -reverse stapelt van onder naar boven.
		var kids []*html.Node
		for c := el.FirstChild; c != nil; c = c.NextSibling {
			kids = append(kids, c)
		}
		for i := len(kids) - 1; i >= 0; i-- {
			l.walk(kids[i], childSt)
		}
	} else {
		for c := el.FirstChild; c != nil; c = c.NextSibling {
			l.walk(c, childSt)
		}
	}
	if inlined {
		l.space = true
	}
	if ibIdx >= 0 {
		if len(l.boxes) == ibIdx+1 {
			l.boxes = l.boxes[:ibIdx] // lege doos: weg ermee
		} else {
			r := l.boxes[ibIdx+1].R
			for _, b := range l.boxes[ibIdx+2:] {
				r = r.Union(b.R)
			}
			if pd.t > 0 {
				// ruimte voor de padding-boven: de inhoud een stukje omlaag
				for i := ibIdx + 1; i < len(l.boxes); i++ {
					l.boxes[i].R = l.boxes[i].R.Add(image.Pt(0, pd.t))
				}
				r = r.Add(image.Pt(0, pd.t))
			}
			doos := image.Rect(ibX0, ibY0, r.Max.X+pd.r, r.Max.Y+pd.b)
			l.boxes[ibIdx].R = doos
			if l.x < doos.Max.X {
				l.x = doos.Max.X
			}
			l.x += mar.r
			if h := doos.Max.Y - l.y; h > l.lineH {
				l.lineH = h
			}
			l.space = true
		}
	}
	if fieldStart >= 0 && len(l.boxes) > fieldStart {
		r := l.boxes[fieldStart].R
		for _, b := range l.boxes[fieldStart+1:] {
			r = r.Union(b.R)
		}
		l.fields = append(l.fields, Field{R: r.Inset(-2), Submit: true, node: el})
	}
	if isBlock {
		l.depth--
		// Impliciet clearen: floats die ín dit blok ontstonden eindigen
		// hier — echte sites clearfixen hun kaarten toch.
		l.clearFloats(l.depth)
		l.blockGap(botGap)
	}
	if bgIdx >= 0 {
		l.breakLine()
		if len(l.boxes) == bgIdx+1 && tag != "body" && l.boxes[bgIdx].Tile == nil {
			// Er is niets ín het blok beland (een logo-div vol svg): geen
			// vlak achterlaten — een lege gekleurde doos is alleen maar ruis.
			l.boxes = l.boxes[:bgIdx]
			l.y = bgY0
		} else {
			if tag == "body" {
				bgX1 = l.width
			} else {
				l.y += pd.b
			}
			// Verticaal exact de blokgrenzen: de binnenmarge zit er al in,
			// en ±2 zou aangrenzende kaarten laten overlappen.
			l.boxes[bgIdx].R = image.Rect(bgX0, bgY0, bgX1, l.y)
			// cover: nu de vlakmaat bekend is beeldvullend schalen — één
			// keer per layout, renderen blijft een kale draw (de tegel past
			// dan precies één keer). Reuze-vlakken blijven tegels.
			if w, h := bgX1-bgX0, l.y-bgY0; bgCover != nil && w >= 8 && h >= 8 && w <= 1600 && h <= 800 {
				l.boxes[bgIdx].Tile = scaleCover(bgCover, w, h)
			}
		}
	}
	if pinning {
		l.endPin()
	}
}

// elementChildren geeft de element-kinderen; direct-tekst telt apart.
func elementChildren(el *html.Node) []*html.Node {
	var out []*html.Node
	for c := el.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && !skip[c.Data] {
			out = append(out, c)
		}
	}
	return out
}

// renderableText: de tekst die wíj zouden tekenen — alles onder skip-
// elementen (svg, script, style) telt niet mee (een <svg><title> is geen
// zichtbare tekst).
func renderableText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.ElementNode && skip[c.Data] {
			return
		}
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	walk(n)
	return b.String()
}

// isRootHref: linkt dit naar de voorpagina ("/", of de site-root)?
// Een kaal "#" (hamburger-triggers) is géén voorpagina-link.
func isRootHref(href string) bool {
	href = strings.TrimSpace(href)
	if href == "" || href == "#" {
		return false
	}
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	return (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == ""
}

// hasDirectText: staat er échte tekst (geen witruimte) direct in dit
// element? Dan is kolommen maken gevaarlijk — de tekst zou verdwijnen.
func hasDirectText(el *html.Node) bool {
	for c := el.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) != "" {
			return true
		}
	}
	return false
}

// emptyContent: zit er niets renderbaars in — geen tekst (buiten svg/
// script om), geen geladen afbeelding, geen formulier-widget?
func (l *layouter) emptyContent(n *html.Node) bool {
	if strings.TrimSpace(renderableText(n)) != "" {
		return false
	}
	found := false
	var w func(*html.Node)
	w = func(c *html.Node) {
		if found {
			return
		}
		if c.Type == html.ElementNode {
			switch c.Data {
			case "img":
				if src, _ := attr(c, "src"); l.imgs[src] != nil {
					found = true
					return
				}
			case "input", "textarea", "select":
				found = true
				return
			}
		}
		for k := c.FirstChild; k != nil && !found; k = k.NextSibling {
			w(k)
		}
	}
	w(n)
	return !found
}

// cellVisible: gaat deze flex-cel iets laten zien? Renderbare inhoud, of
// een vulbaar logo-slot (voorpagina-link + site-icoon) — anders filtert de
// cel weg vóórdat het slot gevuld kan worden.
func (l *layouter) cellVisible(n *html.Node) bool {
	if !l.emptyContent(n) {
		return true
	}
	if l.icon == nil {
		return false
	}
	found := false
	var w func(*html.Node)
	w = func(c *html.Node) {
		if found {
			return
		}
		if c.Type == html.ElementNode && c.Data == "a" {
			if href, ok := attr(c, "href"); ok && isRootHref(href) {
				found = true
				return
			}
		}
		for k := c.FirstChild; k != nil && !found; k = k.NextSibling {
			w(k)
		}
	}
	w(n)
	return found
}

// columnPlan beslist of dit element als kolommen rendert en hoe breed die
// worden: een tabel (rijen van td/th-cellen), een grid (tracks uit
// grid-template-columns) of een flex-rij met blok-kinderen. nil = gewone
// flow. Menu's (flex-rij vol linkjes) blijven bewust inline.
func (l *layouter) columnPlan(el *html.Node, cp props, st style, tag string) ([][]*html.Node, []int, int) {
	availW := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
	if availW < 320 {
		return nil, nil, 0 // te smal om te verdelen: stapelen leest beter
	}
	gap := cssGap(cp)
	equal := func(n int) []int {
		w := (availW - gap*(n-1)) / n
		if w < 100 {
			return nil
		}
		colW := make([]int, n)
		for i := range colW {
			colW[i] = w
		}
		return colW
	}
	switch {
	case tag == "table":
		var rows [][]*html.Node
		ncol := 0
		var walkT func(n *html.Node)
		walkT = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "tr" {
				var cells []*html.Node
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
						cells = append(cells, c)
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
					if len(cells) > ncol {
						ncol = len(cells)
					}
				}
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkT(c)
			}
		}
		walkT(el)
		if len(rows) == 0 || ncol < 2 || ncol > 4 {
			return nil, nil, 0 // één kolom of te veel: stapelen leest beter
		}
		if colW := equal(ncol); colW != nil {
			return rows, colW, gap
		}
	case cp["display"] == "grid":
		tracks := gridTracks(cp["grid-template-columns"], availW, gap)
		items := elementChildren(el)
		if tracks == nil || len(items) < 2 {
			return nil, nil, 0
		}
		var rows [][]*html.Node
		for i := 0; i < len(items); i += len(tracks) {
			end := i + len(tracks)
			if end > len(items) {
				end = len(items)
			}
			rows = append(rows, items[i:end])
		}
		return rows, tracks, gap
	case cp["display"] == "flex" || cp["display"] == "inline-flex":
		fd := cp["flex-direction"]
		if fd != "row" && fd != "row-reverse" && cp["flex-wrap"] == "" {
			// Zonder expliciet rij-signaal (flex-direction: row of een
			// flex-wrap) niet naast elkaar leggen: de default is weliswaar
			// row, maar een gemiste declaratie is in de praktijk vaker
			// "column" (kaarten) — en gestapeld leest altijd nog goed,
			// terwijl onterechte kolommen de pagina slopen (tweakers'
			// uitgelichte teasers: foto hoort bóven de tekst).
			return nil, nil, 0
		}
		if hasDirectText(el) {
			return nil, nil, 0
		}
		// Cellen zonder zichtbare inhoud (een svg-logo dat wij niet
		// rasteren) doen niet mee — anders wordt zo'n cel een lege
		// gekleurde doos en staat de rest scheef ernaast geperst.
		var items []*html.Node
		for _, it := range elementChildren(el) {
			if l.cellVisible(it) {
				items = append(items, it)
			}
		}
		if fd == "row-reverse" {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		if len(items) < 2 {
			return nil, nil, 0
		}
		blockish := false
		for _, it := range items {
			if blocks[it.Data] || it.Data == "img" || it.Data == "picture" || it.Data == "video" {
				blockish = true
			}
		}
		if !blockish {
			return nil, nil, 0 // allemaal linkjes: dat is een menu
		}
		// Meer items dan er naast elkaar passen (of expliciet flex-wrap):
		// wrappen in rijen van kaartmaat — dat ís flex-wrap.
		if len(items) > 4 || cp["flex-wrap"] == "wrap" {
			cols := availW / 220
			if cols < 2 {
				cols = 2
			}
			if cols > 4 {
				cols = 4
			}
			if cols > len(items) {
				cols = len(items)
			}
			colW := equal(cols)
			if colW == nil {
				return nil, nil, 0
			}
			var rows [][]*html.Node
			for i := 0; i < len(items); i += cols {
				end := i + cols
				if end > len(items) {
					end = len(items)
				}
				rows = append(rows, items[i:end])
			}
			return rows, colW, gap
		}
		if colW := equal(len(items)); colW != nil {
			return [][]*html.Node{items}, colW, gap
		}
	}
	return nil, nil, 0
}

// columns legt één rij cellen naast elkaar: elke cel zijn eigen
// sub-layouter op kolombreedte, daarna verschoven naar zijn kolom-x. De
// rij wordt zo hoog als de hoogste cel. Eerst wordt speculatief gelegd:
// zijn de celhoogtes wild uit balans, dan is dit geen kaartenrij maar
// pagina-steigerwerk (een titelblokje naast een eindeloze nieuwskolom) —
// dan géén commit (false) en stapelt de aanroeper gewoon.
func (l *layouter) columns(cells []*html.Node, colW []int, gap int, st style) bool {
	subs := make([]*layouter, len(cells))
	maxH, minH := 0, 1<<30
	for i, cell := range cells {
		w := colW[i%len(colW)]
		sub := &layouter{width: w, imgs: l.imgs, styles: l.styles, edits: l.edits, icon: l.icon}
		cst := st
		cst.indent, cst.rIndent = 0, 0
		cst.inline, cst.center, cst.pre = false, false, false
		cst.blockify = true // een cel gedraagt zich als blok (ook een <a>)
		sub.walk(cell, cst)
		sub.breakLine()
		subs[i] = sub
		if sub.y > maxH {
			maxH = sub.y
		}
		if sub.y < minH {
			minH = sub.y
		}
	}
	// Balans-check: kaartenrijen en teasers zijn (ruwweg) even hoog; een
	// kolom die torenhoog boven de rest uitsteekt hoort niet naast maar
	// boven/onder de rest. Kleine rijen zijn altijd goed.
	if maxH > 700 && maxH > 3*minH {
		return false
	}
	l.breakLine()
	l.flushGap()
	x0 := l.lineLeft(st.indent)
	y0 := l.y
	cx := x0
	for i, sub := range subs {
		off := image.Pt(cx-pad, y0) // sub begint op zijn eigen pad-marge
		base := len(l.fields)
		for _, b := range sub.boxes {
			if b.Field > 0 {
				b.Field += base
			}
			b.Pin = false
			b.R = b.R.Add(off)
			l.boxes = append(l.boxes, b)
		}
		for _, b := range sub.late {
			if b.Field > 0 {
				b.Field += base
			}
			b.Pin = false
			b.R = b.R.Add(off)
			l.late = append(l.late, b)
		}
		for _, f := range sub.fields {
			f.R = f.R.Add(off)
			l.fields = append(l.fields, f)
		}
		cx += colW[i%len(colW)] + gap
	}
	l.y = y0 + maxH
	l.x, l.lineH, l.space = 0, 0, false
	l.line0 = len(l.boxes)
	return true
}

// input legt één <input> in de flow; hidden doet niet mee, knoppen en
// tekstvelden worden widgets, checkbox/radio (v0) een kaal vinkje.
func (l *layouter) input(el *html.Node, st style) {
	typ, _ := attr(el, "type")
	typ = strings.ToLower(strings.TrimSpace(typ))
	val, _ := attr(el, "value")
	if v, ok := l.edits[el]; ok {
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
		if _, ok := attr(el, "checked"); ok {
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
func (l *layouter) widget(el *html.Node, val string, submit bool, st style) {
	l.flushGap()
	chars := 20
	if submit {
		chars = len(val) + 2
	} else if v, ok := attr(el, "size"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			chars = n
		}
	}
	w := chars*charW(st.scale) + 8
	if max := l.lineRight(st.rIndent) - l.lineLeft(st.indent); w > max {
		w = max
	}
	h := charH(st.scale) + 8
	sp := 0
	if l.space && l.x > 0 {
		sp = charW(st.scale)
	}
	if l.x > 0 && l.x+sp+w > l.lineRight(st.rIndent) {
		l.breakLine()
		sp = 0
	}
	if l.x == 0 {
		l.x = l.lineLeft(st.indent)
	}
	x := l.x + sp
	r := image.Rect(x, l.y, x+w, l.y+h)
	name, _ := attr(el, "name")
	l.fields = append(l.fields, Field{R: r, Name: name, Value: val, Submit: submit, node: el})
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

// ascii vouwt tekst naar het 8x8-font (ASCII) via de folds-tabel; wat daar
// niet in staat wordt één '?' — zonder dit werd een em-dash drie '?'-en
// (één per UTF-8-byte).
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
		if r < 0x80 {
			b.WriteRune(r)
		} else if t, ok := folds[r]; ok {
			b.WriteString(t)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

// folds: niet-ASCII → ASCII. Typografie naar de schrijfmachine-vorm,
// accentletters naar hun kale vorm (ë → e — Nederlandse pagina's staan er
// vol mee: Oekraïne, één, financiën), ligaturen uit elkaar, valuta naar hun
// ISO-code.
var folds = map[rune]string{
	'–': "-", '—': "-", '−': "-", '‐': "-", '‑': "-",
	'‘': "'", '’': "'", '‚': ",", '“': "\"", '”': "\"", '„': "\"",
	'«': "<<", '»': ">>", '‹': "<", '›': ">",
	' ': " ", ' ': " ", ' ': " ", '​': "",
	'•': "-", '·': "-", '…': "...", '×': "x", '÷': "/",
	'©': "(c)", '®': "(r)", '™': "(tm)", '°': "*", '±': "+/-",
	'→': "->", '←': "<-", '↑': "^", '↓': "v",
	'€': "EUR", '£': "GBP", '¥': "JPY", '¢': "c",
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'æ': "ae",
	'ç': "c", 'è': "e", 'é': "e", 'ê': "e", 'ë': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ñ': "n", 'ð': "d",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'œ': "oe",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ý': "y", 'ÿ': "y",
	'ß': "ss", 'þ': "th", 'ĳ': "ij",
	'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A", 'Æ': "AE",
	'Ç': "C", 'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E",
	'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I", 'Ñ': "N", 'Ð': "D",
	'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O", 'Ø': "O", 'Œ': "OE",
	'Ù': "U", 'Ú': "U", 'Û': "U", 'Ü': "U", 'Ý': "Y", 'Ÿ': "Y", 'Ĳ': "IJ",
	'š': "s", 'Š': "S", 'ž': "z", 'Ž': "Z", 'č': "c", 'Č': "C",
}

// word plaatst één woord, met wrap op de paginabreedte.
func (l *layouter) word(w string, st style) {
	w = ascii(w)
	l.flushGap()
	// Contrastbewaking: tekst die (bijna) wegvalt tegen zijn achtergrond —
	// meestal een link waarvan wij de kleurregel niet dragen, op een donker
	// menuvlak — klapt naar licht of donker. Liever leesbaar dan kleurecht.
	if st.hasOn && absInt(luma(st.col)-luma(st.on)) < 90 {
		if luma(st.on) < 128 {
			st.col = colBarTxt
		} else {
			st.col = colText
		}
	}
	ww := textW(w, st.scale)
	sp := 0
	if l.space && l.x > 0 {
		sp = charW(st.scale)
	}
	if l.x > 0 && l.x+sp+ww > l.lineRight(st.rIndent) {
		l.breakLine()
		sp = 0
	}
	if l.x == 0 {
		l.x = l.lineLeft(st.indent)
		// Past het woord op een verse regel niet naast de float (een kop
		// op schaal 3 naast een foto), spring er dan onder — anders liep
		// hij het beeld uit.
		for l.x+ww > l.lineRight(st.rIndent) {
			bot := 0
			if l.fL.w > 0 && l.y < l.fL.bot {
				bot = l.fL.bot
			}
			if l.fR.w > 0 && l.y < l.fR.bot && (bot == 0 || l.fR.bot < bot) {
				bot = l.fR.bot
			}
			if bot == 0 {
				break
			}
			l.y = bot
			l.x = l.lineLeft(st.indent)
		}
	}
	x := l.x + sp
	l.boxes = append(l.boxes, Box{
		R:     image.Rect(x, l.y, x+ww, l.y+charH(st.scale)),
		Text:  w,
		Scale: st.scale,
		Col:   st.col,
		Href:  st.href,
		Bold:  st.bold,
		BG:    st.bg,
		HasBG: st.hasBG,
	})
	l.x = x + ww
	if h := charH(st.scale); h > l.lineH {
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
	l.imageSized(m, m.Bounds().Dx(), m.Bounds().Dy(), st)
}

// bgReplacement herkent het logo-patroon op dit element: background-image
// met vaste CSS-maat, en geen zichtbare tekst (leeg, of weggeschoven met
// text-indent/sr-only). Geeft de afbeelding en de maat; nil als dit gewoon
// een blok-met-achtergrond is.
func (l *layouter) bgReplacement(el *html.Node, cp props) (image.Image, int, int) {
	src := cssURL(cp["background-image"])
	if src == "" || l.imgs[src] == nil {
		return nil, 0, 0
	}
	w, ok1 := cssLen(cp["width"])
	h, ok2 := cssLen(cp["height"])
	if !ok1 || !ok2 || w < 8 || h < 8 || w > l.width || h > 600 {
		return nil, 0, 0
	}
	if cp[srProp] != "1" && strings.TrimSpace(textContent(el)) != "" {
		return nil, 0, 0 // zichtbare tekst op een achtergrond: geen replacement
	}
	return l.imgs[src], w, h
}

// imageSized plaatst een afbeelding op een gegeven maat in de flow (image
// replacement geeft de CSS-maat mee; een <img> zijn natuurlijke maat).
func (l *layouter) imageSized(m image.Image, w, h int, st style) {
	l.flushGap()
	if w < 1 || h < 1 {
		return
	}
	maxW := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
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
		sp = charW(st.scale)
	}
	if l.x > 0 && l.x+sp+w > l.lineRight(st.rIndent) {
		l.breakLine()
		sp = 0
	}
	if l.x == 0 {
		l.x = l.lineLeft(st.indent)
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

// floatImage legt een afbeelding als float neer: tegen de linker- of
// rechterkant, de lopende tekst stroomt ernaast (lineLeft/lineRight) en
// valt eronder weer breed uit. Nooit meer dan ~60% van de regel breed —
// er moet tekst naast passen, anders was het geen float waard.
func (l *layouter) floatImage(m image.Image, st style, right bool) {
	l.breakLine()
	l.flushGap()
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	if w < 1 || h < 1 {
		return
	}
	maxW := (l.width - 2*pad - st.indent) / 2
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
	x := pad + st.indent
	if right {
		x = l.width - pad - w
	}
	l.boxes = append(l.boxes, Box{R: image.Rect(x, l.y, x+w, l.y+h), Href: st.href, Img: scaleTo(m, w, h)})
	f := flt{w: w + 8, bot: l.y + h + lead, depth: l.depth}
	if right {
		l.fR = f
	} else {
		l.fL = f
	}
	l.space = false
}

// clearFloats sluit de floats die dieper dan blokdiepte d ontstonden: de
// y springt onder de foto. Dít maakt het kaart/teaser-patroon af — de
// volgende teaser hoort ónder de vorige, niet in diens restruimte.
func (l *layouter) clearFloats(d int) {
	bot := 0
	if l.fL.w > 0 && l.fL.depth > d {
		if l.fL.bot > bot {
			bot = l.fL.bot
		}
		l.fL = flt{}
	}
	if l.fR.w > 0 && l.fR.depth > d {
		if l.fR.bot > bot {
			bot = l.fR.bot
		}
		l.fR = flt{}
	}
	if bot > 0 {
		l.breakLine()
		if l.y < bot {
			l.y = bot
		}
	}
}

// fillAbs herkent de aspect-ratio-vulling: absolute op top:0;left:0 (in
// een wrapper met padding-top-percentage, die wij op 0 klemmen). Dat is
// geen overlay maar gewoon de inhoud — die hoort in de flow, anders
// schuiven foto's over elkaar heen.
func fillAbs(cp props) bool {
	zero := func(k string) bool {
		v, ok := cssLen(cp[k])
		return cp[k] != "" && ok && v == 0
	}
	return zero("top") && zero("left")
}

// absolute haalt een element uit de flow: sub-layout op eigen breedte,
// geplaatst op left/top/right t.o.v. de containing block (de dichtst-
// bijzijnde gepositioneerde voorouder, anders de pagina) en ná de flow
// geschilderd — badges, labels, overlays. Zonder coördinaten valt hij op
// de "static position" (waar hij in de flow stond): voor dropdown-panelen
// precies goed. bottom-verankering kan niet (de voorouderhoogte is hier
// nog onbekend) — die valt op de static position terug.
func (l *layouter) absolute(el *html.Node, cp props, st style) {
	o := image.Pt(0, 0)
	if n := len(l.origins); n > 0 {
		o = l.origins[n-1]
	}
	x := l.lineLeft(st.indent)
	if v, ok := cssLenSigned(cp["left"]); ok {
		x = o.X + v
	}
	y := l.y
	if v, ok := cssLenSigned(cp["top"]); ok {
		y = o.Y + v
	}
	w := 0
	if v, ok := cssLenPct(cp["width"], l.width-pad-x); ok {
		w = v
	}
	if v, ok := cssLenSigned(cp["right"]); ok && cp["left"] == "" && w > 0 {
		x = l.width - pad - v - w // rechts geankerd
	}
	if w <= 0 {
		w = l.width - pad - x
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if w < 24 || x >= l.width {
		return // niets zinnigs te leggen
	}
	sub := &layouter{width: w, imgs: l.imgs, styles: l.styles, edits: l.edits, absEl: el, icon: l.icon}
	cst := st
	cst.indent, cst.rIndent = 0, 0
	cst.inline, cst.center, cst.pre = false, false, false
	cst.blockify = true
	sub.walk(el, cst)
	sub.breakLine()
	off := image.Pt(x-pad, y)
	base := len(l.fields)
	for _, b := range append(sub.boxes, sub.late...) {
		if b.Field > 0 {
			b.Field += base
		}
		b.Pin = false
		b.R = b.R.Add(off)
		l.late = append(l.late, b)
	}
	for _, f := range sub.fields {
		f.R = f.R.Add(off)
		l.fields = append(l.fields, f)
	}
}

// scaleCover schaalt src beeldvullend naar w×h (aspect behouden, gecentreerd,
// de rest afgesneden) — background-size: cover, het hero/teaser-patroon.
func scaleCover(src image.Image, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw < 1 || sh < 1 || w < 1 || h < 1 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	// De bron-crop die (op schaal) precies w×h dekt.
	cw, ch := sw, sw*h/w
	if ch > sh || ch < 1 {
		ch = sh
		cw = sh * w / h
		if cw > sw {
			cw = sw
		}
	}
	if cw < 1 {
		cw = 1
	}
	ox, oy := sb.Min.X+(sw-cw)/2, sb.Min.Y+(sh-ch)/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := oy + y*ch/h
		for x := 0; x < w; x++ {
			dst.Set(x, y, src.At(ox+x*cw/w, sy))
		}
	}
	return dst
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
		ww := textW(line, st.scale)
		l.boxes = append(l.boxes, Box{
			R:     image.Rect(l.x, l.y, l.x+ww, l.y+charH(st.scale)),
			Text:  line,
			Scale: st.scale,
			Col:   st.col,
			Href:  st.href,
		})
		l.x += ww
		if h := charH(st.scale); h > l.lineH {
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
// spatie uit elkaar staan aan elkaar: minder boxes, minder tekenwerk.
func merge(in []Box) []Box {
	out := in[:0]
	for _, b := range in {
		if n := len(out); n > 0 {
			p := &out[n-1]
			if !p.Rule && !b.Rule && p.Img == nil && b.Img == nil &&
				p.Field == 0 && b.Field == 0 && p.Tile == nil && b.Tile == nil &&
				p.Scale == b.Scale && p.Col == b.Col && p.Href == b.Href &&
				p.Bold == b.Bold && p.HasBG == b.HasBG && p.BG == b.BG &&
				p.Pin == b.Pin &&
				p.R.Min.Y == b.R.Min.Y && p.R.Max.X+charW(p.Scale) == b.R.Min.X {
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
	canvas := colPage
	if v.Page.HasBG {
		canvas = v.Page.BG // donkere site: ook het canvas donker, niet alleen het body-vlak
	}
	pixel.Fill(img, b, canvas)
	y0 := b.Min.Y + BarH
	pinned := v.pinnedNow()
	for i := range v.Page.Boxes {
		bx := &v.Page.Boxes[i]
		if pinned && bx.Pin {
			continue // komt zo bovenop, op zijn gepinde plek
		}
		v.drawBox(img, bx, y0+bx.R.Min.Y-v.Scroll, y0+bx.R.Max.Y-v.Scroll)
	}
	if pinned {
		// De gepinde header: bovenin, over de gescrolde content heen. Eerst
		// een dekkende strook in de canvaskleur — een header zonder eigen
		// achtergrond zou anders transparant over de tekst zweven.
		strip := image.Rect(b.Min.X, y0, b.Max.X, y0+v.Page.PinY1-v.Page.PinY0)
		pixel.Fill(img, strip, canvas)
		for i := range v.Page.Boxes {
			bx := &v.Page.Boxes[i]
			if !bx.Pin {
				continue
			}
			v.drawBox(img, bx, y0+bx.R.Min.Y-v.Page.PinY0, y0+bx.R.Max.Y-v.Page.PinY0)
		}
	}
	v.RenderBar(img)
	v.RenderStatus(img)
	v.renderScrollbar(img)
}

// pinnedNow: is er een header én zijn we er voorbij gescrold? Daarvóór
// staat hij gewoon in de flow op precies dezelfde plek.
func (v *View) pinnedNow() bool {
	return v.Page.Pinned() && v.Scroll > v.Page.PinY0
}

// drawBox tekent één box op de al berekende schermpositie (top/bot) — de
// hoofdlus geeft scroll-coördinaten, de pin-pas de vastgezette.
func (v *View) drawBox(img *image.RGBA, bx *Box, top, bot int) {
	b := img.Bounds()
	y0 := b.Min.Y + BarH
	if bot <= y0 || top >= b.Max.Y {
		return
	}
	if bx.Rule {
		pixel.Fill(img, image.Rect(b.Min.X+bx.R.Min.X, top, b.Min.X+bx.R.Max.X, bot), bx.Col)
		return
	}
	if bx.Img != nil {
		// Over, niet Src: PNG-transparantie hoort het paginawit te tonen.
		r := image.Rect(b.Min.X+bx.R.Min.X, top, b.Min.X+bx.R.Max.X, bot)
		draw.Draw(img, r, bx.Img, bx.Img.Bounds().Min, draw.Over)
		return
	}
	if bx.Field > 0 {
		v.renderField(img, bx, top, bot)
		return
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
		return
	}
	if bx.HasBG {
		// 1px lucht rondom: leest prettiger en dekt de spatie in een
		// samengevoegde run.
		pixel.Fill(img, image.Rect(b.Min.X+bx.R.Min.X-1, top-1, b.Min.X+bx.R.Max.X+1, bot+1), bx.BG)
	}
	drawTxt(img, b.Min.X+bx.R.Min.X, top, bx.Scale, bx.Col, bx.Text)
	if bx.Bold {
		// Pseudo-vet: het font heeft geen gewichten — dubbel tekenen met
		// 1px offset is er verrassend dichtbij.
		drawTxt(img, b.Min.X+bx.R.Min.X+1, top, bx.Scale, bx.Col, bx.Text)
	}
	if bx.Href != "" {
		pixel.Fill(img, image.Rect(b.Min.X+bx.R.Min.X, bot, b.Min.X+bx.R.Max.X, bot+1), bx.Col)
	}
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
	if max := (r.Dx() - 8) / charW(bx.Scale); len(txt) > max && max > 0 {
		txt = txt[len(txt)-max:] // het einde in beeld: daar wordt getikt
	}
	if f.Submit {
		drawTxtCentered(img, r, bx.Scale, colText, txt)
	} else {
		drawTxt(img, r.Min.X+4, r.Min.Y+(r.Dy()-charH(bx.Scale))/2, bx.Scale, colText, txt)
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
	if max := (b.Dx() - 2*pad) / charW(1); len(txt) > max && max > 0 {
		txt = txt[len(txt)-max:]
	}
	drawTxt(img, b.Min.X+pad, b.Min.Y+(BarH-charH(1))/2, 1, colBarTxt, txt)
}

// RenderStatus tekent alléén de statusbalk onderin (voor het laad-pad:
// partiële damage — de pagina eronder blijft staan).
func (v *View) RenderStatus(img *image.RGBA) {
	r := v.StatusRect(img)
	pixel.Fill(img, r, colBar)
	txt := v.Status
	if max := (r.Dx() - 2*pad) / charW(1); len(txt) > max && max > 0 {
		txt = txt[:max] // begin in beeld houden: daar staat wát hij doet
	}
	col := colBarTxt
	if v.Err {
		col = colErrBar
	}
	drawTxt(img, r.Min.X+pad, r.Min.Y+(StatusH-charH(1))/2, 1, col, txt)
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

// docPoint vertaalt een klik (window-lokaal) naar documentcoördinaten,
// rekening houdend met de gepinde header: een klik op de vaste strook
// hoort bij de header, wáár je ook gescrold bent. inPin zegt of de klik
// op die strook viel (dan telt alleen de header mee als doel).
func (v *View) docPoint(x, y int) (p image.Point, inPin bool) {
	if v.pinnedNow() && y-BarH < v.Page.PinY1-v.Page.PinY0 {
		return image.Pt(x, y-BarH+v.Page.PinY0), true
	}
	return image.Pt(x, y-BarH+v.Scroll), false
}

// Hit vertaalt een klik (window-lokale coördinaten, viewH = windowhoogte)
// naar de href van de link eronder; "" als daar geen link is. Kliks op de
// adres- en statusbalk zijn nooit een link.
func (v *View) Hit(x, y, viewH int) string {
	if y < BarH || y >= viewH-StatusH {
		return ""
	}
	p, inPin := v.docPoint(x, y)
	for _, bx := range v.Page.Boxes {
		if inPin && !bx.Pin {
			continue // de strook dekt de content eronder af
		}
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
	p, inPin := v.docPoint(x, y)
	for i := range v.Page.Fields {
		r := v.Page.Fields[i].R
		if inPin && (r.Min.Y < v.Page.PinY0 || r.Min.Y >= v.Page.PinY1) {
			continue // veld buiten de header telt niet op de strook
		}
		if p.In(r) {
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
