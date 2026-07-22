// CSS, het déél dat een simpele renderer kan laten zien: kleuren, vet,
// verbergen, centreren, lettergrootte. De selector-kant is cascadia
// (een echte selector-parser/matcher op *html.Node) — hier woont alleen
// de declaratie-kant: een tolerante parser voor
// "selector { prop: waarde }" die alles overslaat wat hij niet kent
// (@media, animaties, flexbox — layout-CSS is bewust buiten scope: dat is
// een layout-engine, en het 8x8-flow-model is juist de charme).
package browse

import (
	"image/color"
	"strconv"
	"strings"
)

// props zijn de computed properties van één element — alleen de gedragen
// subset, lowercase prop → lowercase waarde.
type props map[string]string

// supportedProp: regels zonder één van deze properties worden al bij het
// parsen weggegooid — het gros van echte stylesheets blijft junk (fonts,
// animaties, schaduwen), en elke overgebleven regel kost een match-ronde.
// Sinds de box-engine horen de boxmodel-properties er ook bij.
func supportedProp(p string) bool {
	if strings.HasPrefix(p, "--") {
		return true // custom property: voer voor var()-resolutie
	}
	switch p {
	case "display", "visibility", "color", "background-color", "background",
		"background-image", "background-position", "font-weight", "font-size", "text-align",
		"border", "border-color", "border-top", "border-right", "border-bottom",
		"border-left", "flex-direction", "float", "clear",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"margin-inline", "margin-block", "padding-inline", "padding-block",
		"border-radius",
		"width", "max-width", "min-width", "height", "min-height", "gap", "column-gap", "row-gap",
		"list-style", "list-style-type",
		"background-size", "grid-template-columns", "grid-column", "grid-auto-flow", "flex-wrap",
		"grid-template-areas", "grid-area", "justify-items", "justify-self",
		"white-space", "flex", "flex-grow", "flex-basis", "flex-flow", "order",
		"object-fit", "aspect-ratio",
		"justify-content", "align-items", "align-self", "place-items", "place-content",
		"text-transform", "text-decoration", "text-decoration-line":
		return true
	}
	return false
}

// cssBorder leest een border(-color)-waarde: aan/uit, de kleur (grijs als
// er alleen "1px solid" staat) en de dikte in px (1 zonder maat, cap 8).
// "none", "0" en varianten zijn uit — en "transparent" óók: dat is een
// doorzichtige rand (de ruimte-truc tegen verspringen bij hover), geen
// grijze lijn.
func cssBorder(v string) (color.RGBA, int, bool) {
	if v == "" || v == "none" || v == "0" || strings.HasPrefix(v, "0 ") ||
		strings.HasPrefix(v, "0px") || strings.Contains(v, "none") ||
		strings.Contains(v, "transparent") {
		return color.RGBA{}, 0, false
	}
	col, w := colRule, 1 // default: de rustige grijze 1px-lijn
	for _, tok := range strings.Fields(v) {
		if c, ok := cssColor(tok); ok {
			col = c
		} else if n, ok := cssLen(tok); ok && n > 0 {
			w = capEdge(n, 8)
		}
	}
	return col, w, true
}

// cssRule is één selector met zijn declaraties, klaar om te matchen. mq
// zijn de omhullende @media-condities — die worden pas bij het cascaden
// geëvalueerd, tegen de échte framebreedte (mobile óf desktop).
type cssRule struct {
	sel   string
	spec  int // versimpelde specificiteit: id·100 + class/attr/pseudo·10 + tag
	seq   int // bronvolgorde (tiebreaker: later wint)
	decls props
	mq    []string
}

// parseCSS vouwt een stylesheet uit tot regels. Tolerant: commentaar en
// onbekende @-blokken (met hun hele inhoud) verdwijnen, kapotte regels ook.
// Selector-groepen ("h1, h2") splitsen in losse regels met eigen
// specificiteit.
func parseCSS(src string, seq0 int) []cssRule { return parseCSSm(src, seq0, nil) }

// parseCSSm is parseCSS met omhullende media-condities (het media=""-
// attribuut van de sheet, en verderop geneste @media-blokken).
func parseCSSm(src string, seq0 int, mq []string) []cssRule {
	src = stripComments(src)
	var rules []cssRule
	for i := 0; i < len(src); {
		open := strings.IndexByte(src[i:], '{')
		if open < 0 {
			break
		}
		sel := strings.TrimSpace(src[i : i+open])
		body, next := block(src, i+open)
		i = next
		if sel == "" {
			continue
		}
		if sel[0] == '@' {
			// @media: de query reist mee met de geneste regels; welke tak
			// geldt beslist de framebreedte bij het cascaden. Queries die
			// op géén enkele breedte kunnen matchen (print, prefers-light)
			// vallen hier al af. Andere @-blokken blijven genegeerd.
			if strings.HasPrefix(sel, "@media") {
				if q := sel[len("@media"):]; mediaAnyWidth(q) {
					sub := append(append([]string{}, mq...), q)
					rules = append(rules, parseCSSm(body, seq0+len(rules), sub)...)
				}
			}
			continue
		}
		decls := parseDecls(body)
		if len(decls) == 0 {
			continue
		}
		for _, one := range strings.Split(sel, ",") {
			one = simplifySelector(strings.TrimSpace(one))
			if one == "" || deadSelector(one) {
				continue
			}
			rules = append(rules, cssRule{
				sel: one, spec: specificity(one), seq: seq0 + len(rules), decls: decls, mq: mq,
			})
		}
	}
	return rules
}

// mediaProbeWidths: de breedtes waarop we proeven of een query überhaupt
// kán matchen — van telefoon tot breed scherm, plus onze default.
var mediaProbeWidths = []int{320, mobileWidth, 640, 800, 1024, 1280, 1680}

// mediaAnyWidth: kan deze query op énige redelijke framebreedte matchen?
func mediaAnyWidth(q string) bool {
	for _, w := range mediaProbeWidths {
		if mediaMatches(q, w) {
			return true
		}
	}
	return false
}

// ruleMediaOK: gelden alle omhullende media-condities op deze breedte?
func ruleMediaOK(mq []string, w int) bool {
	for _, q := range mq {
		if !mediaMatches(q, w) {
			return false
		}
	}
	return true
}

// mobileWidth is de viewport waar @media-queries tegen geëvalueerd worden.
// De styles worden bij het laden berekend (de windowbreedte is dan nog niet
// bekend) en het venster is 480 breed — wij zíjn gewoon een telefoon.
const mobileWidth = 480

// mediaMatches evalueert een @media-query tegen breedte w. Bewust simpel:
// komma's zijn OR, "and" is AND; gedragen zijn (min-width), (max-width),
// de range-vorm (width <= ...) en de types screen/all. Onbekende features
// en "not" matchen niet — liever een regel te weinig dan desktop-CSS op
// een telefoonvenster.
func mediaMatches(q string, w int) bool {
	for _, branch := range strings.Split(strings.ToLower(q), ",") {
		if mediaBranch(strings.TrimSpace(branch), w) {
			return true
		}
	}
	return false
}

func mediaBranch(b string, w int) bool {
	if b == "" || strings.HasPrefix(b, "not ") || strings.Contains(b, " not ") {
		return false
	}
	for _, part := range strings.Split(b, " and ") {
		part = strings.TrimSpace(part)
		switch part {
		case "screen", "all", "only screen", "only all":
			continue
		}
		if !mediaCond(part, w) {
			return false
		}
	}
	return true
}

