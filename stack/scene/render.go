// Render: het "HOP Slate"-thema (stack/pixel/theme.go) voor álle scene-apps:
// de stijl wordt hier afgedwongen, apps kúnnen niet afwijken (dat is een
// feature). Verzonken vlakken voor lijsten en meters, kaartjes voor values,
// knoppen met bevel en notch, Spleen-fonts. Partiële hertekening gaat per
// widget-rect: PATCH #id → RenderNode(n) → alleen dát rect als damage.
package scene

import (
	"fmt"
	"image"
	"image/color"

	"github.com/xinix00/hop-os-surf/stack/pixel"
)

// Panel-palet: aliassen op het thema, plus de scene-eigen tinten.
var (
	colBG      = pixel.ColPanel
	colText    = pixel.ColText
	colDim     = pixel.ColDim
	colHeading = pixel.ColText
	colMono    = color.RGBA{0x8C, 0xC8, 0xAA, 0xFF} // monospaced groen
	colAccent  = pixel.ColAccent
	colEdge    = pixel.ColLineDim
	colSel     = color.RGBA{0x1E, 0x3A, 0x66, 0xFF} // list-selectie
	colZebra   = color.RGBA{0x0E, 0x15, 0x27, 0xFF} // om-en-om lijstrijen
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
		switch n.Style {
		case StyleHeading:
			// Kop: accent-marker links + F16 — de "sectie begint hier"-anker.
			f := pixel.F16
			if r.Dy() < f.H {
				f = pixel.F12
			}
			y := midY(r, f.H)
			pixel.Fill(img, image.Rect(r.Min.X, y+1, r.Min.X+3, y+f.H-1), colAccent)
			pixel.DrawText(img, r.Min.X+8, y, f, 1, colHeading, clipFace(n.Text, r.Dx()-8, f, 1))
		case StyleMono:
			pixel.DrawText(img, r.Min.X, midY(r, 12), pixel.F12, 1, colMono, clipFace(n.Text, r.Dx(), pixel.F12, 1))
		default:
			pixel.DrawText(img, r.Min.X, midY(r, 12), pixel.F12, 1, colText, clipFace(n.Text, r.Dx(), pixel.F12, 1))
		}

	case KindValue:
		// Het live-cijfer als kaartje: verzonken vlak, groot getal, eenheid
		// klein en gedimd erachter.
		pixel.FillNotched(img, r, pixel.ColSunk)
		pixel.OutlineNotched(img, r, colEdge)
		s := n.Text
		scale := fitScale(s, n.Unit, r)
		w := pixel.TextWidth(pixel.F16, scale, s)
		x := r.Min.X + (r.Dx()-w-pixel.TextWidth(pixel.F12, 1, n.Unit))/2
		if x < r.Min.X+3 {
			x = r.Min.X + 3
		}
		y := midY(r, 16*scale)
		pixel.DrawText(img, x, y, pixel.F16, scale, colHeading, s)
		if n.Unit != "" {
			pixel.DrawText(img, x+w+3, y+16*scale-14, pixel.F12, 1, colDim, n.Unit)
		}

	case KindGauge, KindBar:
		// Meter: verzonken track, accent-vulling met een lichte bovenrand;
		// gauge krijgt schaalstrepen en de waarde als tekst erin.
		pixel.Fill(img, r, pixel.ColSunk)
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
		fr := image.Rect(inner.Min.X, inner.Min.Y, inner.Min.X+fill, inner.Max.Y)
		pixel.Fill(img, fr, colAccent)
		if fill > 2 && fr.Dy() > 3 {
			pixel.Fill(img, image.Rect(fr.Min.X, fr.Min.Y, fr.Max.X, fr.Min.Y+1), pixel.ColAccentL)
		}
		if n.Kind == KindGauge {
			for i := 1; i < 4; i++ { // schaalstrepen op 25/50/75%
				x := inner.Min.X + inner.Dx()*i/4
				pixel.Fill(img, image.Rect(x, inner.Min.Y, x+1, inner.Min.Y+4), colDim)
				pixel.Fill(img, image.Rect(x, inner.Max.Y-4, x+1, inner.Max.Y), colDim)
			}
			txt := fmt.Sprintf("%d%s", n.Val, n.Unit)
			pixel.DrawTextCentered(img, r, pixel.F12, 1, colHeading, txt)
		}

	case KindButton:
		// Functionele kleurgroepen (StylePrimary/StyleDanger): een primaire
		// actie is accent-gevuld, een destructieve toont rood — en ingedrukt
		// is overal "vol + tekst een pixel omlaag".
		fill, border, hovFill, hovBorder, downFill, tcol := pixel.ColRaise, pixel.ColLine,
			pixel.ColRaiseHi, colAccent, pixel.ColAccentD, colHeading
		switch n.Style {
		case StylePrimary:
			fill, border = pixel.ColAccentD, colAccent
			hovFill, hovBorder = colAccent, pixel.ColAccentL
			downFill = colAccent
		case StyleDanger:
			border, tcol = pixel.ColLineDim, pixel.ColErr
			hovFill, hovBorder = pixel.ColRaiseHi, pixel.ColErr
			downFill = pixel.ColErr
		}
		txt := r
		switch {
		case n.Pressed:
			pixel.FillNotched(img, r, downFill)
			pixel.OutlineNotched(img, r, hovBorder)
			txt, tcol = r.Add(image.Pt(0, 1)), colHeading
		case n.Hover:
			pixel.Card(img, r, hovFill, hovBorder)
			if n.Style == StyleDanger {
				tcol = colHeading
			}
		default:
			pixel.Card(img, r, fill, border)
		}
		pixel.DrawTextCentered(img, txt, pixel.F12, 1, tcol, clipFace(n.Text, r.Dx()-6, pixel.F12, 1))

	case KindList:
		pixel.Fill(img, r, pixel.ColSunk)
		rows := (r.Dy() - 2) / listRowH
		for i := 0; i < rows; i++ {
			idx := n.Scroll + i
			if idx >= len(n.Items) {
				break
			}
			rowR := image.Rect(r.Min.X+1, r.Min.Y+1+i*listRowH, r.Max.X-1, r.Min.Y+1+(i+1)*listRowH)
			col := colText
			switch {
			case int32(idx) == n.Sel:
				pixel.Fill(img, rowR, colSel)
				pixel.Fill(img, image.Rect(rowR.Min.X, rowR.Min.Y, rowR.Min.X+2, rowR.Max.Y), colAccent)
				col = colHeading
			case idx%2 == 1:
				pixel.Fill(img, rowR, colZebra) // zebra: leesbare regels
			}
			pixel.DrawText(img, rowR.Min.X+6, rowR.Min.Y+(listRowH-12)/2, pixel.F12, 1, col,
				clipFace(n.Items[idx], rowR.Dx()-12, pixel.F12, 1))
		}
		// Scroll-indicator: een 3px-duim rechts, alleen bij overloop.
		if total := len(n.Items); total > rows && rows > 0 {
			track := image.Rect(r.Max.X-4, r.Min.Y+1, r.Max.X-1, r.Max.Y-1)
			pixel.Fill(img, track, colZebra)
			th := track.Dy() * rows / total
			if th < 8 {
				th = 8
			}
			ty := track.Min.Y + (track.Dy()-th)*n.Scroll/(total-rows)
			pixel.Fill(img, image.Rect(track.Min.X, ty, track.Max.X, ty+th), pixel.ColLine)
		}
		pixel.Outline(img, r, colEdge)

	case KindCanvas:
		// v1-placeholder (open punt in het dossier): het DAMAGE-doorvoerpad
		// komt zodra de eerste app hem nodig heeft. De rechthoek is er al.
		pixel.Outline(img, r, colEdge)
		pixel.DrawTextCentered(img, r, pixel.F12, 1, colDim, "canvas")

	default:
		// Onbekende widget (nieuwere app op oudere display, §4-versioning):
		// lege rechthoek, geen crash — de rest van de scene werkt gewoon.
		pixel.Outline(img, r, colEdge)
	}
}

// listRowH is de rijhoogte van een list (12px font + ademruimte).
const listRowH = 14

func midY(r image.Rectangle, textH int) int {
	y := r.Min.Y + (r.Dy()-textH)/2
	if y < r.Min.Y {
		y = r.Min.Y
	}
	return y
}

// fitScale kiest de grootste F16-schaal waarop tekst+eenheid in het rect past
// (met de kaartje-marge van 3px).
func fitScale(s, unit string, r image.Rectangle) int {
	for scale := 3; scale > 1; scale-- {
		if pixel.TextWidth(pixel.F16, scale, s)+pixel.TextWidth(pixel.F12, 1, unit)+8 <= r.Dx() &&
			16*scale+4 <= r.Dy() {
			return scale
		}
	}
	return 1
}

// clipFace kapt tekst die niet in width past (geen wrap — dat is §4: canvas).
func clipFace(s string, width int, f pixel.Face, scale int) string {
	max := width / (f.W * scale)
	if max < 0 {
		max = 0
	}
	if len(s) > max {
		if max > 1 {
			return s[:max-1] + "." // bitmapfont is ASCII; een punt als ellipsis
		}
		return ""
	}
	return s
}
