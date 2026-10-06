package librespot

import (
	"context"
	"testing"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/tracks"
	"github.com/stretchr/testify/require"
)

func TestSingleTrackContextContainsOnlyRequestedTrack(t *testing.T) {
	ctx, err := singleTrackContext("spotify:track:4iV5W9uYEdYUVa79Axb7Rh")
	require.NoError(t, err)
	require.Equal(t, "spotify:track:4iV5W9uYEdYUVa79Axb7Rh", ctx.Uri)
	require.Len(t, ctx.Pages, 1)
	require.Len(t, ctx.Pages[0].Tracks, 1)
	require.Equal(t, ctx.Uri, ctx.Pages[0].Tracks[0].Uri)
	trackList, err := tracks.NewTrackListFromContext(context.Background(), &golibrespot.NullLogger{}, nil, ctx, 0)
	require.NoError(t, err)
	allTracks := trackList.AllTracks(context.Background())
	require.Len(t, allTracks, 1)
	require.Equal(t, ctx.Uri, allTracks[0].Uri)
}

func TestSingleTrackContextRejectsNonTrackURI(t *testing.T) {
	_, err := singleTrackContext("spotify:album:4iV5W9uYEdYUVa79Axb7Rh")
	require.Error(t, err)
}

func TestTrackListContextKeepsArtistURIAndOrder(t *testing.T) {
	ctx, err := trackListContext("spotify:artist:artist1", []string{
		"spotify:track:1111111111111111111111",
		"spotify:track:2222222222222222222222",
		"spotify:track:3333333333333333333333",
	})
	require.NoError(t, err)
	require.Equal(t, "spotify:artist:artist1", ctx.Uri)
	require.Len(t, ctx.Pages, 1)
	require.Len(t, ctx.Pages[0].Tracks, 3)
	require.Equal(t, "spotify:track:1111111111111111111111", ctx.Pages[0].Tracks[0].Uri)
	require.Equal(t, "spotify:track:3333333333333333333333", ctx.Pages[0].Tracks[2].Uri)
	trackList, err := tracks.NewTrackListFromContext(context.Background(), &golibrespot.NullLogger{}, nil, ctx, 0)
	require.NoError(t, err)
	allTracks := trackList.AllTracks(context.Background())
	require.Len(t, allTracks, 3)
}

func TestTrackListContextRejectsEmptyAndNonTrackURI(t *testing.T) {
	_, err := trackListContext("spotify:artist:artist1", nil)
	require.Error(t, err)
	_, err = trackListContext("spotify:artist:artist1", []string{"spotify:album:4iV5W9uYEdYUVa79Axb7Rh"})
	require.Error(t, err)
}

func TestTrackListContextFallsBackToFirstTrack(t *testing.T) {
	ctx, err := trackListContext("", []string{"spotify:track:1111111111111111111111"})
	require.NoError(t, err)
	require.Equal(t, "spotify:track:1111111111111111111111", ctx.Uri)
}

func TestHandleTUIPlaybackCommandDefault(t *testing.T) {
	p := &AppPlayer{}
	handled, err := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: 999})
	if handled {
		t.Fatal("expected unknown command to not be handled")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleTUIPlaybackCommandShuffleNilState(t *testing.T) {
	p := &AppPlayer{}
	handled, _ := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandShuffle})
	if !handled {
		t.Fatal("expected shuffle to be handled even with nil state")
	}
}

func TestHandleTUIPlaybackCommandRepeatNilState(t *testing.T) {
	p := &AppPlayer{}
	handled, _ := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandCycleRepeat})
	if !handled {
		t.Fatal("expected repeat to be handled even with nil state")
	}
}

func TestHandleTUIContextCommandDefault(t *testing.T) {
	p := &AppPlayer{}
	handled, err := p.handleTUIContextCommand(context.Background(), TUICommand{Kind: 999})
	if handled {
		t.Fatal("expected unknown context command to not be handled")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
