// Package calc is de rekenmachine achter cmd/calc: logica én rendering, los
// van main zodat de host-tests en het screenshot-meetinstrument hem kunnen
// draaien. Klassieke immediate-execution zakrekenmachine: 2 + 3 × 4 = 20
// (geen voorrang — zo deden de echte ook).
package calc

import (
	"image"
	"image/color"
	"strconv"

	"github.com/xinix00/hop-os-surf/pixel"
)

// Calc is de rekentoestand.
type Calc struct {
	entry string  // wat de gebruiker aan het typen is ("" = niets)
	acc   float64 // opgebouwde waarde
	op    byte    // openstaande operator (0 = geen)
	err   bool    // deling door nul e.d.: toon "err" tot C
}

// Press verwerkt één toets: '0'-'9', '.', '+', '-', '*', '/', '=', 'C'
// (clear) of 'b' (backspace). Onbekende toetsen zijn no-ops.
func (c *Calc) Press(key byte) {
	if c.err && key != 'C' {
		return
	}
	switch {
	case key >= '0' && key <= '9':
		if len(c.entry) < 12 { // meer past niet op het display, en float64 ook niet zinvol
			c.entry += string(key)
		}
	case key == '.':
		if c.entry == "" {
			c.entry = "0."
		} else if !contains(c.entry, '.') {
			c.entry += "."
		}
	case key == 'b':
		if c.entry != "" {
			c.entry = c.entry[:len(c.entry)-1]
		}
	case key == 'C':
		*c = Calc{}
	case key == '+' || key == '-' || key == '*' || key == '/':
		if c.entry != "" {
			c.apply() // vouw de invoer in; operator wisselen rekent niet
		}
		c.op = key
	case key == '=':
		c.apply() // lege invoer gebruikt acc als operand: 5+= → 10
		c.op = 0
	}
}

// apply vouwt de operand (de invoer, of bij lege invoer de accumulator) in.
func (c *Calc) apply() {
	v := c.value()
	switch c.op {
	case 0:
		c.acc = v
	case '+':
		c.acc += v
	case '-':
		c.acc -= v
	case '*':
		c.acc *= v
	case '/':
		if v == 0 {
			c.err = true
			return
		}
		c.acc /= v
	}
	c.entry = ""
}

// value is de numerieke waarde van de huidige invoer (of de accumulator).
func (c *Calc) value() float64 {
	if c.entry == "" {
		return c.acc
	}
	v, _ := strconv.ParseFloat(c.entry, 64)
	return v
}

// Op is de openstaande operator (0 = geen) — de UI toont hem in het display
// (Derek 19-07: "als je de x invult, laat die dan zien").
func (c *Calc) Op() byte { return c.op }

// Display is de displayregel: de invoer als er getypt wordt, anders het
// resultaat; "err" na bv. delen door nul.
func (c *Calc) Display() string {
	if c.err {
		return "err"
	}
	if c.entry != "" {
		return c.entry
	}
	s := strconv.FormatFloat(c.acc, 'g', 10, 64)
	if len(s) > 13 {
		s = s[:13]
	}
	return s
}

