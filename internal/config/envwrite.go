package config

import (
	"fmt"
	"os"
	"strings"
)

// UpsertEnvFile rewrites the target .env so that each key in values is set to
// its value, preserving every other line byte-for-byte. An existing line for
// a key is replaced in place; a missing key is appended at the end. Comments
// and blank lines never move. The write is atomic (temp file + rename).
func UpsertEnvFile(path string, values map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed reading env file: %w", err)
	}

	var out []string
	if err == nil {
		out = strings.Split(string(data), "\n")
	}

	remaining := make(map[string]string, len(values))
	for k, v := range values {
		remaining[k] = v
	}

	for i, line := range out {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key := trimmed
		if eq := strings.IndexByte(trimmed, '='); eq >= 0 {
			key = strings.TrimSpace(trimmed[:eq])
		}
		if v, ok := remaining[key]; ok {
			out[i] = key + "=" + v
			delete(remaining, key)
		}
	}

	for k, v := range values {
		if _, still := remaining[k]; still {
			out = append(out, k+"="+v)
		}
	}

	content := strings.Join(out, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed writing env file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed replacing env file: %w", err)
	}
	return nil
}
