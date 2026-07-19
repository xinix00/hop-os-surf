// Package face tekent de analoge klok van de clock-app. Los van main zodat
// het screenshot-meetinstrument (surfserve) er host-side een demo-scène mee
// kan bouwen — pure image.RGBA, geen SURF, geen applib.
package face

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"
)

var (
	colFace   = color.RGBA{0x18, 0x22, 0x36, 0xFF}
	colRing   = color.RGBA{0xF0, 0xF4, 0xFF, 0xFF}
	colTick   = color.RGBA{0x6E, 0xA8, 0xFF, 0xFF}
	colHour   = color.RGBA{0xF0, 0xF4, 0xFF, 0xFF}
	colMinute = color.RGBA{0xC0, 0xD0, 0xF0, 0xFF}
	colSecond = color.RGBA{0xFF, 0x6E, 0x50, 0xFF}
)

// Draw tekent de wijzerplaat over het hele beeld (vierkant werkt het mooist).
func Draw(img *image.RGBA, now time.Time) {
	draw.Draw(img, img.Bounds(), image.NewUniform(colFace), image.Point{}, draw.Src)
	b := img.Bounds()
	cx, cy := b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2
	r := b.Dx()
	if b.Dy() < r {
		r = b.Dy()
	}
	r = r/2 - 8

	ring(img, cx, cy, r, 2, colRing)
	for i := 0; i < 12; i++ {
		a := float64(i) / 12 * 2 * math.Pi
		hand(img, cx, cy, a, float64(r)*0.86, float64(r)*0.96, 1, colTick)
	}

	hr := float64(now.Hour()%12)/12*2*math.Pi + float64(now.Minute())/60*(2*math.Pi/12)
	mi := float64(now.Minute())/60*2*math.Pi + float64(now.Second())/60*(2*math.Pi/60)
	se := float64(now.Second()) / 60 * 2 * math.Pi
	hand(img, cx, cy, hr, 0, float64(r)*0.50, 3, colHour)
	hand(img, cx, cy, mi, 0, float64(r)*0.74, 2, colMinute)
	hand(img, cx, cy, se, 0, float64(r)*0.86, 1, colSecond)
	dot(img, cx, cy, 3, colSecond)
}

// hand tekent een wijzer/streep van radius r0 tot r1 onder hoek a (0 = 12
// uur, met de klok mee), dikte t.
func hand(img *image.RGBA, cx, cy int, a, r0, r1 float64, t int, col color.RGBA) {
	sin, cos := math.Sin(a), -math.Cos(a)
	steps := int(r1 - r0)
	if steps < 1 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		rr := r0 + (r1-r0)*float64(i)/float64(steps)
		dot(img, cx+int(sin*rr+0.5), cy+int(cos*rr+0.5), t, col)
	}
}

// ring tekent een cirkelrand met dikte t via een afstandstest over de
// omsluitende doos — brute force maar zat snel voor 1 frame/s.
func ring(img *image.RGBA, cx, cy, r, t int, col color.RGBA) {
	rOut, rIn := float64(r), float64(r-t)
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			d := math.Hypot(float64(x-cx), float64(y-cy))
			if d <= rOut && d >= rIn {
				img.SetRGBA(x, y, col)
			}
		}
	}
}

// dot tekent een gevuld vierkantje van (2t+1)² rond (x,y) — de "dikke pixel".
func dot(img *image.RGBA, x, y, t int, col color.RGBA) {
	for dy := -t; dy <= t; dy++ {
		for dx := -t; dx <= t; dx++ {
			if image.Pt(x+dx, y+dy).In(img.Bounds()) {
				img.SetRGBA(x+dx, y+dy, col)
			}
		}
	}
}
