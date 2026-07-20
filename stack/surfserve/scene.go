// Scene-kant van de server (P2, docs/gui-ontwerp.md §4): een surface die
// SCENE stuurt wordt display-side gerenderd — PATCH hertekent alleen het
// widget-rect, CONFIGURE re-flowt zonder de app te wekken, en input wordt
// hier ge-hit-test en gaat als semantisch EVENT terug naar de app.
package surfserve

import (
	"image"
	"sync"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"github.com/xinix00/hop-os-surf/stack/scene"
	"github.com/xinix00/hop-os-surf/stack/surf"
)

// sceneView is de display-staat van één scene-surface.
type sceneView struct {
	mu   sync.Mutex
	sess *session
	sur  *compositor.Surface
	id   uint16 // surface-id binnen de sessie (voor EVENT's)

	root *scene.Node
	byID map[uint16]*scene.Node
	img  *image.RGBA

	hover   *scene.Node // knop onder de aanwijzer
	pressed *scene.Node // knop met button-down
}

// sceneOf geeft de view van een surface (nil = gewone pixel-surface).
func (s *Server) sceneOf(sur *compositor.Surface) *sceneView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scenes[sur]
}

// handleScene verwerkt een SCENE-bericht: boom decoderen, layouten op de
// huidige surface-maat en volledig renderen.
func (s *Server) handleScene(sess *session, id uint16, payload []byte) {
	sur := sess.get(id)
	if sur == nil {
		s.logf("surf: %s: SCENE without CREATE (surface %d)", sess.app, id)
		return
	}
	root, err := scene.Decode(payload)
	if err != nil {
		s.logf("surf: %s: bad SCENE (%v)", sess.app, err)
		return
	}
	v := &sceneView{sess: sess, sur: sur, id: id, root: root, byID: scene.Index(root)}
	s.mu.Lock()
	s.scenes[sur] = v
	s.mu.Unlock()
	v.reflow()
}

// handlePatch past een PATCH toe en hertekent alleen het geraakte rect.
func (s *Server) handlePatch(sess *session, id uint16, payload []byte) {
	sur := sess.get(id)
	if sur == nil {
		return
	}
	v := s.sceneOf(sur)
	if v == nil {
		return
	}
	v.mu.Lock()
	n, err := scene.ApplyPatch(v.byID, payload)
	if err != nil {
		v.mu.Unlock()
		s.logf("surf: %s: %v", sess.app, err)
		return
	}
	scene.RenderNode(v.img, n)
	r := n.Rect
	v.mu.Unlock()
	v.push(r)
}

// reflow layout+rendert de hele boom op de actuele surface-maat.
func (v *sceneView) reflow() {
	w, h := v.sur.Size()
	v.mu.Lock()
	v.img = image.NewRGBA(image.Rect(0, 0, w, h))
	scene.Layout(v.root, w, h)
	scene.Render(v.img, v.root)
	v.mu.Unlock()
	v.push(image.Rect(0, 0, w, h))
}

// push schuift één img-rect als damage naar de surface en presenteert hem.
func (v *sceneView) push(r image.Rectangle) {
	v.mu.Lock()
	r = r.Intersect(v.img.Bounds())
	if r.Empty() {
		v.mu.Unlock()
		return
	}
	wire := make([]byte, r.Dx()*r.Dy()*4)
	o := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := v.img.Pix[v.img.PixOffset(r.Min.X, y):]
		for x := 0; x < r.Dx(); x++ {
			// RGBA → XRGB little-endian (B,G,R,X) — het draadformaat.
			wire[o+0] = src[x*4+2]
			wire[o+1] = src[x*4+1]
			wire[o+2] = src[x*4+0]
			o += 4
		}
	}
	v.mu.Unlock()
	if v.sur.Damage(r.Min.X, r.Min.Y, r.Dx(), r.Dy(), wire) == nil {
		v.sess.srv.comp.PresentRects(v.sur, []image.Rectangle{r})
	}
}

// input verwerkt display-side input voor een scene-surface: hover/press op
// knoppen, selectie en scroll op lists; alleen semantiek verlaat de node.
// Toetsen zijn de uitzondering: die zijn geen hit-testbare semantiek (welke
// toets wat doet is app-logica) en gaan rauw door naar de app — dezelfde
// lossy wachtrij als bij een pixel-app.
func (v *sceneView) input(ev surf.Input) {
	if ev.Kind == surf.InputKey {
		select {
		case v.sess.inputQ <- inMsg{id: v.id, ev: ev}:
		default: // app leest even niet: droppen, nooit blokkeren
		}
		return
	}

	x, y := int(ev.X), int(ev.Y)
	var dirty []image.Rectangle
	var events []scene.Event

	v.mu.Lock()
	switch ev.Kind {
	case surf.InputMove:
		hit := scene.HitAt(v.root, x, y)
		if hit != nil && hit.Kind != scene.KindButton {
			hit = nil // hover-feedback is een knoppending
		}
		if hit != v.hover {
			if v.hover != nil {
				v.hover.Hover = false
				scene.RenderNode(v.img, v.hover)
				dirty = append(dirty, v.hover.Rect)
			}
			if hit != nil {
				hit.Hover = true
				scene.RenderNode(v.img, hit)
				dirty = append(dirty, hit.Rect)
			}
			v.hover = hit
		}
	case surf.InputButton:
		hit := scene.HitAt(v.root, x, y)
		if ev.Value != 0 { // down
			if hit != nil && hit.Kind == scene.KindButton {
				hit.Pressed = true
				v.pressed = hit
				scene.RenderNode(v.img, hit)
				dirty = append(dirty, hit.Rect)
			}
			if hit != nil && hit.Kind == scene.KindList {
				row := hit.Scroll + (y-hit.Rect.Min.Y-1)/14
				if row >= 0 && row < len(hit.Items) {
					hit.Sel = int32(row)
					scene.RenderNode(v.img, hit)
					dirty = append(dirty, hit.Rect)
					events = append(events, scene.Event{ID: hit.ID, Kind: scene.EvSelect, Value: int32(row)})
				}
			}
		} else if v.pressed != nil { // up: klik = down+up op dezelfde knop
			v.pressed.Pressed = false
			scene.RenderNode(v.img, v.pressed)
			dirty = append(dirty, v.pressed.Rect)
			if hit == v.pressed {
				events = append(events, scene.Event{ID: v.pressed.ID, Kind: scene.EvClick, Value: 1})
			}
			v.pressed = nil
		}
	case surf.InputWheel:
		hit := scene.HitAt(v.root, x, y)
		if hit != nil && hit.Kind == scene.KindList {
			step := 1
			if ev.Value < 0 {
				step = -1
			}
			max := len(hit.Items) - (hit.Rect.Dy()-2)/14
			if max < 0 {
				max = 0
			}
			ns := hit.Scroll + step
			if ns < 0 {
				ns = 0
			}
			if ns > max {
				ns = max
			}
			if ns != hit.Scroll {
				hit.Scroll = ns
				scene.RenderNode(v.img, hit)
				dirty = append(dirty, hit.Rect)
			}
		}
	}
	v.mu.Unlock()

	for _, r := range dirty {
		v.push(r)
	}
	for _, ev := range events {
		v.sess.send(surf.TypeEvent, v.id, scene.EncodeEvent(ev))
	}
}
