// Package pixel is de kleine gedeelde tekenlaag van de SURF-stack: het
// 8x8-font en rechthoek-helpers, op een kaal image.RGBA. Gebruikt door de
// compositor (titelbalken) én door pixel-apps (calculator-knoppen) — tot de
// scene-laag (P2) tekst en widgets display-side tekent en apps dit niet meer
// nodig hebben.
package pixel

import (
	"image"
	"image/color"
	"image/draw"
)

// Fill vult rechthoek r.
func Fill(img *image.RGBA, r image.Rectangle, col color.RGBA) {
	draw.Draw(img, r, image.NewUniform(col), image.Point{}, draw.Src)
}

// Outline tekent een 1-px rand langs de binnenkant van r.
func Outline(img *image.RGBA, r image.Rectangle, col color.RGBA) {
	for x := r.Min.X; x < r.Max.X; x++ {
		img.SetRGBA(x, r.Min.Y, col)
		img.SetRGBA(x, r.Max.Y-1, col)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		img.SetRGBA(r.Min.X, y, col)
		img.SetRGBA(r.Max.X-1, y, col)
	}
}

// StringWidth is de pixelbreedte van s op deze schaal.
func StringWidth(s string, scale int) int { return len(s) * 8 * scale }

// DrawString tekent ASCII-tekst met het 8x8-font op pixelpositie (x,y).
func DrawString(img *image.RGBA, x, y, scale int, col color.RGBA, s string) {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < 0x20 || ch >= 0x80 {
			ch = '?'
		}
		drawGlyph(img, x+i*8*scale, y, scale, col, ch)
	}
}

// DrawStringCentered centreert s horizontaal én verticaal in r.
func DrawStringCentered(img *image.RGBA, r image.Rectangle, scale int, col color.RGBA, s string) {
	x := r.Min.X + (r.Dx()-StringWidth(s, scale))/2
	y := r.Min.Y + (r.Dy()-8*scale)/2
	DrawString(img, x, y, scale, col, s)
}

func drawGlyph(img *image.RGBA, x, y, scale int, col color.RGBA, ch byte) {
	for gy := 0; gy < 8; gy++ {
		bits := font8x8[ch][gy]
		for gx := 0; gx < 8; gx++ {
			if bits>>gx&1 == 0 {
				continue
			}
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					px, py := x+gx*scale+sx, y+gy*scale+sy
					if image.Pt(px, py).In(img.Bounds()) {
						img.SetRGBA(px, py, col)
					}
				}
			}
		}
	}
}
