// De rekenmachine als scene-boom (P2, 20-07): de display-node rendert en
// hit-test de knoppen, hier komt alleen nog "knop 7 geklikt" binnen. Het
// window is daarmee van ~130KB hover-damage naar bytes per toetsaanslag.
package calc

import "github.com/xinix00/hop-os-surf/stack/scene"

// Tree bouwt de rekenmachine-boom: het display-Value bovenin, het 4×4-grid
// en de volle =-rij. press krijgt de Press-toets van elke aangeklikte knop;
// de aanroeper werkt daarna het display bij met SetText(display, Line(c)).
func Tree(press func(key byte)) (root, display *scene.Node) {
	display = scene.Value("0", "")
	kids := []*scene.Node{display.Sized(56)}
	for r := 0; r < 4; r++ {
		btns := make([]*scene.Node, 4)
		for c := 0; c < 4; c++ {
			k := grid[r][c] // kopie: de closure hoort bij déze knop
			btns[c] = scene.Button(label(k), func() { press(k) })
		}
		kids = append(kids, scene.Row(2, btns...))
	}
	kids = append(kids, scene.Button("=", func() { press('=') }))
	root = scene.Col(4, kids...)
	return root, display
}

// Line is de displayregel voor het Value-widget: het getal, met de
// openstaande operator ervoor (Derek 19-07: "als je de x invult, laat die
// dan zien").
func Line(c *Calc) string {
	if op := c.Op(); op != 0 {
		return label(op) + " " + c.Display()
	}
	return c.Display()
}
