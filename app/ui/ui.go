// Package ui zijn de kleine app-helpers die overbleven nadat de scene-laag
// (P2) het widget-werk display-side trok: de keycode-vertaling (input.go) en
// wat opmaak die elke app nodig heeft. De pixel-widgets die hier woonden
// (tabs, lijsten, badges — voor de pixel-taskman van 19-07) zijn met die
// apps mee naar de scene-laag verhuisd en hier gesloopt: één widget-set,
// display-side, is precies het punt van §4.
package ui

import (
	"fmt"
	"time"
)

// Ago formatteert een duur kort en leesbaar voor statusregels: 8s, 3m20s,
// 2h05m, 3d. Negatief (klokscheefstand) klemt op 0s.
func Ago(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
