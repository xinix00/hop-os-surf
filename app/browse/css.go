// CSS, het déél dat een simpele renderer kan laten zien: kleuren, vet,
// verbergen, centreren, lettergrootte. De selector-kant bestond al —
// gost-dom's QuerySelectorAll draait op een echte selector-parser — dus
// hier woont alleen de declaratie-kant: een tolerante parser voor
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
// parsen weggegooid — het gros van echte stylesheets is layout-junk, en
// elke overgebleven regel kost straks een QuerySelectorAll.
func supportedProp(p string) bool {
	if strings.HasPrefix(p, "--") {
		return true // custom property: voer voor var()-resolutie
	}
	switch p {
	case "display", "visibility", "color", "background-color", "background",
		"background-image", "font-weight", "font-size", "text-align",
		"border", "border-color", "flex-direction", "float", "clear":
		return true
	}
	return false
}

// cssBorder leest een border(-color)-waarde: aan/uit plus de kleur (grijs
// als er alleen "1px solid" staat). "none", "0" en varianten zijn uit.
func cssBorder(v string) (color.RGBA, bool) {
	if v == "" || v == "none" || v == "0" || strings.HasPrefix(v, "0 ") ||
		strings.HasPrefix(v, "0px") || strings.Contains(v, "none") {
		return color.RGBA{}, false
	}
	col := colRule // default: de rustige grijze lijn
	for _, tok := range strings.Fields(v) {
		if c, ok := cssColor(tok); ok {
			col = c
		}
	}
	return col, true
}

// cssRule is één selector met zijn declaraties, klaar om te matchen.
type cssRule struct {
	sel   string
	spec  int // versimpelde specificiteit: id·100 + class/attr/pseudo·10 + tag
	seq   int // bronvolgorde (tiebreaker: later wint)
	decls props
}

// parseCSS vouwt een stylesheet uit tot regels. Tolerant: commentaar en
// onbekende @-blokken (met hun hele inhoud) verdwijnen, kapotte regels ook.
// Selector-groepen ("h1, h2") splitsen in losse regels met eigen
// specificiteit — QuerySelectorAll krijgt ze één voor één.
func parseCSS(src string, seq0 int) []cssRule {
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
			// @media: evalueren tegen onze (mobiele) viewport — mobile-first
			// sites zetten de basisstijl buiten de query, maar desktop-first
			// sites verstoppen juist hun móbiele stijl (menu dicht, header-
			// kleur) in een max-width-blok. Matcht de query, dan doen de
			// geneste regels gewoon mee. Andere @-blokken blijven genegeerd.
			if strings.HasPrefix(sel, "@media") && mediaMatches(sel[len("@media"):], mobileWidth) {
				rules = append(rules, parseCSS(body, seq0+len(rules))...)
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
				sel: one, spec: specificity(one), seq: seq0 + len(rules), decls: decls,
			})
		}
	}
	return rules
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
		// Wij zíjn een donker instrumentenpaneel: sites die hun dark theme
		// netjes via @media schepen krijgen hem. En bewegen doen we niet.
		switch prop {
		case "prefers-color-scheme":
			return val == "dark"
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

// cssLen: een CSS-lengte naar hele pixels (px, em/rem op 16px). Alleen voor
// media-voorwaarden — layout rekent nergens in CSS-lengtes.
func cssLen(v string) (int, bool) {
	v = strings.TrimSpace(v)
	mul := 1.0
	switch {
	case strings.HasSuffix(v, "rem"):
		v, mul = strings.TrimSuffix(v, "rem"), 16
	case strings.HasSuffix(v, "em"):
		v, mul = strings.TrimSuffix(v, "em"), 16
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
// focus — de hele :not vervalt), en :is(X)/:where(X) met één argument ís
// gewoon X. Belangrijk voor de verberg-regels van skip-links
// (".skip:not(:focus)") en voor selector-engines die :is() niet kennen.
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
					sel = sel[:i] + inner + sel[end+1:] // :is(X) = X
					changed = true
					from = i
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
	var clip, clipPath, w, h, pos, top, left, bottom, ti, op string
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
		case "bottom":
			bottom = val
		case "text-indent":
			ti = val
		case "opacity":
			op = val
		}
		if !supportedProp(prop) {
			continue
		}
		if p == nil {
			p = props{}
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
			// var(--x) als hele waarde: bewaren; na var-resolutie wordt het
			// alsnog een kleur (of valt het stil weg).
			if strings.HasPrefix(val, "var(") {
				p["background-color"] = val
			}
			continue
		}
		p[prop] = val
	}
	if srHidden(clip, clipPath, w, h, pos, top, left, ti, op) {
		if p == nil {
			p = props{}
		}
		p[srProp] = "1"
	}
	// Het logo-patroon: een leeg element met background-image en een vaste
	// maat ("image replacement"). De maten dragen we verder nergens — alleen
	// mét een background-image reizen ze mee, zodat de layout er een echte
	// afbeelding van kan maken en de regel-filter even streng blijft.
	if p["background-image"] != "" {
		if w != "" {
			p["width"] = w
		}
		if h != "" {
			p["height"] = h
		}
	}
	// Het positionerings-kado: fixed/sticky vertelt ons wat chrome is —
	// bovenin de header (pinnen bij het scrollen), onderin een cookiebar
	// (weg). Alleen die twee waarden reizen mee (absolute/relative is
	// binnen-layout: daar beginnen we niet aan), met hun top/bottom.
	if pos == "fixed" || pos == "sticky" {
		if p == nil {
			p = props{}
		}
		p["position"] = pos
		if top != "" {
			p["top"] = top
		}
		if bottom != "" {
			p["bottom"] = bottom
		}
	}
	return p
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
		// alpha (f[3]) negeren: wij composen niet — een doorschijnende
		// kleur wordt gewoon de kleur.
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
	case strings.HasSuffix(v, "em"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "em"), 64)
		px = f * 16
	case strings.HasSuffix(v, "rem"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "rem"), 64)
		px = f * 16
	case strings.HasSuffix(v, "%"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		px = f / 100 * 16
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
