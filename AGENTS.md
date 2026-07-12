# AGENTS.md

Compact guide for OpenCode sessions working in this repo.

## Project

Orpheus is a terminal Spotify TUI player built on a fork of `go-librespot` (`github.com/elxgy/go-librespot`, forked from `devgianlu/go-librespot`). Go 1.25.6. Requires Spotify Premium.

The fork lives at `/home/pengusz/repos/go-librespot` (separate repo). For local dev, add a `replace` directive in `go.mod`: `replace github.com/elxgy/go-librespot => /home/pengusz/repos/go-librespot`. Remove it and bump the version after publishing.

Overhaul history lives in `docs/overhaul/roadmap.md` (phases 1-4 done). The error contract is documented in `docs/overhaul/error-taxonomy.md`.

## Commands

```bash
make build        # go build -o orpheus ./cmd/orpheus
make test         # go test ./...
make test-race    # go test -race ./...   (CI runs this, not plain test)
make vet          # go vet ./...
make fix          # go fix ./...          (apply API deprecation fixes)
make fmt          # go fmt ./...          (format)
make lint         # golangci-lint run     (uses .golangci.yml; CI uses v2.11.3)
make check        # fix -> test -> vet -> lint  (run before pushing)
```

**Pre-push procedure**: Always run `make check` before pushing. This applies fixes, runs tests with race detector, vets, and lints.

Run a single package: `go test -race ./internal/librespot/...`
Run a single test: `go test -race ./internal/librespot -run TestStreamCloseOperation`
Run fork's `//go:build test_unit` gated tests: `go test -race -tags test_unit ./tracks/...`

## System dependencies (cgo)

Build fails without native audio codec dev headers:
- Linux: `libasound2-dev libflac-dev libogg-dev libvorbis-dev`
- macOS: `libogg libvorbis flac`

## Architecture Overview

Orpheus has two layers: the **TUI** (bubbletea, `internal/tui`) and the **player backend** (`internal/librespot`, wrapping the fork). They communicate via two channels wired in `cmd/orpheus/main.go:315-357`:

| Channel | Direction | Purpose |
|---|---|---|
| `tuiCmdCh` (cap 8) | TUI -> AppPlayer | `librespot.TUICommand` (play/pause/seek/skip/shuffle/repeat/play-context) |
| `playbackStateCh` (cap 32) | AppPlayer -> TUI | `*librespot.PlaybackStateUpdate` (push-based state: track, position, queue, options) |

Both types are defined in `internal/librespot/bridge.go:3-52` — this is the bridge contract.

`AppPlayer.Run` (`internal/librespot/appplayer.go:445`) is a **single goroutine select loop** — all player state mutation happens on this one goroutine. It multiplexes: TUI commands, dealer requests (remote Spotify Connect commands), dealer messages, AP packets, player events, prefetch timers, volume updates, and an end-transition guard ticker.

## Spotify Protocol Layers

The fork talks to Spotify through five distinct services, all bootstrapped by `apresolve.spotify.com` (returns hostnames for AP/dealer/spclient, cached 1h):

```
apresolve.spotify.com
    |
    +-- AccessPoint (AP): raw TCP + Shannon cipher encryption
    |       carries Mercury (pub/sub RPC) and AudioKey (AES key requests) as muxed channels
    |
    +-- Dealer: WebSocket (wss://), push channel for Spotify Connect commands
    |       remote play/pause/skip/transfer/queue from phone/desktop arrive here
    |
    +-- Spclient: stateless HTTPS, Bearer-auth (login5 token)
            metadata, storage-resolve (CDN URLs), PlayPlay keys, connect-state PUTs,
            context-resolve (playlists/albums/autoplay), Web API passthrough
```

