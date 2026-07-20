// Display is de display-server van HopOS (docs/gui-ontwerp.md): een gewone
// app die SURF-surfaces aanneemt (:7878), ze als tiling-desktop componeert en
// via HTTP het meetinstrument serveert — /screen.png (headless screenshot) en
// /kvm (browser-KVM: kijken + muis/toetsen, nul install). Op een node met een
// FB-grant (FB_BASE e.d. in de jobspec-env) blit hij dezelfde compositie ook
// naar het echte scherm — de Pi is het eerste render-device.
package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/surf"
	"github.com/xinix00/hop-os-surf/stack/surfserve"
	"hop-os/metal/app/applib"
	"hop-os/metal/app/applib/appnet"
)

func main() {
	app := applib.Init()

	ip, err := appnet.Up(app)
	if err != nil {
		app.Logf("net: %v", err)
		app.Exit(1)
	}

	// Schermmaat: van de FB-grant als die er is, anders DISPLAY_SIZE uit de
	// jobspec, anders 1280x720 (de headless/QEMU-default).
	w, h := 1280, 720
	if fb := fbFromEnv(app); fb != nil {
		w, h = fb.w, fb.h
	} else if s := app.Env("DISPLAY_SIZE"); s != "" {
		if pw, ph, ok := parseSize(s); ok {
			w, h = pw, ph
		} else {
			app.Logf("display: bad DISPLAY_SIZE %q, using %dx%d", s, w, h)
		}
	}
	comp := compositor.New(w, h)
	srv := surfserve.New(comp, app.Logf)

	surfPort := app.Env("ER_PORT_SURF")
	if surfPort == "" {
		surfPort = strconv.Itoa(surf.Port)
	}
	l, err := net.Listen("tcp", ":"+surfPort)
	if err != nil {
		app.Logf("surf: listen: %v", err)
		app.Exit(1)
	}
	go func() {
		app.Logf("surf: accept: %v", srv.ServeSURF(l))
		app.Exit(1)
	}()

	if fb := fbFromEnv(app); fb != nil {
		go fb.blitLoop(comp)
		// De glas-cursor: elke muispositie uit de input-stroom landt als
		// overlay direct op de framebuffer — nooit in de compositie (§5).
		srv.OnPointer(fb.CursorTo)
		app.Logf("display: fb grant %dx%d stride %d at %#x", fb.w, fb.h, fb.stride, fb.base)
	}

	httpPort := app.Env("ER_PORT_HTTP")
	if httpPort == "" {
		httpPort = "80"
	}
	app.Logf("display: %dx%d — surf %s:%s, web-kvm http://%s:%s/kvm", w, h, ip, surfPort, ip, httpPort)
	app.Logf("http: %v", http.ListenAndServe(":"+httpPort, srv.Handler()))
	app.Exit(1) // een display die stopt met serveren is een crash, by design
}

// parseSize leest "1280x720".
func parseSize(s string) (w, h int, ok bool) {
	a, b, found := strings.Cut(s, "x")
	if !found {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(a)
	h, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 || w > 8192 || h > 8192 {
		return 0, 0, false
	}
	return w, h, true
}