// mediaCond: één (feature)-conditie. Zowel de klassieke vorm
// (min-width: 768px) als de range-vorm (480px <= width < 64em).
func mediaCond(c string, w int) bool {
	c = strings.TrimSpace(c)
	c = strings.TrimPrefix(c, "(")
	c = strings.TrimSuffix(c, ")")
	if i := strings.IndexByte(c, ':'); i >= 0 {
		prop, val := strings.TrimSpace(c[:i]), strings.TrimSpace(c[i+1:])
		// Wij zijn een lichte lezer (papierwit canvas): dark-mode-CSS hoort
		// niet te matchen — anders bloedt een donker thema half een lichte
		// pagina in (nu.nl's headerchips). En bewegen doen we niet.
		switch prop {
		case "prefers-color-scheme":
			return val == "light"
		case "prefers-reduced-motion":
			return val == "reduce"
		}
		v, ok := cssLen(val)
		if !ok {
			return false
		}
		switch prop {
		case "min-width":
			return w >= v
		case "max-width":
			return w <= v
		}
		return false
	}
	c = strings.ReplaceAll(c, " ", "")
	i := strings.Index(c, "width")
	if i < 0 {
		return false
	}
	left, right := c[:i], c[i+len("width"):]
	if lv, op, ok := splitCmp(left, true); ok {
		if !cmpWidth(w, flip(op), lv) {
			return false
		}
	} else if left != "" {
		return false
	}
	if rv, op, ok := splitCmp(right, false); ok {
		if !cmpWidth(w, op, rv) {
			return false
		}
	} else if right != "" {
		return false
	}
	return left != "" || right != ""
}

// splitCmp haalt operator en lengte uit "63em<=" (links van width) of
// "<=63em" (rechts van width).
func splitCmp(s string, leftSide bool) (px int, op string, ok bool) {
	for _, o := range []string{"<=", ">=", "<", ">", "="} {
		if leftSide && strings.HasSuffix(s, o) {
			if v, ok := cssLen(strings.TrimSuffix(s, o)); ok {
				return v, o, true
			}
			return 0, "", false
		}
		if !leftSide && strings.HasPrefix(s, o) {
			if v, ok := cssLen(strings.TrimPrefix(s, o)); ok {
				return v, o, true
			}
			return 0, "", false
		}
	}
	return 0, "", false
}

// flip spiegelt een operator: "63em <= width" is "width >= 63em".
func flip(op string) string {
	switch op {
	case "<=":
		return ">="
	case ">=":
		return "<="
	case "<":
		return ">"
	case ">":
		return "<"
	}
	return op
}

func cmpWidth(w int, op string, v int) bool {
	switch op {
	case "<=":
		return w <= v
	case ">=":
		return w >= v
	case "<":
		return w < v
	case ">":
		return w > v
	case "=":
		return w == v
	}
	return false
}

// remPx is de wortel-lettergrootte (html { font-size }) in px — de basis
// voor rem én onze em-benadering. De klassieke 62.5%-truc maakt 1rem =
// 10px; layoutStyled zet hem per pagina, media-evaluatie rekent per spec
// op de initiële 16.
var remPx = 16.0

// rootFontPx: de html-font-size naar pixels — %, em en rem zijn hier van
// de browserdefault 16 (62.5% = 10px).
func rootFontPx(v string) float64 {
	v = strings.TrimSpace(v)
	mul := 1.0
	switch {
	case strings.HasSuffix(v, "%"):
		v, mul = strings.TrimSuffix(v, "%"), 0.16
	case strings.HasSuffix(v, "rem"):
		v, mul = strings.TrimSuffix(v, "rem"), 16
	case strings.HasSuffix(v, "em"):
		v, mul = strings.TrimSuffix(v, "em"), 16
	case strings.HasSuffix(v, "px"):
		v = strings.TrimSuffix(v, "px")
	default:
		return 16
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f*mul >= 4 && f*mul <= 32 {
		return f * mul
	}
	return 16
}

// cssLen: een CSS-lengte naar hele pixels (px; em/rem op de wortelbasis).
func cssLen(v string) (int, bool) {
	v = strings.TrimSpace(v)
	mul := 1.0
	switch {
	case strings.HasSuffix(v, "rem"):
		v, mul = strings.TrimSuffix(v, "rem"), remPx
	case strings.HasSuffix(v, "em"):
		v, mul = strings.TrimSuffix(v, "em"), remPx
	case strings.HasSuffix(v, "px"):
		v = strings.TrimSuffix(v, "px")
	case v == "0":
	default:
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, false
	}
	return int(f * mul), true
}

// simplifySelector vouwt pseudo's weg die bij ons statisch vaststaan:
// :not(:hover), :not(:focus) enz. zijn áltijd waar (er is geen muis of
// focus — de hele :not vervalt), en :is(X)/:where(X) met één argument
// wordt herschreven naar X-zonder-:is. Belangrijk voor de verberg-regels
// van skip-links (".skip:not(:focus)") en voor selector-engines die
// :is() niet kennen — tweakers' hele component-CSS is
// ".more:is(:is(twk-site-menu>menu)>li)>.dropdown-menu"-taal.
func simplifySelector(sel string) string {
	for pass := 0; pass < 8; pass++ {
		changed := false
		for _, fn := range []string{":not(", ":is(", ":where("} {
			for from := 0; ; {
				i := strings.Index(sel[from:], fn)
				if i < 0 {
					break
				}
				i += from
				end := closeParen(sel, i+len(fn)-1)
				if end < 0 {
					break
				}
				inner := sel[i+len(fn) : end]
				switch {
				case strings.Contains(inner, ","):
					from = i + 1 // meerdere argumenten: laten staan
				case fn == ":not(" && deadSelector(inner):
					sel = sel[:i] + sel[end+1:] // :not(nooit-waar) = altijd waar
					changed = true
					from = i
				case fn != ":not(":
					// C1:is(A > B) betekent: matcht C1 én A > B — het laatste
					// compound van de binnenkant versmelt dus met het compound
					// eromheen, en A> komt ervóór (type-selector voorop).
					if folded, ok := foldIs(sel, i, end, inner); ok {
						sel = folded
						changed = true
						from = i
					} else {
						from = i + 1
					}
				default:
					from = i + 1 // :not(.iets-echts): laten staan
				}
			}
		}
		if !changed {
			break
		}
	}
	return strings.TrimSpace(sel)
}

// foldIs herschrijft één :is(inner)/:where(inner) op sel[i:end+1] naar een
// :is-loze vorm. pre is het compound-deel vóór de :is (".more"), anc het
// voorouder-deel van de binnenkant ("twk-site-menu>menu>"), last diens
// laatste compound ("li") — samen: anc + last×pre.
func foldIs(sel string, i, end int, inner string) (string, bool) {
	// het compound waar de :is in staat begint ná de vorige combinator —
	// haakjes-bewust terug, en een :is bínnen andermans haakjes laten we
	// met rust (dat compound is niet los te herschrijven).
	cs, depth := i, 0
	for cs > 0 {
		b := sel[cs-1]
		if b == ')' || b == ']' {
			depth++
		} else if b == '(' || b == '[' {
			depth--
			if depth < 0 {
				return sel, false
			}
		}
		if depth == 0 && isCombByte(b) {
			break
		}
		cs--
	}
	pre := sel[cs:i]
	anc, last := "", inner
	if k := lastTopCombinator(inner); k >= 0 {
		anc, last = inner[:k+1], strings.TrimSpace(inner[k+1:])
	}
	merged, ok := mergeCompound(last, pre)
	if !ok {
		return sel, false
	}
	return sel[:cs] + anc + merged + sel[end+1:], true
}

func isCombByte(b byte) bool { return b == ' ' || b == '>' || b == '+' || b == '~' }

// lastTopCombinator: de index van de laatste combinator op het buitenste
// niveau (haakjes en attribuut-blokken tellen niet mee); -1 = compound.
func lastTopCombinator(s string) int {
	depth, last := 0, -1
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ' ', '>', '+', '~':
			if depth == 0 {
				last = j
			}
		}
	}
	return last
}

