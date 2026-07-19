# SURF — the HopOS GUI stack

Network-transparent windows for [HopOS](https://github.com/xinix00/HopOS):
an app draws into an `image.RGBA` anywhere in the cluster, the display node
composites it as a window. Kill the node an app runs on and let HOP restart
it elsewhere — the window comes back by itself.

![demo desktop](docs/desktop-demo.png)

Everything in this repo is a **plain HopOS app** — the OS itself carries no
GUI code. Design dossier (the negotiated source of truth, Dutch):
[HopOS docs/gui-ontwerp.md](https://github.com/xinix00/HopOS/blob/main/docs/gui-ontwerp.md).

## Parts

| package | what |
|---|---|
| `surf/` | the wire protocol: 8-byte header, HELLO/CREATE/DAMAGE/PRESENT/CONFIGURE/INPUT/CLOSE (scene types reserved) |
| `compositor/` | software compositor: tiling grid, title bars, double-buffered surfaces, cursor |
| `surfserve/` | SURF sessions + the built-in web-KVM: `/screen.png` (headless screenshot) and `/kvm` (watch + mouse/keys from any browser) |
| `window/` | the app side: `window.Open`, draw, `Present()` — reconnects and resends a full frame on its own |
| `pixel/` | tiny shared drawing layer: the 8x8 font, fills, outlines |
| `face/` | the demo clock face |
| `calc/` | calculator logic + rendering (host-tested) |
| `browse/` | browser layout + rendering on top of [gost-dom](https://github.com/gost-dom/browser) (host-tested) |
| `cmd/display` | the display server app (SURF on :7878, HTTP on :80) |
| `cmd/clock` | the demo app: an analog clock from anywhere in the cluster |
| `cmd/calc` | the interactive demo: a calculator you operate through the web-KVM |
| `cmd/browser` | a real web browser: gost-dom fetches + parses, `browse/` lays out on the 8x8 font — address bar, scrolling, clickable links (`SURF_HOME` = start page) |

## Build & test

```sh
tools/test.sh        # host tests (incl. end-to-end window↔display) + tamago builds
```

Needs the [tamago toolchain](https://github.com/usbarmory/tamago-go) for the
app builds, and a checkout of HopOS next to this repo (`../hop-os` — see the
replace lines in `go.mod`). Artifacts land in `out/display.elf` and
`out/clock.elf`; submit them as HopOS jobs (see HopOS `docs/app.md`). The
clock finds its display through the job-spec env `SURF_ADDR=<display-node>:7878`.

Render the demo screenshot without any hardware:

```sh
SCREENSHOT_OUT=$PWD/docs/desktop-demo.png go test ./surfserve -run Screenshot
```

## Sizing is the WM's call

CREATE carries a size *hint*; CONFIGURE is authoritative (the Wayland
lesson). Apps re-render at whatever size the tiling layout hands them —
windows always fill their cell exactly. Damage at a stale size is silently
dropped; a presenting app converges on its own.

## Status

P1 of the design dossier: pixels + damage over TCP, software compositor
with WM-driven sizing, web-KVM, and two demo apps (clock, calculator).
Next: the scene layer (SCENE/PATCH/EVENT — bytes per update instead of
kilobytes), the local zero-copy transport, and the HVS plane path on the
Raspberry Pi.
