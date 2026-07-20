package ui

import (
	"testing"
	"time"
)

func TestAgo(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{8 * time.Second, "8s"},
		{3*time.Minute + 20*time.Second, "3m20s"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
		{72 * time.Hour, "3d"},
	}
	for _, c := range cases {
		if got := Ago(c.d); got != c.want {
			t.Errorf("Ago(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestRune(t *testing.T) {
	cases := []struct {
		code  uint32
		shift bool
		want  byte
	}{
		{'A', false, 'a'},
		{'A', true, 'A'},
		{'7', false, '7'},
		{'3', true, '#'},
		{103, false, '7'}, // numpad
		{190, false, '.'},
		{191, true, '?'},
		{16, false, 0}, // shift zelf
	}
	for _, c := range cases {
		if got := Rune(c.code, c.shift); got != c.want {
			t.Errorf("Rune(%d, %v) = %q, want %q", c.code, c.shift, got, c.want)
		}
	}
}