// mergeCompound voegt twee compounds samen tot één, met de type-selector
// voorop ("li" + ".more" = "li.more"). Twee type-selectors tegelijk kan
// niet — dan laten we de :is staan (regel vervalt bij het parsen).
func mergeCompound(a, b string) (string, bool) {
	if a == "" {
		return b, true
	}
	if b == "" {
		return a, true
	}
	aType := a[0] != '.' && a[0] != '#' && a[0] != ':' && a[0] != '['
	bType := b[0] != '.' && b[0] != '#' && b[0] != ':' && b[0] != '['
	if aType && bType {
		return "", false
	}
	if bType {
		return b + a, true
	}
	return a + b, true
}

// closeParen geeft de index van de ')' die de '(' op sel[open] sluit.
func closeParen(sel string, open int) int {
	depth := 0
	for j := open; j < len(sel); j++ {
		switch sel[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// deadSelector: selectors die bij ons per definitie nooit matchen — geen
// muis-hover, geen focusringen, geen gegenereerde ::before-content, geen
// vendor-pseudo's. Echte stylesheets bestaan hier voor een flink deel uit;
// eruit gooien bij het parsen scheelt evenzoveel QuerySelectorAll-rondes.
var deadPseudos = []string{
	":hover", ":focus", ":active", ":visited", ":target", ":checked",
	":disabled", ":enabled", ":before", ":after", ":placeholder",
	":selection", ":backdrop", ":fullscreen", ":-", "::-",
}

func deadSelector(sel string) bool {
	if !strings.ContainsRune(sel, ':') {
		return false // verreweg de meeste selectors: geen pseudo, klaar
	}
	for _, p := range deadPseudos {
		if strings.Contains(sel, p) {
			return true
		}
	}
	return false
}

// block geeft de inhoud tussen de accolade op src[open] en zijn sluiter
// (genest meegeteld), plus de index erna.
func block(src string, open int) (string, int) {
	depth := 0
	for j := open; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open+1 : j], j + 1
			}
		}
	}
	return src[open+1:], len(src)
}

// srProp is de synthetische property waarmee parseDecls "visueel verborgen
// screenreader-tekst" markeert. Het sr-only-patroon is bewust géén
// display:none (dan zwijgt de screenreader óók): het is 1x1px met clip, of
// absoluut buiten beeld geparkeerd. Wij dragen clip/width/top niet als
// layout, maar herkennen de signatuur bij het parsen — de losse properties
// worden niet bewaard, dus de regel-filter blijft even streng.
const srProp = "-surf-sr-hidden"

// parseDecls parset "prop: waarde; prop: waarde" — alleen de gedragen
// properties blijven over. "background: <kleur>" telt als
// background-color als de waarde een kleur is (de gangbare shorthand).
func parseDecls(s string) props {
	var p props
	var clip, clipPath, w, h, pos, top, left, right, bottom, ti, op string
	var ovf, maxh, xform string
	for _, d := range strings.Split(s, ";") {
		colon := strings.IndexByte(d, ':')
		if colon < 0 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(d[:colon]))
		val := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(d[colon+1:]), "!important")))
		if val == "" {
			continue
		}
		switch prop {
		case "clip":
			clip = val
		case "clip-path":
			clipPath = val
		case "width":
			w = val
		case "height":
			h = val
		case "position":
			pos = val
		case "top":
			top = val
		case "left":
			left = val
		case "right":
			right = val
		case "bottom":
			bottom = val
		case "inset":
			// shorthand: 1-4 waarden in CSS-volgorde boven-rechts-onder-links
			if e, ok := expand4(strings.Fields(val)); ok {
				top, right, bottom, left = e[0], e[1], e[2], e[3]
			}
		case "text-indent":
			ti = val
		case "opacity":
			op = val
		case "overflow", "overflow-y":
			if strings.Contains(val, "hidden") || val == "clip" {
				ovf = "hidden"
			}
		case "max-height":
			maxh = val
		case "transform":
			// Off-canvas: een (vrijwel) volledige negatieve translate is de
			// dichte staat van lades en drawers — JS schuift ze pas in
			// beeld. De -50%-centreertruc blijft er expliciet buiten.
			if offCanvas(val) {
				xform = "weg"
			}
		}
		if !supportedProp(prop) {
			continue
		}
		if p == nil {
			p = props{}
		}
		// Een verloop rendert als zijn eerste kleurstop — vlak, maar de
		// juiste kleurfamilie (hero's en headers met een gradient).
		if (prop == "background" || prop == "background-image") && strings.Contains(val, "gradient(") {
			if c := firstColorIn(val[strings.Index(val, "gradient("):]); c != "" {
				p["background-color"] = c
			}
			if prop == "background-image" {
				// Een échte url naast het verloop (wikipedia's
				// "linear-gradient(transparent,transparent), url(sprite.svg)"
				// — de oude svg-fallback-truc): de afbeelding wint.
				if u := cssURL(val); u != "" {
					p["background-image"] = "url(" + u + ")"
				}
				continue
			}
		}
		if prop == "background" {
			// Shorthand uit elkaar trekken: een kleur-token wordt
			// background-color, een url(...) wordt background-image.
			for _, tok := range strings.Fields(val) {
				if _, ok := cssColor(tok); ok {
					p["background-color"] = tok
				}
			}
			if u := cssURL(val); u != "" {
				p["background-image"] = "url(" + u + ")"
			}
			// "background: url(x) center/cover": de maat zit in de shorthand.
			if strings.Contains(val, "cover") {
				p["background-size"] = "cover"
			} else if strings.Contains(val, "contain") {
				p["background-size"] = "contain"
			}
			// var(--x) als hele waarde: bewaren; na var-resolutie wordt het
			// alsnog een kleur (of valt het stil weg).
			if strings.HasPrefix(val, "var(") {
				p["background-color"] = val
			}
			continue
		}
		// flex-flow: de shorthand voor flex-direction + flex-wrap.
		if prop == "flex-flow" {
			for _, tok := range strings.Fields(val) {
				switch tok {
				case "row", "row-reverse", "column", "column-reverse":
					p["flex-direction"] = tok
				case "wrap", "nowrap", "wrap-reverse":
					p["flex-wrap"] = tok
				}
			}
			continue
		}
		// place-items/place-content: de as-shorthands — wij dragen er de
		// align-items- respectievelijk justify-content-kant van.
		if prop == "place-items" {
			p["align-items"] = strings.Fields(val)[0]
			continue
		}
		if prop == "place-content" {
			f := strings.Fields(val)
			p["justify-content"] = f[len(f)-1]
			continue
		}
		// De logische assen (ltr): -inline = links+rechts, -block =
		// boven+onder — modern web schrijft marges bijna alleen nog zo
		// (tweakers' margin-inline:auto centreert zijn menubaan).
		if strings.HasSuffix(prop, "-inline") || strings.HasSuffix(prop, "-block") {
			if base := strings.TrimSuffix(strings.TrimSuffix(prop, "-inline"), "-block"); base == "margin" || base == "padding" {
				f := splitTopLevel(val)
				if len(f) == 1 {
					f = append(f, f[0])
				}
				if len(f) == 2 {
					kanten := [2]string{"-left", "-right"}
					if strings.HasSuffix(prop, "-block") {
						kanten = [2]string{"-top", "-bottom"}
					}
					p[base+kanten[0]] = f[0]
					p[base+kanten[1]] = f[1]
				}
				continue
			}
		}
		// margin/padding: de shorthand schrijft óók zijn vier longhands —
		// dan wint in de cascade gewoon de láátste declaratie, welke vorm
		// die ook had (een margin:0 reset een eerdere margin-left echt).
		if prop == "margin" || prop == "padding" {
			if e, ok := expand4(splitTopLevel(val)); ok {
				for i, kant := range []string{"-top", "-right", "-bottom", "-left"} {
					p[prop+kant] = e[i]
				}
			}
			continue
		}
		p[prop] = val
	}
	if xform == "weg" || srHidden(clip, clipPath, w, h, pos, top, left, ti, op) {
		if p == nil {
			p = props{}
		}
		p[srProp] = "1"
	}
	// Dichtgeklapt: overflow:hidden op (max-)hoogte ~0 is de JS-loze dichte
	// staat van accordeons en uitklapmenu's — die inhoud is er niet. De
	// aspect-ratio-hack (height:0 mét padding-%) is juist een fotolijst en
	// blijft staan.
	if ovf == "hidden" {
		pv := func(k string) string {
			if p == nil {
				return ""
			}
			return p[k]
		}
		if !strings.Contains(pv("padding")+pv("padding-top")+pv("padding-bottom"), "%") {
			for _, hv := range []string{h, maxh} {
				if hv == "" {
					continue
				}
				if n, ok := cssLen(hv); ok && n <= 2 {
					if p == nil {
						p = props{}
					}
					p[srProp] = "1"
					break
				}
			}
		}
	}
	// Het positionerings-kado: fixed/sticky is chrome (header pinnen,
	// cookiebar weg), absolute gaat uit de flow op zijn coördinaten,
	// relative markeert de containing block.
	switch pos {
	case "fixed", "sticky", "absolute", "relative":
		if p == nil {
			p = props{}
		}
		p["position"] = pos
	}
	// De ankers reizen áltijd mee, ook zónder position in dezelfde regel:
	// wikipedia zet position:absolute en top/right in verschillende regels
	// — de cascade voegt ze pas samen. Zonder position blijven ze inert.
	for k, v := range map[string]string{"top": top, "bottom": bottom, "left": left, "right": right} {
		if v != "" {
			if p == nil {
				p = props{}
			}
			p[k] = v
		}
	}
	return p
}

