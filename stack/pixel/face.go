// De Spleen-bitmapfonts: 6x12 (dicht UI-werk: lijsten, knoppen, labels) en
// 8x16 (chrome, koppen, values). Zelfde kosten als het oude 8x8 (één blit,
// een paar KB data), maar echte letters met stokken en staarten — het
// verschil tussen "1985" en "netjes". Sinds 20-07 het enige font: ook de
// browser rekent zijn grid in deze celmaten (charW/charH in browse.go).
package pixel

import (
	"image"
	"image/color"
)

// Face is één bitmapfont: vaste celmaat, ASCII 32..126, MSB = linkerpixel.
type Face struct {
	W, H   int
	glyphs [][]byte // index ch-32 → H bytes
}

var (
	F12 = Face{W: 6, H: 12, glyphs: rows12()}
	F16 = Face{W: 8, H: 16, glyphs: rows16()}
)

func rows12() [][]byte {
	g := make([][]byte, 95)
	for i := range spleen6x12 {
		g[i] = spleen6x12[i][:]
	}
	return g
}

func rows16() [][]byte {
	g := make([][]byte, 95)
	for i := range spleen8x16 {
		g[i] = spleen8x16[i][:]
	}
	return g
}

// TextWidth is de breedte van s in deze face op deze schaal.
func TextWidth(f Face, scale int, s string) int { return len(s) * f.W * scale }

// DrawText tekent s vanaf (x,y) — y is de bovenkant van de cel.
func DrawText(img *image.RGBA, x, y int, f Face, scale int, col color.RGBA, s string) {
	if scale < 1 {
		scale = 1
	}
	for i := 0; i < len(s); i++ {
		drawFaceGlyph(img, x+i*f.W*scale, y, f, scale, col, s[i])
	}
}

// DrawTextCentered tekent s gecentreerd in r.
func DrawTextCentered(img *image.RGBA, r image.Rectangle, f Face, scale int, col color.RGBA, s string) {
	x := r.Min.X + (r.Dx()-TextWidth(f, scale, s))/2
	y := r.Min.Y + (r.Dy()-f.H*scale)/2
	DrawText(img, x, y, f, scale, col, s)
}

func drawFaceGlyph(img *image.RGBA, x, y int, f Face, scale int, col color.RGBA, ch byte) {
	if ch < 32 || ch > 126 {
		ch = '?'
	}
	rows := f.glyphs[ch-32]
	b := img.Bounds()
	for gy := 0; gy < f.H; gy++ {
		bits := rows[gy]
		for gx := 0; gx < f.W; gx++ {
			if bits&(0x80>>gx) == 0 {
				continue
			}
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					px, py := x+gx*scale+sx, y+gy*scale+sy
					if px >= b.Min.X && px < b.Max.X && py >= b.Min.Y && py < b.Max.Y {
						img.SetRGBA(px, py, col)
					}
				}
			}
		}
	}
}
