package tui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	uv "github.com/charmbracelet/ultraviolet"
)

// validKeyActions lists the action names accepted in keys.json, derived from
// the shared registry.
var validKeyActions = func() map[string]struct{} {
	out := make(map[string]struct{}, len(actionRegistry))
	for _, meta := range actionRegistry {
		out[meta.action] = struct{}{}
	}
	return out
}()

// LoadKeyOverrides reads a keys.json file mapping action names to one or more
// key strings. A missing file is not an error: bindings stay at defaults.
// A malformed file warns and falls back to defaults entirely.
func LoadKeyOverrides(path string) (map[string][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("malformed keys file, using default bindings", "path", path, "error", err)
		return nil, nil
	}

	overrides := make(map[string][]string, len(raw))
	for action, rawKeys := range raw {
		_, ok := validKeyActions[action]
		if !ok {
			slog.Warn("unknown action in keys file, ignoring", "path", path, "action", action)
			continue
		}
		var keys []string
		switch v := rawKeys.(type) {
		case string:
			keys = []string{v}
		case []any:
			for _, k := range v {
				s, ok := k.(string)
				if !ok {
					slog.Warn("non-string key in keys file, ignoring entry", "path", path, "action", action)
					continue
				}
				keys = append(keys, s)
			}
		default:
			slog.Warn("unsupported keys value, ignoring action", "path", path, "action", action)
			continue
		}
		if len(keys) == 0 {
			slog.Warn("empty key list in keys file, keeping default binding", "path", path, "action", action)
			continue
		}

		overrides[action] = sanitizeKeys(action, path, keys)
	}
	return overrides, nil
}

func sanitizeKeys(action, path string, keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if len(k) > 1 {
			// A single space is a valid key name; trimming it would erase it.
			k = strings.TrimSpace(k)
		}
		if !isPlausibleKeyName(k) {
			slog.Warn("unrecognized key name in keys file, dropping entry", "path", path, "action", action, "key", k)
			continue
		}
		out = append(out, k)
	}
	return out
}

func isPlausibleKeyName(k string) bool {
	if k == "" {
		return false
	}
	// One shared parser decides what names a key: v2 identity plus the
	// legacy aliases it folds (" "/"space", "esc"/"escape",
	// "enter"/"return"), so loaders accept whatever dispatch matches.
	if _, _, _, ok := parseKeySpec(k); ok {
		return true
	}
	// Beyond the local aliases: the ultraviolet vocabulary (function,
	// keypad, media keys). uv exposes no validator, and its parser maps
	// an unknown multi-rune base to text echo — so probing the base AS
	// pressed text separates named bases (parse to a bare code, never
	// match the probe) from unknown ones (echo matches). Modifier
	// segments still validate against the shared vocabulary, and a
	// modifier-named base stays rejected (a bare modifier is no binding).
	return isExtendedUVKeyName(k)
}

// isExtendedUVKeyName accepts binding strings whose base ultraviolet
// names but the local parser doesn't. Membership is probed through uv
// itself, so the set can never drift from the matcher: if uv ever
// extends its vocabulary, the loader follows automatically.
func isExtendedUVKeyName(k string) bool {
	parts := strings.Split(k, "+")
	base := parts[len(parts)-1]
	if base == "" || isKeyModName(base) {
		return false
	}
	for _, p := range parts[:len(parts)-1] {
		if !isKeyModName(p) {
			return false
		}
	}
	return !uv.Key{Text: base}.MatchString(base)
}

func applyKeyOverrides(m keyMap, overrides map[string][]string) keyMap {
	for action, keys := range overrides {
		if len(keys) == 0 {
			continue
		}
		// The registry decides which actions are rebindable: an unknown
		// action matches nothing and is ignored, like the old switch's
		// implicit default.
		for i := range actionRegistry {
			if actionRegistry[i].action == action {
				actionRegistry[i].set(&m, keys)
			}
		}
	}
	return m
}

func overrideBinding(b key.Binding, keys []string) key.Binding {
	return key.NewBinding(
		key.WithKeys(keys...),
		key.WithHelp(shortKeyLabel(keys), b.Help().Desc),
	)
}

func shortKeyLabel(keys []string) string {
	first := keys[0]
	switch first {
	case " ":
		return "space"
	case "left":
		return "←"
	case "right":
		return "→"
	default:
		return first
	}
}

func newKeysFromConfig(overrides map[string][]string) keyMap {
	return applyKeyOverrides(newKeys(), overrides)
}

// defaultKeysForAction resolves an action name to its live key strings via
// the shared registry.
func defaultKeysForAction(k keyMap, action string) ([]string, bool) {
	for _, meta := range actionRegistry {
		if meta.action == action {
			return meta.bind(k).Keys(), true
		}
	}
	return nil, false
}

// SaveKeys writes the full override map back to keys.json. Values that equal
// the default bindings are omitted so the file only carries actual user
// customizations.
func SaveKeys(path string, overrides map[string][]string) error {
	if path == "" {
		return fmt.Errorf("no keys file path configured")
	}
	defaults := newKeys()
	out := make(map[string][]string, len(overrides))
	for action, keys := range overrides {
		if len(keys) == 0 {
			continue
		}
		if def, ok := defaultKeysForAction(defaults, action); ok && slices.Equal(def, keys) {
			continue
		}
		out[action] = keys
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadKeys reads the user's keys.json (or defaults when absent/malformed).
func LoadKeys(path string) map[string][]string {
	overrides, err := LoadKeyOverrides(path)
	if err != nil {
		slog.Warn("failed reading keys file, using default bindings", "path", path, "error", err)
		return nil
	}
	return overrides
}
