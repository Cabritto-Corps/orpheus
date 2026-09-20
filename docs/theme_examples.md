# Theme examples

Copy any block into `~/.config/orpheus/theme.json` and restart orpheus. Or paste the values into the Theme options editor and save from there — same result.

See [theming](theming.md) for what every key does.

## A preset with a solid background

One color everywhere, no header band:

```json
{
  "preset": "tokyo_night",
  "backgrounds": { "style": "solid" }
}
```

## Custom accent

Keep the preset, change the accent. The light accent is derived from it:

```json
{
  "preset": "catppuccin",
  "blue": "#F5A97F",
  "blue_light": "#FABDA4"
}
```

## Square and sharp

Thick borders, line progress bars, no marker glyphs:

```json
{
  "preset": "gruvbox",
  "glyphs": {
    "border": "ascii",
    "now_playing": "dot",
    "play_pause": "ascii",
    "bar": "line"
  },
  "typography": { "bold_titles": true, "italic_descriptions": false },
  "cover": { "frame": "thick" }
}
```

## Warm dark page

The page color drives the whole frame, the header band lifts from it automatically:

```json
{
  "preset": "kanagawa",
  "page": "#17130E",
  "backgrounds": { "style": "divided" }
}
```

Set `panel` too if you want the header band in a specific color instead of the derived one:

```json
{
  "preset": "kanagawa",
  "page": "#17130E",
  "panel": "#241D14"
}
```

## Everything custom

A full theme from scratch, no preset dependency left:

```json
{
  "preset": "default",
  "blue": "#E06C75",
  "blue_light": "#F0959C",
  "off_white": "#DCD7CF",
  "gray": "#8B857D",
  "muted_blue": "#9E8878",
  "dim_blue": "#3B3630",
  "divider": "#2C2823",
  "error": "#D16552",
  "scrim": "#141210",
  "selection_fg": "#DCD7CF",
  "selection_bg": "#43362E",
  "page": "#12100D",
  "glyphs": {
    "border": "double",
    "now_playing": "play",
    "play_pause": "bold",
    "spinner": "points",
    "bar": "block"
  },
  "typography": { "bold_titles": true, "italic_descriptions": true },
  "cover": { "frame": "rounded" },
  "backgrounds": { "style": "divided" }
}
```

## ANSI only (for limited terminals)

Uses terminal palette indices instead of hex, like the built-in `minimal` preset. No terminal background is touched:

```json
{
  "preset": "minimal",
  "glyphs": { "border": "ascii", "now_playing": "arrow" }
}
```

Remember hex and ANSI can be mixed in the same theme, one per color.