func contains(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

// --- rendering + hit-test -------------------------------------------------

// Instrumentenpaneel-look, zelfde familie als de compositor.
var (
	colBG      = color.RGBA{0x18, 0x22, 0x36, 0xFF}
	colDisplay = color.RGBA{0x0A, 0x10, 0x1C, 0xFF}
	colBtn     = color.RGBA{0x24, 0x30, 0x4A, 0xFF}
	colBtnOp   = color.RGBA{0x2D, 0x6C, 0xDF, 0xFF}
	colBtnEq   = color.RGBA{0x39, 0xB5, 0x6A, 0xFF}
	colEdge    = color.RGBA{0x3A, 0x4A, 0x6A, 0xFF}
	colText    = color.RGBA{0xF0, 0xF4, 0xFF, 0xFF}
	// Hover-varianten (Derek 19-07: "dan zie je changes actief"): een tint
	// lichter, plus een lichte rand — de damage-stream maakt dit bijna gratis.
	colBtnHov   = color.RGBA{0x33, 0x42, 0x63, 0xFF}
	colBtnOpHov = color.RGBA{0x47, 0x83, 0xE8, 0xFF}
	colBtnEqHov = color.RGBA{0x4C, 0xC8, 0x7E, 0xFF}
	colEdgeHov  = color.RGBA{0x9A, 0xB8, 0xE8, 0xFF}
)

// grid: 4×4 knoppen + een volle =-rij onderaan. Labels zijn ook de
// Press-toetsen ('x' rendert als ×, key '*').
var grid = [4][4]byte{
	{'7', '8', '9', '/'},
	{'4', '5', '6', '*'},
	{'1', '2', '3', '-'},
	{'0', '.', 'C', '+'},
}

func label(key byte) string {
	switch key {
	case '*':
		return "x"
	case '/':
		return "/"
	default:
		return string(key)
	}
}

// layout geeft de knop-rechthoeken voor een windowmaat: index 0..15 = grid,
// 16 = de =-rij. Alles wordt uit bounds afgeleid — resize-proof.
func layout(b image.Rectangle) (display image.Rectangle, btns [17]image.Rectangle) {
	pad := 4
	dispH := b.Dy() / 5
	display = image.Rect(b.Min.X+pad, b.Min.Y+pad, b.Max.X-pad, b.Min.Y+dispH-pad)

	gridTop := b.Min.Y + dispH
	rowH := (b.Max.Y - gridTop) / 5
	colW := b.Dx() / 4
	for r := 0; r < 4; r++ {
		for cc := 0; cc < 4; cc++ {
			x0 := b.Min.X + cc*colW
			y0 := gridTop + r*rowH
			btns[r*4+cc] = image.Rect(x0+pad, y0+pad, x0+colW-pad, y0+rowH-pad)
		}
	}
	btns[16] = image.Rect(b.Min.X+pad, gridTop+4*rowH+pad, b.Max.X-pad, b.Max.Y-pad)
	return display, btns
}

// Render tekent de rekenmachine over het hele beeld. hover is de toets
// waar de muis boven hangt (0 = geen) — die knop licht op.
func Render(img *image.RGBA, c *Calc, hover byte) {
	b := img.Bounds()
	pixel.Fill(img, b, colBG)
	display, btns := layout(b)

	scale := b.Dy() / 200
	if scale < 1 {
		scale = 1
	}

	// Displayregel: getal rechts, de openstaande operator links (feedback
	// dat de + of × "hangt" — zoals de klassiekers dat met een vlaggetje
	// deden).
	pixel.Fill(img, display, colDisplay)
	pixel.Outline(img, display, colEdge)
	txt := c.Display()
	ty := display.Min.Y + (display.Dy()-8*scale)/2
	tx := display.Max.X - 6 - pixel.StringWidth(txt, scale)
	pixel.DrawString(img, tx, ty, scale, colText, txt)
	if op := c.Op(); op != 0 {
		pixel.DrawString(img, display.Min.X+6, ty, scale, colBtnOpHov, label(op))
	}

	for i, r := range btns {
		drawButton(img, r, keyOf(i), hover, scale)
	}
}

// RenderKey hertekent alléén de knop van deze toets (voor het hover-pad:
// twee knopjes per wissel in plaats van het hele window) en geeft de
// geraakte rechthoek terug; nul-rect als de toets geen knop is.
func RenderKey(img *image.RGBA, c *Calc, key, hover byte) image.Rectangle {
	if key == 0 {
		return image.Rectangle{}
	}
	_, btns := layout(img.Bounds())
	scale := img.Bounds().Dy() / 200
	if scale < 1 {
		scale = 1
	}
	for i, r := range btns {
		if keyOf(i) == key {
			drawButton(img, r, key, hover, scale)
			return r
		}
	}
	return image.Rectangle{}
}

// drawButton tekent één knop (met hover-state).
func drawButton(img *image.RGBA, r image.Rectangle, key, hover byte, scale int) {
	col, edge := colBtn, colEdge
	hov := key == hover
	switch {
	case key == '=':
		col = colBtnEq
		if hov {
			col = colBtnEqHov
		}
	case key == '+' || key == '-' || key == '*' || key == '/' || key == 'C':
		col = colBtnOp
		if hov {
			col = colBtnOpHov
		}
	default:
		if hov {
			col = colBtnHov
		}
	}
	if hov {
		edge = colEdgeHov
	}
	pixel.Fill(img, r, col)
	pixel.Outline(img, r, edge)
	pixel.DrawStringCentered(img, r, scale, colText, label(key))
}

// keyOf geeft de Press-toets van knopindex i (0..16).
func keyOf(i int) byte {
	if i == 16 {
		return '='
	}
	return grid[i/4][i%4]
}

// Hit vertaalt een klik (window-lokale coördinaten) naar een Press-toets;
// 0 als er geen knop onder zit.
func Hit(b image.Rectangle, x, y int) byte {
	_, btns := layout(b)
	p := image.Pt(b.Min.X+x, b.Min.Y+y)
	for i, r := range btns {
		if p.In(r) {
			return keyOf(i)
		}
	}
	return 0
}

// Key vertaalt een browser-keyCode (web-KVM) naar een Press-toets; 0 = geen.
func Key(code uint32) byte {
	switch {
	case code >= '0' && code <= '9': // bovenste rij
		return byte(code)
	case code >= 96 && code <= 105: // numpad
		return byte(code - 96 + '0')
	}
	switch code {
	case 190, 110, 188: // . , (numpad-punt en komma tellen ook)
		return '.'
	case 13:
		return '='
	case 8:
		return 'b'
	case 67, 27: // c / Escape
		return 'C'
	case 187, 107: // = met shift is +, numpad-plus
		return '+'
	case 189, 109:
		return '-'
	case 88, 106: // x, numpad-maal
		return '*'
	case 191, 111:
		return '/'
	}
	return 0
}