// cssLenSignedPct: een (anker)lengte die negatief én een percentage van
// base mag zijn — wikipedia's talencirkel hangt op right:60%/left:60%.
func cssLenSignedPct(v string, base int) (int, bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "-") {
		if n, ok := cssLenPct(v[1:], base); ok {
			return -n, true
		}
		return 0, false
	}
	return cssLenPct(v, base)
}

// cssMinExtent: de gedeclareerde hoogte van een blok (height of
// min-height, in px; cap tegen 100vh-junk). Voor position:fixed is de
// víewport de containing block: procenten — en dus tweakers'
// calc(100% - var(--site-menu-height)) — rekenen tegen viewH, en een
// top+bottom-paar zónder height is dan óók een hoogte. 0 = niets gezegd.
func cssMinExtent(cp props) int {
	base := 0
	if cp["position"] == "fixed" {
		base = viewH
	}
	e := 0
	for _, k := range []string{"min-height", "height"} {
		if v, ok := cssLenPct(cp[k], base); ok && v > e {
			e = v
		}
	}
	if e == 0 && base > 0 && cp["bottom"] != "" {
		if t, ok := anchorLen(cp["top"], base); ok {
			if b, ok2 := anchorLen(cp["bottom"], base); ok2 && base-t-b > 0 {
				e = base - t - b
			}
		}
	}
	if e > 600 {
		e = 600
	}
	return e
}

// cssRadius: border-radius → de hoekstraal in px; -1 betekent "helemaal
// rond" (een procent, of een pil-waarde als 999px — het tekenen klemt op
// de halve bloklengte). Alleen de eerste waarde telt: hoek-per-hoek is
// verfijning die het 8x8-font toch niet haalt.
func cssRadius(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if f := splitTopLevel(v); len(f) > 0 {
		v = f[0]
	}
	if strings.HasSuffix(v, "%") {
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil && f > 0 {
			return -1
		}
		return 0
	}
	if n, ok := cssLen(v); ok && n > 0 {
		return n
	}
	return 0
}

// gridAreas parst grid-template-areas: elke aanhalingstekens-groep is één
// rij, de woorden erin zijn kolomnamen. nil = geen (rechthoekige) template.
func gridAreas(v string) [][]string {
	var rows [][]string
	for {
		i := strings.IndexAny(v, `"'`)
		if i < 0 {
			break
		}
		q := v[i]
		j := strings.IndexByte(v[i+1:], q)
		if j < 0 {
			break
		}
		if row := strings.Fields(v[i+1 : i+1+j]); len(row) > 0 {
			rows = append(rows, row)
		}
		v = v[i+1+j+1:]
	}
	if len(rows) == 0 {
		return nil
	}
	n := len(rows[0])
	if n < 1 || n > 6 {
		return nil
	}
	for _, r := range rows {
		if len(r) != n {
			return nil
		}
	}
	return rows
}

// expand4 expandeert een 1-4-waarden-shorthand naar boven-rechts-onder-
// links, met de CSS-herhaalregels (1 → alle, 2 → v h, 3 → t h b).
func expand4(f []string) ([4]string, bool) {
	if len(f) < 1 || len(f) > 4 {
		return [4]string{}, false
	}
	idx := map[int][4]int{1: {0, 0, 0, 0}, 2: {0, 1, 0, 1}, 3: {0, 1, 2, 1}, 4: {0, 1, 2, 3}}[len(f)]
	return [4]string{f[idx[0]], f[idx[1]], f[idx[2]], f[idx[3]]}, true
}

// hintLen: een presentational hint (het width/height-attribuut van svg's
// en ouderwetse tabellen) als CSS-lengte — kale getallen zijn pixels,
// procenten en echte lengtes gaan ongemoeid door. "" = niets bruikbaars.
func hintLen(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}
	if strings.HasSuffix(v, "%") {
		if _, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
			return v
		}
		return ""
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
		return v + "px"
	}
	if _, ok := cssLen(v); ok {
		return v
	}
	return ""
}

// anchorLen: een anker-lengte voor absolutes — px/em altijd, procenten
// alleen mét een basis (de betreffende maat van de containing block;
// wikipedia's talencirkel: top:20% van de gegeven height).
func anchorLen(v string, base int) (int, bool) {
	if strings.Contains(v, "%") && base <= 0 {
		return 0, false
	}
	return cssLenSignedPct(v, base)
}

// cssPairSigned: twee (mogelijk negatieve) lengtes ("0 -40px") — voor
// background-position/-size; keywords of procenten zijn niet begrepen.
func cssPairSigned(v string) (int, int, bool) {
	f := strings.Fields(v)
	if len(f) != 2 {
		return 0, 0, false
	}
	x, ok1 := cssLenSigned(f[0])
	y, ok2 := cssLenSigned(f[1])
	return x, y, ok1 && ok2
}

// cssLenSigned: een lengte die ook negatief mag zijn (badge-offsets).
func cssLenSigned(v string) (int, bool) {
	if strings.HasPrefix(v, "-") {
		if n, ok := cssLen(v[1:]); ok {
			return -n, true
		}
		return 0, false
	}
	return cssLen(v)
}

