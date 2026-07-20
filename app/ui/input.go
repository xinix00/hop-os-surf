// Toetsenbord-tooling: elke app die tekst aanneemt (adresbalk, zoekveld)
// heeft dezelfde keycode-vertaling nodig — hier woont hij, één keer.
// Verhuisd uit browse (waar hij voor de adresbalk ontstond).
package ui

// Rune vertaalt een web-KVM-keyCode (plus shift-stand) naar een ASCII-teken;
// 0 = geen teken (Enter/Backspace/Shift en pijltjes gaan buitenom).
func Rune(code uint32, shift bool) byte {
	switch {
	case code >= 'A' && code <= 'Z':
		if shift {
			return byte(code)
		}
		return byte(code) + 'a' - 'A'
	case code >= '0' && code <= '9':
		if shift {
			// US-layout: shift-cijfers die in URL's voorkomen.
			switch code {
			case '3':
				return '#'
			case '5':
				return '%'
			case '7':
				return '&'
			}
			return 0
		}
		return byte(code)
	case code >= 96 && code <= 105: // numpad
		return byte(code-96) + '0'
	}
	switch code {
	case 186: // ; / :
		if shift {
			return ':'
		}
		return ';'
	case 187: // = / +
		if shift {
			return '+'
		}
		return '='
	case 189: // - / _
		if shift {
			return '_'
		}
		return '-'
	case 190, 110: // . (en numpad-punt)
		return '.'
	case 191: // / / ?
		if shift {
			return '?'
		}
		return '/'
	case 222: // ' / "
		return '\''
	}
	return 0
}
