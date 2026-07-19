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
	switch p {
	case "display", "visibility", "color", "background-color", "background",
		"font-weight", "font-size", "text-align":
		return true
	}
	return false
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
			// @media e.d.: block() heeft de hele geneste inhoud al
			// overgeslagen; geneste regels bewust negeren (geen media
			// queries op een 8x8-font).
			continue
		}
		decls := parseDecls(body)
		if len(decls) == 0 {
			continue
		}
		for _, one := range strings.Split(sel, ",") {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			rules = append(rules, cssRule{
				sel: one, spec: specificity(one), seq: seq0 + len(rules), decls: decls,
			})
		}
	}
	return rules
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

// parseDecls parset "prop: waarde; prop: waarde" — alleen de gedragen
// properties blijven over. "background: <kleur>" telt als
// background-color als de waarde een kleur is (de gangbare shorthand).
func parseDecls(s string) props {
	var p props
	for _, d := range strings.Split(s, ";") {
		colon := strings.IndexByte(d, ':')
		if colon < 0 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(d[:colon]))
		val := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(d[colon+1:]), "!important")))
		if val == "" || !supportedProp(prop) {
			continue
		}
		if prop == "background" {
			if _, ok := cssColor(val); !ok {
				continue
			}
			prop = "background-color"
		}
		if p == nil {
			p = props{}
		}
		p[prop] = val
	}
	return p
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
		inner := v[strings.IndexByte(v, '(')+1:]
		if j := strings.IndexByte(inner, ')'); j >= 0 {
			inner = inner[:j]
		}
		inner = strings.ReplaceAll(inner, "/", " ")
		f := strings.FieldsFunc(inner, func(r rune) bool { return r == ',' || r == ' ' })
		if len(f) < 3 {
			return color.RGBA{}, false
		}
		var c [3]uint8
		for i := 0; i < 3; i++ {
			n, err := strconv.Atoi(strings.TrimSpace(f[i]))
			if err != nil || n < 0 || n > 255 {
				return color.RGBA{}, false
			}
			c[i] = uint8(n)
		}
		// alpha (f[3]) negeren: wij composen niet — een doorschijnende
		// kleur wordt gewoon de kleur.
		return color.RGBA{c[0], c[1], c[2], 0xFF}, true
	}
	if c, ok := namedColors[v]; ok {
		return c, true
	}
	return color.RGBA{}, false
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
	case px >= 24:
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