// srHidden herkent visueel-verborgen in de losse declaraties: het 1x1px-
// sr-only-doosje, alles weggeknipt, buiten beeld geparkeerd (position +
// negatieve left/top, of text-indent — image replacement), of opacity:0
// (skip-links; zonder JS is dat óók in een grote browser onzichtbaar).
func srHidden(clip, clipPath, w, h, pos, top, left, ti, op string) bool {
	if w == "1px" && h == "1px" {
		return true
	}
	if f, err := strconv.ParseFloat(strings.TrimSuffix(op, "%"), 64); err == nil && f == 0 {
		return true
	}
	if strings.HasPrefix(ti, "-") {
		if n, ok := cssLen(ti[1:]); ok && n >= 999 {
			return true
		}
	}
	if strings.HasPrefix(clipPath, "inset(50%") || strings.HasPrefix(clipPath, "inset(100%") {
		return true
	}
	if strings.HasPrefix(clip, "rect(") {
		inner := strings.TrimSuffix(clip[len("rect("):], ")")
		all := true
		toks := strings.FieldsFunc(inner, func(r rune) bool { return r == ',' || r == ' ' })
		for _, t := range toks {
			if v, ok := cssLen(t); !ok || v > 1 {
				all = false
				break
			}
		}
		if all && len(toks) == 4 {
			return true
		}
	}
	if pos == "absolute" || pos == "fixed" {
		// 100px of meer het beeld uit is geen vormgeving meer, dat is
		// verstoppen (tweakers' skip-links: left:-300px).
		for _, v := range []string{top, left} {
			if n, ok := cssLen(strings.TrimPrefix(v, "-")); ok && strings.HasPrefix(v, "-") && n >= 100 {
				return true
			}
		}
	}
	return false
}

// offCanvas: schuift deze transform het element (vrijwel) volledig uit
// beeld? translate/translateX/translateY met een eerste component van
// -90% of erger, of -100px of erger. De centreertruc translate(-50%,-50%)
// haalt die drempels nooit.
func offCanvas(v string) bool {
	i := strings.Index(v, "translate")
	if i < 0 {
		return false
	}
	rest := v[i:]
	open := strings.IndexByte(rest, '(')
	if open < 0 {
		return false
	}
	end := closeParen(rest, open)
	if end < 0 {
		return false
	}
	arg := strings.TrimSpace(splitArgs(rest[open+1 : end])[0])
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	if strings.HasSuffix(arg, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(arg[1:], "%"), 64)
		return err == nil && f >= 90
	}
	n, ok := cssLen(arg[1:])
	return ok && n >= 100
}

// cssURL haalt de url uit een url(...)-waarde; "" als die er niet is.
// data:-URI's doen niet mee (base64-decoderen is een andere klus).
func cssURL(v string) string {
	i := strings.Index(v, "url(")
	if i < 0 {
		return ""
	}
	rest := v[i+4:]
	j := strings.IndexByte(rest, ')')
	if j < 0 {
		return ""
	}
	u := strings.Trim(strings.TrimSpace(rest[:j]), `"'`)
	if u == "" || strings.HasPrefix(u, "data:") {
		return ""
	}
	return u
}

// resolveVars vervangt var(--x) en var(--x, fallback) door de waarde uit
// vars; een paar rondes diep, want variabelen verwijzen graag naar elkaar
// (gethop.org: --acc → --leaf). Onoplosbaar → lege string (de property
// valt dan stil weg — precies wat je wilt).
func resolveVars(v string, vars map[string]string) string {
	for depth := 0; depth < 4 && strings.Contains(v, "var("); depth++ {
		i := strings.Index(v, "var(")
		rest := v[i+4:]
		j := strings.IndexByte(rest, ')')
		if j < 0 {
			return ""
		}
		inner := rest[:j]
		name, fallback := inner, ""
		if c := strings.IndexByte(inner, ','); c >= 0 {
			name, fallback = inner[:c], strings.TrimSpace(inner[c+1:])
		}
		val, ok := vars[strings.TrimSpace(name)]
		if !ok {
			val = fallback
		}
		v = v[:i] + val + v[i+4+j+1:]
		v = strings.TrimSpace(v)
	}
	if strings.Contains(v, "var(") {
		return ""
	}
	return v
}

func stripComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + " " + s[i+2+j+2:]
	}
}

// specificity: ruw maar in de goede volgorde — id's boven classes boven
// tags. Pseudo-elementen (::) tellen niet dubbel.
func specificity(sel string) int {
	n := 0
	for i := 0; i < len(sel); i++ {
		switch sel[i] {
		case '#':
			n += 100
		case '.', '[':
			n += 10
		case ':':
			if i+1 < len(sel) && sel[i+1] == ':' {
				i++
			}
			n += 10
		default:
			if (i == 0 || sel[i-1] == ' ' || sel[i-1] == '>' || sel[i-1] == '+' || sel[i-1] == '~') &&
				sel[i] != '*' && sel[i] != ' ' {
				n++
			}
		}
	}
	return n
}

// cssColor parset #rgb/#rrggbb, rgb(a) en de gangbare namen. transparent
// en currentcolor zijn bewust geen kleur (ok=false): niets mee te tekenen.
func cssColor(v string) (color.RGBA, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return color.RGBA{}, false
	}
	if v[0] == '#' {
		return hexColor(v[1:])
	}
	if strings.HasPrefix(v, "rgb(") || strings.HasPrefix(v, "rgba(") {
		f := colorArgs(v)
		if len(f) < 3 {
			return color.RGBA{}, false
		}
		var c [3]uint8
		for i := 0; i < 3; i++ {
			// SCSS-output in het wild: ook fracties ("223.176...") en
			// procenten. Afronden en klemmen, niet afkeuren.
			n, ok := colorNum(f[i], 255)
			if !ok {
				return color.RGBA{}, false
			}
			c[i] = uint8(n)
		}
		// alpha 0 (rgba(...,0)) is géén kleur; deels doorschijnend wordt
		// gewoon de kleur — wij composen niet.
		if len(f) >= 4 {
			if a, ok := colorNum(f[3], 1); ok && a == 0 {
				return color.RGBA{}, false
			}
		}
		return color.RGBA{c[0], c[1], c[2], 0xFF}, true
	}
	if strings.HasPrefix(v, "hsl(") || strings.HasPrefix(v, "hsla(") {
		f := colorArgs(v)
		if len(f) < 3 {
			return color.RGBA{}, false
		}
		h, ok1 := colorNum(f[0], 360)
		s, ok2 := colorNum(f[1], 100)
		li, ok3 := colorNum(f[2], 100)
		if !ok1 || !ok2 || !ok3 {
			return color.RGBA{}, false
		}
		return hslColor(h, s/100, li/100), true
	}
	if c, ok := namedColors[v]; ok {
		return c, true
	}
	return color.RGBA{}, false
}

// colorArgs splitst de argumenten van rgb(a)/hsl(a): komma's, spaties en de
// moderne "/"-alphanotatie zijn allemaal scheiders.
func colorArgs(v string) []string {
	inner := v[strings.IndexByte(v, '(')+1:]
	if j := strings.IndexByte(inner, ')'); j >= 0 {
		inner = inner[:j]
	}
	inner = strings.ReplaceAll(inner, "/", " ")
	return strings.FieldsFunc(inner, func(r rune) bool { return r == ',' || r == ' ' })
}

// colorNum parset één kleurcomponent: kaal getal (ook met fractie), of een
// percentage van max. Geklemd op [0, max]; "deg" mag op een hoek.
func colorNum(s string, max float64) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(s, "deg"))
	pct := strings.HasSuffix(s, "%")
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return 0, false
	}
	if pct {
		f = f / 100 * max
	}
	if f < 0 {
		f = 0
	}
	if f > max {
		f = max
	}
	return f, true
}

