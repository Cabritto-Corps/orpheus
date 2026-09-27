#!/usr/bin/env bash

if [ -z "${BASH_VERSION:-}" ]; then
	printf 'install.sh: error: this installer requires bash, not sh.\n  run: curl -fsSL https://raw.githubusercontent.com/Cabritto-Corps/orpheus/main/install.sh | bash\n' >&2
	exit 1
fi

set -euo pipefail

REPO="Cabritto-Corps/orpheus"
SUPPORTED="linux-amd64, darwin-arm64"
install_deps="auto"
seed_config=1
PATH_SETUP=""
CONFIG_CREATED=""
CONFIG_KEPT=""
DEPS_STATUS=""
PLATFORM_DISPLAY=""
DISTRO_FAMILY=""
INSTALLED_VERSION=""

usage() {
	cat <<'EOF'
Usage: install.sh [--bin-dir DIR] [--version TAG] [--install-deps] [--no-config] [--help]

Installs the orpheus Spotify TUI player from a GitHub release binary.

  --bin-dir DIR   install directory (default: $HOME/.local/bin)
  --version TAG   release tag to install, e.g. v0.4.2 (default: latest).
                  A bare number (0.4.2) is accepted and gets a v prefix.
  --install-deps  install missing audio libraries with the detected distro
                  package manager (uses sudo and only affects system audio
                  libraries). Without this flag the installer prints the
                  exact command instead.
  --no-config     do not create starter files under ~/.config/orpheus
  --help          print this help and exit

Environment overrides: BIN_DIR, ORPHEUS_VERSION (flags win).

Examples:
  curl -fsSL https://raw.githubusercontent.com/Cabritto-Corps/orpheus/main/install.sh | bash
  curl -fsSL .../install.sh | bash -s -- --bin-dir "$HOME/bin" --version v0.4.2
EOF
}

die() {
	printf 'install.sh: error: %s\n' "$*" >&2
	exit 1
}

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

color_enabled=0
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	color_enabled=1
fi
if [ "$color_enabled" -eq 1 ]; then
	C_BOLD=$'\033[1m'
	C_GREEN=$'\033[32m'
	C_YELLOW=$'\033[33m'
	C_BLUE=$'\033[34m'
	C_RESET=$'\033[0m'
	G_OK=' ✓'
	G_ARROW=' →'
else
	C_BOLD=''
	C_GREEN=''
	C_YELLOW=''
	C_BLUE=''
	C_RESET=''
	G_OK=' [ok]'
	G_ARROW=' ->'
fi

step() {
	if [ "$color_enabled" -eq 1 ]; then
		printf '%s==>%s %s\n' "$C_BOLD" "$C_RESET" "$*"
	else
		printf '==> %s\n' "$*"
	fi
}

ok() {
	if [ "$color_enabled" -eq 1 ]; then
		printf '  %s%s%s %s\n' "$C_GREEN" "$G_OK" "$C_RESET" "$*"
	else
		printf '  %s %s\n' "$G_OK" "$*"
	fi
}

info() {
	if [ "$color_enabled" -eq 1 ]; then
		printf '  %s%s%s %s\n' "$C_BLUE" "$G_ARROW" "$C_RESET" "$*"
	else
		printf '  %s %s\n' "$G_ARROW" "$*"
	fi
}

warn() {
	if [ "$color_enabled" -eq 1 ]; then
		printf '  %s[!]%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2
	else
		printf '  [!] %s\n' "$*" >&2
	fi
}

