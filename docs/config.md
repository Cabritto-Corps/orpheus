# Configuration files

Orpheus keeps its configuration in one directory: a `.env` for the settings and a few json files that the app writes for you. Everything can be moved, and everything falls back to sane defaults when missing.

## Where files live

The config directory is `~/.config/orpheus` on Linux. Change it with the `ORPHEUS_CONFIG_DIR` variable (in your shell, not in the `.env`).

| File | What it is | Who creates it |
| --- | --- | --- |
| `.env` | your settings | you |
| `theme.json` | your theme | the theme picker / you ([theming](theming.md)) |
| `keys.json` | your keybinds | written when you rebind a key in settings |
| `token.json` | Spotify Web API login | `orpheus auth login` |
| `state.json` | playback session login | the first time the player connects |
| `orpheus.log` | logs | orpheus, every run |

Deleting one of them just resets that part:

- delete `state.json` → the next run asks you to link the player again (browser popup)
- delete `token.json` → run `orpheus auth login` again for library browsing
- delete `theme.json` / `keys.json` → back to the default theme and default keys

## The `.env` loading order

Only one `.env` is read, in this order:

1. the process environment — what your shell already exports always wins
2. `.env` in the directory you launched orpheus from
3. `.env` in the config directory (`~/.config/orpheus/.env`)

So for a globally installed `orpheus`, put your `.env` in the config directory and it works from anywhere. If a `.env` exists in the directory you launch from, that one wins — handy for testing.

When the settings write something (crossfade, audio cache), the values go to the same file the next start will read: the `.env` in the current directory when it exists, otherwise the config directory one — created on the first save if it does not exist yet.

## Every `.env` variable

### Spotify app

| Variable | Default | What it does |
| --- | --- | --- |
| `SPOTIFY_CLIENT_ID` | *(required)* | your app's client id from the [developer dashboard](https://developer.spotify.com/dashboard/) |
| `spotify_redirect_uri` | `http://127.0.0.1:8989/callback` | must match your dashboard redirect |
| `spotify_scopes` | the set orpheus needs | comma separated scope list |
| `spotify_device_name` | `orpheus` | how the player shows up in Spotify's device picker |

### Player

| Variable | Default | What it does |
| --- | --- | --- |
| `orpheus_crossfade` | `false` | crossfade between tracks (applies on restart) |
| `orpheus_crossfade_seconds` | `3` | crossfade length, 0 to 30 |
| `orpheus_audio_cache_enabled` | `false` | on-disk encrypted audio cache (applies on restart) |
| `orpheus_audio_cache_size_mb` | `1024` | cache size cap, 64 to 4096 |
| `orpheus_audio_cache_dir` | *config dir* | where the cache lives |
| `orpheus_poll_interval` | `1500ms` | how often the fallback polling checks Spotify |
| `orpheus_device_resolution_mode` | `strict` | `strict` or `relaxed` device matching |
| `orpheus_allow_active_fallback` | `false` | fall back to Spotify's active device when ours is missing |
| `orpheus_on_song_change` | *(empty)* | command to run when the song changes |

### Look and feel

| Variable | Default | What it does |
| --- | --- | --- |
| `orpheus_theme` | `default` | preset name ([theming](theming.md)) |
| `orpheus_theme_file` | *config dir*`/theme.json` | where the theme lives |
| `orpheus_keys_file` | *config dir*`/keys.json` | where the keybinds live |
| `orpheus_nerd_fonts` | `auto` | `true`/`false`/`auto` — auto-detects a Nerd Font via fontconfig |
| `ORPHEUS_IMAGE_PROTOCOL` | *(auto)* | force image protocol: `kitty`, `ansi` or `none` |

### Files

| Variable | Default | What it does |
| --- | --- | --- |
| `orpheus_token_path` | *config dir*`/token.json` | web api token location |
| `orpheus_theme_file` | *config dir*`/theme.json` | theme file location |
| `orpheus_keys_file` | *config dir*`/keys.json` | keys file location |
| `orpheus_log_file` | *config dir*`/orpheus.log` | app log location (`ORPHEUS_LOG_FILE` overrides it) |

## When values are wrong

A malformed value (say `orpheus_crossfade_seconds=abc`) falls back to the default and shows up as a `⚠` warning row in the settings modal, so nothing silently misconfigures itself. The log has the details.

## See also

- [tutorial](../tutorial.md) — first time setup
- [theming](theming.md) — themes and the options editor
