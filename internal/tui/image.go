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
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"

	"orpheus/internal/cache"
	"orpheus/internal/config"
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
	protocolExplicit bool
	overlay          overlayState
	coverKeysByURL   map[string]map[coverKey]struct{}
	pinned           map[string]struct{}
}

func imageStyleOrDefault(style string) string {
	if normalized := config.NormalizeImageStyle(style); normalized != "" {
		return normalized
	}
	return config.ImageStyleRendered
}

func newImgCache() *imgCache {
	return newImgCacheWithSelection("", false, os.Getenv)
}

func newImgCacheWithSelection(style string, managed bool, getenv func(string) string) *imgCache {
	if getenv == nil {
		getenv = os.Getenv
	}
	protocol, explicit := resolveImageProtocolForStyle(style, managed, getenv)
	return &imgCache{
		imgs:             cache.NewLRU[string, image.Image](maxCachedImages),
		covers:           cache.NewLRU[coverKey, string](maxCachedCoverRenders),
		encoded:          make(map[string]string),
		inflight:         make(map[string]struct{}),
		failedAt:         make(map[string]time.Time),
		rendering:        make(map[coverKey]chan struct{}),
		protocol:         protocol,
		protocolExplicit: explicit,
		coverKeysByURL:   make(map[string]map[coverKey]struct{}),
		pinned:           make(map[string]struct{}),
	}
}

func resolveImageProtocolForStyle(style string, managed bool, getenv func(string) string) (imageProtocol, bool) {
	switch config.NormalizeImageStyle(style) {
	case config.ImageStylePixelated:
		return imageProtocolNone, true
	case config.ImageStyleRendered:
		if managed {
			return detectTerminalImageProtocol(getenv), false
		}
	}
	return detectImageProtocol(getenv), detectProtocolOverride(getenv)
}

func (c *imgCache) getImage(url string) (image.Image, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imgs.Get(url)
}

func (c *imgCache) encodedFor(url string) string {
	if url == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.encoded[url]
}

