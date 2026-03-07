package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"

	"orpheus/internal/cache"
	"orpheus/internal/infra/ports"
)

type coverKey struct {
	url  string
	cols int
	rows int
}

type imageProtocol int

const (
	imageProtocolNone imageProtocol = iota
	imageProtocolKitty
)

type imgCache struct {
	mu               sync.RWMutex
	imgs             *cache.LRU[string, image.Image]
	covers           *cache.LRU[coverKey, string]
	encoded          map[string]string
	inflight         map[string]struct{}
	failedAt         map[string]time.Time
	rendering        map[coverKey]chan struct{}
	protocol         imageProtocol
	stats            imageCacheStats
	lastKittyOverlay string
	kittyVisible     bool
}

type imageCacheStats struct {
	imageHit         uint64
	imageMiss        uint64
	loadStarted      uint64
	loadSkipCached   uint64
	loadSkipInflight uint64
}

func newImgCache() *imgCache {
	return &imgCache{
		imgs:      cache.NewLRU[string, image.Image](maxCachedImages),
		covers:    cache.NewLRU[coverKey, string](maxCachedCoverRenders),
		encoded:   make(map[string]string),
		inflight:  make(map[string]struct{}),
		failedAt:  make(map[string]time.Time),
		rendering: make(map[coverKey]chan struct{}),
		protocol:  detectImageProtocol(os.Getenv),
	}
}

func (c *imgCache) getImage(url string) (image.Image, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imgs.Get(url)
}

