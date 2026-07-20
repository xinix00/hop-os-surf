// Render: de instrumentenpaneel-stijl van §5 — vlak, 1-px randen, harde
// kleuren, geen schaduwen. Eén look-and-feel voor álle scene-apps: de stijl
// wordt hier afgedwongen, apps kúnnen niet afwijken (dat is een feature).
// Partiële hertekening gaat per widget-rect: PATCH #id → RenderNode(n) →
// alleen dát rect als damage het net over.
package scene

import (
	"fmt"
	"image"
	"image/color"

	"github.com/xinix00/hop-os-surf/stack/pixel"
)

// Het panel-palet (afgestemd op de compositor-chrome).
var (
	colBG      = color.RGBA{0x14, 0x1b, 0x2a, 0xFF} // venster-achtergrond
	colText    = color.RGBA{0xDC, 0xE2, 0xF0, 0xFF}
	colDim     = color.RGBA{0x8A, 0x94, 0xAA, 0xFF}
	colHeading = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	colMono    = color.RGBA{0x8C, 0xC8, 0xAA, 0xFF}
	colAccent  = color.RGBA{0x3B, 0x82, 0xF6, 0xFF} // knoppen/vulling
	colBtn     = color.RGBA{0x23, 0x2D, 0x46, 0xFF}
	colBtnHov  = color.RGBA{0x32, 0x3E, 0x5F, 0xFF}
	colEdge    = color.RGBA{0x3A, 0x46, 0x63, 0xFF}
	colSel     = color.RGBA{0x2A, 0x4A, 0x7A, 0xFF} // list-selectie
)

// Render tekent de hele boom (achtergrond + alle widgets) in img.
func Render(img *image.RGBA, root *Node) {
	pixel.Fill(img, root.Rect, colBG)
	renderTree(img, root)
}

func renderTree(img *image.RGBA, n *Node) {
	RenderNode(img, n)
	for _, c := range n.Children {
		renderTree(img, c)
	}
}

// RenderNode tekent één widget in zijn Rect (containers tekenen niets; hun
// achtergrond is de venster-achtergrond). Voor een PATCH-hertekening: eerst
// het rect wissen, dan de widget — precies wat een partiële damage nodig heeft.
func RenderNode(img *image.RGBA, n *Node) {
	r := n.Rect
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return
	}
	switch n.Kind {
	case KindCol, KindRow:
		return // layout, geen pixels
	}
	pixel.Fill(img, r, colBG)

	switch n.Kind {
	case KindLabel:
		col, scale := colText, 1
		switch n.Style {
		case StyleHeading:
			col, scale = colHeading, 2
		case StyleMono:
			col = colMono
		}
		pixel.DrawString(img, r.Min.X, midY(r, 8*scale), scale, col, clipText(n.Text, r.Dx(), scale))

	case KindValue:
		// Het live-cijfer: zo groot als het rect toelaat, eenheid klein erachter.
		s := n.Text
		scale := fitScale(s, n.Unit, r)
		w := pixel.StringWidth(s, scale)
		x := r.Min.X + (r.Dx()-w-pixel.StringWidth(n.Unit, 1))/2
		if x < r.Min.X {
			x = r.Min.X
		}
		y := midY(r, 8*scale)
		pixel.DrawString(img, x, y, scale, colHeading, s)
		if n.Unit != "" {
			pixel.DrawString(img, x+w+2, y+8*(scale-1), 1, colDim, n.Unit)
		}

	case KindGauge, KindBar:
		// Meter: 1-px rand, vulling naar rato; gauge krijgt schaalstrepen en
		// de waarde als tekst erin — het verschil tussen "instrument" en
		// "voortgang" zonder een tweede tekenpad.
		pixel.Outline(img, r, colEdge)
		lo, hi := n.Min, n.Max
		if hi <= lo {
			hi = lo + 100
		}
		v := n.Val
		if v < lo {
			v = lo
		}
		if v > hi {
			v = hi
		}
		inner := image.Rect(r.Min.X+1, r.Min.Y+1, r.Max.X-1, r.Max.Y-1)
		fill := inner.Dx() * int(v-lo) / int(hi-lo)
		pixel.Fill(img, image.Rect(inner.Min.X, inner.Min.Y, inner.Min.X+fill, inner.Max.Y), colAccent)
		if n.Kind == KindGauge {
			for i := 1; i < 4; i++ { // schaalstrepen op 25/50/75%
				x := inner.Min.X + inner.Dx()*i/4
				pixel.Fill(img, image.Rect(x, inner.Min.Y, x+1, inner.Min.Y+4), colDim)
				pixel.Fill(img, image.Rect(x, inner.Max.Y-4, x+1, inner.Max.Y), colDim)
			}
			txt := fmt.Sprintf("%d%s", n.Val, n.Unit)
			pixel.DrawStringCentered(img, r, 1, colHeading, txt)
		}

	case KindButton:
		bg := colBtn
		if n.Pressed {
			bg = colAccent
		} else if n.Hover {
			bg = colBtnHov
		}
		pixel.Fill(img, r, bg)
		pixel.Outline(img, r, colEdge)
		pixel.DrawStringCentered(img, r, 1, colHeading, n.Text)

	case KindList:
		pixel.Outline(img, r, colEdge)
		rows := (r.Dy() - 2) / listRowH
		for i := 0; i < rows; i++ {
			idx := n.Scroll + i
			if idx >= len(n.Items) {
				break
			}
			rowR := image.Rect(r.Min.X+1, r.Min.Y+1+i*listRowH, r.Max.X-1, r.Min.Y+1+(i+1)*listRowH)
			col := colText
			if int32(idx) == n.Sel {
				pixel.Fill(img, rowR, colSel)
				col = colHeading
			}
			pixel.DrawString(img, rowR.Min.X+4, rowR.Min.Y+(listRowH-8)/2, 1, col,
				clipText(n.Items[idx], rowR.Dx()-8, 1))
		}

	case KindCanvas:
		// v1-placeholder (open punt in het dossier): het DAMAGE-doorvoerpad
		// komt zodra de eerste app hem nodig heeft. De rechthoek is er al.
		pixel.Outline(img, r, colEdge)
		pixel.DrawStringCentered(img, r, 1, colDim, "canvas")

	default:
		// Onbekende widget (nieuwere app op oudere display, §4-versioning):
		// lege rechthoek, geen crash — de rest van de scene werkt gewoon.
		pixel.Outline(img, r, colEdge)
	}
}

// listRowH is de rijhoogte van een list (8px font + ademruimte).
const listRowH = 14

func midY(r image.Rectangle, textH int) int {
	y := r.Min.Y + (r.Dy()-textH)/2
	if y < r.Min.Y {
		y = r.Min.Y
	}
	return y
}

// fitScale kiest de grootste fontschaal waarop tekst+eenheid in het rect past.
func fitScale(s, unit string, r image.Rectangle) int {
	for scale := 4; scale > 1; scale-- {
		if pixel.StringWidth(s, scale)+pixel.StringWidth(unit, 1)+2 <= r.Dx() && 8*scale <= r.Dy() {
			return scale
		}
	}
	return 1
}

// clipText kapt tekst die niet in width past (geen wrap — dat is §4: canvas).
func clipText(s string, width, scale int) string {
	max := width / (8 * scale)
	if max < 0 {
		max = 0
	}
	if len(s) > max {
		if max > 1 {
			return s[:max-1] + "." // 8x8-font is ASCII; een punt als ellipsis
		}
		return ""
	}
	return s
}