// hslColor: HSL → RGB (CSS Color 3). h in graden, s en l in 0..1.
func hslColor(h, s, l float64) color.RGBA {
	c := (1 - abs64(2*l-1)) * s
	hh := h / 60
	x := c * (1 - abs64(mod64(hh, 2)-1))
	var r, g, b float64
	switch {
	case hh < 1:
		r, g, b = c, x, 0
	case hh < 2:
		r, g, b = x, c, 0
	case hh < 3:
		r, g, b = 0, c, x
	case hh < 4:
		r, g, b = 0, x, c
	case hh < 5:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	m := l - c/2
	to := func(f float64) uint8 { return uint8((f + m) * 255) }
	return color.RGBA{to(r), to(g), to(b), 0xFF}
}

func abs64(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func mod64(f, m float64) float64 {
	for f >= m {
		f -= m
	}
	for f < 0 {
		f += m
	}
	return f
}

func hexColor(h string) (color.RGBA, bool) {
	nib := func(b byte) (uint8, bool) {
		switch {
		case b >= '0' && b <= '9':
			return b - '0', true
		case b >= 'a' && b <= 'f':
			return b - 'a' + 10, true
		case b >= 'A' && b <= 'F':
			return b - 'A' + 10, true
		}
		return 0, false
	}
	switch len(h) {
	case 3, 4: // #rgb(a)
		if len(h) == 4 {
			// alpha 0 = volledig doorzichtig: dat is géén kleur (nu.nl's
			// #0000-chips werden dekkend zwart). Deels doorschijnend
			// blijft gewoon de kleur — composen doen we bewust niet.
			if a, ok := nib(h[3]); ok && a == 0 {
				return color.RGBA{}, false
			}
		}
		var c [3]uint8
		for i := 0; i < 3; i++ {
			n, ok := nib(h[i])
			if !ok {
				return color.RGBA{}, false
			}
			c[i] = n<<4 | n
		}
		return color.RGBA{c[0], c[1], c[2], 0xFF}, true
	case 6, 8: // #rrggbb(aa)
		if len(h) == 8 {
			hi, ok1 := nib(h[6])
			lo, ok2 := nib(h[7])
			if ok1 && ok2 && hi<<4|lo == 0 {
				return color.RGBA{}, false
			}
		}
		var c [3]uint8
		for i := 0; i < 3; i++ {
			hi, ok1 := nib(h[i*2])
			lo, ok2 := nib(h[i*2+1])
			if !ok1 || !ok2 {
				return color.RGBA{}, false
			}
			c[i] = hi<<4 | lo
		}
		return color.RGBA{c[0], c[1], c[2], 0xFF}, true
	}
	return color.RGBA{}, false
}

// namedColors: de namen die je in het wild echt tegenkomt.
var namedColors = map[string]color.RGBA{
	"black":   {0x00, 0x00, 0x00, 0xFF},
	"white":   {0xFF, 0xFF, 0xFF, 0xFF},
	"red":     {0xFF, 0x00, 0x00, 0xFF},
	"green":   {0x00, 0x80, 0x00, 0xFF},
	"blue":    {0x00, 0x00, 0xFF, 0xFF},
	"yellow":  {0xFF, 0xFF, 0x00, 0xFF},
	"orange":  {0xFF, 0xA5, 0x00, 0xFF},
	"purple":  {0x80, 0x00, 0x80, 0xFF},
	"gray":    {0x80, 0x80, 0x80, 0xFF},
	"grey":    {0x80, 0x80, 0x80, 0xFF},
	"silver":  {0xC0, 0xC0, 0xC0, 0xFF},
	"maroon":  {0x80, 0x00, 0x00, 0xFF},
	"navy":    {0x00, 0x00, 0x80, 0xFF},
	"teal":    {0x00, 0x80, 0x80, 0xFF},
	"olive":   {0x80, 0x80, 0x00, 0xFF},
	"lime":    {0x00, 0xFF, 0x00, 0xFF},
	"aqua":    {0x00, 0xFF, 0xFF, 0xFF},
	"cyan":    {0x00, 0xFF, 0xFF, 0xFF},
	"fuchsia": {0xFF, 0x00, 0xFF, 0xFF},
	"magenta": {0xFF, 0x00, 0xFF, 0xFF},
	"gold":    {0xFF, 0xD7, 0x00, 0xFF},
	"pink":    {0xFF, 0xC0, 0xCB, 0xFF},
	"brown":   {0xA5, 0x2A, 0x2A, 0xFF},
	"darkred": {0x8B, 0x00, 0x00, 0xFF},
	"tomato":  {0xFF, 0x63, 0x47, 0xFF},
}

// fontScale vertaalt een font-size naar onze schaal 1..3 (8/16/24px):
// wij hebben drie lettermaten, CSS heeft er oneindig veel — afronden dus.
func fontScale(v string, cur int) int {
	px := 0.0
	switch {
	case strings.HasSuffix(v, "px"):
		px, _ = strconv.ParseFloat(strings.TrimSuffix(v, "px"), 64)
	case strings.HasSuffix(v, "rem"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "rem"), 64)
		px = f * remPx
	case strings.HasSuffix(v, "em"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "em"), 64)
		px = f * remPx
	case strings.HasSuffix(v, "%"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		px = f / 100 * remPx
	case v == "xx-large" || v == "xxx-large":
		px = 32
	case v == "x-large":
		px = 24
	case v == "large" || v == "larger":
		px = 18
	case v == "small" || v == "smaller" || v == "x-small" || v == "xx-small":
		px = 12
	case v == "medium":
		px = 16
	default:
		return cur
	}
	switch {
	case px <= 0:
		return cur
	case px >= 28:
		// Alleen échte display-koppen naar 3: op een venster van ~480px is
		// schaal 3 maar ~20 tekens per regel — krantenkoppen (24-26px)
		// lezen op 2 een stuk beter.
		return 3
	case px >= 17:
		return 2
	default:
		return 1
	}
}

// edges is één boxmodel-zijde-set (margin of padding) in pixels; autoL/R
// staan voor "margin: 0 auto" — het klassieke centreer-signaal.
type edges struct {
	t, r, b, l   int
	autoL, autoR bool
	setV, setH   bool // verticaal/horizontaal expliciet gezet (anders: UA-default)
}

// capEdge klemt één zijde: negatieve marges en 100vh-achtige uitschieters
// zijn layout-trucs die ons flow-model alleen maar slopen.
func capEdge(v, max int) int {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// cssEdgesOf leest margin of padding uit de props: de shorthand (1-4
// waarden, CSS-volgorde boven-rechts-onder-links) plus de losse zijden
// eroverheen. Procenten tellen als 0 (padding-top:56% is de aspect-ratio-
// hack — die wil je echt niet als lege ruimte renderen).
func cssEdgesOf(cp props, name string, maxPx int) edges {
	e := edges{}
	one := func(v string) (px int, auto, ok bool) {
		v = strings.TrimSpace(v)
		if v == "auto" {
			return 0, true, true
		}
		if strings.HasSuffix(v, "%") {
			return 0, false, true
		}
		if n, ok := cssLen(v); ok {
			return capEdge(n, maxPx), false, true
		}
		return 0, false, false
	}
	// Alleen de longhands: de shorthand is bij het parsen al geëxpandeerd
	// (parseDecls), dus de cascade-volgorde zit dáár al goed.
	for side, dst := range map[string]*int{"-top": &e.t, "-right": &e.r, "-bottom": &e.b, "-left": &e.l} {
		if v, ok := cp[name+side]; ok {
			if px, auto, ok := one(v); ok {
				*dst = px
				switch side {
				case "-top", "-bottom":
					e.setV = true
				case "-left":
					e.autoL, e.setH = auto, true
				case "-right":
					e.autoR, e.setH = auto, true
				}
			}
		}
	}
	return e
}

// cssLenPct: een lengte die ook een percentage (van avail), een simpele
// calc() of een min()/max()/clamp() mag zijn.
func cssLenPct(v string, avail int) (int, bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "calc(") {
		end := closeParen(v, len("calc(")-1)
		if end < 0 {
			return 0, false
		}
		return cssCalc(v[len("calc("):end], avail)
	}
	for _, fn := range []string{"min(", "max(", "clamp("} {
		if strings.HasPrefix(v, fn) {
			end := closeParen(v, len(fn)-1)
			if end < 0 {
				return 0, false
			}
			return cssMinMax(fn, splitArgs(v[len(fn):end]), avail)
		}
	}
	if strings.HasSuffix(v, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		if err != nil {
			return 0, false
		}
		return int(f / 100 * float64(avail)), true
	}
	return cssLen(v)
}

// cssCalc rekent een calc-expressie uit: lengtes, percentages en kale
// getallen met + - * / ertussen (spaties om + en -, zoals de spec eist),
// haakjes-groepen incluis — tweakers' page-grid rekent
// "(3 - 1) * 1rem + ( 3 * 344px )". * en / gaan vóór + en -.
func cssCalc(expr string, avail int) (int, bool) {
	t, ok := calcEval(expr, avail)
	if !ok || !t.px {
		return 0, false
	}
	return int(t.v), true
}

// calcTerm is één calc-waarde: een kaal getal (schaal) of een lengte (px).
type calcTerm struct {
	v  float64
	px bool
}

func calcEval(expr string, avail int) (calcTerm, bool) {
	toks := splitTopLevel(expr)
	if len(toks) == 0 || len(toks)%2 == 0 {
		return calcTerm{}, false
	}
	term := func(tok string) (calcTerm, bool) {
		if strings.HasPrefix(tok, "(") && strings.HasSuffix(tok, ")") {
			return calcEval(tok[1:len(tok)-1], avail)
		}
		if strings.HasPrefix(tok, "calc(") {
			end := closeParen(tok, len("calc(")-1)
			if end < 0 {
				return calcTerm{}, false
			}
			return calcEval(tok[len("calc("):end], avail)
		}
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			return calcTerm{v: f}, true
		}
		if n, ok := cssLenPct(tok, avail); ok {
			return calcTerm{v: float64(n), px: true}, true
		}
		return calcTerm{}, false
	}
	// Eerst alle termen oplossen, dan * en /, dan + en -.
	vals := make([]calcTerm, 0, (len(toks)+1)/2)
	ops := make([]string, 0, len(toks)/2)
	for i, tok := range toks {
		if i%2 == 1 {
			if tok != "+" && tok != "-" && tok != "*" && tok != "/" {
				return calcTerm{}, false
			}
			ops = append(ops, tok)
			continue
		}
		v, ok := term(tok)
		if !ok {
			return calcTerm{}, false
		}
		vals = append(vals, v)
	}
	for i := 0; i < len(ops); {
		a, b := vals[i], vals[i+1]
		switch ops[i] {
		case "*":
			if a.px && b.px {
				return calcTerm{}, false // px maal px bestaat niet
			}
			vals[i] = calcTerm{v: a.v * b.v, px: a.px || b.px}
		case "/":
			if b.v == 0 || b.px {
				return calcTerm{}, false
			}
			vals[i] = calcTerm{v: a.v / b.v, px: a.px}
		default:
			i++
			continue
		}
		vals = append(vals[:i+1], vals[i+2:]...)
		ops = append(ops[:i], ops[i+1:]...)
	}
	total := vals[0]
	for i, op := range ops {
		b := vals[i+1]
		if total.px != b.px {
			return calcTerm{}, false // px plus schaal is geen maat
		}
		if op == "+" {
			total.v += b.v
		} else {
			total.v -= b.v
		}
	}
	return total, true
}

