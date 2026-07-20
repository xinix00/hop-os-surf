// fbblit: de compositie naar een écht scherm. Een display-app krijgt géén
// MMIO of framebuffer te zien — tenzij HOP hem die expliciet in de kooi mapt
// (de FB-grant: de eerste, kleinste DeviceGrant uit docs/gui-ontwerp.md §7 —
// een lineaire pixelbuffer, geen registers, geen DMA). De grant komt binnen
// als jobspec-env: FB_BASE (hex of dec, IPA zoals gemapt), FB_WIDTH,
// FB_HEIGHT, FB_STRIDE (bytes/rij), FB_BPP (alleen 32 in v1). Zonder die env
// draait de display headless en is /screen.png het scherm.
package main

import (
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/xinix00/hop-os-surf/stack/compositor"
	"hop-os/metal/app/applib"
)

type fbTarget struct {
	base         uintptr
	w, h, stride int
	bpp          int // 32 (XRGB8888) of 16 (RGB565)
	pix          []byte
	pix32        []uint32 // 32-bpp-venster als woorden — NC wil brede stores
	pix16        []uint16 // 16-bpp-venster als halfwoorden

	// De glas-cursor: een overlay rechtstreeks op de framebuffer, bewust
	// búíten de compositie (docs/gui-ontwerp.md §5 — de KVM heeft z'n eigen
	// cursor, en op de Pi wordt dit later een HVS-plane). saved bewaart de
	// ondergrond zodat een move hem kan terugleggen.
	mu      sync.Mutex
	curX    int
	curY    int
	curOn   bool
	saved   [curW * curH]uint32
	savedW  int
	savedH  int
	blitGen uint64 // alleen diagnose
}

// fbFromEnv leest de FB-grant uit de env; nil zonder (complete) grant.
// 32-bpp (XRGB8888) én 16-bpp (RGB565) — de Pi-firmware default is 16, en
// het glas hoort niet van een config.txt-regel af te hangen.
func fbFromEnv(app *applib.App) *fbTarget {
	base := parseUint(app.Env("FB_BASE"))
	w := int(parseUint(app.Env("FB_WIDTH")))
	h := int(parseUint(app.Env("FB_HEIGHT")))
	stride := int(parseUint(app.Env("FB_STRIDE")))
	bpp := int(parseUint(app.Env("FB_BPP")))
	if bpp == 0 {
		bpp = 32
	}
	if base == 0 || w <= 0 || h <= 0 || (bpp != 32 && bpp != 16) {
		return nil
	}
	bpx := bpp / 8
	if stride == 0 {
		stride = w * bpx
	}
	if stride < w*bpx || w > 8192 || h > 8192 || stride%bpx != 0 {
		return nil
	}
	f := &fbTarget{
		base: uintptr(base), w: w, h: h, stride: stride, bpp: bpp,
		pix: unsafe.Slice((*byte)(unsafe.Pointer(uintptr(base))), stride*h),
	}
	if bpp == 32 {
		f.pix32 = unsafe.Slice((*uint32)(unsafe.Pointer(uintptr(base))), stride*h/4)
	} else {
		f.pix16 = unsafe.Slice((*uint16)(unsafe.Pointer(uintptr(base))), stride*h/2)
		if stride%4 == 0 {
			// Voor het paren-pad: twee RGB565-pixels per 32-bit-store.
			f.pix32 = unsafe.Slice((*uint32)(unsafe.Pointer(uintptr(base))), stride*h/4)
		}
	}
	return f
}

// blitLoop schuift elke hertekening naar het scherm (RGBA → XRGB8888) —
// als kijker op FrameSince, net als de KVM-stream: het compositor-lock wordt
// alleen vastgehouden om de damage-rects úít te kopiëren, de conversie en de
// (dure) NC-writes gebeuren hier, buiten elk lock. Eerdere vorm (full-frame
// byte-stores onder RenderTo, dus mét het lock) verzadigde op de Pi 5 de
// hele core — Normal-NC betaalt per store, en SURF/HTTP verhongerden mee.
// 20 Hz polling is de P1-steiger; het HVS-plane-pad van P4 maakt dit (en de
// hele software-compose) op de Pi overbodig.
func (f *fbTarget) blitLoop(comp *compositor.Compositor) {
	var gen uint64
	for {
		t0 := time.Now()
		frame, g := comp.FrameSince(gen)
		if frame != nil {
			gen = g
			f.blitFrame(frame)
		}
		// Adaptieve cadans: na een dure blit (vol frame bij drukte — de
		// >16-rects-collapse van FrameSince) evenredig langer slapen, zodat
		// de blit nooit meer dan ~1/3 van de core opeist en SURF/KVM altijd
		// blijven ademen. Idle scherm = gewoon 20 Hz.
		d := time.Since(t0)
		sleep := 2 * d
		if sleep < 50*time.Millisecond {
			sleep = 50 * time.Millisecond
		}
		if sleep > time.Second {
			sleep = time.Second
		}
		time.Sleep(sleep)
	}
}

