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

var validKeyActions = func() map[string]struct{} {
	out := make(map[string]struct{}, len(actionRegistry))
	for _, meta := range actionRegistry {
		out[meta.action] = struct{}{}
	}
	return out
}()

// A missing keys.json is not an error; a malformed one warns and falls back to defaults.
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
	// Accept whatever dispatch matches.
	if _, _, _, ok := parseKeySpec(k); ok {
		return true
	}
	// uv exposes no validator and maps unknown bases to text echo, so probe the
	// base as pressed text: named bases never match, unknown ones do.
	return isExtendedUVKeyName(k)
}

// Membership is probed through uv itself, so the loader follows its vocabulary automatically.
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

func defaultKeysForAction(k keyMap, action string) ([]string, bool) {
	for _, meta := range actionRegistry {
		if meta.action == action {
			return meta.bind(k).Keys(), true
		}
	}
	return nil, false
}

// Values equal to defaults are omitted so the file only carries customizations.
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

func LoadKeys(path string) map[string][]string {
	overrides, err := LoadKeyOverrides(path)
	if err != nil {
		slog.Warn("failed reading keys file, using default bindings", "path", path, "error", err)
		return nil
	}
	return overrides
}
