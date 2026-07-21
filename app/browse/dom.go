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
