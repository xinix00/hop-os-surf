// Package pixel is de kleine gedeelde tekenlaag van de SURF-stack:
// rechthoek-helpers en de Spleen-bitmapfonts (face.go), plus het thema
// (theme.go), op een kaal image.RGBA. Gebruikt door de compositor, de
// scene-renderer en de pixel-apps. Het oude 8x8-font is 20-07 vervangen
// door Spleen 6x12/8x16 — zelfde blit, echte letters.
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
