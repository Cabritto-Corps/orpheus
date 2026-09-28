package tui

import (
	"image/color"
	"os"
	"testing"
)

func TestLikedArtPaletteFollowsPreset(t *testing.T) {
	def := likedArtPalette(themePreset("default"))
	drac := likedArtPalette(themePreset("dracula"))
	if def == drac {
		t.Fatal("dracula and default palettes must differ")
	}
	// Dracula's accent #BD93F9 carries far more red than default #4A90D9;
	// every corner leans on the accent, so red must rise across the board.
	for i := range def {
		if drac[i].R <= def[i].R {
			t.Fatalf("corner %d: dracula red %d must exceed default red %d", i, drac[i].R, def[i].R)
		}
	}
}

func TestLikedArtRenderFollowsPalette(t *testing.T) {
	def := generateLikedSongsImage(8, likedArtPalette(themePreset("default")))
	drac := generateLikedSongsImage(8, likedArtPalette(themePreset("dracula")))
	// The top-left corner sits outside the heart motif: pure background.
	dc, ok := def.At(0, 0).(color.RGBA)
	if !ok {
		t.Fatalf("default corner pixel = %T, want color.RGBA", def.At(0, 0))
	}
	rc, ok := drac.At(0, 0).(color.RGBA)
	if !ok {
		t.Fatalf("dracula corner pixel = %T, want color.RGBA", drac.At(0, 0))
	}
	if rc.R <= dc.R {
		t.Fatalf("dracula corner red %d must exceed default corner red %d", rc.R, dc.R)
	}
	// The white heart lightens the middle: center must outshine the corner
	// in the same render, proving the motif survived the re-palette.
	cc, ok := drac.At(4, 4).(color.RGBA)
	if !ok {
		t.Fatalf("dracula center pixel = %T, want color.RGBA", drac.At(4, 4))
	}
	brightness := func(c color.RGBA) int { return int(c.R) + int(c.G) + int(c.B) }
	if brightness(cc) <= brightness(rc) {
		t.Fatalf("center brightness %d must exceed corner %d (heart motif)", brightness(cc), brightness(rc))
	}
}

func TestLikedArtAnsiPaletteIsGrayscale(t *testing.T) {
	pal := likedArtPalette(themeColors{Blue: "7", Page: "0"})
	for i, c := range pal {
		if c.R != c.G || c.G != c.B {
			t.Fatalf("corner %d = %+v, want neutral gray", i, c)
		}
	}
	if pal == likedArtPalette(themePreset("default")) {
		t.Fatal("grayscale fallback must differ from the default gradient")
	}
}

func TestLikedArtOverridesChangePalette(t *testing.T) {
	base := likedArtPalette(resolveThemeColors("default", nil))
	tweaked := likedArtPalette(resolveThemeColors("default", map[string]any{"blue": "#FF0000"}))
	// The full-accent corner carries the override verbatim.
	if tweaked[3].R <= base[3].R {
		t.Fatalf("override red %d must exceed base red %d", tweaked[3].R, base[3].R)
	}
}

func TestLikedArtThemeColorsFollowThemeFile(t *testing.T) {
	m, _, themePath, _ := newSettingsTestModel(t)
	if err := os.WriteFile(themePath, []byte("{\"preset\": \"gruvbox\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Mirror production order: newModel resolves the file marker first
	// and hands the winning preset to the settings model.
	_, resolved := LoadTheme("default", themePath)
	m.ui.settings.themePreset = resolved
	got := m.likedArtThemeColors()
	if got.Blue != "#FE8019" {
		t.Fatalf("art palette blue = %q, want gruvbox #FE8019", got.Blue)
	}
	if likedArtPaletteKey(got) == likedArtPaletteKey(resolveThemeColors("default", nil)) {
		t.Fatal("gruvbox palette key must differ from default")
	}
}