// ---- De glas-cursor ----

// Het pijltje: 8x12, 'X' = zwarte rand, 'o' = witte vulling, spatie = door-
// zichtig. Klein en klassiek; op NC-geheugen is klein ook snel.
const (
	curW = 8
	curH = 12
)

var cursorSprite = [curH]string{
	"X       ",
	"XX      ",
	"XoX     ",
	"XooX    ",
	"XoooX   ",
	"XooooX  ",
	"XoooooX ",
	"XooooooX",
	"XooooXXX",
	"XoXXoX  ",
	"XX  XoX ",
	"     XX ",
}

// CursorTo verplaatst de glas-cursor (schermcoördinaten) — de OnPointer-
// callback van surfserve. Ondergrond terugleggen, nieuwe bewaren, tekenen.
func (f *fbTarget) CursorTo(x, y int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreLocked()
	f.curX, f.curY = x, y
	f.curOn = true
	f.saveDrawLocked()
}

// restoreLocked legt de bewaarde ondergrond terug (no-op zonder cursor).
// saved bewaart native pixels (u32 of u16 in een u32-vak) — diepte-neutraal.
func (f *fbTarget) restoreLocked() {
	if !f.curOn {
		return
	}
	for r := 0; r < f.savedH; r++ {
		for c := 0; c < f.savedW; c++ {
			f.putPx(f.curX+c, f.curY+r, f.saved[r*curW+c])
		}
	}
}

// saveDrawLocked bewaart de ondergrond op de cursorpositie en tekent het
// pijltje er direct overheen (alleen sprite-pixels; de rest blijft beeld).
func (f *fbTarget) saveDrawLocked() {
	w, h := curW, curH
	if f.curX+w > f.w {
		w = f.w - f.curX
	}
	if f.curY+h > f.h {
		h = f.h - f.curY
	}
	if w <= 0 || h <= 0 || f.curX < 0 || f.curY < 0 {
		f.savedW, f.savedH = 0, 0
		return
	}
	f.savedW, f.savedH = w, h
	black, white := f.rgb(0, 0, 0), f.rgb(0xFF, 0xFF, 0xFF)
	for r := 0; r < h; r++ {
		row := cursorSprite[r]
		for c := 0; c < w; c++ {
			f.saved[r*curW+c] = f.getPx(f.curX+c, f.curY+r)
			switch row[c] {
			case 'X':
				f.putPx(f.curX+c, f.curY+r, black)
			case 'o':
				f.putPx(f.curX+c, f.curY+r, white)
			}
		}
	}
}

// rgb maakt de native pixelwaarde: XRGB8888 of RGB565.
func (f *fbTarget) rgb(r, g, b uint32) uint32 {
	if f.bpp == 32 {
		return b | g<<8 | r<<16
	}
	return (r&0xF8)<<8 | (g&0xFC)<<3 | b>>3
}

func (f *fbTarget) putPx(x, y int, px uint32) {
	if f.bpp == 32 {
		f.pix32[y*f.stride/4+x] = px
	} else {
		f.pix16[y*f.stride/2+x] = uint16(px)
	}
}

func (f *fbTarget) getPx(x, y int) uint32 {
	if f.bpp == 32 {
		return f.pix32[y*f.stride/4+x]
	}
	return uint32(f.pix16[y*f.stride/2+x])
}

// blitFrame schrijft één FrameSince-wire-frame naar het scherm: per damage-
// rect RGBA → één 32-bit XRGB-store per pixel (LE-woord X<<24|R<<16|G<<8|B —
// dezelfde bytevolgorde B,G,R,X als voorheen, maar als brede store).
// Wire-formaat: u32 payloadLen | u16 nRects | nRects×(x,y,w,h u16) | pixels.
// De cursor gaat er even af en erna weer op: een blit die over de cursor
// heen schrijft zou anders de bewaarde ondergrond laten verouderen.
func (f *fbTarget) blitFrame(frame []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreLocked()
	f.blitFrameLocked(frame)
	if f.curOn { // vóór de eerste muisbeweging is er geen cursor te tekenen
		f.saveDrawLocked()
	}
	f.blitGen++
}