os_release_value() {
	local file="$1" key="$2" value
	[ -r "$file" ] || return 1
	value=$(sed -n "s/^${key}=//p" "$file" | head -n 1) || return 1
	value=${value#\"}
	value=${value%\"}
	value=${value#\'}
	value=${value%\'}
	printf '%s' "$value"
}

distro_pretty_name() {
	local file="${1:-/etc/os-release}" name
	if [ "$(uname -s)" = "Darwin" ]; then
		printf 'macOS\n'
		return 0
	fi
	if name=$(os_release_value "$file" PRETTY_NAME) && [ -n "$name" ]; then
		printf '%s\n' "$name"
		return 0
	fi
	if name=$(os_release_value "$file" ID) && [ -n "$name" ]; then
		printf '%s\n' "$name"
		return 0
	fi
	printf 'unknown Linux distribution\n'
}

detect_distro_family() {
	local file="${1:-/etc/os-release}" id='' id_like='' combined=''
	if [ ! -r "$file" ]; then
		if [ "$(uname -s)" = "Darwin" ]; then
			printf 'brew\n'
			return 0
		fi
		printf 'unknown\n'
		return 0
	fi
	id=$(os_release_value "$file" ID || true)
	id_like=$(os_release_value "$file" ID_LIKE || true)
	id=$(printf '%s' "$id" | tr '[:upper:]' '[:lower:]')
	id_like=$(printf '%s' "$id_like" | tr '[:upper:]' '[:lower:]')
	combined=" $id $id_like "
	case "$combined" in
		*" debian "*|*" ubuntu "*|*" linuxmint "*|*" pop "*|*" pop_os "*|*" elementary "*|*" zorin "*|*" kali "*|*" parrot "*|*" raspbian "*|*" mx "*|*" devuan "*)
			printf 'apt\n'
			;;
		*" fedora "*|*" rhel "*|*" redhat "*|*" centos "*|*" rocky "*|*" alma "*|*" ol "*|*" amzn "*)
			printf 'dnf\n'
			;;
		*" arch "*|*" manjaro "*|*" endeavouros "*|*" cachyos "*|*" garuda "*)
			printf 'pacman\n'
			;;
		*" opensuse "*|*" suse "*|*" sles "*)
			printf 'zypper\n'
			;;
		*" alpine "*)
			printf 'apk\n'
			;;
		*)
			if [ "$(uname -s)" = "Darwin" ]; then
				printf 'brew\n'
			else
				printf 'unknown\n'
			fi
			;;
	esac
}

is_musl() {
	local candidate
	for candidate in "$@"; do
		[ -e "$candidate" ] && return 0
	done
	case "$(ldd --version 2>&1 || true)" in
		*musl*) return 0 ;;
	esac
	return 1
}

missing_audio_libs() {
	local binary="$1" soname
	ldd "$binary" 2>/dev/null | awk '/=> not found/ {print $1}' | while IFS= read -r soname; do
		case "$soname" in
			libasound.so.*) printf 'alsa\n' ;;
			libFLAC.so.*) printf 'flac\n' ;;
			libogg.so.*) printf 'ogg\n' ;;
			libvorbis*.so.*) printf 'vorbis\n' ;;
		esac
	done | sort -u
}

deps_install_command() {
	local family="$1"
	case "$family" in
		apt) printf 'sudo apt-get update && sudo apt-get install -y '\''libasound2t64|libasound2'\'' '\''libflac12t64|libflac12'\'' libogg0 libvorbis0a\n' ;;
		dnf) printf 'sudo dnf install -y alsa-lib flac-libs libogg libvorbis\n' ;;
		pacman) printf 'sudo pacman -Syu --needed --noconfirm alsa-lib flac libogg libvorbis\n' ;;
		zypper) printf 'sudo zypper refresh && sudo zypper install -y libasound2 libFLAC12 libogg0 libvorbis0\n' ;;
		brew) printf 'brew install libogg libvorbis flac\n' ;;
		*) return 1 ;;
	esac
}

prompt_yes_no() {
	local prompt="$1" reply=''
	[ -t 1 ] || return 1
	[ -r /dev/tty ] && [ -w /dev/tty ] || return 1
	printf '%s [y/N]: ' "$prompt" > /dev/tty || return 1
	IFS= read -r reply < /dev/tty || return 1
	case "$reply" in
		[Yy]|[Yy][Ee][Ss]) return 0 ;;
		*) return 1 ;;
	esac
}