// splitTopLevel splitst op spaties van het buitenste niveau: alles binnen
// haakjes blijft één token ("repeat(3, minmax(0, 1fr))").
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, -1
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '(':
			depth++
		case s[i] == ')':
			depth--
		case (s[i] == ' ' || s[i] == '\t') && depth == 0:
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// cssMinMax rekent min()/max()/clamp() uit over de oplosbare argumenten
// (een vw-term valt gewoon af). clamp(a, x, b) klemt x op [a, b]; is de
// middenterm onoplosbaar dan is het midden van a en b de beste gok.
func cssMinMax(fn string, args []string, avail int) (int, bool) {
	if fn == "clamp(" && len(args) == 3 {
		lo, okLo := cssLenPct(args[0], avail)
		mid, okMid := cssLenPct(args[1], avail)
		hi, okHi := cssLenPct(args[2], avail)
		switch {
		case okMid:
			if okLo && mid < lo {
				mid = lo
			}
			if okHi && mid > hi {
				mid = hi
			}
			return mid, true
		case okLo && okHi:
			return (lo + hi) / 2, true
		}
		return 0, false
	}
	best, ok := 0, false
	for _, a := range args {
		v, okA := cssLenPct(a, avail)
		if !okA {
			continue
		}
		if !ok || (fn == "min(" && v < best) || (fn == "max(" && v > best) {
			best, ok = v, true
		}
	}
	return best, ok
}

// splitArgs splitst functie-argumenten op de komma's van het buitenste
// niveau (geneste haakjes blijven heel).
func splitArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// cssRatio parst een aspect-ratio-waarde: "16 / 9", "16/9" of "1.5".
func cssRatio(v string) (num, den float64, ok bool) {
	parts := strings.SplitN(strings.ReplaceAll(v, " ", ""), "/", 2)
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || num <= 0 {
		return 0, 0, false
	}
	den = 1
	if len(parts) == 2 {
		den, err = strconv.ParseFloat(parts[1], 64)
		if err != nil || den <= 0 {
			return 0, 0, false
		}
	}
	return num, den, true
}

// markerType vertaalt list-style(-type) naar ons lijstteken: "" (geen),
// "1" (tellen — ook voor letter/romeinse lijsten) of "-" (elk bolletje).
func markerType(v, cur string) string {
	for _, tok := range strings.Fields(v) {
		switch tok {
		case "none":
			return ""
		case "decimal", "decimal-leading-zero", "lower-alpha", "upper-alpha",
			"lower-latin", "upper-latin", "lower-roman", "upper-roman":
			return "1"
		case "disc", "circle", "square", "disclosure-closed", "disclosure-open":
			return "-"
		}
	}
	return cur
}

// firstColorIn zoekt de eerste kleur in een gradient-waarde — onze vlakke
// benadering van een verloop is zijn eerste kleurstop.
func firstColorIn(v string) string {
	for _, tok := range strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ' ' || r == '(' || r == ')'
	}) {
		if _, ok := cssColor(tok); ok {
			return tok
		}
	}
	return ""
}

// flexItem leest het groeigewicht en de vaste basis (px; -1 = geen) van een
// flex-kind: de losse properties én de flex-shorthand ("flex: 1",
// "flex: 0 0 200px").
func flexItem(cp props, avail int) (grow float64, basis int) {
	basis = -1
	if v, ok := cp["flex-basis"]; ok {
		if px, ok := cssLenPct(v, avail); ok && px > 0 {
			basis = px
		}
	}
	if v, ok := cp["flex-grow"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			grow = f
		}
	}
	if v, ok := cp["flex"]; ok && v != "none" && v != "auto" && v != "initial" {
		f := strings.Fields(v)
		if g, err := strconv.ParseFloat(f[0], 64); err == nil && g >= 0 {
			grow = g
		}
		// een lengte-token in de shorthand is de basis (de derde waarde,
		// maar "flex: 0 200px" bestaat ook)
		for _, tok := range f[1:] {
			if px, ok := cssLenPct(tok, avail); ok && px > 0 {
				basis = px
			}
		}
	}
	return grow, basis
}

