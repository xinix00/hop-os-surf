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
| `face/` | the demo clock face |
| `cmd/display` | the display server app (SURF on :7878, HTTP on :80) |
| `cmd/clock` | the demo app: an analog clock from anywhere in the cluster |

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

## Status

P1 of the design dossier: pixels + damage over TCP, software compositor,
web-KVM. Next: the scene layer (SCENE/PATCH/EVENT — bytes per update instead
of kilobytes), the local zero-copy transport, and the HVS plane path on the
Raspberry Pi.
