package spotify

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestPlaylistEntryWireResolvedItemPrefersItem(t *testing.T) {
	entry := PlaylistEntryWire{
		Item:  &PlaylistItemWire{ID: "item-id"},
		Track: &PlaylistItemWire{ID: "track-id"},
	}
	got := entry.ResolvedItem()
	if got == nil || got.ID != "item-id" {
		t.Fatalf("expected item field to win, got %#v", got)
	}
}

func TestPlaylistEntryWireResolvedItemFallsBackToTrack(t *testing.T) {
	entry := PlaylistEntryWire{
		Track: &PlaylistItemWire{ID: "track-id"},
	}
	got := entry.ResolvedItem()
	if got == nil || got.ID != "track-id" {
		t.Fatalf("expected fallback to track field, got %#v", got)
	}
}

func TestPlaylistCountPrefersItemsWhenLarger(t *testing.T) {
	if got := PlaylistCount(21, 14); got != 21 {
		t.Fatalf("expected 21, got %d", got)
	}
	if got := PlaylistCount(0, 8); got != 8 {
		t.Fatalf("expected 8 fallback, got %d", got)
	}
}

func TestDecodeWebAPIJSONStatusErrorMapper(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       ioNopCloser("forbidden"),
	}
	err := DecodeWebAPIJSON(resp, http.StatusOK, &struct{}{}, func(status int, body string) error {
		if status != http.StatusForbidden || body != "forbidden" {
			t.Fatalf("unexpected mapper args: status=%d body=%q", status, body)
		}
		return errors.New("mapped")
	})
	if err == nil || err.Error() != "mapped" {
		t.Fatalf("expected mapped error, got %v", err)
	}
}

func ioNopCloser(s string) *readCloser {
	return &readCloser{Reader: strings.NewReader(s)}
}

type readCloser struct {
	*strings.Reader
}

func (r *readCloser) Close() error { return nil }
