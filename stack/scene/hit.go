// Hit-testing: display-side, één keer voor alle apps (§4-winst 2 — de app
// krijgt "#save clicked", geen coördinaten).
package scene

import "image"

// HitAt geeft de diepste interactieve widget (button/list) op punt x,y;
// nil als daar niets klikbaars ligt.
func HitAt(root *Node, x, y int) *Node {
	var hit *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if !image.Pt(x, y).In(n.Rect) {
			return
		}
		if n.Kind == KindButton || n.Kind == KindList {
			hit = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return hit
}
