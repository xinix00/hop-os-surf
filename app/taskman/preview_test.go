package taskman

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/xinix00/hop-os-surf/stack/scene"
)

// TestScreenshotPreview is het meetinstrument als dev-tool: hij rendert de
// vier schermen — agents, tasks, jobdetail en de logstaart — met de échte
// scene-pipeline (Layout+Render, wat de display doet) naast elkaar in één
// PNG. Zonder $SCREENSHOT_OUT slaat hij over; in CI is dit een no-op.
//
//	SCREENSHOT_OUT=docs/taskman.png go test ./app/taskman -run Screenshot
func TestScreenshotPreview(t *testing.T) {
	out := os.Getenv("SCREENSHOT_OUT")
	if out == "" {
		t.Skip("set SCREENSHOT_OUT=<file.png> to render the taskman preview")
	}

	const w, h, gap = 480, 360, 12
	views := []func(a *App, f *fakeConn){
		func(a *App, f *fakeConn) {}, // agents
		func(a *App, f *fakeConn) { a.Key(9, true) },
		func(a *App, f *fakeConn) { // jobdetail van web
			a.Key(9, true)
			a.list.OnSelect(2)
			a.SetDetail("web", demoDetail())
		},
		func(a *App, f *fakeConn) { // logstaart van web-2
			a.Key(9, true)
			a.list.OnSelect(2)
			a.SetDetail("web", demoDetail())
			a.list.OnSelect(6)
			a.LogLines("node-b", "web-2", "stdout", []string{
				"2026/07/20 12:00:01 web: listening on :8080",
				"2026/07/20 12:00:03 GET /health 200 0.4ms",
				"2026/07/20 12:00:07 GET /orders 200 12ms",
				"2026/07/20 12:00:09 GET /orders/4812 404 1ms",
			})
		},
	}

	sheet := image.NewRGBA(image.Rect(0, 0, len(views)*w+(len(views)-1)*gap, h))
	for i, setup := range views {
		a, f := demo(t)
		setup(a, f)
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		scene.Layout(f.root, w, h)
		scene.Render(img, f.root)
		x0 := i * (w + gap)
		for y := 0; y < h; y++ {
			copy(sheet.Pix[sheet.PixOffset(x0, y):sheet.PixOffset(x0+w, y)], img.Pix[img.PixOffset(0, y):img.PixOffset(w, y)])
		}
	}

	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, sheet); err != nil {
		t.Fatal(err)
	}
	t.Logf("preview: %s (%dx%d)", out, sheet.Bounds().Dx(), sheet.Bounds().Dy())
}