// gridSpan: hoeveel tracks beslaat dit grid-item? "1 / -1" is de hele rij,
// "span N" is N, "a / b" is b-a. 1 als er niets (begrijpelijks) staat.
func gridSpan(cp props, n int) int {
	clamp := func(s int) int {
		if s < 1 {
			return 1
		}
		if s > n {
			return n
		}
		return s
	}
	v := strings.TrimSpace(cp["grid-column"])
	if v == "" {
		return 1
	}
	span := func(s string) (int, bool) {
		if !strings.HasPrefix(s, "span") {
			return 0, false
		}
		i, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(s, "span")))
		return i, err == nil
	}
	parts := strings.SplitN(v, "/", 2)
	p0 := strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		p1 := strings.TrimSpace(parts[1])
		if s, ok := span(p1); ok {
			return clamp(s)
		}
		a, errA := strconv.Atoi(p0)
		if p1 == "-1" {
			if errA == nil {
				return clamp(n - a + 1)
			}
			return n
		}
		if b, errB := strconv.Atoi(p1); errA == nil && errB == nil && b > a {
			return clamp(b - a)
		}
		return 1
	}
	if s, ok := span(p0); ok {
		return clamp(s)
	}
	return 1
}

// cssGap: de flex/grid-gap in px (gap of column-gap), geklemd.
func cssGap(cp props) int {
	for _, k := range []string{"column-gap", "gap"} {
		if v, ok := cp[k]; ok {
			// "gap: 12px 8px" → de tweede is de kolom-gap.
			f := strings.Fields(v)
			if n, ok := cssLen(f[len(f)-1]); ok {
				return capEdge(n, 32)
			}
		}
	}
	return 8
}

// cssRowGap: de verticale gap tussen flex/grid-rijen — expliciete row-gap,
// of gap (bij twee waarden is de eerste de rijgap). 0 zonder declaratie.
func cssRowGap(cp props) int {
	if v, ok := cp["row-gap"]; ok {
		if n, ok := cssLen(v); ok {
			return capEdge(n, 48)
		}
	}
	if v, ok := cp["gap"]; ok {
		if n, ok := cssLen(strings.Fields(v)[0]); ok {
			return capEdge(n, 48)
		}
	}
	return 0
}

// cssOrder: de flex/grid order-property (0 zonder declaratie).
func cssOrder(cp props) int {
	if v, ok := cp["order"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// gridTracks vertaalt grid-template-columns naar kolombreedtes voor deze
// beschikbare breedte: px is vast, fr/auto/minmax is gewicht, repeat()
// wordt uitgevouwen, repeat(auto-fill|auto-fit, minmax(Xpx, ...)) rekent
// het aantal kolommen uit de breedte. nil = niet te begrijpen (of één
// kolom): gewoon stapelen.
func gridTracks(v string, avail, gap int) []int {
	toks := gridTokens(v, avail, gap)
	if len(toks) < 2 || len(toks) > 6 {
		return nil // één kolom is stapelen; meer dan 6 wordt confetti
	}
	fixed, weight := 0, 0.0
	for _, t := range toks {
		if t.px > 0 {
			fixed += t.px
		} else {
			weight += t.fr
		}
	}
	free := avail - fixed - gap*(len(toks)-1)
	if free < 0 {
		return nil // vaste kolommen passen niet: stapelen
	}
	out := make([]int, len(toks))
	for i, t := range toks {
		if t.px > 0 {
			// een gedeclareerd smal spoor (tweakers' 0.75rem-rail) is legitiem
			out[i] = t.px
		} else if weight > 0 {
			out[i] = int(float64(free) * t.fr / weight)
			if out[i] < 60 {
				return nil // een fr-kolom moet nog iets kunnen dragen
			}
		}
	}
	return out
}

type gridTok struct {
	px int     // > 0: vaste breedte
	fr float64 // anders: gewicht
}

func gridTokens(v string, avail, gap int) []gridTok {
	var out []gridTok
	for _, tok := range splitTopLevel(strings.TrimSpace(v)) {
		switch {
		case strings.HasPrefix(tok, "["):
			// Regelnamen ([content-start]) benoemen lijnen, geen sporen.
			continue
		case strings.HasPrefix(tok, "repeat("):
			end := closeParen(tok, len("repeat(")-1)
			if end < 0 {
				return nil
			}
			inner := tok[len("repeat("):end]
			c := strings.IndexByte(inner, ',')
			if c < 0 {
				return nil
			}
			count, rest := strings.TrimSpace(inner[:c]), strings.TrimSpace(inner[c+1:])
			unit := gridTokens(rest, avail, gap)
			if len(unit) == 0 {
				return nil
			}
			n := 0
			switch count {
			case "auto-fill", "auto-fit":
				// De responsive standaard: zoveel kolommen van minstens
				// minmax-X als er passen.
				min := unit[0].px
				if min <= 0 {
					return nil
				}
				n = (avail + gap) / (min + gap)
				if n < 1 {
					n = 1
				}
				// De kolommen mogen meegroeien: maak ze gewichten.
				unit = []gridTok{{fr: 1}}
			default:
				m, err := strconv.Atoi(count)
				if err != nil || m < 1 || m > 6 {
					return nil
				}
				n = m
			}
			for i := 0; i < n; i++ {
				out = append(out, unit...)
			}
		case strings.HasPrefix(tok, "minmax("):
			end := closeParen(tok, len("minmax(")-1)
			if end < 0 {
				return nil
			}
			inner := tok[len("minmax("):end]
			// minmax(Xpx, 1fr): de min is interessant (voor auto-fill),
			// verder is het gewoon een groeikolom.
			if c := strings.IndexByte(inner, ','); c >= 0 {
				if px, ok := cssLen(strings.TrimSpace(inner[:c])); ok && px > 0 {
					out = append(out, gridTok{px: px, fr: 1})
					continue
				}
			}
			out = append(out, gridTok{fr: 1})
		case strings.HasPrefix(tok, "fit-content("):
			// fit-content(X): inhoud tot maximaal X — voor ons het spoor X.
			end := closeParen(tok, len("fit-content(")-1)
			if end < 0 {
				return nil
			}
			if px, ok := cssLenPct(tok[len("fit-content("):end], avail); ok && px > 0 {
				out = append(out, gridTok{px: px})
			} else {
				out = append(out, gridTok{fr: 1})
			}
		case strings.HasPrefix(tok, "calc("):
			if px, ok := cssLenPct(tok, avail); ok && px > 0 {
				out = append(out, gridTok{px: px})
			} else {
				return nil
			}
		case strings.HasSuffix(tok, "fr"):
			f, err := strconv.ParseFloat(strings.TrimSuffix(tok, "fr"), 64)
			if err != nil || f <= 0 {
				return nil
			}
			out = append(out, gridTok{fr: f})
		case tok == "auto" || tok == "min-content" || tok == "max-content":
			out = append(out, gridTok{fr: 1})
		case strings.HasSuffix(tok, "%"):
			if px, ok := cssLenPct(tok, avail); ok && px > 0 {
				out = append(out, gridTok{px: px})
			} else {
				return nil
			}
		default:
			if px, ok := cssLen(tok); ok && px > 0 {
				out = append(out, gridTok{px: px})
			} else {
				return nil
			}
		}
	}
	return out
}

// gridRailPx herkent het centreer-spoor "1fr <vast> 1fr" (tweakers'
// page-grid): de vaste middenbaan is de inhoudsbreedte, de fr-flanken
// zijn marge — dat is centrering, geen kolommenset. 0 = geen rail.
func gridRailPx(v string, avail, gap int) int {
	toks := gridTokens(v, avail, gap)
	if len(toks) != 3 || toks[0].px != 0 || toks[2].px != 0 || toks[1].px <= 0 {
		return 0
	}
	if toks[0].fr <= 0 || toks[2].fr <= 0 {
		return 0
	}
	return toks[1].px
}

// boldWeight: is deze font-weight vet op een font zonder gewichten?
func boldWeight(v string) (bold, known bool) {
	switch v {
	case "bold", "bolder":
		return true, true
	case "normal", "lighter":
		return false, true
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n >= 600, true
	}
	return false, false
}
