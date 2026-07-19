// fbblit: de compositie naar een écht scherm. Een display-app krijgt géén
// MMIO of framebuffer te zien — tenzij HOP hem die expliciet in de kooi mapt
// (de FB-grant: de eerste, kleinste DeviceGrant uit docs/gui-ontwerp.md §7 —
// een lineaire pixelbuffer, geen registers, geen DMA). De grant komt binnen
// als jobspec-env: FB_BASE (hex of dec, IPA zoals gemapt), FB_WIDTH,
// FB_HEIGHT, FB_STRIDE (bytes/rij), FB_BPP (alleen 32 in v1). Zonder die env
// draait de display headless en is /screen.png het scherm.
package main

import (
	"image"
	"time"
	"unsafe"

	"hop-os/metal/app/applib"
	"github.com/xinix00/hop-os-surf/compositor"
)

type fbTarget struct {
	base         uintptr
	w, h, stride int
	pix          []byte
}

// fbFromEnv leest de FB-grant uit de env; nil zonder (complete) grant.
func fbFromEnv(app *applib.App) *fbTarget {
	base := parseUint(app.Env("FB_BASE"))
	w := int(parseUint(app.Env("FB_WIDTH")))
	h := int(parseUint(app.Env("FB_HEIGHT")))
	stride := int(parseUint(app.Env("FB_STRIDE")))
	bpp := parseUint(app.Env("FB_BPP"))
	if bpp == 0 {
		bpp = 32
	}
	if base == 0 || w <= 0 || h <= 0 || bpp != 32 {
		return nil
	}
	if stride == 0 {
		stride = w * 4
	}
	if stride < w*4 || w > 8192 || h > 8192 {
		return nil
	}
	return &fbTarget{
		base: uintptr(base), w: w, h: h, stride: stride,
		pix: unsafe.Slice((*byte)(unsafe.Pointer(uintptr(base))), stride*h),
	}
}

// blitLoop schuift elke hertekening naar het scherm (RGBA → XRGB8888).
// 20 Hz polling is de P1-steiger; het HVS-plane-pad van P4 maakt dit (en de
// hele software-compose) op de Pi overbodig.
func (f *fbTarget) blitLoop(comp *compositor.Compositor) {
	for {
		comp.RenderTo(func(img *image.RGBA) {
			w, h := f.w, f.h
			if iw := img.Bounds().Dx(); iw < w {
				w = iw
			}
			if ih := img.Bounds().Dy(); ih < h {
				h = ih
			}
			for y := 0; y < h; y++ {
				src := img.Pix[y*img.Stride:]
				dst := f.pix[y*f.stride:]
				for x := 0; x < w; x++ {
					// RGBA-bytes → XRGB8888 little-endian (B,G,R,X).
					dst[x*4+0] = src[x*4+2]
					dst[x*4+1] = src[x*4+1]
					dst[x*4+2] = src[x*4+0]
					dst[x*4+3] = 0
				}
			}
		})
		time.Sleep(50 * time.Millisecond)
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