func (f *fbTarget) blitFrameLocked(frame []byte) {
	le16 := func(off int) int { return int(frame[off]) | int(frame[off+1])<<8 }
	if len(frame) < 6 {
		return
	}
	n := le16(4)
	off := 6
	if len(frame) < off+n*8 {
		return
	}
	type rect struct{ x, y, w, h int }
	rects := make([]rect, n)
	for i := range rects {
		rects[i] = rect{le16(off), le16(off + 2), le16(off + 4), le16(off + 6)}
		off += 8
	}
	for _, r := range rects {
		if r.x < 0 || r.y < 0 || r.w <= 0 || r.h <= 0 ||
			r.x+r.w > f.w || r.y+r.h > f.h || len(frame) < off+r.w*r.h*4 {
			return // kapot/afgeknot frame: niets half schrijven
		}
		// Elke 32 rijen even afgeven: NC-stores zijn duur, tamago preempt
		// niet, en een vol-frame-blit zonder yield starft de netstack van
		// de hele app (SURF-accepts, KVM — gemeten 19-07, twee keer zelfs:
		// eerst met byte-stores op 32-bpp, daarna met halfword-stores op
		// 16-bpp). Compute-lussen geven coöperatief af — huisregel.
		if f.bpp == 32 {
			for y := 0; y < r.h; y++ {
				src := frame[off+y*r.w*4:]
				dst := f.pix32[(r.y+y)*f.stride/4+r.x:]
				for x := 0; x < r.w; x++ {
					s := src[x*4:]
					dst[x] = uint32(s[2]) | uint32(s[1])<<8 | uint32(s[0])<<16
				}
				if y%32 == 31 {
					runtime.Gosched()
				}
			}
		} else {
			// RGB565, twee pixels per 32-bit-store waar het kan (even x en
			// even breedte-rest); randen met een enkele 16-bit store.
			for y := 0; y < r.h; y++ {
				src := frame[off+y*r.w*4:]
				row := (r.y + y) * f.stride / 2 // halfword-index van de rij
				xa, i, n := r.x, 0, r.w         // absolute kolom, src-byteindex, rest
				if f.pix32 != nil {
					if xa%2 == 1 { // los begin: paar-store wil een even kolom
						f.pix16[row+xa] = uint16(src[0]&0xF8)<<8 | uint16(src[1]&0xFC)<<3 | uint16(src[2])>>3
						xa, i, n = xa+1, 4, n-1
					}
					for ; n >= 2; n -= 2 { // LE: lage halfword = linkerpixel
						a := uint32(src[i]&0xF8)<<8 | uint32(src[i+1]&0xFC)<<3 | uint32(src[i+2])>>3
						b := uint32(src[i+4]&0xF8)<<8 | uint32(src[i+5]&0xFC)<<3 | uint32(src[i+6])>>3
						f.pix32[(row+xa)/2] = a | b<<16
						xa, i = xa+2, i+8
					}
				}
				for ; n > 0; n-- { // staart (of hele rij zonder paren-pad)
					f.pix16[row+xa] = uint16(src[i]&0xF8)<<8 | uint16(src[i+1]&0xFC)<<3 | uint16(src[i+2])>>3
					xa, i = xa+1, i+4
				}
				if y%32 == 31 {
					runtime.Gosched()
				}
			}
		}
		off += r.w * r.h * 4
	}
}

// parseUint leest "123" of "0x1A2B"; 0 bij fouten (grant dan genegeerd).
func parseUint(s string) uint64 {
	if s == "" {
		return 0
	}
	var v uint64
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		for i := 2; i < len(s); i++ {
			c := s[i]
			switch {
			case c >= '0' && c <= '9':
				v = v<<4 | uint64(c-'0')
			case c >= 'a' && c <= 'f':
				v = v<<4 | uint64(c-'a'+10)
			case c >= 'A' && c <= 'F':
				v = v<<4 | uint64(c-'A'+10)
			default:
				return 0
			}
		}
		return v
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		v = v*10 + uint64(s[i]-'0')
	}
	return v
}
