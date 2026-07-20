// Package calc is de rekenmachine achter cmd/calc: de logica en de
// scene-boom (scene.go), los van main zodat de host-tests en het screenshot-
// meetinstrument hem kunnen draaien. Sinds 20-07 een scene-app: de display
// rendert de knoppen, hier leeft alleen wat een klik betékent. Klassieke
// immediate-execution zakrekenmachine: 2 + 3 × 4 = 20 (geen voorrang — zo
// deden de echte ook).
package calc

import "strconv"

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

// --- toetsen ----------------------------------------------------------------

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
