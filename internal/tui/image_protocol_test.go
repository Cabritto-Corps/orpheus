package tui

import (
	"strings"
	"testing"
)

func TestDetectImageProtocol(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want imageProtocol
	}{
		{
			name: "override none",
			env: map[string]string{
				"ORPHEUS_IMAGE_PROTOCOL": "none",
				"KITTY_WINDOW_ID":        "1",
			},
			want: imageProtocolNone,
		},
		{
			name: "kitty window id",
			env: map[string]string{
				"KITTY_WINDOW_ID": "1",
			},
			want: imageProtocolKitty,
		},
		{
			name: "ghostty term program",
			env: map[string]string{
				"TERM_PROGRAM": "ghostty",
			},
			want: imageProtocolKitty,
		},
		{
			name: "fallback ansi",
			env:  map[string]string{},
			want: imageProtocolNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectImageProtocol(func(key string) string {
				return tc.env[key]
			})
			if got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestRenderKittyImageChunks(t *testing.T) {
	payload := strings.Repeat("A", 9000)
	out := renderKittyImage(payload, 10, 6)

	if !strings.Contains(out, "\x1b_Ga=T,f=100,c=10,r=6,q=2,m=1;") {
		t.Fatalf("missing kitty first chunk header")
	}
	if strings.Count(out, "\x1b_Gm=") < 1 {
		t.Fatalf("expected continuation chunks")
	}
	if !strings.HasSuffix(out, strings.Repeat("\n", 5)) {
		t.Fatalf("expected row padding to preserve panel height")
	}
}

func TestRenderKittyImageRawWithIDIncludesImageID(t *testing.T) {
	out := renderKittyImageRawWithID("ZmFrZQ==", 10, 6, 42)
	if !strings.Contains(out, "i=42") {
		t.Fatalf("expected kitty payload to include image id, got %q", out)
	}
}

func TestKittyImageOverlayDoesNotDeleteOnNormalDraw(t *testing.T) {
	out := kittyImageOverlay(8, 2, "ZmFrZQ==", 10, 6, 7)
	if strings.Contains(out, kittyDeleteAll) {
		t.Fatalf("expected normal kitty draw not to prepend delete-all, got %q", out)
	}
}
