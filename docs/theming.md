# Theming

Orpheus ships 11 themes and lets you change almost everything it draws: colors, backgrounds, glyphs, text weight and cover art.

Two ways to do it:

- in the app: press `o`, then **Theme options**
- editing `~/.config/orpheus/theme.json` yourself

The editor saves to the same file, so `theme.json` is always the source of truth. If you edit the file by hand, restart orpheus to see it.

## Picking a preset

1. Press `o` to open settings
2. Enter on **Theme**
3. Press up/down to preview themes live, enter to save, esc to go back without saving

The presets: `default`, `minimal`, `high_contrast`, `catppuccin`, `tokyo_night`, `gruvbox`, `nord`, `dracula`, `solarized_dark`, `rose_pine`, `kanagawa`

## Theme options

Press `o` → **Theme options**. Every row cycles with `+`/`-` (or enter) and previews live, so you can watch before you save:

- **Base palette** — the preset everything starts from. Changing it resets color changes but keeps your glyph, typography, cover and backgrounds choices
- **Page tone** — the color of the whole frame. `preset` uses the preset's own
- **Accent** — sets the accent and derives the light accent from it
- **Backgrounds** — `solid` paints the whole frame in one page-colored surface, `transparent` paints no frame background so your terminal's shows through
- **Border** — box style of modals and placeholder covers: `rounded`, `thick`, `double`, `ascii`
- **Queue cursor** — the marker on the selected up-next row: `>` (arrow), `♪` (note), `●` (dot), `▶` (play) or none (plain)
- **Play/pause** — the transport icons: `▶ ⏸`, `► ⏸`, `▷ ⏸` or `>` `||`
- **Spinner** — the loading animation style
- **Progress bar** — block (`█ ░`) or line (`━ ─`) characters
- **Titles bold** / **Descriptions italic** — text weight
- **Cover frame** — border around cover art. The art moves one cell in, the layout never changes
- **Save to theme.json** — writes what you changed
- **Reset to preset** — throws away your changes

`esc` reverts to the theme you opened the editor with.

## theme.json

Location: `~/.config/orpheus/theme.json`. You can move it with `orpheus_theme_file` in your `.env` and set the preset name directly with `orpheus_theme`. See [configuration files](config.md) for the whole `.env` story.

The file only stores what you changed from the preset. Everything you leave out keeps following the preset, so your file stays short and survives preset improvements.

```json
{
  "preset": "tokyo_night",
  "page": "#101014",
  "glyphs": { "border": "thick" }
}
```

### Colors

| Key | What it paints |
| --- | --- |
| `blue` | accent: progress bar, selection, markers |
| `blue_light` | bright accent (end of the progress gradient) |
| `off_white` | main text |
| `gray` | secondary text |
| `muted_blue` | hints, labels, artists |
| `dim_blue` | dimmed text and the active tab background |
| `divider` | the horizontal rules and cover placeholder border |
| `error` | error messages |
| `scrim` | the dimmed backdrop behind modals |
| `selection_fg` / `selection_bg` | selected row colors |
| `page` | the frame background |
| `panel` | modal boxes. Leave it out and it is derived from `page` |

Accepted values: hex (`#4A90D9` or `#F00`), ANSI names (`red` up to `bright_white`), or a number from 0 to 15.

### Glyphs

```json
"glyphs": {
  "border": "rounded",
  "queue_cursor": "note",
  "play_pause": "modern",
  "spinner": "minidot",
  "bar": "block"
}
```

- `border`: `rounded`, `thick`, `double`, `ascii`
- `queue_cursor`: `arrow`, `note`, `dot`, `play`, `plain` (none). Old theme files may still say `now_playing`; it is read as the cursor.
- `play_pause`: `modern`, `bold`, `thin`, `ascii`
- `spinner`: `minidot`, `dot`, `line`, `points`, `meter`, `pulse`
- `bar`: `block`, `line`

If your terminal uses a Nerd Font (`orpheus_nerd_fonts=true` in your `.env`), the play/pause icons come from the font and ignore this setting.

### Typography

```json
"typography": { "bold_titles": true, "italic_descriptions": true }
```

### Cover

```json
"cover": { "frame": "rounded" }
```

`none`, `rounded` or `thick`. `none` keeps covers bare like they have always been.

### Backgrounds

```json
"backgrounds": { "style": "solid" }
```

- `solid`: the whole frame is one uniform page surface — modal boxes included
- `transparent`: the frame paints no background at all and your terminal's own background shows through. Selection highlights and modal boxes keep their colors (a box is floating chrome), the modal backdrop keeps its pattern but stops painting behind it, and orpheus hands back the terminal background color it would otherwise set for itself while this mode is active

## Terminal background

Orpheus sets the terminal's own background color to the page color while it runs, so the padding around the grid becomes part of the theme instead of a frame of whatever color your terminal was. Terminals that report their background get it restored when you exit; terminals without support for it ignore the whole thing, and ANSI-only themes like `minimal` are left alone.

## Examples

Complete copy-paste files in [theme_examples](theme_examples.md).