func (c *imgCache) hasImage(url string) bool {
	if url == "" {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.imgs.Peek(url)
	return ok
}

func (c *imgCache) encodedFor(url string) string {
	if url == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.encoded[url]
}

func (c *imgCache) beginKittyOverlayState(key string) (changed bool, shouldDelete bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	shouldDelete = c.kittyVisible
	if key == "" {
		c.lastKittyOverlay = ""
		c.kittyVisible = false
		return false, shouldDelete
	}
	if c.kittyVisible && c.lastKittyOverlay == key {
		return false, false
	}
	c.lastKittyOverlay = key
	c.kittyVisible = true
	return true, shouldDelete
}

func (c *imgCache) setImage(url string, img image.Image) {
	encoded := ""
	if c.protocol == imageProtocolKitty {
		if s, err := encodeImageAsPNGBase64(img); err == nil {
			encoded = s
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	evictedURL, _, evicted := c.imgs.Set(url, img)
	if encoded != "" {
		c.encoded[url] = encoded
	}
	if evicted {
		c.deleteCoversForURLLocked(evictedURL)
		delete(c.encoded, evictedURL)
	}
}

func (c *imgCache) preRenderCovers(url string, coverSizes [][2]int) {
	if c.protocol == imageProtocolKitty {
		return
	}
	c.mu.RLock()
	img, ok := c.imgs.Peek(url)
	encoded := c.encoded[url]
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
		_, already := c.covers.Peek(key)
		c.mu.RUnlock()
		if already {
			c.mu.Lock()
			_, _ = c.covers.Get(key)
			c.mu.Unlock()
			continue
		}
		s := renderCover(c.protocol, img, encoded, cols, rows)
		c.mu.Lock()
		if _, exists := c.covers.Peek(key); !exists {
			c.covers.Set(key, s)
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
	if _, ok := c.imgs.Peek(url); ok {
		c.stats.loadSkipCached++
		return false
	}
	if _, ok := c.inflight[url]; ok {
		c.stats.loadSkipInflight++
		return false
	}
	c.inflight[url] = struct{}{}
	c.stats.loadStarted++
	return true
}

func (c *imgCache) snapshotStats() imageCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	lruStats := c.imgs.Stats()
	out := c.stats
	out.imageHit = lruStats.Hits
	out.imageMiss = lruStats.Misses
	return out
}

func (c *imgCache) shouldQueueLoad(url string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if url == "" {
		return false
	}
	if _, ok := c.imgs.Peek(url); ok {
		return false
	}
	if _, ok := c.inflight[url]; ok {
		return false
	}
	if t, ok := c.failedAt[url]; ok && time.Since(t) < imageFetchFailCooldown {
		return false
	}
	return true
}

func (c *imgCache) markFailed(url string) {
	if url == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failedAt[url] = time.Now()
}

func (c *imgCache) finishLoad(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, url)
}

func (c *imgCache) invalidateCovers() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.covers.Clear()
}

func (c *imgCache) cover(url string, cols, rows int) (string, bool) {
	if url == "" || cols <= 0 || rows <= 0 {
		return "", true
	}

	key := coverKey{url: url, cols: cols, rows: rows}
	for {
		c.mu.Lock()
		if s, ok := c.covers.Get(key); ok {
			c.mu.Unlock()
			return s, true
		}
		img, ok := c.imgs.Peek(url)
		if !ok {
			c.mu.Unlock()
			return "", false
		}
		encoded := c.encoded[url]
		if ch, rendering := c.rendering[key]; rendering {
			c.mu.Unlock()
			<-ch
			continue
		}
		ch := make(chan struct{})
		c.rendering[key] = ch
		c.mu.Unlock()

		s := renderCover(c.protocol, img, encoded, cols, rows)

		c.mu.Lock()
		delete(c.rendering, key)
		if existing, ok := c.covers.Get(key); ok {
			c.mu.Unlock()
			close(ch)
			return existing, true
		}
		c.covers.Set(key, s)
		c.mu.Unlock()
		close(ch)
		return s, true
	}
}

const (
	imageFetchTimeout      = 6 * time.Second
	imageFetchFailCooldown = 30 * time.Second
	maxCachedImages        = 256
	maxCachedCoverRenders  = 512
)

func (c *imgCache) deleteCoversForURLLocked(url string) {
	for _, key := range c.covers.Keys() {
		if key.url != url {
			continue
		}
		c.covers.Delete(key)
	}
}

func encodeImageAsPNGBase64(img image.Image) (string, error) {
	if img == nil {
		return "", nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func renderCover(protocol imageProtocol, img image.Image, encoded string, cols, rows int) string {
	if protocol == imageProtocolKitty && encoded != "" {
		return renderKittyImage(encoded, cols, rows)
	}
	return renderHalfBlock(img, cols, rows)
}

func renderKittyImage(encoded string, cols, rows int) string {
	if encoded == "" || cols <= 0 || rows <= 0 {
		return ""
	}
	s := renderKittyImageRaw(encoded, cols, rows)
	if s == "" {
		return ""
	}
	if rows > 1 {
		s += strings.Repeat("\n", rows-1)
	}
	return s
}

func detectImageProtocol(getenv func(string) string) imageProtocol {
	if override := strings.ToLower(strings.TrimSpace(getenv("ORPHEUS_IMAGE_PROTOCOL"))); override != "" {
		switch override {
		case "none", "ansi":
			return imageProtocolNone
		case "kitty":
			return imageProtocolKitty
		}
	}

	term := strings.ToLower(getenv("TERM"))
	termProgram := strings.ToLower(getenv("TERM_PROGRAM"))
	if strings.TrimSpace(getenv("KITTY_WINDOW_ID")) != "" {
		return imageProtocolKitty
	}
	if strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || termProgram == "ghostty" {
		return imageProtocolKitty
	}
	return imageProtocolNone
}

// kittyDeleteAll is a Kitty graphics protocol command that removes all visible
// image placements from the terminal. It must be sent before rendering a new
// image so that stale images (from a previous size, position, or content) do
// not accumulate.
const kittyDeleteAll = "\x1b_Ga=d,d=A\x1b\\"

// kittyImageOverlay returns a cursor-save + absolute-position + Kitty
// graphics payload (sized to cols×rows cells) + cursor-restore sequence.
// A delete-all command is prepended so any previously rendered image is
// cleared first, preventing image multiplication on resize or navigation.
func kittyImageOverlay(row, col int, encoded string, cols, rows int) string {
	if encoded == "" || cols <= 0 || rows <= 0 || row <= 0 || col <= 0 {
		return kittyDeleteAll
	}
	payload := renderKittyImageRaw(encoded, cols, rows)
	if payload == "" {
		return kittyDeleteAll
	}
	return kittyDeleteAll + fmt.Sprintf("\x1b7\x1b[%d;%dH%s\x1b8", row, col, payload)
}

// renderKittyImageRaw returns the Kitty APC payload without trailing newlines.
// Use kittyImageOverlay for cursor-positioned overlay rendering.
func renderKittyImageRaw(encoded string, cols, rows int) string {
	if encoded == "" || cols <= 0 || rows <= 0 {
		return ""
	}
	const chunkSize = 4096
	var sb strings.Builder
	first := true
	for off := 0; off < len(encoded); off += chunkSize {
		end := off + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		part := encoded[off:end]
		more := 0
		if end < len(encoded) {
			more = 1
		}
		if first {
			fmt.Fprintf(&sb, "\x1b_Ga=T,f=100,c=%d,r=%d,q=2,m=%d;%s\x1b\\", cols, rows, more, part)
			first = false
			continue
		}
		fmt.Fprintf(&sb, "\x1b_Gm=%d;%s\x1b\\", more, part)
	}
	return sb.String()
}

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

type httpImageProvider struct{}

func (httpImageProvider) Fetch(ctx context.Context, url string) ([]byte, error) {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read image body: %w", err)
	}
	return body, nil
}

var imageProvider ports.ImageProvider = httpImageProvider{}

func fetchImage(ctx context.Context, url string) (image.Image, error) {
	body, err := imageProvider.Fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return img, nil
}

func resizeBilinear(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	sb := src.Bounds()
	sw := sb.Dx()
	sh := sb.Dy()
	if sw <= 0 || sh <= 0 || width <= 0 || height <= 0 {
		return dst
	}
	if sw == 1 || sh == 1 || width == 1 || height == 1 {
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

	scaleX := float64(sw-1) / float64(width-1)
	scaleY := float64(sh-1) / float64(height-1)
	for y := 0; y < height; y++ {
		fy := float64(y) * scaleY
		y0 := int(fy)
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		wy := fy - float64(y0)
		for x := 0; x < width; x++ {
			fx := float64(x) * scaleX
			x0 := int(fx)
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			wx := fx - float64(x0)

			r00, g00, b00, _ := src.At(sb.Min.X+x0, sb.Min.Y+y0).RGBA()
			r10, g10, b10, _ := src.At(sb.Min.X+x1, sb.Min.Y+y0).RGBA()
			r01, g01, b01, _ := src.At(sb.Min.X+x0, sb.Min.Y+y1).RGBA()
			r11, g11, b11, _ := src.At(sb.Min.X+x1, sb.Min.Y+y1).RGBA()

			interp := func(c00, c10, c01, c11 uint32) uint8 {
				top := (1.0-wx)*float64(c00) + wx*float64(c10)
				bot := (1.0-wx)*float64(c01) + wx*float64(c11)
				v := (1.0-wy)*top + wy*bot
				return uint8((uint32(v) >> 8) & 0xff)
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: interp(r00, r10, r01, r11),
				G: interp(g00, g10, g01, g11),
				B: interp(b00, b10, b01, b11),
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

	px := resizeBilinear(img, cols, rows*2)

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
