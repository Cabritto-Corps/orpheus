package tui

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

// validKeyActions lists the action names accepted in keys.json. They are
// stable snake_case identifiers over the keyMap fields.
var validKeyActions = map[string]struct{}{
	"tab": {}, "play_pause": {}, "next": {}, "prev": {}, "shuffle": {},
	"loop": {}, "vol_up": {}, "vol_down": {}, "seek_back": {}, "seek_fwd": {},
	"refresh": {}, "filter": {}, "toggle_help": {}, "select": {}, "close_modal": {},
	"quit": {}, "queue_up": {}, "queue_down": {}, "queue_jump": {},
	"queue_remove": {}, "queue_move_up": {}, "queue_move_down": {},
}

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
		case []interface{}:
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
	runes := []rune(k)
	if len(runes) == 1 {
		return true
	}
	switch k {
	case " ", "space", "tab", "esc", "enter", "return", "up", "down", "left", "right",
		"home", "end", "pgup", "pgdown", "delete", "backspace", "insert", "ctrl+c",
		"shift+tab", "ctrl+j", "ctrl+k", "ctrl+d", "ctrl+u", "ctrl+a", "ctrl+e":
		return true
	}
	if strings.HasPrefix(k, "ctrl+") || strings.HasPrefix(k, "alt+") {
		suffix := strings.TrimPrefix(strings.TrimPrefix(k, "ctrl+"), "alt+")
		if suffix == "" || len([]rune(suffix)) > 3 {
			return false
		}
		return true
	}
	return false
}

func applyKeyOverrides(m keyMap, overrides map[string][]string) keyMap {
	for action, keys := range overrides {
		if len(keys) == 0 {
			continue
		}
		switch action {
		case "tab":
			m.Tab = overrideBinding(m.Tab, keys)
		case "play_pause":
			m.PlayPause = overrideBinding(m.PlayPause, keys)
		case "next":
			m.Next = overrideBinding(m.Next, keys)
		case "prev":
			m.Prev = overrideBinding(m.Prev, keys)
		case "shuffle":
			m.Shuffle = overrideBinding(m.Shuffle, keys)
		case "loop":
			m.Loop = overrideBinding(m.Loop, keys)
		case "vol_up":
			m.VolUp = overrideBinding(m.VolUp, keys)
		case "vol_down":
			m.VolDown = overrideBinding(m.VolDown, keys)
		case "seek_back":
			m.SeekBack = overrideBinding(m.SeekBack, keys)
		case "seek_fwd":
			m.SeekFwd = overrideBinding(m.SeekFwd, keys)
		case "refresh":
			m.Refresh = overrideBinding(m.Refresh, keys)
		case "filter":
			m.Filter = overrideBinding(m.Filter, keys)
		case "toggle_help":
			m.ToggleHelp = overrideBinding(m.ToggleHelp, keys)
		case "select":
			m.Select = overrideBinding(m.Select, keys)
		case "close_modal":
			m.CloseModal = overrideBinding(m.CloseModal, keys)
		case "quit":
			if !keysContainList(keys, "ctrl+c") {
				keys = append(append([]string{}, keys...), "ctrl+c")
			}
			m.Quit = overrideBinding(m.Quit, keys)
		case "queue_up":
			m.QueueUp = overrideBinding(m.QueueUp, keys)
		case "queue_down":
			m.QueueDown = overrideBinding(m.QueueDown, keys)
		case "queue_jump":
			m.QueueJump = overrideBinding(m.QueueJump, keys)
		case "queue_remove":
			m.QueueRemove = overrideBinding(m.QueueRemove, keys)
		case "queue_move_up":
			m.QueueMoveUp = overrideBinding(m.QueueMoveUp, keys)
		case "queue_move_down":
			m.QueueMoveDown = overrideBinding(m.QueueMoveDown, keys)
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

func keysContainList(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func newKeysFromConfig(overrides map[string][]string) keyMap {
	return applyKeyOverrides(newKeys(), overrides)
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

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
