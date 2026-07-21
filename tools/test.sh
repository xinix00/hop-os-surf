#!/bin/sh
# Host-tests + tamago-compile-gate voor de SURF-stack (zelfde recept als
# HopOS' tools/test.sh: logica host-getest, mains door de tamago-gate).
#
# Extra argumenten gaan naar go test door: tools/test.sh -run EndToEnd -v
set -e
cd "$(dirname "$0")/.."

# De mains (cmd/*) kunnen niet op de host: applib is tamago-only. De
# bibliotheek-packages wel — inclusief de end-to-end-keten in surfserve.
go test "$@" ./stack/... ./app/...

# De host-desktop (go run ./cmd/desktop) is de enige host-main: meebouwen.
go build -o /dev/null ./cmd/desktop

TAMAGO="${TAMAGO:-$HOME/tamago-go/bin/go}"
if [ ! -x "$TAMAGO" ]; then
	echo "tamago-gate OVERGESLAGEN ($TAMAGO ontbreekt)" >&2
	exit 0
fi
mkdir -p out
# Canonieke app-link (zie HopOS docs/app.md): één artifact voor elk slot.
for app in display clock calc browser taskman dash launcher; do
	GOWORK=off GOTOOLCHAIN=local GOOS=tamago GOOSPKG=github.com/usbarmory/tamago GOARCH=arm64 \
		"$TAMAGO" build -tags linkcpuinit -trimpath \
		-ldflags "-w -T 0x50010000 -R 0x1000" -o "out/$app.elf" "./cmd/$app"
done
# De lnetonet-variant moet blijven bouwen (opt-in netstack, zie HopOS).
for app in display clock calc browser taskman dash launcher; do
	GOWORK=off GOTOOLCHAIN=local GOOS=tamago GOOSPKG=github.com/usbarmory/tamago GOARCH=arm64 \
		"$TAMAGO" build -tags "lnetonet linkcpuinit" -o /dev/null "./cmd/$app"
done
echo "OK: host-tests groen, out/{display,clock,calc,browser,taskman,dash,launcher}.elf gebouwd" >&2
