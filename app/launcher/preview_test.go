package launcher

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/xinix00/hop-os-surf/stack/scene"
)

// TestScreenshotPreview rendert het menu met de échte scene-pipeline
// (Layout+Render) in twee toestanden naast elkaar. Zonder $SCREENSHOT_OUT
// slaat hij over.
//
//	SCREENSHOT_OUT=docs/launcher.png go test ./app/launcher -run Screenshot
func TestScreenshotPreview(t *testing.T) {
	out := os.Getenv("SCREENSHOT_OUT")
	if out == "" {
		t.Skip("set SCREENSHOT_OUT=<file.png> to render the launcher preview")
	}

	const w, h, gap = 480, 360, 12
	views := []func(m *Menu){
		func(m *Menu) {},                         // rust: twee draaiend, drie niet
		func(m *Menu) { m.buttons[1].OnClick() }, // calc is net gestart
	}
	sheet := image.NewRGBA(image.Rect(0, 0, len(views)*w+(len(views)-1)*gap, h))
	for i, setup := range views {
		m, f := demo(t)
		setup(m)
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
