package tui

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"

	tea "charm.land/bubbletea/v2"
)

const likedSongsImageURL = "orpheus://liked-songs"

// Ascii-profile terminals render no cover mosaic, so generation stays
// profile-independent and total by construction.
func generateLikedSongsImage(size int, corners [4]color.NRGBA) image.Image {
	if size < 2 {
		size = 2
	}

	ssFactor := 4
	ssSize := size * ssFactor
	ssImg := image.NewRGBA(image.Rect(0, 0, ssSize, ssSize))

	tl, tr, bl, br := corners[0], corners[1], corners[2], corners[3]

	heartScale := float64(ssSize) * 0.07
	cx := float64(ssSize) / 2
	cy := float64(ssSize) / 2

	for y := range ssSize {
		ty := float64(y) / float64(ssSize-1)
		for x := range ssSize {
			tx := float64(x) / float64(ssSize-1)
			bgR := lerp4(tl.R, tr.R, bl.R, br.R, tx, ty)
			bgG := lerp4(tl.G, tr.G, bl.G, br.G, tx, ty)
			bgB := lerp4(tl.B, tr.B, bl.B, br.B, tx, ty)

			nx := (float64(x) - cx) / heartScale
			ny := -(float64(y)-cy)/heartScale + 0.3

			if isInHeart(nx, ny) {
				ssImg.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			} else {
				ssImg.SetRGBA(x, y, color.RGBA{R: bgR, G: bgG, B: bgB, A: 255})
			}
		}
	}

	return downsample(ssImg, size)
}

var (
	likedArtKey    string
	likedArtColors [4]color.NRGBA
)

// Only the roles the art reads participate, so unrelated color edits
// never pay a regen.
func likedArtPaletteKey(colors themeColors) string {
	return colors.Blue + "|" + colors.Page
}

// Corners run page-leaning toward full accent so the white heart stays
// legible. Non-hex palettes degrade to a neutral grayscale ramp: ANSI
// values are terminal-remapped and have no portable RGB.
func likedArtPalette(colors themeColors) [4]color.NRGBA {
	accent, okA := hexToNRGBA(colors.Blue)
	page, okP := hexToNRGBA(colors.Page)
	if !okA || !okP {
		return [4]color.NRGBA{
			{R: 0x38, G: 0x38, B: 0x38, A: 255},
			{R: 0x48, G: 0x48, B: 0x48, A: 255},
			{R: 0x58, G: 0x58, B: 0x58, A: 255},
			{R: 0x68, G: 0x68, B: 0x68, A: 255},
		}
	}
	mix := func(t float64) color.NRGBA {
		return color.NRGBA{
			R: mixChannel(accent.R, page.R, t),
			G: mixChannel(accent.G, page.G, t),
			B: mixChannel(accent.B, page.B, t),
			A: 255,
		}
	}
	return [4]color.NRGBA{mix(0.30), mix(0.45), mix(0.55), mix(0.0)}
}

func hexToNRGBA(hex string) (color.NRGBA, bool) {
	r, g, b, ok := hexToRGB(hex)
	return color.NRGBA{R: r, G: g, B: b, A: 255}, ok
}

// Call only from the event loop: Init and theme changes both run there,
// so the palette state needs no locking.

func likedArtCorners(colors themeColors) [4]color.NRGBA {
	key := likedArtPaletteKey(colors)
	if key == likedArtKey {
		return likedArtColors
	}
	likedArtKey = key
	likedArtColors = likedArtPalette(colors)
	return likedArtColors
}

func isInHeart(x, y float64) bool {
	a := x*x + y*y - 1
	return a*a*a-x*x*y*y*y <= 0
}

func downsample(src *image.RGBA, dstSize int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dstSize, dstSize))
	if dstSize <= 0 {
		return dst
	}
	// Pixel values shift vs the old box average, but the gradient/heart
	// properties served are resolution-independent, not exact pixels.
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func lerp4(tl, tr, bl, br uint8, tx, ty float64) uint8 {
	top := float64(tl) + float64(tr-tl)*tx
	bot := float64(bl) + float64(br-bl)*tx
	v := top + (bot-top)*ty
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

const likedSongsArtSize = 600

func (m *model) preloadLikedSongsArt(corners [4]color.NRGBA) {
	if m.ui.imgs == nil {
		return
	}
	img := generateLikedSongsImage(likedSongsArtSize, corners)
	m.ui.imgs.setImage(likedSongsImageURL, img, likedSongsArtSize, likedSongsArtSize)
	m.ui.imgs.pinURL(likedSongsImageURL)
}

// Same resolution the picker previews. Read at settle points, never
// on the keypress path: the supersampled render dominates a keypress.
func (m model) likedArtThemeColors() themeColors {
	return resolveThemeColors(m.ui.settings.themePreset, m.cachedThemeOverrides())
}

// No-op when the palette didn't move (the render is not cheap).
func (m *model) refreshLikedSongsArt() {
	if m.ui.imgs == nil {
		return
	}
	colors := m.likedArtThemeColors()
	if key := likedArtPaletteKey(colors); key == likedArtKey {
		return
	}
	img := generateLikedSongsImage(likedSongsArtSize, likedArtCorners(colors))
	m.ui.imgs.refreshURL(likedSongsImageURL, img, likedSongsArtSize, likedSongsArtSize)
}

func preloadLikedSongsArtCmd(m model) tea.Cmd {
	corners := likedArtCorners(m.likedArtThemeColors())
	return func() tea.Msg {
		m.preloadLikedSongsArt(corners)
		return nil
	}
}
