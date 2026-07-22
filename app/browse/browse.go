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
	"sort"
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

// viewH is de viewport-hoogte: de basis voor position:fixed-panelen en hun
// 100%-hoogtes (tweakers' calc(100% - var(--site-menu-height))). drive zet
// hem op de echte vensterhoogte; los daarvan is 600 een nette lezing —
// dezelfde cap die cssMinExtent al hanteerde.
var viewH = 600

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
	Under  bool        // onderstreept (text-decoration; de UA-default voor links)
	BG     color.RGBA  // achtergrondvlak achter de run (of het blok)
	HasBG  bool
	Border color.RGBA // blokrand (kaarten, panelen)
	HasBrd bool
	BrdW   int  // randdikte in px (0/1 = de klassieke 1px-lijn)
	Rad    int  // border-radius: hoekstraal in px (-1 = helemaal rond, klemt bij het tekenen)
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
	bold     bool   // pseudo-vet: glyph dubbel getekend met 1px offset
	under    bool   // onderstreept (text-decoration door de cascade)
	xform    string // text-transform: uppercase/lowercase/capitalize
	center   bool   // text-align:center / <center>
	right    bool   // text-align:right — prijzen, datums
	marker   string // het lijstteken voor li's: "-", "1" (tellen) of "" (geen)
	list     *int   // de teller van de omvattende <ol>
	inline   bool   // in een flex/inline-context: blokken breken hier niet
	blockify bool   // direct kind van een grid/flex-kolom: word een blok (ook een <a>)
	rad      int    // border-radius voor vervangen inhoud (ronde avatars) — niet geërfd, per element gezet
	rIndent  int    // inspringing vanaf rechts (marges/padding van blokken)
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
	right  bool                  // deze regel rechts uitlijnen bij breakLine
	lineR  int                   // rechterrand van de uit te lijnen regel (0 = paginabreed)
	fL, fR flt                   // actieve floats links en rechts
	depth  int                   // blokdiepte tijdens de wandeling

	pageBG    color.RGBA // body-achtergrond: het paginacanvas (Page.BG)
	hasPageBG bool
	pin       pinState

	origins  []absOrigin // gepositioneerde voorouders (containing blocks)
	rootEl   *html.Node  // celwortel van een sub-layout: zijn width is al verrekend
	pend     []pendAbs   // bottom-verankerde absolutes: wachten op de voorouderhoogte
	late     []Box       // absolute boxes: geschilderd ná de flow (erbovenop)
	absEl    *html.Node  // absolute() legt dit element zelf — geen recursie
	icon     image.Image // site-icoon (apple-touch-icon) voor het logo-slot
	iconUsed bool        // één logo-slot per pagina: het eerste (de header)
	svgN     int         // gerasterde inline-svg's deze layout (budget)
}

// absOrigin is één containing block voor absolute nazaten: zijn hoekpunt
// én zijn breedte — procent-ankers (wikipedia's right:60%) resolven tegen
// die breedte, niet tegen de pagina.
type absOrigin struct {
	p image.Point
	w int
	h int // gedeclareerde hoogte (0 = onbekend) — de basis voor top/bottom-%
}

