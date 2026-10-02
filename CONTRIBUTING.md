# Contributing to Orpheus

Thanks for helping improve Orpheus! Bug reports, documentation fixes, and code contributions are welcome. For questions or bugs, [open an issue](https://github.com/Cabritto-Corps/orpheus/issues); check existing issues first.

## Set up a local build

The [README](README.md#install) describes installing a published release. To change the source and run your own build instead, you need:

- Git and Go 1.26 or newer (see [`go.mod`](go.mod) for the required version).
- A C compiler, `pkg-config`, and development headers/libraries for ALSA (Linux), FLAC, Ogg, and Vorbis. The audio dependencies use CGO, so a Go-only install is not enough.
- A Spotify Premium account to log in and play music. Tests and compilation do not require Spotify login.

For Ubuntu/Debian, install the same audio packages used in CI:

```sh
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libasound2-dev libflac-dev libogg-dev libvorbis-dev
```

On macOS (Apple Silicon), install the libraries with Homebrew; you also need Xcode Command Line Tools and Go:

```sh
xcode-select --install
brew install pkgconf libogg libvorbis flac
```

On other Linux distributions, install equivalent development packages for ALSA, FLAC, Ogg, and Vorbis, plus a C compiler and pkg-config. These are **development** packages, not just the runtime libraries used by the release installer.

Clone the repository (or your fork, if you plan to submit a pull request):

```sh
git clone https://github.com/Cabritto-Corps/orpheus.git
cd orpheus
go mod download
make build
```

`make build` writes an `orpheus` executable at the repository root. To configure and launch it:

1. Follow the [initial setup tutorial](tutorial.md) to create a Spotify developer app, add your account as a user, and obtain its client ID.
2. Create a `.env` in the repository root with `SPOTIFY_CLIENT_ID=your_client_id_here`, or put it in `~/.config/orpheus/.env`. The local `.env` is gitignored; do not commit credentials or tokens. See [configuration file locations](docs/config.md#the-env-loading-order).
3. From the repository root, run `./orpheus auth login`, complete the browser authorization, then run `./orpheus`. The first player launch may require a second authorization, as described in the tutorial.

Use `./orpheus --version` to verify that the executable starts. Local builds are not stamped with a release version like downloaded binaries.

## Check your changes

Run these before opening a pull request:

```sh
make test
make vet
make build
```

For audio/player changes, `make test-race` runs tests with the race detector. If you have [golangci-lint](https://golangci-lint.run/) installed (CI uses v2.11.3), run `make lint` too. `make fmt` runs the configured gofmt/goimports formatters via golangci-lint. `make check` also runs `go fix` and formatting, which may modify files; review the resulting diff before committing.

CI runs vet, race-enabled tests, a build, and lint on pull requests. It uses the Go version from `go.mod` and the Linux audio development packages listed above.

## Send a pull request

1. Check existing [issues](https://github.com/Cabritto-Corps/orpheus/issues) and [pull requests](https://github.com/Cabritto-Corps/orpheus/pulls); for larger changes, open an issue to discuss the approach first.
2. Create a branch from `dev` in your fork (or in the repository if you have write access), make a focused change, and add or update tests/docs where appropriate.
3. Run the relevant checks above, then open a pull request against `Cabritto-Corps/orpheus:main`. Describe what changed, link any related issue, and include how you tested it. Do not include `.env`, tokens, or generated binaries.

For configuration and UI behavior, see the [configuration docs](docs/config.md) and [theming docs](docs/theming.md).
