// De DOM van deze browser is gewoon golang.org/x/net/html — dezelfde
// parser die elke Go-scraper (en gost-dom zelf) gebruikt: WHATWG-compliant
// error-recovery, puur Go, dus ook op tamago. Geen interfacelaag eromheen;
// deze paar helpers zijn alles wat de layout en de sessie nodig hebben.
package browse

import (
	"strings"

	"golang.org/x/net/html"
)

// attr geeft een attribuut van een element; ok=false als het ontbreekt.
func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

// textContent plakt alle tekst onder n aan elkaar (zoals DOM textContent).
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
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

// imgSrc: de bron van een <img> — src, of de lazy-loading-conventie
// data-src, of de eerste kandidaat uit (data-)srcset. Laden (Session) en
// leggen (layout) gebruiken allebei déze sleutel. data:-URI's zijn
// placeholder-pixels: die tellen niet, dan liever de echte lazy-bron.
func imgSrc(el *html.Node) string {
	if v, ok := attr(el, "src"); ok {
		if v = strings.TrimSpace(v); v != "" && !strings.HasPrefix(v, "data:") {
			return v
		}
	}
	if v, ok := attr(el, "data-src"); ok {
		if v = strings.TrimSpace(v); v != "" && !strings.HasPrefix(v, "data:") {
			return v
		}
	}
	for _, name := range []string{"srcset", "data-srcset"} {
		if v, ok := attr(el, name); ok {
			if c := srcsetFirst(v); c != "" {
				return c
			}
		}
	}
	return ""
}

// srcsetFirst: de eerste bruikbare kandidaat-URL uit een srcset-waarde.
func srcsetFirst(v string) string {
	for _, cand := range strings.Split(v, ",") {
		f := strings.Fields(strings.TrimSpace(cand))
		if len(f) > 0 && !strings.HasPrefix(f[0], "data:") {
			return f[0]
		}
	}
	return ""
}

// findEl zoekt het eerste element met deze (lowercase) tagnaam onder n.
func findEl(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if e := findEl(c, tag); e != nil {
			return e
		}
	}
	return nil
}

// eachEl bezoekt elk element onder n (n zelf inbegrepen), diepte-eerst —
// dé wandeling onder loadStyles, loadImages, hints en use-resolutie.
func eachEl(n *html.Node, f func(*html.Node)) {
	if n == nil {
		return
	}
	if n.Type == html.ElementNode {
		f(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		eachEl(c, f)
	}
}