// pendAbs is een uitgestelde absolute: bottom-verankerd — die is pas te
// leggen als de onderkant van zijn containing block bekend is, dus bij het
// sluiten van die voorouder (of van de pagina; oi -1).
type pendAbs struct {
	el *html.Node
	cp props
	st style
	oi int // index in origins van de containing block (-1 = de pagina)
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
	// De rem-basis van deze pagina: html { font-size } (62.5% = 10px).
	remPx = 16
	if body != nil && body.Parent != nil && styles != nil {
		if v, ok := styles[body.Parent]["font-size"]; ok {
			remPx = rootFontPx(v)
		}
	}
	l := &layouter{width: width, imgs: imgs, styles: styles, edits: edits, icon: icon}
	if body != nil {
		l.walk(body, style{scale: 1, col: colText, marker: "-"})
	}
	l.breakLine()
	// Bottom-verankerde absolutes zonder gepositioneerde voorouder: hun
	// containing block is de pagina — die onderkant is er nu.
	l.flushAbs(-1, l.y)
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
	"th":         {"font-weight": "bold", "text-align": "center"},
	"code":       {"color": "#6a2a8a"},
	"kbd":        {"color": "#6a2a8a"},
	"samp":       {"color": "#6a2a8a"},
	"pre":        {"color": "#6a2a8a", "white-space": "pre"},
	"mark":       {"background-color": "gold"},
	"center":     {"text-align": "center"},
	"summary":    {"font-weight": "bold"},
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
	if tag == "svg" {
		// Vóór de skip: een inline <svg> ís vaak het logo — rasteren.
		// (skip houdt hem wel uit de tekst-helpers: <svg><title> is geen
		// zichtbare tekst.)
		l.inlineSVG(el, st)
		return
	}
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
	cp := l.propsOf(el)
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
		l.imageSized(m, w, h, st, cp["background-size"] == "cover")
		return
	}
	// srProp is onze eigen vondst uit parseDecls: het sr-only-patroon
	// (1x1px, weggeknipt of buiten beeld) — verborgen zónder display:none.
	if cp[srProp] == "1" {
		return
	}
	// ARIA: een leeg element met role="img" en een aria-label ís een
	// afbeelding met alt-tekst (tweakers' <twk-icon> komt zonder JS leeg
	// over de lijn) — het alt-principe, net als bij een kapotte <img>.
	if v, ok := attr(el, "role"); ok && strings.TrimSpace(v) == "img" && l.emptyContent(el) {
		if lbl, ok := attr(el, "aria-label"); ok && strings.TrimSpace(lbl) != "" {
			l.word("["+strings.TrimSpace(lbl)+"]", style{scale: st.scale, col: colRule, href: st.href, indent: st.indent})
			l.space = true
			return
		}
	}
	// <details> zonder open is dichtgeklapt: alleen de <summary> zichtbaar
	// — het HTML-mechanisme zelf, geen JS voor nodig.
	if tag == "details" {
		if _, open := attr(el, "open"); !open {
			for c := el.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "summary" {
					l.walk(c, st)
				}
			}
			return
		}
	}
	// Onderin vastgeplakt (fixed + bottom, geen top): een cookiebar of
	// app-banner. Zonder JS is die niet weg te klikken en hij zou in de
	// flow midden door de pagina renderen — weg ermee.
	if cp["position"] == "fixed" && cp["top"] == "" && cp["bottom"] != "" {
		return
	}
	// position:fixed mét een anker ónder de bovenrand: een zijbalk of
	// paneel tegen het venster geplakt (tweakers' panes: top:48px, right:0,
	// height:calc(100% - 48px)). De containing block is de víewport — niet
	// een voorouder. Wat wél tegen de bovenrand zit blijft de gepinde
	// header (verderop, beginPin).
	if cp["position"] == "fixed" && el != l.absEl {
		if v, ok := anchorLen(cp["top"], viewH); ok && v > 8 {
			l.fixedPanel(el, cp, st)
			return
		}
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
		if cp["top"] == "" && cp["bottom"] != "" {
			// bottom-anker: de voorouderhoogte is er pas bij het sluiten —
			// parkeren; flushAbs legt hem zodra de onderkant bekend is.
			l.pend = append(l.pend, pendAbs{el: el, cp: cp, st: st, oi: len(l.origins) - 1})
			return
		}
		l.absolute(el, cp, st, -1)
		return
	}
	// float: het blok drijft naar links of rechts en de flow stroomt
	// ernaast — het krantenpatroon, ook voor niet-afbeeldingen (kaders,
	// tags). <img> heeft zijn eigen float-pad (floatImage) verderop, en
	// position wint per spec van float.
	if fl := cp["float"]; (fl == "left" || fl == "right") && tag != "img" &&
		el != l.rootEl && el != l.absEl && cp["position"] != "absolute" && cp["position"] != "fixed" {
		if l.floatBlock(el, cp, st, fl == "right") {
			return
		}
	}
	// Elk gepositioneerd element is de containing block voor zijn
	// absolute nazaten.
	originIdx := -1
	if p := cp["position"]; p == "relative" || p == "absolute" || p == "fixed" || p == "sticky" {
		l.origins = append(l.origins, absOrigin{
			p: image.Pt(pad+st.indent, l.y),
			w: l.width - 2*pad - st.indent - st.rIndent,
			h: cssMinExtent(cp),
		})
		originIdx = len(l.origins) - 1
		oi := originIdx
		defer func() {
			// Nu is de onderkant van deze containing block bekend: de
			// geparkeerde bottom-verankerde nazaten kunnen gelegd worden.
			l.flushAbs(oi, l.y)
			l.origins = l.origins[:oi]
		}()
	}
	// Het logo-slot: een voorpagina-link zonder renderbare inhoud (het
	// logo is svg of een webcomponent) — het alt-tekst-principe, met het
	// site-eigen icoon als vulling. Zo staat het logo wáár de site hem
	// heeft staan, niet in een verzonnen balk.
	// Géén verzonnen naam naast het icoon: het echte wordmark bevat de
	// merknaam al — kunnen we die niet renderen (tweakers hangt hem pas
	// met JS in de DOM), dan is alléén het icoon de eerlijke weergave.
	if tag == "a" && l.icon != nil && !l.iconUsed && l.emptyContent(el) {
		if href, ok := attr(el, "href"); ok && isRootHref(href) {
			l.iconUsed = true
			st.href = href
			l.imageSized(l.icon, 28, 28, st, false)
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
		if src := imgSrc(el); src != "" && l.imgs[src] != nil {
			m := l.imgs[src]
			avail := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
			w, h := imgSize(el, cp, m.Bounds().Dx(), m.Bounds().Dy(), avail)
			st.rad = cssRadius(cp["border-radius"]) // ronde avatars
			// display:block + margin:auto: het klassiek gecentreerde plaatje.
			if mar := cssEdgesOf(cp, "margin", 96); mar.autoL && mar.autoR {
				st.center = true
			}
			fl := cp["float"]
			if fl != "left" && fl != "right" && st.inline && l.fL.w == 0 && l.fR.w == 0 {
				// Teaser-patroon: in een flex-rij gaat het (eerste) plaatje
				// naar links en stroomt de kop ernaast — zonder dit stapelt
				// alles onder elkaar en lijkt geen nieuwssite op zichzelf.
				fl = "left"
			}
			if fl == "left" || fl == "right" {
				l.floatImage(m, w, h, st, fl == "right")
			} else {
				l.imageSized(m, w, h, st, cp["object-fit"] == "cover")
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
	case "video":
		// Zonder afspeler is de poster het eerlijke beeld van een video;
		// zonder poster doet de fallback-inhoud (tekst) gewoon zijn ding.
		if v, ok := attr(el, "poster"); ok && l.imgs[v] != nil {
			m := l.imgs[v]
			avail := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
			w, h := imgSize(el, cp, m.Bounds().Dx(), m.Bounds().Dy(), avail)
			l.imageSized(m, w, h, st, cp["object-fit"] == "cover")
			return
		}
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
		if pinning && p == "fixed" {
			// fixed: de viewport is de containing block — de balk ontsnapt
			// aan de rail en marges van zijn voorouders (tweakers' menubalk:
			// left:0; width:100%, midden in hun gecentreerde page-grid).
			st.indent, st.rIndent = 0, 0
		}
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
			// :link-default: onderstreept — tenzij de site text-decoration
			// zet (dan beslist de cascade hieronder).
			if _, ok := cp["text-decoration"]; !ok {
				st.under = true
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
		// clamp()/min()/max() eerst naar pixels (procenten tegen 16px);
		// fontScale hakt het daarna in onze drie maten.
		if strings.HasPrefix(v, "clamp(") || strings.HasPrefix(v, "min(") || strings.HasPrefix(v, "max(") {
			if n, ok := cssLenPct(v, 16); ok && n > 0 {
				v = strconv.Itoa(n) + "px"
			}
		}
		st.scale = fontScale(v, st.scale)
	}
	if v, ok := cp["text-align"]; ok {
		st.center = v == "center"
		st.right = v == "right" || v == "end"
	}
	if v, ok := cp["white-space"]; ok {
		st.pre = v == "pre"
	}
	if v, ok := cp["text-decoration"]; ok {
		st.under = strings.Contains(v, "underline")
	}
	if v, ok := cp["text-decoration-line"]; ok {
		st.under = strings.Contains(v, "underline")
	}
	if v, ok := cp["text-transform"]; ok {
		st.xform = v
	}
	// Lijsttekens: een ul geeft zijn items een bolletje, een ol een teller;
	// list-style(-type) verandert of verwijdert hem — een menu met
	// list-style:none is geen opsomming.
	switch tag {
	case "ul", "menu", "dir":
		st.marker = "-"
	case "ol":
		st.marker = "1"
		n := 0
		st.list = &n
	}
	if v, ok := cp["list-style"]; ok {
		st.marker = markerType(v, st.marker)
	}
	if v, ok := cp["list-style-type"]; ok {
		st.marker = markerType(v, st.marker)
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
		// De wortel van een sub-layout (inline-block-tegel, kolomcel) is
		// dáár al inline geplaatst — binnenin gedraagt hij zich als blok.
		if el != l.rootEl {
			isBlock = false
		}
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
	brdW, hasBrd := 1, false
	if v, ok := cp["border"]; ok {
		brdCol, brdW, hasBrd = cssBorder(v)
	}
	if v, ok := cp["border-color"]; ok {
		if c, ok := cssColor(v); ok {
			brdCol, hasBrd = c, true
		}
	}
	// Zijranden (border-left enz.): het accent-patroon van meldingen,
	// citaten en tabs — elk een eigen gekleurde strook langs het blok.
	type sideBrd struct {
		side int // 0=boven 1=rechts 2=onder 3=links
		col  color.RGBA
		w    int
	}
	var sides []sideBrd
	if isBlock {
		for i, name := range []string{"border-top", "border-right", "border-bottom", "border-left"} {
			if v, ok := cp[name]; ok {
				if c, w, on := cssBorder(v); on {
					sides = append(sides, sideBrd{side: i, col: c, w: w})
				}
			}
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
		// centreert (de klassieke artikel-kolom). De wortel van een
		// kolomcel niet: zijn width bepaalde al de célbreedte — nog eens
		// rekenen zou hem tegen de cel resolven (kwart i.p.v. helft).
		availW := l.width - 2*pad - st.indent - st.rIndent
		if availW > 64 && el != l.rootEl {
			target := availW
			if v, ok := cssLenPct(cp["width"], availW); ok && v >= 64 && v < target {
				target = v
			}
			if v, ok := cssLenPct(cp["max-width"], availW); ok && v >= 64 && v < target {
				target = v
			}
			// min-width tilt een te smal blok weer op (tot wat er past).
			if v, ok := cssLenPct(cp["min-width"], availW); ok && v > target {
				target = v
				if target > availW {
					target = availW
				}
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
		// Het grid-centreer-spoor (1fr <vast> 1fr, tweakers' page-grid):
		// de vaste middenbaan is de inhoud, de fr-flanken zijn marge —
		// hetzelfde als margin: 0 auto.
		if cp["display"] == "grid" {
			avail2 := l.width - 2*pad - st.indent - st.rIndent
			if railW := gridRailPx(cp["grid-template-columns"], avail2, cssGap(cp)); railW > 0 && railW < avail2 {
				extra := avail2 - railW
				st.indent += extra / 2
				st.rIndent += extra - extra/2
			}
		}
		// De containing block ligt wáár het blok ná zijn marges en
		// margin:auto-centrering terechtkwam — dat weten we nu pas
		// (wikipedia's cirkelcontainer: width + margin:0 auto).
		if originIdx >= 0 {
			l.origins[originIdx] = absOrigin{
				p: image.Pt(pad+st.indent, l.y),
				w: l.width - 2*pad - st.indent - st.rIndent,
				h: cssMinExtent(cp),
			}
		}
	}

	tile := l.imgs[cssURL(cp["background-image"])]
	decorated := (isBlock || tag == "body") && (st.hasBG || tile != nil || hasBrd || len(sides) > 0)
	// Padding: bij een gedecoreerd blok kleurt hij mee (binnen het vlak);
	// zonder decoratie is het gewoon lucht. Een kaart zonder expliciete
	// padding krijgt de oude kaart-default, en de rand zelf telt ook mee.
	if decorated && tag != "body" && !pd.setV && !pd.setH {
		pd = edges{t: 4, r: 6, b: 4, l: 6, setV: true, setH: true}
	}
	if hasBrd {
		pd.t, pd.r, pd.b, pd.l = pd.t+brdW, pd.r+brdW, pd.b+brdW, pd.l+brdW
	}
	for _, sb := range sides {
		switch sb.side {
		case 0:
			pd.t += sb.w
		case 1:
			pd.r += sb.w
		case 2:
			pd.b += sb.w
		case 3:
			pd.l += sb.w
		}
	}
	if !decorated && isBlock {
		topGap += pd.t
		botGap += pd.b
		st.indent += pd.l
		st.rIndent += pd.r
	}

	// white-space: nowrap — dit element breekt niet middenin: past hij niet
	// meer op de lopende regel, dan begint hij op een verse (labels,
	// knoppen, prijzen). Langer dan een hele regel: dan wrapt hij alsnog.
	if cp["white-space"] == "nowrap" && l.x > 0 {
		txt := strings.Join(strings.Fields(renderableText(el)), " ")
		if tw := textW(ascii(txt), st.scale); tw > 0 &&
			l.x+charW(st.scale)+tw > l.lineRight(st.rIndent) &&
			tw <= l.lineRight(st.rIndent)-l.lineLeft(st.indent) {
			l.breakLine()
		}
	}

	// display:inline-block mét een expliciete breedte is een mini-blok ín
	// de regelflow — het float:left-gevoel: tegels naast elkaar zolang het
	// past. (De sprite-replacement hierboven ving de lege varianten al.)
	if !isBlock && cp["display"] == "inline-block" {
		availIB := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
		if w, ok := cssLenPct(cp["width"], availIB); ok && w >= 24 && w <= availIB {
			l.inlineBlock(el, w, st)
			return
		}
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
		l.boxes = append(l.boxes, Box{BG: st.bg, HasBG: st.hasBG, Border: brdCol, HasBrd: hasBrd, Rad: cssRadius(cp["border-radius"])})
		ibX0, ibY0 = l.x, l.y
		l.x += pd.l
		l.space = false
		st.hasBG = false // de inhoud ligt al óp de doos
	}

	blockY0 := 0
	if isBlock {
		l.blockGap(topGap)
		l.depth++
		blockY0 = l.y + l.gap // de blok-top, inclusief de nog hangende marge
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
		box := Box{BG: st.bg, HasBG: st.hasBG, Border: brdCol, HasBrd: hasBrd, BrdW: brdW, Rad: cssRadius(cp["border-radius"])}
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

	// margin-left: auto in een rij absorbeert de vrije ruimte: het item
	// (gethops header-nav) hoort tegen de rechterrand. Auto aan béíde
	// kanten (margin-inline: auto — tweakers' mobiele logo) is centreren,
	// dus half zo ver.
	pushRight := -1
	pushHalf := false
	if mar.autoL && !isBlock && st.inline {
		pushRight = len(l.boxes)
		pushHalf = mar.autoR
	}

	if tag == "li" && isBlock && !st.blockify && st.marker != "" {
		// In een grid/flex-cel is een <li> een kaart, geen lijstitem.
		m := st.marker
		if m == "1" {
			n := 1
			if st.list != nil {
				*st.list++
				n = *st.list
			}
			m = strconv.Itoa(n) + "."
		}
		l.word(m, st)
		l.space = true
	}
	if inlined {
		l.space = true
	}
	// Kolommen: een flex-rij met blok-kinderen (foto naast kop), een grid
	// met begrijpelijke tracks, of een tabel — elke cel een eigen
	// sub-layout naast elkaar. Lukt dat niet (of is de rij uit balans),
	// dan stapelen de cellen als blokken; zonder plan de gewone flow.
	jcIdx := -1
	var jcStarts []int
	var jcSelf []string
	if rows, gap := l.columnPlan(el, cp, st, tag); rows != nil {
		rowGap := cssRowGap(cp)
		for ri, row := range rows {
			if ri > 0 && rowGap > 0 {
				l.y += rowGap
			}
			rowG := gap
			if row.gap >= 0 {
				rowG = row.gap // maat-rijen: de échte site-gap (vaak 0)
			}
			if !l.columns(row.cells, row.w, rowG, st, cp["justify-content"], cp["align-items"], cp["justify-items"]) {
				cst := childSt
				cst.inline, cst.blockify = false, true
				for _, cell := range row.cells {
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
	} else if kids := elementChildren(el); childInline && len(kids) > 0 && !hasDirectText(el) &&
		(cp["display"] == "flex" || cp["display"] == "inline-flex") {
		// Een inline flex-rij: kinderen in order-volgorde leggen en per kind
		// onthouden waar hij begon — justify-content verdeelt er straks de
		// vrije ruimte mee en align-items lijnt de kinderen verticaal uit.
		jcIdx = len(l.boxes)
		for _, c := range l.flexOrder(kids) {
			jcStarts = append(jcStarts, len(l.boxes))
			jcSelf = append(jcSelf, l.propsOf(c)["align-self"])
			l.walk(c, childSt)
			// Een kind dat niets renderde (een svg-icoon) doet niet mee in
			// de verdeling — anders centreert space-between het logo rond
			// onzichtbare buren (tweakers' hamburger en usermenu).
			if n := len(jcStarts); jcStarts[n-1] == len(l.boxes) {
				jcStarts, jcSelf = jcStarts[:n-1], jcSelf[:n-1]
			}
		}
	} else {
		for c := el.FirstChild; c != nil; c = c.NextSibling {
			l.walk(c, childSt)
		}
	}
	if inlined {
		l.space = true
	}
	if pushRight >= 0 && len(l.boxes) > pushRight {
		maxX := 0
		for i := pushRight; i < len(l.boxes); i++ {
			if x := l.boxes[i].R.Max.X; x > maxX {
				maxX = x
			}
		}
		if shift := l.lineRight(st.rIndent) - maxX; shift > 0 {
			if pushHalf {
				shift /= 2
			}
			for i := pushRight; i < len(l.boxes); i++ {
				l.boxes[i].R = l.boxes[i].R.Add(image.Pt(shift, 0))
			}
			l.x += shift
		}
	}
	if jcIdx >= 0 && len(l.boxes) > jcIdx {
		if gs := l.rowGroups(jcIdx, jcStarts); gs != nil {
			if jc := cp["justify-content"]; jc != "" {
				l.justify(jc, gs, st)
			}
			if ai := cp["align-items"]; ai != "" {
				l.alignRow(ai, gs, jcSelf)
			}
		}
	}
	if ibIdx >= 0 {
		if len(l.boxes) == ibIdx+1 {
			// Leeg maar mét een eigen maat: dan ís het vlak de inhoud —
			// carrousel-stipjes, statuslampjes, kleurstalen.
			w, wok := cssLen(cp["width"])
			h, hok := cssLen(cp["height"])
			if wok && hok && w > 0 && h > 0 {
				doos := image.Rect(ibX0, ibY0, ibX0+w, ibY0+h)
				l.boxes[ibIdx].R = doos
				l.x = doos.Max.X + mar.r
				if h > l.lineH {
					l.lineH = h
				}
				l.space = true
			} else {
				l.boxes = l.boxes[:ibIdx] // lege doos: weg ermee
			}
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
		// height/min-height reserveren óók zonder decoratie ruimte:
		// wikipedia's talencirkel-container (alle kinderen absoluut, de
		// hoogte komt uit de CSS) en kale spacer-divs. Nooit inkrimpen.
		// Gedecoreerde blokken doen dit bij hun vlak (hieronder).
		if bgIdx < 0 {
			if minE := cssMinExtent(cp); minE > 0 {
				l.breakLine()
				l.flushGap()
				if l.y < blockY0+minE {
					l.y = blockY0 + minE
				}
			}
		}
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
				// min-height (en een expliciete height) rekken het vlak op —
				// hero's en stroken met ademruimte. Nooit inkrimpen (inhoud
				// wint), met een cap tegen 100vh-achtige uitschieters.
				if minE := cssMinExtent(cp); minE > 0 && l.y+pd.b < bgY0+minE {
					l.y = bgY0 + minE - pd.b
				}
				l.y += pd.b
			}
			// Verticaal exact de blokgrenzen: de binnenmarge zit er al in,
			// en ±2 zou aangrenzende kaarten laten overlappen.
			l.boxes[bgIdx].R = image.Rect(bgX0, bgY0, bgX1, l.y)
			// Zijranden: gekleurde stroken langs de vlakranden (de padding
			// hierboven hield er al ruimte voor vrij).
			for _, sb := range sides {
				s := l.boxes[bgIdx].R
				switch sb.side {
				case 0:
					s.Max.Y = s.Min.Y + sb.w
				case 1:
					s.Min.X = s.Max.X - sb.w
				case 2:
					s.Min.Y = s.Max.Y - sb.w
				case 3:
					s.Max.X = s.Min.X + sb.w
				}
				l.boxes = append(l.boxes, Box{R: s, Col: sb.col, Rule: true})
			}
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

// rowGroup is één flex-kind op een inline rij: zijn boxrange en zijn
// omhullende rechthoek (een knop-doos mét zijn tekst is één groep).
type rowGroup struct {
	i0, i1 int
	r      image.Rectangle
}

// rowGroups bouwt de kind-groepen van een inline flex-rij uit de
// startmarkers. nil als de rij niet op één regel bleef (elke groep hoort
// bovenaan dezelfde regel te beginnen) — gewrapte regels zijn al vol.
func (l *layouter) rowGroups(from int, starts []int) []rowGroup {
	if len(starts) == 0 {
		return nil
	}
	gs := make([]rowGroup, len(starts))
	for gi, g0 := range starts {
		end := len(l.boxes)
		if gi+1 < len(starts) {
			end = starts[gi+1]
		}
		r := l.boxes[g0].R
		for i := g0 + 1; i < end; i++ {
			r = r.Union(l.boxes[i].R)
		}
		gs[gi] = rowGroup{i0: g0, i1: end, r: r}
	}
	top := gs[0].r.Min.Y
	for _, g := range gs {
		if g.r.Min.Y != top {
			return nil
		}
	}
	return gs
}

// justify verdeelt de vrije regelruimte van een inline flex-rij volgens
// justify-content: center schuift de rij op, flex-end tegen de rechterrand,
// space-between/around/evenly spreiden de kinderen.
func (l *layouter) justify(jc string, gs []rowGroup, st style) {
	maxX := 0
	for _, g := range gs {
		if g.r.Max.X > maxX {
			maxX = g.r.Max.X
		}
	}
	free := l.lineRight(st.rIndent) - maxX
	if free <= 0 {
		return
	}
	shift := func(g rowGroup, d int) {
		for i := g.i0; i < g.i1; i++ {
			l.boxes[i].R = l.boxes[i].R.Add(image.Pt(d, 0))
		}
	}
	n := len(gs)
	switch jc {
	case "center":
		for _, g := range gs {
			shift(g, free/2)
		}
		l.x += free / 2
	case "flex-end", "end", "right":
		for _, g := range gs {
			shift(g, free)
		}
		l.x += free
	case "space-between", "space-around", "space-evenly":
		if n < 2 && jc == "space-between" {
			return
		}
		pos := func(i int) int {
			switch jc {
			case "space-between":
				return free * i / (n - 1)
			case "space-around":
				return free * (2*i + 1) / (2 * n)
			default: // space-evenly
				return free * (i + 1) / (n + 1)
			}
		}
		for i, g := range gs {
			shift(g, pos(i))
		}
		l.x += pos(n - 1)
	}
}

// alignRow: align-items op een inline flex-rij — het hoogste kind bepaalt
// de rijhoogte (dat doet de regel al), de rest centreert of hangt aan de
// onderkant; align-self per kind wint. baseline benadert flex-end: bij één
// fontfamilie liggen de baselines onderin.
func (l *layouter) alignRow(ai string, gs []rowGroup, selves []string) {
	rowH := 0
	for _, g := range gs {
		if h := g.r.Dy(); h > rowH {
			rowH = h
		}
	}
	for gi, g := range gs {
		a := ai
		if gi < len(selves) && selves[gi] != "" && selves[gi] != "auto" {
			a = selves[gi]
		}
		d := 0
		switch a {
		case "center":
			d = (rowH - g.r.Dy()) / 2
		case "flex-end", "end", "baseline", "last baseline":
			d = rowH - g.r.Dy()
		}
		if d > 0 {
			for i := g.i0; i < g.i1; i++ {
				l.boxes[i].R = l.boxes[i].R.Add(image.Pt(0, d))
			}
		}
	}
}

// flexOrder sorteert flex/grid-items op hun order-property (stabiel, de
// DOM-volgorde als tiebreaker). Vrijwel altijd een no-op.
func (l *layouter) flexOrder(items []*html.Node) []*html.Node {
	ordered := false
	for _, it := range items {
		if cssOrder(l.propsOf(it)) != 0 {
			ordered = true
			break
		}
	}
	if !ordered {
		return items
	}
	out := append([]*html.Node{}, items...)
	sort.SliceStable(out, func(i, j int) bool {
		return cssOrder(l.propsOf(out[i])) < cssOrder(l.propsOf(out[j]))
	})
	return out
}

// propsOf: de computed props van een element — de stylesheet-match plus het
// inline style=""-attribuut (dat wint altijd).
func (l *layouter) propsOf(el *html.Node) props {
	cp := l.styles[el]
	if inline, ok := attr(el, "style"); ok && inline != "" {
		if d := parseDecls(inline); d != nil {
			m := make(props, len(cp)+len(d))
			for k, v := range cp {
				m[k] = v
			}
			for k, v := range d {
				m[k] = v
			}
			return m
		}
	}
	return cp
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
			case "svg":
				// Sinds we svg rasteren is een svg-logo échte inhoud: het
				// logo-slot hoeft hem niet meer te vervangen — mits hij een
				// maat heeft, anders valt er niets te rasteren.
				if svgRenderable(c) {
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

// cellVisible: gaat deze flex-cel iets laten zien? Een verborgen kind
// (display:none-hamburger, sr-only-kop) is geen flex-item; verder telt
// renderbare inhoud, of een vulbaar logo-slot (voorpagina-link +
// site-icoon) — anders filtert de cel weg vóórdat het slot gevuld kan
// worden.
func (l *layouter) cellVisible(n *html.Node) bool {
	if cp := l.propsOf(n); cp["display"] == "none" || cp["visibility"] == "hidden" || cp[srProp] == "1" {
		return false
	}
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

// colRow is één kolommenrij uit columnPlan: de cellen en hun breedtes —
// per rij, want een grid-cel met een grid-column-span is breder dan zijn
// eigen track.
type colRow struct {
	cells []*html.Node
	w     []int
	gap   int // -1: de standaard kolom-gap; anders expliciet (maat-rijen: 0)
}

// columnPlan beslist of dit element als kolommen rendert en hoe breed die
// worden: een tabel (rijen van td/th-cellen), een grid (tracks uit
// grid-template-columns) of een flex-rij met blok-kinderen. nil = gewone
// flow. Menu's (flex-rij vol linkjes) blijven bewust inline.
func (l *layouter) columnPlan(el *html.Node, cp props, st style, tag string) ([]colRow, int) {
	availW := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
	if availW < 320 {
		return nil, 0 // te smal om te verdelen: stapelen leest beter
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
	sameW := func(rows [][]*html.Node, colW []int) []colRow {
		out := make([]colRow, len(rows))
		for i, r := range rows {
			out[i] = colRow{cells: r, w: colW, gap: -1}
		}
		return out
	}
	switch {
	case tag == "table":
		// Rijen van td/th-cellen; colspan telt mee in de kolomtelling en
		// geeft de cel de breedte van zijn overspannen kolommen.
		type trow struct {
			cells []*html.Node
			spans []int
		}
		var rows []trow
		ncol := 0
		var walkT func(n *html.Node)
		walkT = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "tr" {
				var row trow
				total := 0
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
						s := 1
						if v, ok := attr(c, "colspan"); ok {
							if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 1 && n <= 6 {
								s = n
							}
						}
						row.cells = append(row.cells, c)
						row.spans = append(row.spans, s)
						total += s
					}
				}
				if len(row.cells) > 0 {
					rows = append(rows, row)
					if total > ncol {
						ncol = total
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
			return nil, 0 // één kolom of te veel: stapelen leest beter
		}
		// Gedeclareerde kolombreedtes: een cel met een CSS-width (of het
		// ouderwetse width-attribuut — presentational hint) pint zijn
		// kolom; de overige kolommen delen wat overblijft. Zonder
		// declaraties (of als het niet past): gelijke kolommen.
		decl := make([]int, ncol)
		for _, r := range rows {
			t := 0
			for i, c := range r.cells {
				if r.spans[i] == 1 && t < ncol && decl[t] == 0 {
					if v, ok := cssLenPct(l.propsOf(c)["width"], availW); ok && v >= 48 && v < availW {
						decl[t] = v
					}
				}
				t += r.spans[i]
			}
		}
		base := equal(ncol)
		nvrij, som := 0, 0
		for _, d := range decl {
			som += d
			if d == 0 {
				nvrij++
			}
		}
		if som > 0 {
			if rest := availW - gap*(ncol-1) - som; nvrij == 0 && rest >= 0 {
				base = decl
			} else if nvrij > 0 && rest/nvrij >= 100 {
				base = make([]int, ncol)
				for i, d := range decl {
					if d == 0 {
						d = rest / nvrij
					}
					base[i] = d
				}
			}
		}
		if base != nil {
			out := make([]colRow, len(rows))
			for i, r := range rows {
				cr := colRow{cells: r.cells, gap: -1}
				t := 0
				for _, s := range r.spans {
					if s > ncol-t {
						s = ncol - t
					}
					w := gap * (s - 1)
					for k := 0; k < s; k++ {
						w += base[t+k]
					}
					cr.w = append(cr.w, w)
					t += s
				}
				out[i] = cr
			}
			return out, gap
		}
	case cp["display"] == "grid":
		// Het centreer-spoor is géén kolommenset: element() schuift de
		// inhoud al naar de middenbaan, de kinderen stapelen daar.
		if gridRailPx(cp["grid-template-columns"], availW, gap) > 0 {
			return nil, 0
		}
		// grid-template-areas: benoemde gebieden — de rijen komen
		// letterlijk uit de template ("kop kop" / "zij hoofd"), de
		// kolombreedtes uit de tracks; een naam die kolommen herhaalt
		// spant die tracks (het holy-grail-patroon). Rowspans (zelfde naam
		// in meerdere rijen) of gaten kan het rijen-model niet aan — dan
		// liever eerlijk stapelen dan half plaatsen.
		if areas := gridAreas(cp["grid-template-areas"]); areas != nil {
			tracks := gridTracks(cp["grid-template-columns"], availW, gap)
			if tracks == nil || len(tracks) != len(areas[0]) {
				tracks = equal(len(areas[0]))
			}
			if tracks != nil {
				byName := map[string]*html.Node{}
				for _, it := range elementChildren(el) {
					if n := l.propsOf(it)["grid-area"]; n != "" {
						byName[n] = it
					}
				}
				seen := map[string]int{}
				var rows []colRow
				ok := len(byName) > 0
				for ri, row := range areas {
					cr := colRow{gap: -1}
					for c := 0; c < len(row); {
						name := row[c]
						span := 1
						for c+span < len(row) && row[c+span] == name {
							span++
						}
						if r0, was := seen[name]; was && r0 != ri {
							ok = false // rowspan: buiten ons rijen-model
						}
						seen[name] = ri
						it := byName[name]
						if it == nil {
							ok = false // gat ("." of naam zonder element)
						} else {
							w := gap * (span - 1)
							for k := 0; k < span && c+k < len(tracks); k++ {
								w += tracks[c+k]
							}
							cr.cells = append(cr.cells, it)
							cr.w = append(cr.w, w)
						}
						c += span
					}
					if len(cr.cells) > 0 {
						rows = append(rows, cr)
					}
				}
				if ok && len(rows) > 0 {
					return rows, gap
				}
			}
		}
		items := elementChildren(el)
		tracks := gridTracks(cp["grid-template-columns"], availW, gap)
		if tracks == nil && strings.HasPrefix(cp["grid-auto-flow"], "column") && len(items) >= 2 {
			// grid-auto-flow: column zonder template: elk item zijn eigen
			// kolom — één rij naast elkaar (tweakers' categoriebalk). Te
			// smal per cel: dan stapelen (zo doet hun mobiel het ook).
			if w := (availW - gap*(len(items)-1)) / len(items); w >= 48 {
				colW := make([]int, len(items))
				for i := range colW {
					colW[i] = w
				}
				return []colRow{{cells: items, w: colW, gap: -1}}, gap
			}
		}
		if tracks == nil || len(items) < 2 {
			return nil, 0
		}
		// Plaatsen mét grid-column-spans: een cel die (via "1 / -1" of
		// "span N") meer tracks pakt krijgt de breedte van die tracks; past
		// hij niet meer op de lopende rij, dan begint hij een nieuwe.
		spanW := func(t, s int) int {
			w := gap * (s - 1)
			for k := 0; k < s; k++ {
				w += tracks[t+k]
			}
			return w
		}
		var rows []colRow
		cur := colRow{gap: -1}
		t := 0
		for _, it := range items {
			s := gridSpan(l.propsOf(it), len(tracks))
			if t+s > len(tracks) && t > 0 {
				rows = append(rows, cur)
				cur, t = colRow{gap: -1}, 0
			}
			cur.cells = append(cur.cells, it)
			cur.w = append(cur.w, spanW(t, s))
			t += s
			if t >= len(tracks) {
				rows = append(rows, cur)
				cur, t = colRow{gap: -1}, 0
			}
		}
		if len(cur.cells) > 0 {
			rows = append(rows, cur)
		}
		return rows, gap
	case cp["display"] == "flex" || cp["display"] == "inline-flex":
		fd := cp["flex-direction"]
		if fd == "column" || fd == "column-reverse" {
			return nil, 0 // een kolom stapelt — dat doet de gewone flow al
		}
		if hasDirectText(el) {
			return nil, 0
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
		items = l.flexOrder(items)
		if fd == "row-reverse" {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		if len(items) < 2 {
			return nil, 0
		}
		blockish := false
		for _, it := range items {
			if blocks[it.Data] || it.Data == "img" || it.Data == "picture" || it.Data == "video" {
				blockish = true
			}
		}
		if !blockish {
			return nil, 0 // allemaal linkjes: dat is een menu
		}
		// Wrappen: bij expliciete flex-wrap, of bij veel items mét een
		// eigen maat (width/flex-basis, vaak calc(50% - marge) — NRC): die
		// maat bepaalt hoeveel er per rij passen, echte flex-wrap. Zónder
		// wrap en zonder maten is nowrap de default — veel kale linkjes
		// (tweakers' menubalk) zijn een menu, geen kaartenraster.
		wrapDeclared := strings.HasPrefix(cp["flex-wrap"], "wrap")
		if rows := l.sizedWrapRows(items, availW, cp); (rows != nil && (wrapDeclared || len(items) > 4)) || wrapDeclared {
			if rows == nil {
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
					return nil, 0
				}
				var chunks [][]*html.Node
				for i := 0; i < len(items); i += cols {
					end := i + cols
					if end > len(items) {
						end = len(items)
					}
					chunks = append(chunks, items[i:end])
				}
				rows = sameW(chunks, colW)
			}
			// wrap-reverse: de rijen stapelen van onder naar boven.
			if cp["flex-wrap"] == "wrap-reverse" {
				for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
					rows[i], rows[j] = rows[j], rows[i]
				}
			}
			return rows, gap
		}
		// Eén rij: breedtes uit width/flex-basis/flex-grow. Zonder expliciete
		// rij-declaratie én zonder maat-signalen niet committen: inhoud-
		// gemeten items naast elkaar is precies wat de inline-flow al doet
		// (mét auto-marges), en een header is geen kaartenrij.
		explicitRow := fd == "row" || fd == "row-reverse" || cp["flex-wrap"] != ""
		colW, sized := l.flexTracks(items, availW, gap)
		if colW != nil && (explicitRow || sized) {
			return []colRow{{cells: items, w: colW, gap: -1}}, gap
		}
		if explicitRow {
			if colW := equal(len(items)); colW != nil {
				return []colRow{{cells: items, w: colW, gap: -1}}, gap
			}
		}
	}
	return nil, 0
}

// sizedWrapRows pakt wrap-items met een eigen breedte (width/flex-basis)
// in rijen zoals echte flex-wrap: zoveel als er passen. Elke cel krijgt de
// búitenmaat (breedte + marges) — het item positioneert zich daarbinnen
// met zijn eigen marges, precies zoals in de flow. De gap telt alleen als
// de site hem echt declareert (de 8px-default is van ons; marges zitten
// al in de maat). nil zodra een item geen (bruikbare) maat heeft — dan is
// de kaartmaat-heuristiek aan zet.
func (l *layouter) sizedWrapRows(items []*html.Node, availW int, cp props) []colRow {
	packGap := 0
	if cp["gap"] != "" || cp["column-gap"] != "" {
		packGap = cssGap(cp)
	}
	type sized struct {
		n   *html.Node
		eff int
	}
	var its []sized
	for _, n := range items {
		icp := l.propsOf(n)
		_, basis := flexItem(icp, availW)
		if basis < 0 {
			if v, ok := cssLenPct(icp["width"], availW); ok && v > 0 {
				basis = v
			}
		}
		if basis < 90 || basis > availW {
			return nil
		}
		mar := cssEdgesOf(icp, "margin", 96)
		its = append(its, sized{n: n, eff: basis + mar.l + mar.r})
	}
	var rows []colRow
	cur := colRow{gap: packGap}
	x := 0
	for _, s := range its {
		adv := s.eff
		if len(cur.cells) > 0 {
			adv += packGap
		}
		if len(cur.cells) > 0 && x+adv > availW {
			rows = append(rows, cur)
			cur, x = colRow{gap: packGap}, 0
			adv = s.eff
		}
		cur.cells = append(cur.cells, s.n)
		cur.w = append(cur.w, s.eff)
		x += adv
	}
	if len(cur.cells) > 0 {
		rows = append(rows, cur)
	}
	return rows
}

// flexTracks: kolombreedtes voor één flex-rij — vaste maten (width of
// flex-basis) eerst, de vrije ruimte naar rato van flex-grow; een item
// zonder maat of gewicht deelt mee als gewicht 1 (zijn content-maat kennen
// we hier niet). sized zegt of er überhaupt een expliciet maat-signaal
// stond; nil als het niet past of te smal wordt.
func (l *layouter) flexTracks(items []*html.Node, availW, gap int) (colW []int, sized bool) {
	out := make([]int, len(items))
	weight := make([]float64, len(items))
	free := availW - gap*(len(items)-1)
	totW := 0.0
	for i, it := range items {
		cp := l.propsOf(it)
		g, basis := flexItem(cp, availW)
		if basis < 0 {
			if v, ok := cssLenPct(cp["width"], availW); ok && v > 0 {
				basis = v
			}
		}
		if basis > 0 {
			out[i] = basis
			free -= basis
			sized = true
		}
		weight[i] = g
		if g > 0 {
			sized = true
		}
		totW += weight[i]
	}
	// Zonder maat en zonder grow is een flex-item content-sized (flex-grow
	// is per spec 0!) — meten dus, in plaats van de rij vol te delen: zo
	// blijft er échte vrije ruimte over en heeft justify-content iets te
	// verdelen (easyflorists space-between-header: menu links, knoppen
	// uiterst rechts).
	for i, it := range items {
		if out[i] == 0 && weight[i] == 0 {
			w := l.measureCell(it, free)
			if w < 16 {
				w = 16
			}
			out[i] = w
			free -= w
		}
	}
	if free < 0 {
		return nil, sized // past niet naast elkaar
	}
	for i := range out {
		if weight[i] > 0 && totW > 0 {
			out[i] += int(float64(free) * weight[i] / totW)
			if out[i] < 90 {
				return nil, sized // een grow-kolom hoort ruimte te hebben
			}
		}
	}
	return out, sized
}

// measureCell: de natuurlijke (max-content-achtige) breedte van een
// flex-item — een proeflayout op de beschikbare ruimte, gemeten op wat er
// echt staat. Menu's en knoppenrijtjes zijn zo breed als hun inhoud.
func (l *layouter) measureCell(cell *html.Node, avail int) int {
	if avail < 24 {
		return 0
	}
	sub := l.subLayout(cell, avail, style{scale: 1, col: colText, marker: "-"}, false)
	uMin, uMax := subExtent(sub, false)
	if uMax <= uMin {
		return 0
	}
	// +2×pad: de cel-sub legt zijn inhoud tussen de pad-kantlijnen — die
	// marge moet mee in de celbreedte, anders wrapt de cel-layout nét.
	w := uMax - uMin + 2*pad
	if w > avail {
		w = avail
	}
	return w
}

// columns legt één rij cellen naast elkaar: elke cel zijn eigen
// sub-layouter op kolombreedte, daarna verschoven naar zijn kolom-x. De
// rij wordt zo hoog als de hoogste cel; justify-content verdeelt de vrije
// rijruimte (als de kolommen de rij niet vullen) en align-items/-self
// bepaalt waar een lagere cel verticaal hangt. Eerst wordt speculatief
// gelegd: zijn de celhoogtes wild uit balans, dan is dit geen kaartenrij
// maar pagina-steigerwerk (een titelblokje naast een eindeloze
// nieuwskolom) — dan géén commit (false) en stapelt de aanroeper gewoon.
func (l *layouter) columns(cells []*html.Node, colW []int, gap int, st style, jc, ai, ji string) bool {
	subs := make([]*layouter, len(cells))
	maxH, minH := 0, 1<<30
	for i, cell := range cells {
		sub := l.subLayout(cell, colW[i%len(colW)], st, false)
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
	// justify-content: kolommen met vaste maten vullen de rij niet — de
	// vrije ruimte wordt verdeeld (rijen uit flexTracks hebben die zelden).
	total := gap * (len(cells) - 1)
	for i := range cells {
		total += colW[i%len(colW)]
	}
	if free := l.lineRight(st.rIndent) - x0 - total; free > 0 {
		n := len(cells)
		switch jc {
		case "center":
			x0 += free / 2
		case "flex-end", "end", "right":
			x0 += free
		case "space-between":
			if n > 1 {
				gap += free / (n - 1)
			}
		case "space-around":
			x0 += free / (2 * n)
			gap += free / n
		case "space-evenly":
			x0 += free / (n + 1)
			gap += free / (n + 1)
		}
	}
	y0 := l.y
	cx := x0
	for i, sub := range subs {
		// align-items (align-self van de cel wint): waar hangt een lagere
		// cel in de rij? De default (stretch) rekt zijn kaartvlak op.
		a := ai
		if v, ok := l.propsOf(cells[i])["align-self"]; ok && v != "auto" {
			a = v
		}
		dy := 0
		switch a {
		case "center":
			dy = (maxH - sub.y) / 2
		case "flex-end", "end", "baseline", "last baseline":
			dy = maxH - sub.y
		}
		// align-items: stretch (de flex-default): een kaartvlak dat de
		// hele cel besloeg groeit mee tot de rijhoogte — gelijke kaarten.
		if a == "" || a == "stretch" || a == "normal" {
			for k := range sub.boxes {
				b := &sub.boxes[k]
				if b.Text == "" && b.Img == nil && (b.HasBG || b.HasBrd || b.Tile != nil) &&
					b.R.Min.Y <= 4 && b.R.Max.Y >= sub.y-4 {
					b.R.Max.Y = maxH
				}
			}
		}
		// justify-items (justify-self van de cel wint): bij center/end
		// krimpt het item naar zijn inhoud (shrink-to-fit) en schuift het
		// binnen zijn cel — grid-knoppen midden of tegen de celrand.
		j, shrink := ji, true
		if v, ok := l.propsOf(cells[i])["justify-self"]; ok && v != "auto" {
			j = v
		}
		// space-between/end: het láátste item raakt de containerrand (echte
		// flex) — de gemeten cel houdt anders zijn pad-marges als kier.
		// Zonder klemmen: een kaart die zijn cel vult blijft gewoon staan.
		if j == "" && i == len(subs)-1 && (jc == "space-between" || jc == "flex-end" || jc == "end" || jc == "right") {
			j, shrink = "end", false
		}
		dx := 0
		if j == "center" || j == "end" || j == "flex-end" || j == "right" {
			m0, m1 := subExtent(sub, !shrink)
			if free := colW[i%len(colW)] - (m1 - m0); free > 0 {
				dx = free
				if j == "center" {
					dx = free / 2
				}
			}
		}
		// sub begint op zijn eigen pad-marge
		l.adopt(sub, image.Pt(cx-pad+dx, y0+dy), false)
		cx += colW[i%len(colW)] + gap
	}
	l.y = y0 + maxH
	l.x, l.lineH, l.space = 0, 0, false
	l.line0 = len(l.boxes)
	return true
}

// inlineBlock legt een display:inline-block met expliciete breedte als
// mini-blok in de regelflow: een eigen sub-layout, geplaatst als een
// (groot) woord — naast elkaar zolang het past (tegels, taalvakken).
func (l *layouter) inlineBlock(el *html.Node, w int, st style) {
	sub := l.subLayout(el, w, st, false)
	if sub.y < 1 || len(sub.boxes) == 0 {
		return
	}
	l.flushGap()
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
	l.adopt(sub, image.Pt(x-pad, l.y), false)
	l.x = x + w
	if sub.y > l.lineH {
		l.lineH = sub.y
	}
	l.space = false
	l.alignLine(st)
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
	max := l.lineRight(st.rIndent) - l.lineLeft(st.indent)
	// De site-CSS bepaalt de veldbreedte als hij dat wil (wikipedia's
	// zoekbalk: width 100%); anders het size-attribuut.
	if v, ok := cssLenPct(l.propsOf(el)["width"], max); ok && v >= 40 {
		w = v
	}
	if w > max {
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
	l.alignLine(st)
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
	'­': "", // zacht koppelteken: onzichtbaar tot je hem nodig hebt (nooit, bij ons)
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
	switch st.xform {
	case "uppercase":
		w = strings.ToUpper(w)
	case "lowercase":
		w = strings.ToLower(w)
	case "capitalize":
		if len(w) > 0 && w[0] >= 'a' && w[0] <= 'z' {
			w = string(w[0]-32) + w[1:]
		}
	}
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
		Under: st.under,
		BG:    st.bg,
		HasBG: st.hasBG,
	})
	l.x = x + ww
	if h := charH(st.scale); h > l.lineH {
		l.lineH = h
	}
	l.space = false
	l.alignLine(st)
}

// image plaatst een afbeelding in de flow, als een (groot) woord: past hij
// nog op de regel dan inline, anders op een nieuwe. Breder dan de pagina →
// proportioneel verkleind; het schalen gebeurt hier (één keer per layout),
// renderen is daarna een kale draw.Draw.
func (l *layouter) image(m image.Image, st style) {
	l.imageSized(m, m.Bounds().Dx(), m.Bounds().Dy(), st, false)
}

// imgSize: de weergavemaat van een <img> — CSS width/height wint van de
// width/height-attributen (kale pixels, zoals HTML ze definieert); één
// gegeven maat schaalt de andere proportioneel mee. Zonder aanwijzing de
// eigen maat. CSS "auto" schakelt het attribuut úit (het klassieke
// img{height:auto}-reset: de verhouding komt uit het beeld) — en een
// hoogte-procent heeft bij ons geen basis (de ouderhoogte is onbekend),
// dus die telt als auto. Bronnen mengen gaf eieren (wikipedia's logo:
// CSS width:57px naast het height=183-attribuut).
func imgSize(el *html.Node, cp props, iw, ih, avail int) (int, int) {
	side := func(name string, pctBase int) int {
		if v, ok := cp[name]; ok {
			v = strings.TrimSpace(v)
			if v == "auto" || (pctBase <= 0 && strings.Contains(v, "%")) {
				return 0 // afleiden uit de andere maat; het attribuut vervalt
			}
			if n, ok := cssLenPct(v, pctBase); ok && n > 0 {
				return n
			}
		}
		if v, ok := attr(el, name); ok {
			v = strings.TrimSuffix(strings.TrimSpace(v), "px")
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return n
			}
		}
		return 0
	}
	w, h := side("width", avail), side("height", 0)
	// aspect-ratio maakt de ontbrekende maat af als er maar één gegeven is.
	if v, ok := cp["aspect-ratio"]; ok && (w > 0) != (h > 0) {
		if rn, rd, ok := cssRatio(v); ok {
			if w > 0 {
				h = int(float64(w) * rd / rn)
			} else {
				w = int(float64(h) * rn / rd)
			}
			return w, h
		}
	}
	switch {
	case w > 0 && h > 0:
		return w, h
	case w > 0 && iw > 0:
		return w, ih * w / iw
	case h > 0 && ih > 0:
		return iw * h / ih, h
	}
	return iw, ih
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
	m := l.imgs[src]
	// Sprite-sheets: background-position knipt het juiste plaatje eruit
	// (wikipedia's wordmark en zuster-iconen leven in één svg-sheet).
	if crop := spriteCrop(m, cp, w, h); crop != nil {
		return crop, w, h
	}
	return m, w, h
}

// spriteCrop knipt het background-position-venster (w×h) uit een sprite-
// sheet, met de sheet eerst op background-size geschaald als dat er staat.
// nil = geen (bruikbare) positie: dan is het gewoon een achtergrond.
func spriteCrop(m image.Image, cp props, w, h int) image.Image {
	px, py, ok := cssPairSigned(cp["background-position"])
	if !ok {
		return nil
	}
	sheet := m
	if bw, bh, ok := cssPairSigned(cp["background-size"]); ok && bw > 0 && bh > 0 {
		sheet = scaleTo(m, bw, bh)
	}
	b := sheet.Bounds()
	if px == 0 && py == 0 && b.Dx() <= w && b.Dy() <= h {
		// positie 0 0 op een passend beeld: gewoon een achtergrond. Op een
		// gróter vel is het wél een crop — het eerste plaatje van de sheet
		// staat nu eenmaal op (0,0) (wikipedia's Commons-logo).
		return nil
	}
	r := image.Rect(b.Min.X-px, b.Min.Y-py, b.Min.X-px+w, b.Min.Y-py+h)
	if !r.In(b) {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), sheet, r.Min, draw.Src)
	return out
}

// imageSized plaatst een afbeelding op een gegeven maat in de flow (image
// replacement geeft de CSS-maat mee; een <img> zijn natuurlijke maat).
// cover=true snijdt beeldvullend bij (object-fit: cover) in plaats van te
// pletten.
func (l *layouter) imageSized(m image.Image, w, h int, st style, cover bool) {
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
	scaled := scaleTo(m, w, h)
	if cover {
		scaled = scaleCover(m, w, h)
	}
	maskRounded(scaled, st.rad)
	l.boxes = append(l.boxes, Box{
		R:    image.Rect(x, l.y, x+w, l.y+h),
		Href: st.href,
		Img:  scaled,
	})
	l.x = x + w
	if h > l.lineH {
		l.lineH = h
	}
	l.space = false
	l.alignLine(st)
}

// floatImage legt een afbeelding als float neer: tegen de linker- of
// rechterkant, de lopende tekst stroomt ernaast (lineLeft/lineRight) en
// valt eronder weer breed uit. Nooit meer dan ~60% van de regel breed —
// er moet tekst naast passen, anders was het geen float waard.
func (l *layouter) floatImage(m image.Image, w, h int, st style, right bool) {
	l.breakLine()
	l.flushGap()
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
	scaled := scaleTo(m, w, h)
	maskRounded(scaled, st.rad)
	l.boxes = append(l.boxes, Box{R: image.Rect(x, l.y, x+w, l.y+h), Href: st.href, Img: scaled})
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

// absolute haalt een element uit de flow: sub-layout, geplaatst op zijn
// ankers t.o.v. de containing block (de dichtstbijzijnde gepositioneerde
// voorouder, anders de pagina) en ná de flow geschilderd — badges, labels,
// overlays. Zonder expliciete width is hij shrink-to-fit: zo breed als zijn
// inhoud (een badge is geen balk). bottom-ankers komen hier via flushAbs,
// mét de dan bekende voorouder-onderkant (containerBottom; -1 = onbekend,
// dan valt bottom terug op de static position).
func (l *layouter) absolute(el *html.Node, cp props, st style, containerBottom int) {
	o := absOrigin{w: l.width - 2*pad} // zonder voorouder: de pagina
	if n := len(l.origins); n > 0 {
		o = l.origins[n-1]
	}
	l.absoluteAt(el, cp, st, containerBottom, o)
}

// fixedPanel legt een position:fixed-element tegen de viewport — dé
// containing block van fixed: (0,0), paginabreed, viewH hoog. De hoogte
// zelf (100%-calc of het top+bottom-paar) rekent cssMinExtent al tegen
// viewH. Gescrold reist het paneel mee met het document: de eerlijke
// statische lezing van "hangt in het venster".
func (l *layouter) fixedPanel(el *html.Node, cp props, st style) {
	l.absoluteAt(el, cp, st, viewH, absOrigin{p: image.Pt(pad, 0), w: l.width - 2*pad, h: viewH})
}

// absoluteAt is absolute() met een expliciete containing block (voor
// fixed: de viewport).
func (l *layouter) absoluteAt(el *html.Node, cp props, st style, containerBottom int, o absOrigin) {
	x := l.lineLeft(st.indent)
	if v, ok := cssLenSignedPct(cp["left"], o.w); ok {
		x = o.p.X + v
	}
	w := 0
	if v, ok := cssLenPct(cp["width"], o.w); ok && v > 0 {
		w = v
	}
	wExplicit := w > 0
	if v, ok := cssLenSignedPct(cp["right"], o.w); ok && cp["left"] == "" && wExplicit {
		x = o.p.X + o.w - v - w // rechts geankerd op de containing block
	}
	if w <= 0 {
		w = l.width - pad - x
	}
	if x < 0 {
		x = 0
	}
	if w < 24 || x >= l.width {
		return // niets zinnigs te leggen
	}
	sub := l.subLayout(el, w, st, true)
	uMin, uMax := subExtent(sub, wExplicit)
	if uMax <= uMin {
		return // leeg element
	}
	natW := uMax - uMin
	if v, ok := cssLenSignedPct(cp["right"], o.w); ok && cp["left"] == "" && !wExplicit {
		x = o.p.X + o.w - v - natW // rechts geankerd op de gemeten breedte
		if x < 0 {
			x = 0
		}
	}
	y := l.y
	if v, ok := anchorLen(cp["top"], o.h); ok {
		y = o.p.Y + v
	} else if v, ok := anchorLen(cp["bottom"], o.h); ok && containerBottom >= 0 {
		y = containerBottom - v - sub.y // onderkant tegen de voorouder-onderkant
	}
	if y < 0 {
		y = 0
	}
	l.adopt(sub, image.Pt(x-uMin, y), true)
}

// subLayout legt een element in zijn eigen mini-layouter van breedte w —
// dé bouwsteen voor cellen, floats, inline-blokken, metingen en
// absolutes. De tekststijl erft mee (ook text-align, zoals in echte CSS);
// de flow-context (inspringing, inline, pre) reset, en binnenin gedraagt
// alles zich als blok.
func (l *layouter) subLayout(el *html.Node, w int, st style, abs bool) *layouter {
	sub := &layouter{width: w, imgs: l.imgs, styles: l.styles, edits: l.edits, icon: l.icon, rootEl: el}
	if abs {
		sub.absEl = el
	}
	st.indent, st.rIndent = 0, 0
	st.inline, st.pre = false, false
	st.blockify = true
	sub.walk(el, st)
	sub.breakLine()
	sub.flushAbs(-1, sub.y)
	return sub
}

// verhuis neemt boxes over op hun plek in de ouder: veld-indexen herbast,
// Pin vervalt (alleen de hoofdlaag pint), alles schuift met off mee.
func verhuis(dst *[]Box, src []Box, off image.Point, fbase int) {
	for _, b := range src {
		if b.Field > 0 {
			b.Field += fbase
		}
		b.Pin = false
		b.R = b.R.Add(off)
		*dst = append(*dst, b)
	}
}

// adopt haalt een complete sub-layout binnen: boxes naar de hoofdlaag en
// late naar late — of álles naar late (absolutes schilderen bovenop).
func (l *layouter) adopt(sub *layouter, off image.Point, late bool) {
	fbase := len(l.fields)
	if late {
		verhuis(&l.late, sub.boxes, off, fbase)
	} else {
		verhuis(&l.boxes, sub.boxes, off, fbase)
	}
	verhuis(&l.late, sub.late, off, fbase)
	for _, f := range sub.fields {
		f.R = f.R.Add(off)
		l.fields = append(l.fields, f)
	}
}

// subExtent meet de inhoud van een sub-layout en krimpt (zonder expliciete
// width) vlakken die de volle sub-breedte besloegen mee — shrink-to-fit,
// met symmetrische binnenmarge. Terug: de linker- en rechterrand van wat
// er echt staat.
func subExtent(sub *layouter, wExplicit bool) (int, int) {
	cMin, cMax := 1<<30, 0
	for _, s := range [][]Box{sub.boxes, sub.late} {
		for _, b := range s {
			if b.Text == "" && b.Img == nil && b.Field == 0 {
				continue
			}
			if b.R.Min.X < cMin {
				cMin = b.R.Min.X
			}
			if b.R.Max.X > cMax {
				cMax = b.R.Max.X
			}
		}
	}
	uMin, uMax := 1<<30, -(1 << 30)
	for _, boxes := range []*[]Box{&sub.boxes, &sub.late} {
		for i := range *boxes {
			b := &(*boxes)[i]
			if !wExplicit && cMax > 0 && b.Text == "" && b.Img == nil && b.R.Max.X > cMax {
				if inzet := cMin - b.R.Min.X; inzet >= 0 && cMax+inzet < b.R.Max.X {
					b.R.Max.X = cMax + inzet
				}
			}
			if b.R.Min.X < uMin {
				uMin = b.R.Min.X
			}
			if b.R.Max.X > uMax {
				uMax = b.R.Max.X
			}
		}
	}
	return uMin, uMax
}

// floatBlock legt een gefloat element neer zoals floatImage een foto:
// tegen de linker- of rechterkant, de lopende flow stroomt ernaast
// (lineLeft/lineRight) en valt eronder weer breed uit. Met een CSS-width
// op maat, anders shrink-to-fit (een tag is geen balk). Nooit meer dan
// ~60% van de regel: er moet tekst naast passen, anders was het geen
// float waard — dan doet de gewone flow het (false).
func (l *layouter) floatBlock(el *html.Node, cp props, st style, right bool) bool {
	l.breakLine()
	l.flushGap()
	// Meetbreedte: 60% van de regel op dit niveau — er moet iets naast
	// kunnen. Actieve floats tellen hier níet mee: hoe breed een knop wil
	// zijn hangt niet af van waar hij landt (of hij pást komt ná het meten).
	base := l.width - pad - st.rIndent - pad - st.indent
	maxW := base * 3 / 5
	if maxW < 48 {
		return false // te smal om nog iets naast te zetten
	}
	mar := cssEdgesOf(cp, "margin", 96)
	w, wExplicit := maxW, false
	if v, ok := cssLenPct(cp["width"], base); ok && v >= 24 && v <= maxW {
		w, wExplicit = v, true
	}
	sub := l.subLayout(el, w, st, false)
	uMin, uMax := subExtent(sub, wExplicit)
	if uMax <= uMin {
		return false // niets gerenderd: laat de gewone flow het proberen
	}
	natW := uMax - uMin
	// Past hij niet meer naast de lopende floats, dan begint hieronder de
	// volgende rij (zo wrapt een te lange knoppenbalk).
	if natW+mar.l+mar.r+8 > l.lineRight(st.rIndent)-l.lineLeft(st.indent) && (l.fL.w > 0 || l.fR.w > 0) {
		l.clearFloats(-1)
	}
	// lineLeft/lineRight rekenen de al-actieve floats mee: de nieuwe komt
	// er gewoon naast — de float-rij (NRC's header: float:left-knoppen in
	// een float:right-balk).
	x := l.lineLeft(st.indent) + mar.l
	if right {
		x = l.lineRight(st.rIndent) - natW - mar.r
	}
	l.adopt(sub, image.Pt(x-uMin, l.y), false)
	f := flt{w: natW + mar.l + mar.r + 8, bot: l.y + sub.y + lead, depth: l.depth}
	old := l.fL
	if right {
		old = l.fR
	}
	if old.w > 0 {
		// De kant was al bezet: één gezamenlijke claim — breder, tot de
		// laagste onderkant, en hij leeft zolang het buitenste blok.
		f.w += old.w
		if old.bot > f.bot {
			f.bot = old.bot
		}
		if old.depth < f.depth {
			f.depth = old.depth
		}
	}
	if right {
		l.fR = f
	} else {
		l.fL = f
	}
	l.space = false
	return true
}

// flushAbs legt de geparkeerde bottom-verankerde absolutes van containing
// block oi, nu zijn onderkant (bottom) bekend is.
func (l *layouter) flushAbs(oi, bottom int) {
	if len(l.pend) == 0 {
		return
	}
	var rest []pendAbs
	for _, p := range l.pend {
		if p.oi == oi {
			l.absolute(p.el, p.cp, p.st, bottom)
		} else {
			rest = append(rest, p)
		}
	}
	l.pend = rest
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
			Under: st.under,
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
	if l.center || l.right {
		// Uitlijnen binnen de éigen rechterrand: in een gecentreerde
		// smalle container (wikipedia's wordmark-blok) is dat niet de
		// paginarand — anders centreer je dubbel en schuift alles rechts.
		edge := l.lineR
		if edge <= 0 {
			edge = l.width - pad
		}
		shift := edge - l.x
		if l.center {
			shift /= 2
		}
		if shift > 0 {
			for i := l.line0; i < len(l.boxes); i++ {
				// Alles wat bij déze regel hoort schuift mee — ook inhoud
				// die een doos-padding omlaag zette (chips!). Boxes van
				// eerdere regels (<hr> e.d.) blijven staan.
				if l.boxes[i].R.Min.Y >= l.y {
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
	l.right = false
	l.lineR = 0
}

// alignLine markeert de huidige regel voor uitlijning volgens de stijl,
// mét de rechterrand van de context — in een gecentreerde smalle container
// is dat niet de paginarand.
func (l *layouter) alignLine(st style) {
	if st.center {
		l.center = true
	}
	if st.right {
		l.right = true
	}
	if st.center || st.right {
		l.lineR = l.lineRight(st.rIndent)
	}
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
		// Ook boven het allereerste blok: een expliciete top-marge
		// (wikipedia's 4rem boven het wordmark) hoort alles omlaag te
		// schuiven — dat is geen witruimte-junk maar vormgeving.
		l.y += l.gap
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
			if p.Text != "" && b.Text != "" && !p.Rule && !b.Rule && p.Img == nil && b.Img == nil &&
				p.Field == 0 && b.Field == 0 && p.Tile == nil && b.Tile == nil &&
				p.Scale == b.Scale && p.Col == b.Col && p.Href == b.Href &&
				p.Bold == b.Bold && p.Under == b.Under &&
				p.HasBG == b.HasBG && p.BG == b.BG &&
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

// eachCorner loopt de vier kwart-hoekvlakken van r af (straal rad) en
// roept f aan met de pixel én zijn afstand-index (i,j) vanaf de rechte
// rand — de gedeelde lus onder vullen, omranden en maskeren.
func eachCorner(r image.Rectangle, rad int, f func(x, y, i, j int)) {
	for j := 0; j < rad; j++ {
		for i := 0; i < rad; i++ {
			f(r.Min.X+rad-1-i, r.Min.Y+rad-1-j, i, j)
			f(r.Max.X-rad+i, r.Min.Y+rad-1-j, i, j)
			f(r.Min.X+rad-1-i, r.Max.Y-rad+j, i, j)
			f(r.Max.X-rad+i, r.Max.Y-rad+j, i, j)
		}
	}
}

// clampRad klemt een hoekstraal op de halve korte zijde van r; -1 (een
// procent of pil-waarde) betekent: precies de halve korte zijde.
func clampRad(r image.Rectangle, rad int) int {
	m := r.Dx()
	if r.Dy() < m {
		m = r.Dy()
	}
	if rad < 0 || rad > m/2 {
		rad = m / 2
	}
	return rad
}

// inCorner: ligt hoekpixel (i,j) — geteld vanaf de rechte rand naar de
// hoek toe — binnen de kwartcirkel met straal rad? (midden-van-pixel-test)
func inCorner(i, j, rad int) bool {
	dx, dy := 2*i+1, 2*j+1
	return dx*dx+dy*dy <= 4*rad*rad
}

// fillRounded vult r met kleur c en afgeronde hoeken (border-radius):
// drie rechte banen plus vier kwartcirkels van losse pixels.
func fillRounded(img *image.RGBA, r image.Rectangle, c color.RGBA, rad int) {
	rad = clampRad(r, rad)
	if rad <= 0 {
		pixel.Fill(img, r, c)
		return
	}
	pixel.Fill(img, image.Rect(r.Min.X+rad, r.Min.Y, r.Max.X-rad, r.Max.Y), c)
	pixel.Fill(img, image.Rect(r.Min.X, r.Min.Y+rad, r.Min.X+rad, r.Max.Y-rad), c)
	pixel.Fill(img, image.Rect(r.Max.X-rad, r.Min.Y+rad, r.Max.X, r.Max.Y-rad), c)
	eachCorner(r, rad, func(x, y, i, j int) {
		if inCorner(i, j, rad) {
			img.SetRGBA(x, y, c)
		}
	})
}

// outlineRounded tekent één randlijn met afgeronde hoeken: rechte stukken
// tussen de hoeken, en op de hoeken de cirkelring (binnen rad, buiten
// rad-1).
func outlineRounded(img *image.RGBA, r image.Rectangle, c color.RGBA, rad int) {
	rad = clampRad(r, rad)
	if rad <= 0 {
		pixel.Outline(img, r, c)
		return
	}
	pixel.Fill(img, image.Rect(r.Min.X+rad, r.Min.Y, r.Max.X-rad, r.Min.Y+1), c)
	pixel.Fill(img, image.Rect(r.Min.X+rad, r.Max.Y-1, r.Max.X-rad, r.Max.Y), c)
	pixel.Fill(img, image.Rect(r.Min.X, r.Min.Y+rad, r.Min.X+1, r.Max.Y-rad), c)
	pixel.Fill(img, image.Rect(r.Max.X-1, r.Min.Y+rad, r.Max.X, r.Max.Y-rad), c)
	eachCorner(r, rad, func(x, y, i, j int) {
		if inCorner(i, j, rad) && !inCorner(i, j, rad-1) {
			img.SetRGBA(x, y, c)
		}
	})
}

// maskRounded maakt de hoeken van een (al geschaalde) afbeelding
// doorzichtig — border-radius op een <img>: de ronde avatar. rad 0 = niets.
func maskRounded(m *image.RGBA, rad int) {
	if rad == 0 || m == nil {
		return
	}
	r := m.Bounds()
	rad = clampRad(r, rad)
	eachCorner(r, rad, func(x, y, i, j int) {
		if !inCorner(i, j, rad) {
			m.SetRGBA(x, y, color.RGBA{})
		}
	})
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
			fillRounded(img, r, bx.BG, bx.Rad)
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
			// tekenen bij een half-zichtbaar blok. Dikte = geneste lijnen.
			for i := 0; i < bx.BrdW || i == 0; i++ {
				if bx.Rad != 0 {
					rad := clampRad(r, bx.Rad)
					outlineRounded(img, r.Inset(i), bx.Border, rad-i)
				} else {
					pixel.Outline(img, r.Inset(i), bx.Border)
				}
			}
		}
		return
	}
	if bx.HasBG {
		// 1px lucht rondom: leest prettiger en dekt de spatie in een
		// samengevoegde run.
		fillRounded(img, image.Rect(b.Min.X+bx.R.Min.X-1, top-1, b.Min.X+bx.R.Max.X+1, bot+1), bx.BG, bx.Rad)
	}
	drawTxt(img, b.Min.X+bx.R.Min.X, top, bx.Scale, bx.Col, bx.Text)
	if bx.Bold {
		// Pseudo-vet: het font heeft geen gewichten — dubbel tekenen met
		// 1px offset is er verrassend dichtbij.
		drawTxt(img, b.Min.X+bx.R.Min.X+1, top, bx.Scale, bx.Col, bx.Text)
	}
	if bx.Under {
		// text-decoration door de cascade: de UA-default voor links, door
		// de site aan of uit te zetten — geen hardgekoppelde href-streep.
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

// BackW is de terug-knop links in de adresbalk (hit-vlak én chip).
const BackW = 26

// RenderBar tekent alléén de adresbalk (voor het tik-pad: een strook van
// een paar KB damage per toets in plaats van een vol frame): de terug-knop
// links, het adres ernaast.
func (v *View) RenderBar(img *image.RGBA) {
	b := img.Bounds()
	bar := image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+BarH)
	pixel.Fill(img, bar, colBar)

	chip := image.Rect(b.Min.X+2, b.Min.Y+2, b.Min.X+BackW-2, b.Min.Y+BarH-2)
	pixel.Card(img, chip, pixel.ColRaise, pixel.ColLine)
	pixel.DrawTextCentered(img, chip, pixel.F12, 1, pixel.ColText, "<")

	txt := v.Addr + "_"
	// Houd het einde in beeld: daar wordt getypt.
	if max := (b.Dx() - BackW - 2*pad) / charW(1); len(txt) > max && max > 0 {
		txt = txt[len(txt)-max:]
	}
	drawTxt(img, b.Min.X+BackW+pad, b.Min.Y+(BarH-charH(1))/2, 1, colBarTxt, txt)
}

// HitBack: valt (x,y) — window-lokaal — op de terug-knop in de adresbalk?
func (v *View) HitBack(x, y int) bool {
	return y >= 0 && y < BarH && x >= 0 && x < BackW
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
