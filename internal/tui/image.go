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

type coverKey struct {
	url  string
	cols int
	rows int
}

type imgCache struct {
	mu        sync.RWMutex
	imgs      map[string]image.Image
	covers    map[coverKey]string
	inflight  map[string]struct{}
	rendering map[coverKey]chan struct{}
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

func (c *imgCache) preRenderCovers(url string, coverSizes [][2]int) {
	c.mu.RLock()
	img, ok := c.imgs[url]
	c.mu.RUnlock()
	if !ok {
		return
	}
	for _, sz := range coverSizes {
		cols, rows := sz[0], sz[1]
		if cols <= 0 || rows <= 0 {
			continue
		}
		key := coverKey{url: url, cols: cols, rows: rows}
		c.mu.RLock()
		_, already := c.covers[key]
		c.mu.RUnlock()
		if already {
			continue
		}
		s := renderHalfBlock(img, cols, rows)
		c.mu.Lock()
		if _, exists := c.covers[key]; !exists {
			c.covers[key] = s
		}
		c.mu.Unlock()
	}
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

func (c *imgCache) cover(url string, cols, rows int) (string, bool) {
	if url == "" || cols <= 0 || rows <= 0 {
		return "", true
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
			return "", false
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

func renderHalfBlock(img image.Image, cols, rows int) string {
	if cols <= 0 || rows <= 0 || img == nil {
		return ""
	}

	px := resizeNN(img, cols, rows*2)

	var sb strings.Builder
	sb.Grow(cols * rows * 30)

	reset := "\x1b[0m"

	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			top := px.RGBAAt(col, row*2)
			bot := px.RGBAAt(col, row*2+1)

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
