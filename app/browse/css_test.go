package browse

import (
	"image/color"
	"testing"
)

func TestMediaMatches(t *testing.T) {
	cases := []struct {
		q    string
		want bool
	}{
		{"(max-width: 600px)", true},  // 480 <= 600
		{"(max-width: 479px)", false}, //
		{"(min-width: 768px)", false}, // desktop-only
		{"(min-width: 320px)", true},  //
		{"screen and (max-width: 799px)", true},
		{"only screen and (min-width: 64em)", false}, // 1024px
		{"print", false},
		{"screen", true},
		{"(min-width: 900px), (max-width: 500px)", true}, // OR: tweede tak
		{"not screen", false},                            // conservatief
		{"(width <= 63.9375em)", true},                   // range-vorm
		{"(width >= 40em)", false},                       // 640px
		{"(30em <= width <= 40em)", true},                // 480 zit ertussen
		{"(orientation: portrait)", false},               // onbekend: nee
		{"(prefers-color-scheme: dark)", true},           // wij zijn donker
		{"(prefers-color-scheme: light)", false},
		{"(prefers-reduced-motion: reduce)", true}, // en bewegen niet
	}
	for _, c := range cases {
		if got := mediaMatches(c.q, mobileWidth); got != c.want {
			t.Errorf("mediaMatches(%q) = %v, wil %v", c.q, got, c.want)
		}
	}
}

func TestKleurModern(t *testing.T) {
	cases := []struct {
		in   string
		want color.RGBA
	}{
		{"rgb(223.1769911504, 224.8230088496, 224.8230088496)", color.RGBA{223, 224, 224, 0xFF}}, // SCSS-fracties
		{"rgb(100% 0% 50%)", color.RGBA{255, 0, 127, 0xFF}},                                      // moderne notatie
		{"hsl(0, 100%, 50%)", color.RGBA{255, 0, 0, 0xFF}},                                       // rood
		{"hsl(120deg, 100%, 25%)", color.RGBA{0, 127, 0, 0xFF}},                                  // donkergroen
	}
	for _, c := range cases {
		got, ok := cssColor(c.in)
		if !ok || got != c.want {
			t.Errorf("cssColor(%q) = %v/%v, wil %v", c.in, got, ok, c.want)
		}
	}
	if _, ok := cssColor("hsl(kapot)"); ok {
		t.Error("kapotte hsl hoort geen kleur te zijn")
	}
}

func TestSrHidden(t *testing.T) {
	// Het Bootstrap/WordPress sr-only-patroon — bewust geen display:none.
	p := parseDecls("position:absolute;width:1px;height:1px;clip:rect(0,0,0,0);overflow:hidden")
	if p[srProp] != "1" {
		t.Fatalf("sr-only-patroon niet herkend: %+v", p)
	}
	p = parseDecls("position:absolute;left:-9999px;top:auto")
	if p[srProp] != "1" {
		t.Fatalf("offscreen-patroon niet herkend: %+v", p)
	}
	// Image replacement: tekst het beeld uit geschoven.
	p = parseDecls("overflow:hidden;position:absolute;text-indent:-1000px")
	if p[srProp] != "1" {
		t.Fatalf("text-indent-replacement niet herkend: %+v", p)
	}
	// Skip-links: opacity 0 (nu.nl) en ver buiten beeld geparkeerd (tweakers).
	if p = parseDecls("opacity:0;position:fixed;left:2.5rem"); p[srProp] != "1" {
		t.Fatalf("opacity:0 niet herkend: %+v", p)
	}
	if p = parseDecls("position:fixed;left:-300px"); p[srProp] != "1" {
		t.Fatalf("offscreen-fixed niet herkend: %+v", p)
	}
	// Een gewone kaart met 1px border is géén sr-patroon.
	p = parseDecls("width:320px;height:200px;background-color:#fff")
	if p[srProp] == "1" {
		t.Fatalf("gewone maten ten onrechte sr-hidden: %+v", p)
	}
	// Het logo-patroon: maten reizen alleen mee mét een background-image.
	p = parseDecls("background-image:url(logo.png);width:120px;height:40px")
	if p["width"] != "120px" || p["height"] != "40px" {
		t.Fatalf("logo-maten niet meegereisd: %+v", p)
	}
	if p = parseDecls("width:120px;height:40px"); p["width"] != "" {
		t.Fatalf("maten zonder background-image horen te vervallen: %+v", p)
	}
}

func TestSimplifySelector(t *testing.T) {
	for in, want := range map[string]string{
		".skip:not(:focus)":                        ".skip",
		".skip:not(:focus):not(:active)":           ".skip",
		":is(twk-site-menu .site-logo)+.site-name": "twk-site-menu .site-logo+.site-name",
		":where(.a) .b":                            ".a .b",
		".card:not(.disabled)":                     ".card:not(.disabled)", // echt onderscheid: blijft
		":is(.a, .b) .c":                           ":is(.a, .b) .c",       // meerdere argumenten: blijft
	} {
		if got := simplifySelector(in); got != want {
			t.Errorf("simplifySelector(%q) = %q, wil %q", in, got, want)
		}
	}
}

func TestDeadSelector(t *testing.T) {
	for sel, dead := range map[string]bool{
		"a:hover":              true,
		".x::before":           true,
		"::-webkit-scrollbar":  true,
		".menu > li":           false,
		"nav .item":            false,
		"input:checked+label":  true,
		".card:not(.disabled)": false,
	} {
		if got := deadSelector(sel); got != dead {
			t.Errorf("deadSelector(%q) = %v, wil %v", sel, got, dead)
		}
	}
}
