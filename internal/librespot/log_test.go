package librespot

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestLogrusAdapterSendsAuthURLWithoutLoggingIt(t *testing.T) {
	const link = "https://accounts.spotify.com/authorize?client_id=abc&state=secret"
	var output bytes.Buffer
	log := logrus.New()
	log.SetOutput(&output)
	var gotURL string
	adapter := LogrusAdapter{
		Log:            logrus.NewEntry(log),
		OnAuthRequired: func(url string) { gotURL = url },
	}

	adapter.Infof("to complete authentication visit the following link: %s", link)

	if gotURL != link {
		t.Fatalf("auth callback URL = %q, want %q", gotURL, link)
	}
	if strings.Contains(output.String(), link) || strings.Contains(output.String(), "secret") {
		t.Fatalf("one-time auth URL must not be written to logs: %q", output.String())
	}
	if !strings.Contains(output.String(), "Spotify authentication is required") {
		t.Fatalf("expected a sanitized auth log entry, got %q", output.String())
	}
}

func TestLogrusAdapterPropagatesAuthHandlerToChildLoggers(t *testing.T) {
	var output bytes.Buffer
	log := logrus.New()
	log.SetOutput(&output)
	var called bool
	adapter := LogrusAdapter{
		Log:            logrus.NewEntry(log),
		OnAuthRequired: func(string) { called = true },
	}

	child := adapter.WithField("component", "session").(LogrusAdapter)
	child.Infof("complete authentication")
	if !called {
		t.Fatal("auth handler was not propagated to child logger")
	}
}

func TestAuthURLFromMessageIgnoresOrdinaryLogs(t *testing.T) {
	if url, required := authURLFromMessage("playing track %s", "some-track"); required || url != "" {
		t.Fatalf("ordinary message was recognized as auth: url=%q required=%v", url, required)
	}
}
