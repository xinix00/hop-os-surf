// Layout: col/row met vaste maten en gewichten — het hele layoutmodel (§4:
// "geen CSS, geen absolute positionering"). Pad is de ruimte róndom elk kind
// (dus ook tussen kinderen); langs de hoofdas krijgt een kind zijn Size in
// pixels, of anders een gewicht-aandeel van de restruimte (Weight, default 1).
// Alles wat hier niet in past is per §4 een canvas.
package scene

import "image"

// Layout kent rects toe aan de hele boom binnen w×h (display-kant; draait
// óók bij CONFIGURE — de re-flow waarvoor de app niet gewekt wordt).
func Layout(root *Node, w, h int) {
	root.Rect = image.Rect(0, 0, w, h)
	layoutNode(root)
}

func layoutNode(n *Node) {
	if len(n.Children) == 0 {
		return
	}
	horiz := n.Kind == KindRow
	pad := int(n.Pad)
	r := n.Rect

	// Hoofdas-lengte minus de pads (n+1 tussenruimtes).
	main := r.Dy()
	if horiz {
		main = r.Dx()
	}
	main -= pad * (len(n.Children) + 1)

	// Vaste maten eraf, gewichten optellen.
	fixed, weights := 0, 0
	for _, c := range n.Children {
		if c.Size > 0 {
			fixed += int(c.Size)
		} else {
			w := int(c.Weight)
			if w == 0 {
				w = 1
			}
			weights += w
		}
	}
	rest := main - fixed
	if rest < 0 {
		rest = 0
	}

	// Toekennen, restruimte naar rato van gewicht; afronding schuift door
	// zodat de laatste flex-kind de rest opvult (geen gaten).
	pos := pad
	used := 0
	flexSeen := 0
	for _, c := range n.Children {
		ext := int(c.Size)
		if ext == 0 {
			w := int(c.Weight)
			if w == 0 {
				w = 1
			}
			flexSeen += w
			ext = rest*flexSeen/weights - used
			used += ext
		}
		if horiz {
			c.Rect = image.Rect(r.Min.X+pos, r.Min.Y+pad, r.Min.X+pos+ext, r.Max.Y-pad)
		} else {
			c.Rect = image.Rect(r.Min.X+pad, r.Min.Y+pos, r.Max.X-pad, r.Min.Y+pos+ext)
		}
		pos += ext + pad
		layoutNode(c)
	}
}
