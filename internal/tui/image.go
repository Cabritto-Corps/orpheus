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

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	_ "golang.org/x/image/webp"

	"orpheus/internal/cache"
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
	lastKittyOverlay string
	lastKittyURL     string
	kittyVisible     bool
	kittyForceRedraw bool
	kittyImageID     uint64
	kittyChunks      map[string][]string
	kittyChunkOrder  []string
	coverKeysByURL   map[string]map[coverKey]struct{}
	pinned           map[string]struct{}
}

func newImgCache() *imgCache {
	return &imgCache{
		imgs:             cache.NewLRU[string, image.Image](maxCachedImages),
		covers:           cache.NewLRU[coverKey, string](maxCachedCoverRenders),
		encoded:          make(map[string]string),
		inflight:         make(map[string]struct{}),
		failedAt:         make(map[string]time.Time),
		rendering:        make(map[coverKey]chan struct{}),
		protocol:         detectImageProtocol(os.Getenv),
		kittyChunks:      make(map[string][]string),
		kittyChunkOrder:  make([]string, 0, maxKittyChunkCacheEntries),
		protocolExplicit: detectProtocolOverride(os.Getenv),
		coverKeysByURL:   make(map[string]map[coverKey]struct{}),
		pinned:           make(map[string]struct{}),
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

func kittyOverlayPlacement(key string) string {
	n := 0
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			n++
			if n == 4 {
				return key[:i]
			}
		}
	}
	return key
}

func (c *imgCache) beginKittyOverlayState(key, url string) (changed bool, shouldDelete bool, placementChanged bool, urlChanged bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	wasVisible := c.kittyVisible
	forceRedraw := c.kittyForceRedraw
	prevURL := c.lastKittyURL
	if key == "" {
		c.lastKittyOverlay = ""
		c.lastKittyURL = ""
		c.kittyVisible = false
		return false, wasVisible, false, strings.TrimSpace(prevURL) != ""
	}
	if wasVisible && c.lastKittyOverlay == key && !forceRedraw {
		return false, false, false, false
	}
	placementChanged = kittyOverlayPlacement(c.lastKittyOverlay) != kittyOverlayPlacement(key)
	trimmedURL := strings.TrimSpace(url)
	urlChanged = strings.TrimSpace(prevURL) != trimmedURL
	c.lastKittyOverlay = key
	c.lastKittyURL = trimmedURL
	c.kittyVisible = true
	c.kittyForceRedraw = false
	return true, wasVisible || forceRedraw, placementChanged, urlChanged
}

func (c *imgCache) resetKittyOverlayState() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetKittyOverlayStateLocked()
}

func (c *imgCache) resetKittyOverlayStateLocked() {
	c.lastKittyOverlay = ""
	c.lastKittyURL = ""
	c.kittyVisible = false
	c.kittyForceRedraw = true
}

func (c *imgCache) forceKittyRedraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kittyForceRedraw = true
}

func (c *imgCache) nextKittyImageID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kittyImageID++
	return c.kittyImageID
}

func (c *imgCache) kittyDisplayedURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastKittyURL
}

func (c *imgCache) buildKittyPayload(url, encoded string, cols, rows int, imageID uint64) string {
	if encoded == "" || cols <= 0 || rows <= 0 {
		return ""
	}
	c.mu.Lock()
	chunks := c.kittyChunks[url]
	if chunks == nil {
		chunks = chunkBase64(encoded, 4096)
		for len(c.kittyChunks) >= maxKittyChunkCacheEntries {
			oldest := c.kittyChunkOrder[0]
			delete(c.kittyChunks, oldest)
			c.kittyChunkOrder = c.kittyChunkOrder[1:]
		}
		c.kittyChunks[url] = chunks
		c.kittyChunkOrder = append(c.kittyChunkOrder, url)
	}
	localChunks := append([]string(nil), chunks...)
	c.mu.Unlock()
	return encodeKittyChunks(localChunks, cols, rows, imageID)
}

func chunkBase64(encoded string, size int) []string {
	if size <= 0 {
		return nil
	}
	var parts []string
	for off := 0; off < len(encoded); off += size {
		end := min(off+size, len(encoded))
		parts = append(parts, encoded[off:end])
	}
	return parts
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
		c.deleteKittyChunksLocked(url)
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
		c.deleteKittyChunksLocked(evictedURL)
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

func detectProtocolOverride(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("ORPHEUS_IMAGE_PROTOCOL"))) {
	case "none", "ansi", "kitty":
		return true
	}
	return false
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
		s := renderCover(protocol, img, encoded, cols, rows)

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
		encoded := c.encoded[url]
		protocol := c.protocol
		if _, rendering := c.rendering[key]; rendering {
			c.mu.Unlock()
			continue
		}
		ch := make(chan struct{})
		c.rendering[key] = ch
		c.mu.Unlock()
		return c.renderAndCache(key, url, img, encoded, protocol, cols, rows, ch)
	}
}

func (c *imgCache) renderAndCache(key coverKey, url string, img image.Image, encoded string, protocol imageProtocol, cols, rows int, ch chan struct{}) (string, bool) {
	s := renderCover(protocol, img, encoded, cols, rows)

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
	maxKittyChunkCacheEntries      = 64
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

func (c *imgCache) deleteKittyChunksLocked(url string) {
	if _, exists := c.kittyChunks[url]; exists {
		delete(c.kittyChunks, url)
		for i, u := range c.kittyChunkOrder {
			if u == url {
				c.kittyChunkOrder = append(c.kittyChunkOrder[:i], c.kittyChunkOrder[i+1:]...)
				break
			}
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

const kittyDeleteAll = "\x1b_Ga=d,d=A\x1b\\"

func renderKittyImageRaw(encoded string, cols, rows int) string {
	return renderKittyImageRawWithID(encoded, cols, rows, 0)
}

func renderKittyImageRawWithID(encoded string, cols, rows int, imageID uint64) string {
	if encoded == "" || cols <= 0 || rows <= 0 {
		return ""
	}
	return encodeKittyChunks(chunkBase64(encoded, 4096), cols, rows, imageID)
}

func encodeKittyChunks(chunks []string, cols, rows int, imageID uint64) string {
	var sb strings.Builder
	for i, part := range chunks {
		more := 0
		if i < len(chunks)-1 {
			more = 1
		}
		if i == 0 {
			if imageID > 0 {
				fmt.Fprintf(&sb, "\x1b_Ga=T,f=100,i=%d,c=%d,r=%d,q=2,m=%d;%s\x1b\\", imageID, cols, rows, more, part)
			} else {
				fmt.Fprintf(&sb, "\x1b_Ga=T,f=100,c=%d,r=%d,q=2,m=%d;%s\x1b\\", cols, rows, more, part)
			}
		} else {
			fmt.Fprintf(&sb, "\x1b_Gm=%d;%s\x1b\\", more, part)
		}
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
	if lipgloss.DefaultRenderer().ColorProfile() == termenv.Ascii {
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
