package tui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ── Cache ─────────────────────────────────────────────────────────────────────

type coverKey struct {
	url  string
	cols int
	rows int
}

// imgCache holds decoded images and pre-rendered half-block strings.
// It is allocated once and shared via pointer across all model copies.
type imgCache struct {
	mu        sync.RWMutex
	imgs      map[string]image.Image     // url -> decoded image
	covers    map[coverKey]string        // (url, cols, rows) -> rendered half-block string
	inflight  map[string]struct{}        // urls currently being fetched
	rendering map[coverKey]chan struct{} // covers currently being rendered
}

func newImgCache() *imgCache {
	return &imgCache{
		imgs:      make(map[string]image.Image),
		covers:    make(map[coverKey]string),
		inflight:  make(map[string]struct{}),
		rendering: make(map[coverKey]chan struct{}),
	}
}

func (c *imgCache) getImage(url string) (image.Image, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	img, ok := c.imgs[url]
	return img, ok
}

func (c *imgCache) setImage(url string, img image.Image) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.imgs[url] = img
}

func (c *imgCache) beginLoad(url string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if url == "" {
		return false
	}
	if _, ok := c.imgs[url]; ok {
		return false
	}
	if _, ok := c.inflight[url]; ok {
		return false
	}
	c.inflight[url] = struct{}{}
	return true
}

func (c *imgCache) finishLoad(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, url)
}

// cover returns the cached half-block string for the given url and dimensions,
// rendering it from the cached image if necessary.
// Returns ("", false) if the image has not been fetched yet.
func (c *imgCache) cover(url string, cols, rows int) (string, bool) {
	if url == "" || cols <= 0 || rows <= 0 {
		return "", true // nothing to show, but not a cache miss
	}

	key := coverKey{url: url, cols: cols, rows: rows}
	for {
		c.mu.Lock()
		if s, ok := c.covers[key]; ok {
			c.mu.Unlock()
			return s, true
		}
		img, ok := c.imgs[url]
		if !ok {
			c.mu.Unlock()
			return "", false // fetch not complete yet
		}
		if ch, rendering := c.rendering[key]; rendering {
			c.mu.Unlock()
			<-ch
			continue
		}
		ch := make(chan struct{})
		c.rendering[key] = ch
		c.mu.Unlock()

		s := renderHalfBlock(img, cols, rows)

		c.mu.Lock()
		delete(c.rendering, key)
		if existing, ok := c.covers[key]; ok {
			c.mu.Unlock()
			close(ch)
			return existing, true
		}
		c.covers[key] = s
		c.mu.Unlock()
		close(ch)
		return s, true
	}
}

// ── Fetch ─────────────────────────────────────────────────────────────────────

const imageFetchTimeout = 6 * time.Second

var imageHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: imageFetchTimeout,
	},
}

// fetchImageFromBytes downloads and decodes an image from url.
func fetchImageFromBytes(ctx context.Context, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build image request: %w", err)
	}

	resp, err := imageHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch image: unexpected status %s", resp.Status)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return img, nil
}

// ── Resize ────────────────────────────────────────────────────────────────────

// resizeNN resizes src to (width × height) pixels using nearest-neighbour
// sampling. Fast and dependency-free; quality is sufficient for pixel-art
// terminal rendering.
func resizeNN(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	sb := src.Bounds()
	sw := sb.Dx()
	sh := sb.Dy()
	for y := 0; y < height; y++ {
		sy := sb.Min.Y + y*sh/height
		for x := 0; x < width; x++ {
			sx := sb.Min.X + x*sw/width
			r, g, b, _ := src.At(sx, sy).RGBA()
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: 255,
			})
		}
	}
	return dst
}

// ── Half-block renderer ───────────────────────────────────────────────────────

// renderHalfBlock converts img into a cols×rows character string using the ▀
// (UPPER HALF BLOCK) trick:
//
//   - foreground colour = top pixel of the pair
//   - background colour = bottom pixel of the pair
//
// Each character row encodes two image pixel rows, so the source image is
// resized to (cols × rows*2) pixels before rendering. For a visually square
// result in a typical terminal (cells ~2× taller than wide), use rows = cols/2.
func renderHalfBlock(img image.Image, cols, rows int) string {
	if cols <= 0 || rows <= 0 || img == nil {
		return ""
	}

	// Resize once to the exact pixel grid we need.
	px := resizeNN(img, cols, rows*2)

	var sb strings.Builder
	sb.Grow(cols * rows * 30) // rough pre-alloc: ~28 bytes per ▀ cell

	reset := "\x1b[0m"

	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			top := px.RGBAAt(col, row*2)
			bot := px.RGBAAt(col, row*2+1)

			// Set foreground (top half) and background (bottom half).
			fmt.Fprintf(&sb,
				"\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				top.R, top.G, top.B,
				bot.R, bot.G, bot.B,
			)
		}
		sb.WriteString(reset)
		if row < rows-1 {
			sb.WriteByte('\n')
		}
	}

	return sb.String()
}

// squareDims computes image dimensions (cols, rows) for a panel with the given
// inner width and height so that the rendered image appears square.
// Terminal cells are approximately 2× taller than wide, so rows = cols/2 gives
// a square visual result. The result is clamped to the available space.
func squareDims(innerW, innerH int) (cols, rows int) {
	if innerW <= 0 || innerH <= 0 {
		return 0, 0
	}
	cols = innerW
	rows = cols / 2
	if rows > innerH {
		rows = innerH
		cols = rows * 2
		if cols > innerW {
			cols = innerW
			rows = cols / 2
		}
	}
	return cols, rows
}
