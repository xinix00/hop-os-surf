// Het "HOP Slate"-thema (20-07): één stijl voor chrome, widgets en apps —
// basic maar netjes. De regels:
//
//  1. Eén accent, veel rust: diepe slate-vlakken, één HOP-blauw accent;
//     status-kleuren (ok/warn/err) alléén voor status.
//  2. Pixel-precisie als esthetiek: 1px licht-boven/donker-onder ("bevel"),
//     1px afgesnoepte hoeken ("notch") — geen anti-aliasing, het bitmapfont
//     hoort erbij.
//  3. Diepte zonder blur: een 2px verdonker-schaduw (geen alpha-gradients),
//     verzonken vlakken (Sunk) voor tracks/lijsten, verhoogde (Raise) voor
//     knoppen/kaartjes.
//  4. Compressie-vriendelijk: vlakke vullingen en per-rij gradients — de
//     damage-stream blijft bytes, geen kilobytes.
//
// Alles heet Col* zodat een callsite leest als pixel.ColAccent.
package pixel

import (
	"image"
	"image/color"
)

var (
	// Desktop (verticale gradient, boven → onder).
	ColDesk0 = color.RGBA{0x12, 0x1A, 0x30, 0xFF}
	ColDesk1 = color.RGBA{0x06, 0x0A, 0x14, 0xFF}

	// Vlakken.
	ColPanel   = color.RGBA{0x12, 0x1A, 0x2E, 0xFF} // window-body
	ColSunk    = color.RGBA{0x0B, 0x11, 0x20, 0xFF} // verzonken: lists, tracks
	ColRaise   = color.RGBA{0x1D, 0x28, 0x44, 0xFF} // verhoogd: knoppen, kaartjes
	ColRaiseHi = color.RGBA{0x28, 0x36, 0x5C, 0xFF} // hover
	ColBevel   = color.RGBA{0x33, 0x42, 0x6B, 0xFF} // 1px lichtrand op Raise

	// Lijnen.
	ColLine    = color.RGBA{0x2E, 0x3E, 0x66, 0xFF}
	ColLineDim = color.RGBA{0x1C, 0x26, 0x40, 0xFF}

	// Tekst.
	ColText = color.RGBA{0xE9, 0xEE, 0xF8, 0xFF}
	ColDim  = color.RGBA{0x8D, 0x9C, 0xBE, 0xFF}

	// Accent + status.
	ColAccent  = color.RGBA{0x4C, 0x8D, 0xFF, 0xFF}
	ColAccentD = color.RGBA{0x2B, 0x5C, 0xC8, 0xFF}
	ColAccentL = color.RGBA{0x8A, 0xB4, 0xFF, 0xFF}
	ColOK      = color.RGBA{0x45, 0xC6, 0x8A, 0xFF}
	ColWarn    = color.RGBA{0xE6, 0xAE, 0x4A, 0xFF}
	ColErr     = color.RGBA{0xE8, 0x64, 0x64, 0xFF}
)

// VGrad vult clip met de verticale gradient van top naar bottom, uitgemeten
// over full — deterministisch per y, dus partieel hertekenen geeft exact
// dezelfde pixels als een volle hertekening (en elke rij is één kleur: de
// deflate in de kijker-stream blijft er niets van voelen).
func VGrad(img *image.RGBA, clip, full image.Rectangle, top, bottom color.RGBA) {
	clip = clip.Intersect(img.Bounds())
	h := full.Dy() - 1
	if h < 1 {
		h = 1
	}
	for y := clip.Min.Y; y < clip.Max.Y; y++ {
		t := y - full.Min.Y
		c := color.RGBA{
			uint8(int(top.R) + (int(bottom.R)-int(top.R))*t/h),
			uint8(int(top.G) + (int(bottom.G)-int(top.G))*t/h),
			uint8(int(top.B) + (int(bottom.B)-int(top.B))*t/h),
			0xFF,
		}
		Fill(img, image.Rect(clip.Min.X, y, clip.Max.X, y+1), c)
	}
}

// FillNotched vult r met 1px afgesnoepte hoeken: de vier hoekpixels blijven
// staan — pixel-afronding.
func FillNotched(img *image.RGBA, r image.Rectangle, col color.RGBA) {
	if r.Dx() < 3 || r.Dy() < 3 {
		Fill(img, r, col)
		return
	}
	Fill(img, image.Rect(r.Min.X+1, r.Min.Y, r.Max.X-1, r.Min.Y+1), col)
	Fill(img, image.Rect(r.Min.X, r.Min.Y+1, r.Max.X, r.Max.Y-1), col)
	Fill(img, image.Rect(r.Min.X+1, r.Max.Y-1, r.Max.X-1, r.Max.Y), col)
}

// OutlineNotched is Outline met dezelfde afgesnoepte hoeken.
func OutlineNotched(img *image.RGBA, r image.Rectangle, col color.RGBA) {
	if r.Dx() < 3 || r.Dy() < 3 {
		Outline(img, r, col)
		return
	}
	Fill(img, image.Rect(r.Min.X+1, r.Min.Y, r.Max.X-1, r.Min.Y+1), col)
	Fill(img, image.Rect(r.Min.X+1, r.Max.Y-1, r.Max.X-1, r.Max.Y), col)
	Fill(img, image.Rect(r.Min.X, r.Min.Y+1, r.Min.X+1, r.Max.Y-1), col)
	Fill(img, image.Rect(r.Max.X-1, r.Min.Y+1, r.Max.X, r.Max.Y-1), col)
}

// Card is het standaard verhoogde vlak: notched vulling, notched rand en een
// subtiele bevel (licht boven, donker onder) — knoppen, kaartjes, pillen.
func Card(img *image.RGBA, r image.Rectangle, fill, border color.RGBA) {
	FillNotched(img, r, fill)
	if r.Dx() >= 5 && r.Dy() >= 5 {
		Fill(img, image.Rect(r.Min.X+2, r.Min.Y+1, r.Max.X-2, r.Min.Y+2), ColBevel)
		Fill(img, image.Rect(r.Min.X+2, r.Max.Y-2, r.Max.X-2, r.Max.Y-1), ColSunk)
	}
	OutlineNotched(img, r, border)
}

// Disc vult de cirkel die in r past (afstandstest — klein en zeldzaam
// genoeg: de stoplichtjes in een titelbalk).
func Disc(img *image.RGBA, r image.Rectangle, col color.RGBA) {
	d := r.Dx()
	if r.Dy() < d {
		d = r.Dy()
	}
	rad := float64(d) / 2
	cx := float64(r.Min.X) + rad - 0.5
	cy := float64(r.Min.Y) + rad - 0.5
	b := img.Bounds()
	for y := r.Min.Y; y < r.Min.Y+d; y++ {
		for x := r.Min.X; x < r.Min.X+d; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= rad*rad && x >= b.Min.X && x < b.Max.X && y >= b.Min.Y && y < b.Max.Y {
				img.SetRGBA(x, y, col)
			}
		}
	}
}

// Shade verdonkert de bestaande pixels in r tot ~55% — de schaduwlaag onder
// windows: geen alpha-compositing, wel diepte, en deterministisch (hangt
// alleen af van wat er al staat).
func Shade(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		off := img.PixOffset(r.Min.X, y)
		for x := 0; x < r.Dx(); x++ {
			img.Pix[off+0] = img.Pix[off+0] >> 1
			img.Pix[off+1] = img.Pix[off+1] >> 1
			img.Pix[off+2] = img.Pix[off+2]>>1 + img.Pix[off+2]>>3
			off += 4
		}
	}
}
