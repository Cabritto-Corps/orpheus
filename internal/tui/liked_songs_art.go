package tui

import (
	"image"
	"image/color"

	tea "github.com/charmbracelet/bubbletea"
)

const likedSongsImageURL = "orpheus://liked-songs"

// generateLikedSongsImage builds the pseudo-playlist cover from the live
// theme: the gradient blends the accent into the page tone, corners get
// progressively closer to full accent. Non-hex palettes (ANSI names) keep
// the original blue gradient.
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

// likedArtPaletteKey identifies the theme palette the gradient follows.
func likedArtPaletteKey() string {
	return string(colorBlue) + "|" + string(colorPage)
}

// likedArtCorners derives the gradient corners from the live theme (accent
// blending into the page tone), caching the last generated palette so
// re-theming only regenerates when the palette actually moved. Call it
// only from the event loop: Init and theme changes both run there, so the
// palette state needs no locking — the preload cmd receives the corners
// by value.
func likedArtCorners() [4]color.NRGBA {
	key := likedArtPaletteKey()
	if key == likedArtKey {
		return likedArtColors
	}
	likedArtKey = key
	likedArtColors = deriveLikedArtColors()
	return likedArtColors
}

func deriveLikedArtColors() [4]color.NRGBA {
	defaults := [4]color.NRGBA{
		{R: 60, G: 30, B: 120, A: 255}, {R: 40, G: 60, B: 150, A: 255},
		{R: 30, G: 90, B: 160, A: 255}, {R: 50, G: 130, B: 200, A: 255},
	}
	toNRGBA := func(hex string) (color.NRGBA, bool) {
		r, g, b, ok := hexToRGB(hex)
		return color.NRGBA{R: r, G: g, B: b, A: 255}, ok
	}
	accent, okA := toNRGBA(string(colorBlue))
	page, okP := toNRGBA(string(colorPage))
	if !okA || !okP {
		return defaults
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

// refreshLikedSongsArt regenerates the procedural cover when the theme
// palette moved; a no-op otherwise (the supersampled render is not cheap).
func (m *model) refreshLikedSongsArt() {
	if m.ui.imgs == nil {
		return
	}
	if key := likedArtPaletteKey(); key == likedArtKey {
		return
	}
	img := generateLikedSongsImage(likedSongsArtSize, likedArtCorners())
	m.ui.imgs.refreshURL(likedSongsImageURL, img, likedSongsArtSize, likedSongsArtSize)
}

func preloadLikedSongsArtCmd(m model) tea.Cmd {
	corners := likedArtCorners()
	return func() tea.Msg {
		m.preloadLikedSongsArt(corners)
		return nil
	}
}
