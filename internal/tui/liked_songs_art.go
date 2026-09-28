package tui

import (
	"image"
	"image/color"

	tea "github.com/charmbracelet/bubbletea"
)

const likedSongsImageURL = "orpheus://liked-songs"

// generateLikedSongsImage builds the pseudo-playlist cover from explicit
// gradient corners: the background blends across them, the heart motif
// stays white. Ascii-profile terminals render no cover mosaic at all
// (renderHalfBlock returns ""), so the art is invisible there —
// generation stays profile-independent and total by construction.
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

	return downsample(ssImg, ssSize, size)
}

var (
	likedArtKey    string
	likedArtColors [4]color.NRGBA
)

// likedArtPaletteKey identifies the palette a gradient was built from.
// Only the roles the art reads participate, so unrelated color edits
// never pay a regen.

// likedArtCorners derives the gradient corners from resolved theme
// colors, caching the last palette so re-theming only regenerates when
// the palette actually moved. Call it only from the event loop: Init and
// theme changes both run there, so the palette state needs no locking —
// the preload cmd receives the corners by value.

func likedArtPaletteKey(colors themeColors) string {
	return colors.Blue + "|" + colors.Page
}

// likedArtPalette maps a resolved theme palette to the cover gradient:
// the accent carries the hue, the page anchors the depth. Corners run
// from page-leaning tints toward full accent, so the white heart stays
// legible on every preset. Non-hex palettes (ANSI names/indices, e.g.
// the minimal theme) degrade to a neutral grayscale ramp — ANSI values
// are terminal-remapped and have no portable RGB.
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

func downsample(src *image.RGBA, srcSize, dstSize int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dstSize, dstSize))
	ratio := srcSize / dstSize
	for dy := range dstSize {
		for dx := range dstSize {
			var r, g, b, count uint32
			for sy := range ratio {
				for sx := range ratio {
					sy2 := dy*ratio + sy
					sx2 := dx*ratio + sx
					c := src.RGBAAt(sx2, sy2)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
					count++
				}
			}
			dst.SetRGBA(dx, dy, color.RGBA{
				R: uint8(r / count),
				G: uint8(g / count),
				B: uint8(b / count),
				A: 255,
			})
		}
	}
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

// likedArtThemeColors resolves the palette the procedural cover follows:
// the active preset plus theme.json overrides — the same resolution the
// theme picker previews. Read it at settle points (save/revert/close)
// and startup, never on the keypress path: the supersampled render
// dominates a keypress, and the palette memo keeps repeat calls to a
// key comparison.
func (m model) likedArtThemeColors() themeColors {
	return resolveThemeColors(m.ui.settings.themePreset, m.cachedThemeOverrides())
}

// refreshLikedSongsArt regenerates the procedural cover when the theme
// palette moved; a no-op otherwise (the supersampled render is not cheap).
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