- **AP** (`ap/ap.go`): encrypted TCP, auto-reconnecting. `recvLoop` dispatches packets to registered channels. `pongAckTicker` (120s) detects dead connections. Reconnect re-runs full DH handshake + auth using stored credentials. Receives the session's `baseCtx` from the constructor; I/O in `recvLoop` uses it, but blocking reads are still interrupted via `conn.Close()` (the context is hygiene, not the cancellation mechanism).
- **Dealer** (`dealer/dealer.go`): WebSocket, auto-reconnecting. `recvLoop` handles `message` (one-way push) and `request` (requires reply). `pingTicker` (30s) detects dead connections. Connect commands arrive as requests on `hm://connect-state/v1/player/command`. Also receives `baseCtx` from the constructor.
- **Mercury** (`mercury/client.go`): request/response tunneled over AP packets. 15s timeout per request. Used for older APIs (event reporting, some playlist access). No own reconnect — depends on AP. Receives `baseCtx` from the constructor.
- **Spclient** (`spclient/spclient.go`): stateless HTTPS. Refreshes login5 token on 401. Used for everything metadata/storage/connect-state. Does not receive `baseCtx`; per-request contexts only.
- **Login5** (`login5/login5.go`): mints Bearer tokens for dealer + spclient. Solves Hashcash challenges. `AccessToken()` refreshes by re-running login with stored credentials when expired.

The fork's `Session` (`session/session.go:46-222`) owns `baseCtx, baseCancel := context.WithCancel(context.Background())` (decoupled from the bootstrap ctx, which has a login-time deadline). All four fork components (AP, Dealer, Mercury, AudioKey) receive `baseCtx` via constructor. `Session.Close()` calls `baseCancel()` first so in-flight I/O observes shutdown.

## Authentication: Two Separate Systems

Orpheus has **two independent auth tokens**, serving different purposes:

**1. Librespot session auth** (fork's `sessionconfig` package):
- First run: interactive OAuth2 via go-librespot's own callback server (port 8080). AP returns `ReusableAuthCredentials` which persist to `AppState`.
- Subsequent runs: `StoredCredentials` -> `ap.ConnectStored`, no browser needed.
- Used for: playback (NewStream, audio keys, connect-state, dealer commands).
- Orpheus entry: `cmd/orpheus/main.go:302` calls `sessionconfig.NewSessionFromConfigDir`. The previous `internal/librespot/session.go` was deleted in the overhaul (Phase 3d).

**2. Orpheus PKCE OAuth for Web API** (`internal/auth/`):
- `orpheus auth login` (`main.go:41-65`): PKCE flow with browser, redirect URI `http://127.0.0.1:8989/callback`. Token stored at `~/.config/orpheus/token.json` (`auth/token_store.go`).
- Auto-refreshed via `NotifyingTokenSource` (`auth/notify.go`) which re-saves on change.
- Used for: library browsing (playlists/albums/liked songs via `spotify.Service` or `playlistCatalog`).
- If no PKCE token exists, falls back to `librespot.NewPlaylistCatalog(sess)` which proxies Web API through the spclient (`webapi.go:18`).

## Track Loading & Playback Pipeline

End-to-end flow when user presses play on a playlist:

1. **TUI** sends `TUICommandPlayContext` on `tuiCmdCh` (`app_keys.go:363`). `beginTransportTransition()` fires the TUI's transport FSM (`playback_state.go:104`).
2. **AppPlayer** `handleTUIContextCommand` (`command_router.go:15`) resolves context via `sp.ContextResolve` -> `loadContext` (`controls.go:429`).
3. `loadContext` builds `tracks.List` from context, applies shuffle, syncs state -> `loadCurrentTrack` (`controls.go:499`).
4. `loadCurrentTrack`: checks secondary stream / transition cache for a ready stream. If none, calls `player.NewStream` (`controls.go:555`).
5. **`NewStream`** (fork `player/player.go:643`):
   - Extended metadata request (track info + audio files) via spclient
   - `selectBestMediaFormat` picks file (FLAC if enabled, else closest bitrate; MP3/AAC skipped per Phase 4 fixes)
   - Parallel: fetch audio key (AP audio-key channel) + storage resolve (CDN URL) via spclient
   - `HttpChunkedReader` (`audio/chunked-reader.go`): HTTP Range requests to Spotify CDN, 1MB chunks, 3-chunk prefetch
   - `AesAudioDecryptor` (`audio/decryptor.go`): AES-CTR decryption wrapping the chunked reader
   - Vorbis or FLAC cgo decoder (`vorbis/decoder.go` / `flac/decoder.go`): decodes to float32 PCM
   - Returns `Stream{Source: decoder, closers: [decryptedStream, rawStream]}`
6. `player.SetPrimaryStream` (`controls.go:561`) -> `manageLoop` (fork `player.go:235`) lazily creates output device (PulseAudio), sets it as primary on `SwitchingAudioSource`, resumes -> emits `EventTypePlay`.
7. **`SwitchingAudioSource.Read`** (fork `source.go:45`): output device reads float32 samples. On EOF: if secondary exists, closes old primary, flips to secondary (gapless). If not, signals `done` -> `EventTypeNotPlaying`.
8. **State sync**: `updateState` -> `putConnectState` (`state.go:62`) PUTs to Spotify Connect. `emitPlaybackState` (`appplayer.go:425`) pushes `PlaybackStateUpdate` to TUI.
9. **Prefetch**: `schedulePrefetchNext` (`controls.go:211`) arms timer ~30s before track end -> `runPrefetchWorker` (`controls.go:235`, background goroutine) loads next track's `Stream` -> `handlePrefetchResult` promotes to `secondaryStream`.

## Stream Cache & Prefetching

Three tiers of ready-to-play streams (`stream_cache.go` + `controls.go:120-301`):

1. **`primaryStream`** — currently playing.
2. **`secondaryStream`** — next track, staged in `SwitchingAudioSource` for gapless transition. At most one.
3. **`transitionCache`** — `*transitionCache` struct (`stream_cache.go:16`): map `uri -> *Stream`, capped at 16 (`transitionStreamCacheMax`, `stream_cache.go:22`). FIFO eviction. Private mutex; methods: `Has`, `Take`, `Put` (evicts at cap), `Clear` (close all + reset pending), `HasPending`, `MarkPending`, `ClearPending`, `ResetPending`.

`prefetchGen` (`atomic.Uint64` on `AppPlayer`) guards against stale prefetch results — stale workers (from a previous context/shuffle) close and drop their streams.

Invalidation: `bumpPrefetchGeneration` (`stream_cache.go:146`) on context load, shuffle toggle, or skip — clears pending set and all caches via `resetPlaybackCaches` (`controls.go:78`).

## State Management

**Player state** (`state.go:13`): holds `device *connectpb.DeviceInfo` + `player *connectpb.PlayerState` (protobuf with current track, position, IsPlaying/IsPaused/IsBuffering, Options{shuffle/repeat}, PrevTracks/NextTracks, context uri).

**Position** is derived, not stored continuously: `currentPositionMs` (`controls.go:109`) computes `PositionAsOfTimestamp + (now - Timestamp)` when playing. Timestamp rebased on transport state changes / seeks.

**Queue** lives inside the fork's `tracks.List` (`tracks/tracks.go:14`): `tracks` in context order, `playbackOrder []int` maps playback position -> context index (shuffled or identity), `queue []*ContextTrack` for manually-added "up next" items. Shuffle uses `math/rand/v2` with `rand.NewPCG(seed, seed)`.

**Connect state sync**: `putConnectState` (`state.go:68`) PUTs to `/connect-state/v1/devices/<deviceId>` with `X-Spotify-Connection-Id` header (obtained from dealer's `pusher/v1/connections/` message, `appplayer.go:182`). This makes orpheus visible in Spotify's device picker. Remote commands from other clients arrive as dealer requests -> `handlePlayerCommand` (`appplayer.go:226`).

## TUI Architecture

**Bubbletea model** (`internal/tui/app.go`): three tabs (playlists/albums/player). 200ms tick loop (`tickCmd` in `cmds.go:328`) drives: progress interpolation, input executor pump, cover refresh scheduling. In librespot mode, **no polling** — state is push-based via `playbackStateCh`.

**Model split** (`internal/tui/models.go`): top-level `model` (~10 fields) groups three sub-models:
- `transportModel` (28 fields): playback status, queue, input queue, debounce, transport transition FSM, cover epoch, playback error.
- `browseModel` (19 fields): playlist/album browsing, active playlist, preloaded IDs, track cache, retry counts.
- `uiModel` (32 fields): tabs, popups, layout, polling, image caches, width/height.

**File decomposition**:
- `app.go` (253 LOC): types, init, Run. `app_msg.go` (678 LOC): message dispatch. `app_keys.go` (389 LOC): key handlers.
- `view.go` (211 LOC): layout. `view_chrome.go` (317 LOC): header/footer/help/kitty. `view_panels.go` (348 LOC): content panels.
- `cmds.go` (355 LOC): types, listeners, poll/action, tick. `cmds_io.go` (438 LOC): I/O cmds.
- `image.go` (716 LOC): cover cache + protocols. `cover_manager.go`: cover queue.

**Command flow** (`input_pipeline.go`): key presses enqueue `playbackInputKind` (priority: next/prev=critical, play/pause=high, shuffle/loop=normal, vol/seek=low). `pumpInputExecutor` runs up to 8 actions/tick when idle. Volume/seek are debounced (50ms). Context-play commands (selecting a playlist) bypass the queue and write directly to `tuiCmdCh`. Queue predecessors dropped via `dropQueuedByPredicate(isVolumeAction)` / `dropQueuedByPredicate(isSeekAction)` (unlambda'd in Phase 4 cleanup).

**Transport transition FSM** (`internal/tui/transport_transition.go`, 99 LOC + 13 tests): consolidated 5 scattered fields into one `transportTransition` struct with named states (`transportIdle`, `transportAwaitingTrack`) and event enum (`transportEventTrackChanged`, `transportEventTrackPlaying`, `transportEventStuck`). Methods: `Begin`, `MaybeClear` (returns event), `Clear`, `ConsumeRecovery`, `Pending`, `RecoveryPending`, `StuckCount`, `FromTrack`, `StartedAt`. Constants `transportTransitionStuckTimeout` (4s) + `transportTransitionProgressMaxMS` (2000) co-located. Callers in `playback_state.go`, `input_pipeline.go`, `update_handlers.go`, `app_msg.go`.

**State flow** (`playback_state.go`): `handlePlaybackStateMsg` (`app_msg.go:165`) applies pushed updates with settle windows (volume 3s, seek 1.2s) to prevent UI flicker. Progress is interpolated locally every 200ms tick, resynced from pushes when delta > 300ms. Track changes fire `orpheus_on_song_change` hook.

**Data fetching**: library loaded via `loadPlaylistsCmd` (`cmds_io.go:22`) — parallel pagination of playlists (50/page) + albums. Liked songs is a synthetic pseudo-playlist (`URI="spotify:collection"`) with procedurally generated cover art (`liked_songs_art.go`). Playlist tracks loaded on selection via `TUICommandGetContextTracks` (returns on `ResultCh`). Album art fetched via HTTP, rendered with kitty graphics protocol or half-block ANSI (`image.go`), cached in LRU(256 images / 512 covers). `cover()` does cache lookup + dispatch; `renderAndCache()` does render+cache+cleanup (single lock take, no defer-in-loop).

## Error Handling

Canonical error taxonomy: `docs/overhaul/error-taxonomy.md`. Summary:

- **Fork playback recoverable** (`golibrespot.ErrMediaRestricted`, `ErrNoSupportedFormats`): auto-skip in `controls.go:487` (load context) and `controls.go:892` (`advanceNext`, 10-attempt cap). Never surfaces to TUI.
- **Fork player lifecycle** (`player.ErrPlayerClosed`): non-recoverable; logged.
- **Web API** (`spotify.ErrDeviceNotFound`, `ErrNoActiveTrack`, `ErrNoPlaybackContext` + HTTP 429/403/404/5xx): classified by `spotify.DiagnoseError` (`service.go:175`), surfaced in TUI via `transport.playbackErr`.
- **Network/context**: `DeadlineExceeded` (command timeouts — see `internal/librespot/timeouts.go`), `Canceled` (shutdown).

Invariants: fork errors are wrapped (`%w`), not re-sentinelled. `transport.playbackErr` is the TUI's single source of error truth — cleared on every successful state update and every key press that initiates a new context.

## Breaking Points & Gotchas

### cgo decoder lifecycle (critical)

`closeStream` (`internal/librespot/controls.go:71`) calls `s.Close()` (fork `player/stream.go:32`) which closes the vorbis/flac decoder (cgo) + underlying HTTP connections via `closers`. **Order matters**: the output device must stop reading from a decoder before it's closed, or you get use-after-free in cgo vorbis/flac state. See `crashes/double-free/crash.log`.

The fork's `manageLoop` exit (`player.go:371-376`) closes output BEFORE source precisely to prevent this. Any orpheus code that closes a stream outside the manage loop (e.g. `clearTransitionStreamCache`, `resetPlaybackCaches`) must ensure no reader is active on that stream.

### Stream.Close() and connection leaks

`Stream.Close()` (fork `player/stream.go:32`) closes the decoder `Source` + all `closers` (AES decryptor + HTTP chunked reader). Tested by `player/stream_test.go` (4 tests: Close invokes Source + closers, joins errors, idempotent, nil-safe). If closers are not closed, every played track leaks an HTTP connection to Spotify CDN -> connection pool exhaustion -> `NewStream` hangs -> playback freezes. This was the root cause of the extended-listening freeze bug.

### AP/Dealer reconnect goroutine leaks

When AP or dealer reconnect, `connect()` creates new stop channels, replacing old ones. Old goroutines (`recvLoop`, `pongAckTicker`/`pingTicker`) listen on old channels that are no longer referenced -> goroutines never stop -> leak per reconnect. The fork now saves old stop channels before reconnect and signals them after successful reconnect.

Phase 4a added `baseCtx` to AP/Dealer/Mercury/AudioKey. `Session.Close()` calls `baseCancel()` first. This does NOT replace the stop-channel sync (those remain for `recvLoopOnce` coordination); it gives in-flight I/O a cancellation signal.

### Mercury has no reconnect of its own

Mercury (`mercury/client.go`) depends entirely on AP reconnecting. If AP drops and reconnects, mercury's `ap.Receive(...)` channel survives (registrations are on the `Accesspoint` struct, not the connection). But in-flight mercury requests lose their `seq` mapping on reconnect -> 15s timeout -> `context.DeadlineExceeded`. No automatic retry.

### HttpChunkedReader.Close() is synchronous

`Close()` (fork `audio/chunked-reader.go:395`) cancels context and **blocks on `prefetchWg.Wait()`** — waits for all prefetch goroutines to finish. Closing a chunked reader while prefetch is in progress will block.

### Session.Close() ordering

Fork `session/session.go:225`: calls `baseCancel()` first, then closes AP (stops transport that mercury/audioKey depend on), then events, audioKey, mercury, dealer. Reversing this risks mercury/audioKey `Close()` hanging (their recv loops may already be dead from AP drop).

### Dealer requestReceivers panic on duplicate

`ReceiveRequest(uri)` (fork `dealer/recv.go:244`) panics if a receiver for the URI already exists. After a permanent dealer stop, re-registering requires a new `Dealer` instance — the map is not cleared on reconnect, only on permanent stop.

### Single-goroutine player state

All `AppPlayer` state mutation happens on the `Run` goroutine. Never call `loadCurrentTrack`, `updateState`, `skipNext`, etc. from another goroutine. TUI commands arrive on `tuiCmdCh` and are dispatched inside `Run`. The only exception is `runPrefetchWorker` (background goroutine); it returns results via channels consumed by `Run`.

### Non-blocking channel sends

All `tuiCmdCh` sends from the TUI use `select`/`default` (non-blocking) so a blocked AppPlayer never freezes the UI. Dropped actions are requeued (max 3 retries). `playbackStateCh` sends from AppPlayer are also non-blocking (counted as dropped if full).

## go-librespot fork: local dev gotcha

When modifying the fork: changes to `go-librespot` `Close()` signatures or vorbis/flac decoder lifecycle directly affect orpheus's `closeStream` (`internal/librespot/controls.go:71`), which calls `s.Close()` which now type-asserts `io.Closer` on the decoder (the fork's `AudioSource` interface embeds `io.Closer` as of Phase 1c). A decoder that previously leaked (didn't satisfy the interface) will start being closed after such a change, which can surface use-after-free bugs in cgo vorbis state cleanup. See `crashes/double-free/` for prior examples.

After fork changes: build + test the fork (`go build ./... && go test -race ./...`), commit, push, then update orpheus `go.mod` with `go get github.com/elxgy/go-librespot@<hash> && go mod tidy`. Remove any `replace` directive first. `go mod tidy` strips the replace directive — re-add it if you want to keep testing locally without bumping.

Fork runtime requirements: Go 1.25+ (uses `math/rand/v2`, stdlib `slices`/`maps`, `errors.Join`). `golang.org/x/exp/*` no longer imported directly.

## Entry points

- `cmd/orpheus/main.go` — subcommands:
  - `orpheus` (no arg / `librespot`) — run the TUI (default)
  - `orpheus auth login` — Spotify PKCE OAuth via browser
  - `orpheus check` — verify config + token
- `internal/librespot` — core player: `AppPlayer` wraps go-librespot session/player; `controls.go` (track loading, transitions, stream cache), `stream_cache.go` (`transitionCache` struct, prefetch generation), `state.go`/`queue_state.go` (player state), `appplayer.go` (lifecycle + Run loop), `command_router.go` (TUI command dispatch), `webapi.go` (Spotify Web API bridge via spclient), `bridge.go` (TUICommand/PlaybackStateUpdate types), `state_adapter.go` (builds PlaybackStateUpdate for TUI), `timeouts.go` (named timeout constants), `api_types.go` (orpheus-specific `ApiEvent`/`ApiResponse` types for the daemon event API — error sentinels deleted in Phase 4 cleanup), `config.go` (uses fork's `sessionconfig.ParseDeviceType`)
- `internal/tui` — bubbletea TUI (`app.go` is the model; `models.go` sub-model types; `app_msg.go` message dispatch; `app_keys.go` key handlers; `view.go`/`view_chrome.go`/`view_panels.go` rendering; `cmds.go`/`cmds_io.go` tea.Cmds + channel listeners; `input_pipeline.go` action queue/executor; `playback_state.go` settle/interpolation; `transport_transition.go` transport FSM; `image.go` image cache + protocols; `cover_manager.go` cover queue)
- `internal/auth` — PKCE OAuth flow + file token store + notifying token source
- `internal/config` — env-based config loaded from `.env` (cwd first, falls back to `<configDir>/.env`, see `loadEnvFile`); config dir resolution via `DefaultConfigDir` (`ORPHEUS_CONFIG_DIR` or `UserConfigDir/orpheus`)
- `internal/spotify` — Web API client (`service.go`, `service_playback.go` transport, `service_catalog.go` browsing, `service_errors.go` `DiagnoseError`, `timeutil.go` `sleepWithContext`)
- `internal/playbackdomain` — option resolution helpers (shuffle/repeat traversal)
- `internal/cache` — LRU + TTL caches (for images and track metadata)
- `internal/loader` — background loader pool (128 workers) for image fetching

## Config

`.env` file (gitignored). Lookup order: cwd first, falls back to `<configDir>/.env` (so a globally-installed orpheus works from any directory — put `.env` at `~/.config/orpheus/.env`). Required: `SPOTIFY_CLIENT_ID`. See `tutorial.md` for Spotify developer dashboard setup (redirect URI `http://127.0.0.1:8989/callback`). Other vars prefixed `orpheus_*` — see `internal/config/config.go:36-47`.

## Testing

- Tests are self-contained: mocks only, no Spotify credentials or network needed.
- Unit tests live alongside code (`*_test.go`); cross-package integration tests in `tests/`.
- `internal/librespot` has the most tests (stream cache, controls, queue state, command router, state adapter).
- `internal/tui/transport_transition_test.go`: 13 tests for the transport FSM.
- Fork `player/stream_test.go`: 4 tests for `Stream.Close` invariant.
- Fork `tracks/paged-list_internal_test.go` (gated `//go:build test_unit`): paged list shuffle/unshuffle with `math/rand/v2` PCG seeds.

## Code style

- DO NOT add comments unless asked. Write code that documents itself.
- Comment only on unusual implementations or confusing code. Explain WHY it's done a certain way, not HOW.

## Commit conventions (enforced on PR)

Format: `<type>: <description>` or `<type>(scope): <description>`
Allowed types: `feat | fix | hotfix | release | refactor | perf | chore | docs | test`
Enforced by `.github/workflows/commitlint.yml` on all PR commits to `main`. Example: `fix(queue): prevent duplicate tracks after skip`.

## CI

`.github/workflows/build_test.yml` (push/PR to main): `go vet` -> `go test -race` -> coverage -> build; separate `golangci-lint` job. Release workflow triggers on `v*` tags, builds linux-amd64 + darwin-arm64.