func (c *imgCache) setImage(url string, img image.Image, displayCols, displayRows int) {
	c.mu.RLock()
	protocol := c.protocol
	c.mu.RUnlock()
	encoded := ""
	if protocol == imageProtocolKitty {
		if s, err := encodeImageAsPNGBase64(img); err == nil {
			encoded = s
		} else {
			slog.Warn("kitty encode failed", "url", url, "cols", displayCols, "rows", displayRows, "error", err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	evictedURL, evictedImg, evicted := c.imgs.Set(url, img)
	if encoded != "" {
		c.encoded[url] = encoded
	}
	for evicted {
		if _, pinned := c.pinned[evictedURL]; pinned {
			if len(c.pinned) >= c.imgs.Capacity() {
				break
			}
			evictedURL, evictedImg, evicted = c.imgs.Set(evictedURL, evictedImg)
			continue
		}
		c.deleteCoversForURLLocked(evictedURL)
		delete(c.encoded, evictedURL)
		break
	}
}

func (c *imgCache) setProtocol(protocol imageProtocol) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.protocolExplicit || c.protocol == protocol {
		return
	}
	c.protocol = protocol
	c.covers.Clear()
	for url := range c.coverKeysByURL {
		delete(c.coverKeysByURL, url)
	}
	c.resetKittyOverlayStateLocked()
}

func (c *imgCache) setImageStyle(style string, managed bool, getenv func(string) string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	protocol, explicit := resolveImageProtocolForStyle(style, managed, getenv)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.protocol = protocol
	// A forced pixelated choice must behave like an environment override:
	// the automatic fallback and recovery paths must not restore kitty.
	c.protocolExplicit = explicit
	c.covers.Clear()
	for url := range c.coverKeysByURL {
		delete(c.coverKeysByURL, url)
	}
	clear(c.encoded)
	c.resetKittyOverlayStateLocked()
}

func detectProtocolOverride(getenv func(string) string) bool {
	_, ok := imageProtocolEnvOverride(getenv)
	return ok
}

func imageProtocolEnvOverride(getenv func(string) string) (imageProtocol, bool) {
	switch strings.ToLower(strings.TrimSpace(getenv("ORPHEUS_IMAGE_PROTOCOL"))) {
	case "none", "ansi":
		return imageProtocolNone, true
	case "kitty":
		return imageProtocolKitty, true
	default:
		return imageProtocolNone, false
	}
}

// refreshURL replaces an image and drops every cached render of the old
// one — used when a procedurally generated cover's palette changes.
func (c *imgCache) refreshURL(url string, img image.Image, w, h int) {
	c.mu.Lock()
	c.deleteCoversForURLLocked(url)
	c.mu.Unlock()
	c.setImage(url, img, w, h)
}

func (c *imgCache) pinURL(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinned[url] = struct{}{}
}

func (c *imgCache) preRenderCovers(url string, coverSizes [][2]int) {
	c.mu.RLock()
	protocol := c.protocol
	if protocol == imageProtocolKitty {
		c.mu.RUnlock()
		return
	}
	img, ok := c.imgs.Peek(url)
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
		c.mu.Lock()
		_, already := c.covers.Get(key)
		if already {
			c.mu.Unlock()
			continue
		}
		if _, rendering := c.rendering[key]; rendering {
			c.mu.Unlock()
			continue
		}
		ch := make(chan struct{})
		c.rendering[key] = ch
		c.mu.Unlock()

		// Resize+render happens outside the lock so per-frame View() reads
		// are not blocked behind bilinear work.
		s := renderCover(img, cols, rows)

		c.mu.Lock()
		delete(c.rendering, key)
		close(ch)
		if _, exists := c.covers.Peek(key); !exists {
			if evictedKey, _, evicted := c.covers.Set(key, s); evicted {
				c.removeCoverKeyFromURLMap(evictedKey)
			}
			if _, ok := c.coverKeysByURL[url]; !ok {
				c.coverKeysByURL[url] = make(map[coverKey]struct{})
			}
			c.coverKeysByURL[url][key] = struct{}{}
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
	if _, ok := c.imgs.Peek(url); ok && c.hasKittyEncodingLocked(url) {
		return false
	}
	if _, ok := c.inflight[url]; ok {
		return false
	}
	c.inflight[url] = struct{}{}
	return true
}

func (c *imgCache) shouldQueueLoad(url string) bool {
	c.mu.RLock()
	if url == "" {
		c.mu.RUnlock()
		return false
	}
	if _, ok := c.imgs.Peek(url); ok && c.hasKittyEncodingLocked(url) {
		c.mu.RUnlock()
		return false
	}
	if _, ok := c.inflight[url]; ok {
		c.mu.RUnlock()
		return false
	}
	t, failed := c.failedAt[url]
	c.mu.RUnlock()
	if !failed {
		return true
	}
	if time.Since(t) < imageFetchFailCooldown {
		return false
	}
	c.mu.Lock()
	if t2, ok := c.failedAt[url]; ok && time.Since(t2) >= imageFetchFailCooldown {
		delete(c.failedAt, url)
	}
	c.mu.Unlock()
	return true
}

func (c *imgCache) shouldQueuePriorityLoad(url string) bool {
	c.mu.RLock()
	if url == "" {
		c.mu.RUnlock()
		return false
	}
	if _, ok := c.imgs.Peek(url); ok && c.hasKittyEncodingLocked(url) {
		c.mu.RUnlock()
		return false
	}
	if _, ok := c.inflight[url]; ok {
		c.mu.RUnlock()
		return false
	}
	t, failed := c.failedAt[url]
	c.mu.RUnlock()
	if !failed {
		return true
	}
	if time.Since(t) < imageFetchPriorityFailCooldown {
		return false
	}
	c.mu.Lock()
	if t2, ok := c.failedAt[url]; ok && time.Since(t2) >= imageFetchPriorityFailCooldown {
		delete(c.failedAt, url)
	}
	c.mu.Unlock()
	return true
}

// protocolForRender reads the negotiated protocol under the lock so render
// paths cannot race the writer (the bare field read was a latent race).
func (c *imgCache) protocolForRender() imageProtocol {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.protocol
}

func (c *imgCache) hasKittyEncoding(url string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasKittyEncodingLocked(url)
}

func (c *imgCache) hasKittyEncodingLocked(url string) bool {
	if c.protocol != imageProtocolKitty {
		return true
	}
	return strings.TrimSpace(c.encoded[url]) != ""
}

func (c *imgCache) ensureKittyEncoding(url string, img image.Image) error {
	if url == "" || img == nil {
		return nil
	}
	c.mu.RLock()
	needsEncode := c.protocol == imageProtocolKitty && strings.TrimSpace(c.encoded[url]) == ""
	c.mu.RUnlock()
	if !needsEncode {
		return nil
	}
	encoded, err := encodeImageAsPNGBase64(img)
	if err != nil {
		return err
	}
	if strings.TrimSpace(encoded) == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.protocol == imageProtocolKitty && strings.TrimSpace(c.encoded[url]) == "" {
		c.encoded[url] = encoded
	}
	return nil
}

func (c *imgCache) markFailed(url string) {
	if url == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failedAt[url] = time.Now()
}

func (c *imgCache) clearFailed(url string) {
	if url == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failedAt, url)
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
	for url := range c.coverKeysByURL {
		delete(c.coverKeysByURL, url)
	}
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
		if ch, rendering := c.rendering[key]; rendering {
			// Another goroutine is already rendering this key: wait for
			// its completion signal instead of hot-spinning the event
			// loop. Both paths close the channel exactly once, so every
			// waiter wakes and re-checks the cache above.
			c.mu.Unlock()
			<-ch
			continue
		}
		ch := make(chan struct{})
		c.rendering[key] = ch
		c.mu.Unlock()
		return c.renderAndCache(key, url, img, cols, rows, ch)
	}
}

func (c *imgCache) renderAndCache(key coverKey, url string, img image.Image, cols, rows int, ch chan struct{}) (string, bool) {
	s := renderCover(img, cols, rows)

	c.mu.Lock()
	var result string
	if existing, ok := c.covers.Get(key); ok {
		result = existing
	} else {
		if evictedKey, _, evicted := c.covers.Set(key, s); evicted {
			c.removeCoverKeyFromURLMap(evictedKey)
		}
		if _, ok := c.coverKeysByURL[url]; !ok {
			c.coverKeysByURL[url] = make(map[coverKey]struct{})
		}
		c.coverKeysByURL[url][key] = struct{}{}
		result = s
	}
	delete(c.rendering, key)
	c.mu.Unlock()
	close(ch)
	return result, true
}

const (
	imageFetchTimeout              = 6 * time.Second
	imageFetchFailCooldown         = 30 * time.Second
	imageFetchPriorityFailCooldown = 5 * time.Second
	maxCachedImages                = 256
	maxCachedCoverRenders          = 512
	kittyEncodePixelBudget         = 512
)

func (c *imgCache) deleteCoversForURLLocked(url string) {
	if keys, ok := c.coverKeysByURL[url]; ok {
		for key := range keys {
			c.covers.Delete(key)
		}
		delete(c.coverKeysByURL, url)
	}
}

func (c *imgCache) removeCoverKeyFromURLMap(key coverKey) {
	if keys, ok := c.coverKeysByURL[key.url]; ok {
		delete(keys, key)
		if len(keys) == 0 {
			delete(c.coverKeysByURL, key.url)
		}
	}
}

func encodeImageAsPNGBase64(img image.Image) (string, error) {
	if img == nil {
		return "", nil
	}
	sb := img.Bounds()
	pw, ph := sb.Dx(), sb.Dy()
	longest := max(pw, ph)
	if longest > kittyEncodePixelBudget {
		if pw >= ph {
			ph = ph * kittyEncodePixelBudget / pw
			pw = kittyEncodePixelBudget
		} else {
			pw = pw * kittyEncodePixelBudget / ph
			ph = kittyEncodePixelBudget
		}
		if pw < 1 {
			pw = 1
		}
		if ph < 1 {
			ph = 1
		}
		img = resizeBilinear(img, pw, ph)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func renderCover(img image.Image, cols, rows int) string {
	return renderHalfBlock(img, cols, rows)
}

func detectImageProtocol(getenv func(string) string) imageProtocol {
	if protocol, ok := imageProtocolEnvOverride(getenv); ok {
		return protocol
	}
	return detectTerminalImageProtocol(getenv)
}

func detectTerminalImageProtocol(getenv func(string) string) imageProtocol {
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch image: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read image body: %w", err)
	}
	return body, nil
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
		for y := range height {
			sy := sb.Min.Y + y*sh/height
			for x := range width {
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
	for y := range height {
		fy := float64(y) * scaleY
		y0 := int(fy)
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		wy := fy - float64(y0)
		for x := range width {
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
	if !colorEnabled() {
		// NO_COLOR / no-color terminals strip truecolor ANSI, turning the
		// half-block mosaic into meaningless blank blocks.
		return ""
	}

	px := resizeBilinear(img, cols, rows*2)

	var sb strings.Builder
	sb.Grow(cols * rows * 30)

	reset := "\x1b[0m"

	for row := range rows {
		for col := range cols {
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
	rows = min(innerH, innerW/2)
	cols = 2 * rows
	return cols, rows
}
