package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// CDN-realistic probe: photographic JPEG at the ~300px variant size the
// picker selects, so the benches measure the real swap-path workload.
func parityProbeImage(tb testing.TB) image.Image {
	tb.Helper()
	src := image.NewNRGBA(image.Rect(0, 0, 320, 320))
	for y := range 320 {
		for x := range 320 {
			src.Set(x, y, color.NRGBA{R: uint8((x * y) % 256), G: uint8(x % 256), B: uint8(y % 256), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 85}); err != nil {
		tb.Fatalf("encode probe jpeg: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		tb.Fatalf("decode probe jpeg: %v", err)
	}
	return fitCachedImage(img)
}

// Both renderers pre-render at load; the per-skip swap must cost a lookup
// on either side. These benches pin the parity the user asked for:
// pixelated and kitty swaps within the same order of magnitude.
func BenchmarkWarmSwapKittyLookup(b *testing.B) {
	c := newImgCacheWithSelection("rendered", true, func(string) string { return "xterm-kitty" })
	img := parityProbeImage(b)
	c.setImage("art", img, 40, 20)
	b.ResetTimer()
	for range b.N {
		if enc := c.encodedFor("art"); enc == "" {
			b.Fatal("missing kitty encoding")
		}
	}
}

func BenchmarkWarmSwapPixelatedLookup(b *testing.B) {
	c := newImgCacheWithSelection("pixelated", true, func(string) string { return "xterm" })
	img := parityProbeImage(b)
	c.setImage("art", img, 40, 20)
	c.preRenderCovers("art", [][2]int{{40, 20}}, colorprofile.TrueColor)
	b.ResetTimer()
	for range b.N {
		if s, ok := c.cover("art", 40, 20, colorprofile.TrueColor); !ok || s == "" {
			b.Fatal("missing half-block render")
		}
	}
}

// Load-path costs for reference: PNG encode (kitty) vs half-block string
// build (pixelated) for the same source. Both run once per URL in
// background workers, never on the swap path.
func BenchmarkLoadPathKittyPNGEncode(b *testing.B) {
	img := parityProbeImage(b)
	b.ResetTimer()
	for range b.N {
		if _, err := encodeImageAsPNGBase64(img); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadPathHalfblockRender(b *testing.B) {
	img := parityProbeImage(b)
	b.ResetTimer()
	for range b.N {
		if s := renderHalfBlock(img, 40, 20, colorprofile.TrueColor); s == "" {
			b.Fatal("empty render")
		}
	}
}

// One fetch must serve both renderers: the decoded source is shared, so a
// protocol switch re-renders from cache instead of refetching bytes.
func TestOneFetchServesBothProtocols(t *testing.T) {
	c := newImgCacheWithSelection("rendered", true, func(string) string { return "xterm-kitty" })
	if !c.beginLoad("art") {
		t.Fatal("first load must start a fetch")
	}
	c.setImage("art", parityProbeImage(t), 40, 20)
	c.finishLoad("art")
	if c.beginLoad("art") {
		t.Fatal("cached art must not refetch under kitty")
	}
	if enc := c.encodedFor("art"); enc == "" {
		t.Fatal("kitty side needs its encoding from the shared fetch")
	}
	if s, ok := c.cover("art", 40, 20, colorprofile.TrueColor); !ok || s == "" {
		t.Fatal("pixelated side needs its render from the shared fetch")
	}
	c.setImageStyle("pixelated", true, func(string) string { return "xterm" })
	if c.beginLoad("art") {
		t.Fatal("protocol switch must not refetch the shared source")
	}
	if s, ok := c.cover("art", 40, 20, colorprofile.TrueColor); !ok || s == "" {
		t.Fatal("pixelated render must survive the protocol switch from cache")
	}
}