run_deps_install() {
	local family="$1"
	need_cmd sudo
	case "$family" in
		apt)
			need_cmd apt-get
			sudo apt-get update && sudo apt-get install -y 'libasound2t64|libasound2' 'libflac12t64|libflac12' libogg0 libvorbis0a
			;;
		dnf)
			need_cmd dnf
			sudo dnf install -y alsa-lib flac-libs libogg libvorbis
			;;
		pacman)
			need_cmd pacman
			sudo pacman -Syu --needed --noconfirm alsa-lib flac libogg libvorbis
			;;
		zypper)
			need_cmd zypper
			sudo zypper refresh && sudo zypper install -y libasound2 libFLAC12 libogg0 libvorbis0
			;;
		brew)
			need_cmd brew
			brew install libogg libvorbis flac
			;;
		*) return 1 ;;
	esac
}

setup_path() {
	local bin_dir="$1" marker='Added by the orpheus installer' shell_name rc_file addition
	PATH_SETUP='skipped'
	case ":${PATH:-}:" in
		*":$bin_dir:"*)
			PATH_SETUP='on-path'
			info "$bin_dir is already on PATH; startup files were not changed."
			return 0
			;;
	esac
	[ -n "${HOME:-}" ] || { warn 'HOME is not set; cannot configure PATH automatically.'; return 0; }
	case "$bin_dir" in
		"$HOME"/*) ;;
		*)
			info "$bin_dir is outside HOME; PATH setup was left for you."
			return 0
			;;
	esac
	shell_name=$(basename "${SHELL:-unknown}")
	case "$shell_name" in
		bash) rc_file="$HOME/.bashrc" ;;
		zsh) rc_file="$HOME/.zshrc" ;;
		fish) rc_file="$HOME/.config/fish/config.fish" ;;
		*)
			PATH_SETUP='unsupported'
			info "shell '$shell_name' is not handled automatically; add this line to its startup file:"
			printf '  export PATH="%s:$PATH"\n' "$bin_dir"
			return 0
			;;
	esac
	if [ -f "$rc_file" ] && grep -qF "$marker" "$rc_file"; then
		PATH_SETUP='already'
		info "PATH setup is already present in $rc_file; open a new shell or source it."
		return 0
	fi
	if [ "$shell_name" = 'fish' ]; then
		addition="fish_add_path -mP \"$bin_dir\""
	else
		addition="export PATH=\"$bin_dir:\$PATH\""
	fi
	mkdir -p "$(dirname "$rc_file")" || { warn "cannot create $(dirname "$rc_file"); PATH was not changed."; return 0; }
	if [ -e "$rc_file" ] && [ ! -w "$rc_file" ]; then
		warn "$rc_file is not writable; PATH was not changed."
		return 0
	fi
	{
		printf '\n# %s: use the release binary from %s.\n' "$marker" "$bin_dir"
		printf '%s\n' "$addition"
	} >> "$rc_file" || { warn "$rc_file could not be updated; PATH was not changed."; return 0; }
	PATH_SETUP="installed:$rc_file"
	ok "added $bin_dir to PATH in $rc_file (open a new shell or source it)"
}

config_json_template() {
	cat <<'EOF'
{
  "crossfade": {
    "enabled": false,
    "seconds": 3
  },
  "audio_cache": {
    "enabled": false,
    "size_mb": 1024
  }
}
EOF
}

theme_json_template() {
	cat <<'EOF'
{
  "preset": "default",
  "glyphs": {
    "border": "rounded",
    "now_playing": "note",
    "play_pause": "modern",
    "spinner": "minidot",
    "bar": "block"
  },
  "typography": {
    "bold_titles": false,
    "italic_descriptions": false
  },
  "cover": {
    "frame": "none"
  },
  "backgrounds": {
    "style": "divided"
  }
}
EOF
}

keys_json_template() {
	cat <<'EOF'
{
  "play_pause": [" "],
  "next": ["n"],
  "prev": ["p"],
  "shuffle": ["s"],
  "loop": ["l"],
  "vol_up": ["+", "="],
  "vol_down": ["-"],
  "seek_back": ["left"],
  "seek_fwd": ["right"],
  "tab": ["tab"],
  "refresh": ["r"],
  "filter": ["/"],
  "select": ["enter", "return"],
  "toggle_help": ["?"],
  "settings": ["o"],
  "close_modal": ["esc"],
  "quit": ["q", "ctrl+c"],
  "queue_up": ["up"],
  "queue_down": ["down"],
  "queue_jump": ["enter", "return"],
  "queue_remove": ["x", "d"],
  "queue_move_up": ["["],
  "queue_move_down": ["]"]
}
EOF
}

env_template() {
	local config_dir="$1"
	cat <<EOF
# orpheus bootstrap settings. Every line below is commented out, so this file
# changes nothing until you remove a leading '#'. Starter values shown here
# match the app defaults. Values in config.json win over these variables.
#
# The player's Web API login needs your own Spotify app:
# SPOTIFY_CLIENT_ID=PASTE_YOUR_CLIENT_ID_HERE
#
# spotify_redirect_uri=http://127.0.0.1:8989/callback
# spotify_scopes=streaming,user-read-playback-state,user-modify-playback-state,user-read-currently-playing,playlist-read-private,playlist-read-collaborative,user-library-read
# spotify_device_name=orpheus
#
# These usually stay unset because config.json manages them in the UI:
# orpheus_crossfade=false
# orpheus_crossfade_seconds=3
# orpheus_audio_cache_enabled=false
# orpheus_audio_cache_size_mb=1024
# orpheus_audio_cache_dir=
#
# orpheus_on_song_change=
# orpheus_nerd_fonts=auto
# ORPHEUS_IMAGE_PROTOCOL=
# orpheus_theme=default
# orpheus_theme_file=$config_dir/theme.json
# orpheus_keys_file=$config_dir/keys.json
# orpheus_config_file=$config_dir/config.json
# orpheus_token_path=$config_dir/token.json
# orpheus_log_file=$config_dir/orpheus.log
# ORPHEUS_LOG_FILE=
# ORPHEUS_CONFIG_DIR=
#
# After setting SPOTIFY_CLIENT_ID, run: orpheus auth login
EOF
}

write_seed_file() {
	local path="$1" mode="$2" content="$3" label="$4"
	if [ -e "$path" ]; then
		CONFIG_KEPT="${CONFIG_KEPT:+$CONFIG_KEPT }$label"
		info "kept existing $label"
		return 0
	fi
	mkdir -p "$(dirname "$path")" || die "cannot create $(dirname "$path")"
	printf '%s\n' "$content" > "$path.tmp" || die "cannot write $path"
	chmod "$mode" "$path.tmp" || die "cannot set permissions on $path"
	mv "$path.tmp" "$path" || die "cannot install $path"
	CONFIG_CREATED="${CONFIG_CREATED:+$CONFIG_CREATED }$label"
	ok "created $label"
}

seed_config() {
	local dir="$1"
	CONFIG_CREATED=''
	CONFIG_KEPT=''
	if [ "$seed_config" -ne 1 ]; then
		info 'config-file seeding skipped (--no-config)'
		return 0
	fi
	mkdir -p "$dir" || die "cannot create $dir"
	chmod 0700 "$dir" 2>/dev/null || true
	write_seed_file "$dir/config.json" 600 "$(config_json_template)" 'config.json'
	write_seed_file "$dir/theme.json" 644 "$(theme_json_template)" 'theme.json'
	write_seed_file "$dir/keys.json" 644 "$(keys_json_template)" 'keys.json'
	write_seed_file "$dir/.env" 600 "$(env_template "$dir")" '.env'
}

detect_platform() {
	local os arch
	os="$(uname -s)"
	arch="$(uname -m)"
	case "$os" in
		Linux) os="linux" ;;
		Darwin) os="darwin" ;;
		*) die "unsupported operating system: $os (supported platforms: $SUPPORTED)" ;;
	esac
	case "$arch" in
		x86_64 | amd64) arch="amd64" ;;
		arm64 | aarch64) arch="arm64" ;;
		*) die "unsupported architecture: $arch (supported platforms: $SUPPORTED)" ;;
	esac
	local platform="${os}-${arch}"
	case "$platform" in
		linux-amd64 | darwin-arm64) printf '%s\n' "$platform" ;;
		*) die "no prebuilt orpheus binary for ${platform} (published: $SUPPORTED). Build from source instead: git clone https://github.com/$REPO.git && cd orpheus && make build" ;;
	esac
}

sha256_of_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		die "no SHA-256 tool found (need sha256sum or shasum)"
	fi
}

verify_checksum() {
	local tarball="$1" sha_file="$2"
	local listed expected actual base
	base="$(basename "$tarball")"
	listed="$(awk '{print $2}' "$sha_file")"
	[ "$listed" = "$base" ] || die "checksum manifest names '$listed', expected '$base' (wrong release file?)"
	expected="$(awk '{print $1}' "$sha_file")"
	[ -n "$expected" ] || die "could not parse checksum from $sha_file"
	actual="$(sha256_of_file "$tarball")"
	if [ "$expected" != "$actual" ]; then
		printf 'install.sh: error: checksum mismatch for %s\n  expected: %s\n  actual:   %s\n' "$base" "$expected" "$actual" >&2
		return 1
	fi
	ok "checksum OK: $base"
}

check_supported_libc() {
	local platform="$1"
	shift
	case "$platform" in
		linux-*)
			if is_musl "$@"; then
				die "this release is linked against glibc, but musl libc was detected. Prebuilt orpheus binaries do not support musl/Alpine; build from source instead: git clone https://github.com/$REPO.git && cd orpheus && make build"
			fi
			;;
	esac
}

handle_runtime_dependencies() {
	local binary="$1" platform="$2" family="$3" mode="$4"
	local missing='' cmd=''
	DEPS_STATUS='checked'
	if [ "$platform" = 'linux-amd64' ]; then
		if ! command -v ldd >/dev/null 2>&1; then
			DEPS_STATUS='unchecked'
			warn 'ldd is unavailable, so missing libraries could not be inspected.'
			info "if orpheus fails to start, install your distribution's ALSA, FLAC, Ogg and Vorbis runtime packages."
			return 0
		fi
		missing=$(missing_audio_libs "$binary")
		if [ -z "$missing" ]; then
			DEPS_STATUS='present'
			ok 'all required audio libraries resolve'
			return 0
		fi
		info 'missing audio libraries:'
		printf '%s\n' "$missing" | sed 's/^/  - /'
		if ! cmd=$(deps_install_command "$family"); then
			DEPS_STATUS='manual-unknown'
			warn "unknown distribution family '$family'; install its ALSA, FLAC, Ogg and Vorbis runtime packages."
			return 0
		fi
		info "package command: $cmd"
		if [ "$mode" = 'yes' ] || prompt_yes_no 'Install the missing audio libraries now?'; then
			info "running: $cmd"
			if run_deps_install "$family"; then
				DEPS_STATUS="installed:$cmd"
				ok 'missing audio libraries installed'
			else
				die "dependency installation failed; install them manually first: $cmd"
			fi
		else
			DEPS_STATUS="manual:$cmd"
			info 'installation skipped; run the command above before starting orpheus.'
		fi
		return 0
	fi
	if [ "$platform" = 'darwin-arm64' ]; then
		cmd=$(deps_install_command 'brew')
		info "package command: $cmd"
		if [ "$mode" = 'yes' ] || prompt_yes_no 'Install the macOS audio libraries now?'; then
			info "running: $cmd"
			if run_deps_install 'brew'; then
				DEPS_STATUS="installed:$cmd"
				ok 'macOS audio libraries installed'
			else
				die "dependency installation failed; install them manually first: $cmd"
			fi
		else
			DEPS_STATUS="manual:$cmd"
			info 'installation skipped; run the command above before starting orpheus.'
		fi
		return 0
	fi
	DEPS_STATUS='unchecked'
	warn "unsupported platform '$platform' for dependency inspection."
}

print_summary() {
	local bin_path="$1" path_status deps_status config_status
	if [ "$color_enabled" -eq 1 ]; then
		printf '%s========================================%s\n' "$C_BOLD" "$C_RESET"
		printf '%sorpheus install summary%s\n' "$C_BOLD" "$C_RESET"
		printf '%s----------------------------------------%s\n' "$C_BOLD" "$C_RESET"
	else
		printf '========================================\norpheus install summary\n----------------------------------------\n'
	fi
	if [ -n "$INSTALLED_VERSION" ]; then
		printf '%-14s %s (%s)\n' 'binary:' "$bin_path" "$INSTALLED_VERSION"
	else
		printf '%-14s %s\n' 'binary:' "$bin_path"
	fi
	printf '%-14s %s\n' 'platform:' "$PLATFORM_DISPLAY"
	case "$PATH_SETUP" in
		installed:*)
			path_status="updated ${PATH_SETUP#installed:} (open a new shell or source it)"
			;;
		on-path) path_status='already on PATH; startup files unchanged' ;;
		already) path_status='startup file already updated (open a new shell or source it)' ;;
		unsupported) path_status='shell startup file not edited; see the export line above' ;;
		skipped) path_status='unchanged' ;;
		*) path_status='unchanged' ;;
	esac
	printf '%-14s %s\n' 'PATH:' "$path_status"
	case "$DEPS_STATUS" in
		present) deps_status='all required audio libraries resolve' ;;
		installed:*) deps_status="installed (${DEPS_STATUS#installed:})" ;;
		manual:*) deps_status="not installed (${DEPS_STATUS#manual:})" ;;
		manual-unknown) deps_status='not installed; distro-specific command unavailable' ;;
		unchecked) deps_status='not inspected' ;;
		*) deps_status='not inspected' ;;
	esac
	printf '%-14s %s\n' 'libraries:' "$deps_status"
	if [ "$seed_config" -eq 1 ]; then
		config_status="created:${CONFIG_CREATED:-none}; kept:${CONFIG_KEPT:-none}"
	else
		config_status='skipped (--no-config)'
	fi
	printf '%-14s %s\n' 'config:' "$config_status"
	printf '%-14s %s\n' 'docs:' 'https://github.com/Cabritto-Corps/orpheus/blob/main/tutorial.md'
	printf '%-14s %s\n' '' 'https://github.com/Cabritto-Corps/orpheus/tree/main/docs'
	if [[ "$PATH_SETUP" == installed:* ]]; then
		printf '%-14s %s\n' 'next:' "open a new shell (or source ${PATH_SETUP#installed:}), then run 'orpheus auth login'"
	else
		printf '%-14s %s\n' 'next:' "run 'orpheus auth login' (requires Spotify Premium)"
	fi
}

main() {
	local bin_dir="${BIN_DIR:-}" version="${ORPHEUS_VERSION:-latest}"
	while [ $# -gt 0 ]; do
		case "$1" in
			--bin-dir)
				[ $# -ge 2 ] || die "--bin-dir needs a directory argument"
				bin_dir="$2"
				shift 2
				;;
			--version)
				[ $# -ge 2 ] || die "--version needs a tag argument"
				version="$2"
				shift 2
				;;
			--install-deps)
				install_deps='yes'
				shift
				;;
			--no-config)
				seed_config=0
				shift
				;;
			--help | -h)
				usage
				return 0
				;;
			*)
				die "unknown argument: $1 (see --help)"
				;;
		esac
	done
	case "$version" in
		latest) ;;
		v*) ;;
		*) version="v$version" ;;
	esac

	local platform distro_name base_url asset tmp_tarball tmp_sha
	step "starting orpheus installer ($version requested)"
	platform="$(detect_platform)"
	check_supported_libc "$platform" /lib/ld-musl-x86_64.so.1 /lib/ld-musl-aarch64.so.1
	DISTRO_FAMILY="$(detect_distro_family)"
	distro_name="$(distro_pretty_name)"
	PLATFORM_DISPLAY="$platform ($distro_name; $DISTRO_FAMILY)"
	step "detected platform: $PLATFORM_DISPLAY"
	asset="orpheus-${platform}.tar.gz"
	if [ "$version" = "latest" ]; then
		base_url="https://github.com/$REPO/releases/latest/download"
	else
		base_url="https://github.com/$REPO/releases/download/$version"
	fi

	need_cmd curl
	need_cmd tar
	need_cmd awk
	need_cmd mktemp

	tmpdir="$(mktemp -d)"
	trap 'rm -rf "$tmpdir"' EXIT
	tmp_tarball="$tmpdir/$asset"
	tmp_sha="$tmpdir/$asset.sha256"
	step 'downloading release assets'
	info "$base_url/$asset"
	curl -fsSL --retry 3 -o "$tmp_tarball" "$base_url/$asset"
	curl -fsSL --retry 3 -o "$tmp_sha" "$base_url/$asset.sha256"
	step 'verifying SHA-256 checksum'
	verify_checksum "$tmp_tarball" "$tmp_sha"
	step 'extracting release archive'
	tar -xzf "$tmp_tarball" -C "$tmpdir"
	[ -f "$tmpdir/orpheus" ] || die "archive did not contain an orpheus binary"
	step 'installing binary'
	if [ -z "$bin_dir" ]; then
		[ -n "${HOME:-}" ] || die "\$HOME is not set and no --bin-dir was given"
		bin_dir="$HOME/.local/bin"
fi
	local existing
	existing="$(command -v orpheus 2>/dev/null || true)"
	if [ -x "$bin_dir/orpheus" ]; then
		info "upgrading existing binary at $bin_dir/orpheus"
	fi
	if [ -n "$existing" ] && [ "$existing" != "$bin_dir/orpheus" ]; then
		warn "found another orpheus at $existing — this install goes to $bin_dir/orpheus and may shadow it depending on PATH order"
	fi
	mkdir -p "$bin_dir" || die "cannot create $bin_dir"
	[ -w "$bin_dir" ] || die "$bin_dir is not writable (try --bin-dir with a directory you own)"
	cp "$tmpdir/orpheus" "$bin_dir/orpheus"
	chmod 755 "$bin_dir/orpheus"
	ok "binary installed to $bin_dir/orpheus"

	local installed_version
	INSTALLED_VERSION=''
	if installed_version="$("$bin_dir/orpheus" --version 2>/dev/null)"; then
		INSTALLED_VERSION="$installed_version"
		ok "installed $installed_version"
	else
		info 'installed orpheus (the release binary did not report a version here)'
	fi

	step 'configuring PATH'
	setup_path "$bin_dir"

	step 'creating starter configuration'
	local config_dir=''
	if [ -n "${HOME:-}" ]; then
		config_dir="$HOME/.config/orpheus"
		info "using the default config directory ($config_dir; ORPHEUS_CONFIG_DIR is still honored when orpheus runs)"
		seed_config "$config_dir"
	else
		warn 'HOME is not set; starter configuration was skipped.'
	fi

	step 'checking system libraries'
	handle_runtime_dependencies "$bin_dir/orpheus" "$platform" "$DISTRO_FAMILY" "$install_deps"
	print_summary "$bin_dir/orpheus"
}

if [[ -z "${BASH_SOURCE[0]:-}" || "${BASH_SOURCE[0]:-}" == "${0}" ]]; then
	main "$@"
fi
