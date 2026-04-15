package tui

import (
	"image"
	"image/color"
)

const likedSongsImageURL = "orpheus://liked-songs"

func generateLikedSongsImage(size int) image.Image {
	if size < 2 {
		size = 2
	}

	ssFactor := 16
	ssSize := size * ssFactor
	ssImg := image.NewRGBA(image.Rect(0, 0, ssSize, ssSize))

	tl := color.NRGBA{R: 60, G: 30, B: 120, A: 255}
	tr := color.NRGBA{R: 40, G: 60, B: 150, A: 255}
	bl := color.NRGBA{R: 30, G: 90, B: 160, A: 255}
	br := color.NRGBA{R: 50, G: 130, B: 200, A: 255}

	heartScale := float64(ssSize) * 0.07
	cx := float64(ssSize) / 2
	cy := float64(ssSize) / 2

	for y := 0; y < ssSize; y++ {
		ty := float64(y) / float64(ssSize-1)
		for x := 0; x < ssSize; x++ {
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

func isInHeart(x, y float64) bool {
	a := x*x + y*y - 1
	return a*a*a-x*x*y*y*y <= 0
}

func downsample(src *image.RGBA, srcSize, dstSize int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dstSize, dstSize))
	ratio := srcSize / dstSize
	for dy := 0; dy < dstSize; dy++ {
		for dx := 0; dx < dstSize; dx++ {
			var r, g, b, count uint32
			for sy := 0; sy < ratio; sy++ {
				for sx := 0; sx < ratio; sx++ {
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

func (m *model) preloadLikedSongsArt() {
	if m.imgs == nil {
		return
	}
	img := generateLikedSongsImage(likedSongsArtSize)
	m.imgs.setImage(likedSongsImageURL, img, likedSongsArtSize, likedSongsArtSize)
}
