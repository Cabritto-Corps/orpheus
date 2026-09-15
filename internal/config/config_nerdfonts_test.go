package config

import "testing"

func TestResolveNerdFontsExplicitValues(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"TRUE":  true,
		"1":     true,
		"on":    true,
		"false": false,
		"0":     false,
		"off":   false,
		"no":    false,
	}
	for raw, want := range cases {
		if got := resolveNerdFonts(raw); got != want {
			t.Errorf("resolveNerdFonts(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestResolveNerdFontsInvalidFallsBackToDetection(t *testing.T) {
	if got := resolveNerdFonts("maybe"); got != detectNerdFontsInstalled() {
		t.Errorf("invalid value should fall back to detection, got %v want %v", got, detectNerdFontsInstalled())
	}
}
