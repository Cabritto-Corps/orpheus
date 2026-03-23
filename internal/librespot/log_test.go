package librespot

import (
	"testing"
)

func TestStderrIfAuthMatchesKeyword(t *testing.T) {
	stderrIfAuth("complete authentication for user %s", "testuser")
	stderrIfAuth("visit the following link to authenticate")
}

func TestStderrIfAuthIgnoresNormalMessages(t *testing.T) {
	stderrIfAuth("playing track %s", "some-track")
	stderrIfAuth("volume updated to %d", 50)
}

func TestStderrIfAuthFormatsCorrectly(t *testing.T) {
	stderrIfAuth("%s: %s", "complete authentication", "user123")
	stderrIfAuth("visit the following link: %s", "http://example.com")
}
