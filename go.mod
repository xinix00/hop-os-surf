module github.com/xinix00/hop-os-surf

go 1.26.4

require (
	github.com/andybalholm/cascadia v1.3.4
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef
	golang.org/x/image v0.44.0
	golang.org/x/net v0.55.0
	hop-os/metal v0.0.0
)

require (
	github.com/google/btree v1.1.2 // indirect
	github.com/soypat/lneto v0.1.1-0.20260609173350-82f946154800 // indirect
	github.com/usbarmory/go-net v0.0.0-20260626130943-dad9ef39fd9b // indirect
	github.com/usbarmory/tamago v1.26.4 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	gvisor.dev/gvisor v0.0.0-20250911055229-61a46406f068 // indirect
)

// hop-os/metal is (nog) geen fetchbare module: lokaal naast deze repo.
// Zijn replaces gelden niet transitief, dus hier herhaald (zelfde paden als
// in hop-os/metal/go.mod).
replace (
	github.com/xinix00/hoplock => /Users/derek/haaslock
	github.com/xinix00/hoplockserver => /Users/derek/Git/easy/hoplockserver
	hop => /Users/derek/Git/easy/hop
	hop-os/metal => ../hop-os/metal
)
