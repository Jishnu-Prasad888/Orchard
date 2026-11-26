#!/usr/bin/env bash
# Install the latest Orchard release, and optionally have it start on its own.
#
#   curl -fsSL https://raw.githubusercontent.com/Jishnu-Prasad888/Orchard/main/scripts/install.sh | bash
#
# It picks the build for this OS (Linux, macOS) and CPU (amd64, arm64),
# checks it against the release's SHA256SUMS, installs it, and then asks
# (y/n) whether to set up a systemd service (Linux) or a launchd agent (macOS)
# so Orchard starts automatically. Running it again upgrades in place and
# restarts the service.
#
# Options (when piping, pass them after `bash -s --`, e.g. `| bash -s -- -y`):
#
#   -y, --yes            take the default answer to every question
#   --version vX.Y.Z     install that release instead of the latest
#   --dir DIR            install the binary into DIR
#   --no-service         install the binary only; don't ask about autostart
#   -h, --help           show this help
#
# The same can be set with ORCHARD_VERSION, ORCHARD_INSTALL_DIR and
# ORCHARD_REPO (default Jishnu-Prasad888/Orchard). With no terminal to ask on
# (e.g. in CI), every question takes its default answer.

# Everything is inside main(), called on the last line, so a download cut off
# halfway through runs nothing.
main() {
  set -euo pipefail

  REPO="${ORCHARD_REPO:-Jishnu-Prasad888/Orchard}"
  VERSION="${ORCHARD_VERSION:-}"
  INSTALL_DIR="${ORCHARD_INSTALL_DIR:-}"
  ASSUME_YES=0
  SERVICE=ask
  PORT=8090

  while [ $# -gt 0 ]; do
    case "$1" in
      -y | --yes) ASSUME_YES=1 ;;
      --version) VERSION="${2:?--version needs a tag, e.g. v1.4.0}"; shift ;;
      --dir) INSTALL_DIR="${2:?--dir needs a directory}"; shift ;;
      --no-service) SERVICE=no ;;
      -h | --help) usage; return 0 ;;
      *) die "unknown option $1 (try --help)" ;;
    esac
    shift
  done

  setup_output
  TTY=""
  if [ "$ASSUME_YES" = 0 ] && (: </dev/tty) 2>/dev/null; then TTY=/dev/tty; fi

  need curl
  # Fail rather than hang on a stalled connection: give up connecting after
  # 15s, or when a transfer moves under 1 KB/s for 30s.
  CURL=(curl --connect-timeout 15 --speed-limit 1024 --speed-time 30)
  need uname
  detect_platform
  resolve_version

  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT

  choose_install_dir
  BIN="$INSTALL_DIR/orchard"
  install_binary

  if [ "$SERVICE" = no ]; then
    finish_manual
    return 0
  fi
  case "$OS" in
    linux) setup_systemd ;;
    darwin) setup_launchd ;;
  esac
}

usage() {
  cat <<'EOF'
Install the latest Orchard release, and optionally have it start on its own.

  curl -fsSL https://raw.githubusercontent.com/Jishnu-Prasad888/Orchard/main/scripts/install.sh | bash
  curl -fsSL .../install.sh | bash -s -- [options]

Options:
  -y, --yes            take the default answer to every question
  --version vX.Y.Z     install that release instead of the latest
  --dir DIR            install the binary into DIR
  --no-service         install the binary only; don't ask about autostart
  -h, --help           show this help

Environment: ORCHARD_VERSION, ORCHARD_INSTALL_DIR, ORCHARD_REPO
EOF
}

# ---------------------------------------------------------------- output ---

setup_output() {
  if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    B=$'\033[1m' G=$'\033[32m' Y=$'\033[33m' R=$'\033[31m' D=$'\033[2m' N=$'\033[0m'
  else
    B="" G="" Y="" R="" D="" N=""
  fi
}

say() { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$G" "$N" "$*"; }
warn() { printf '%swarning:%s %s\n' "$Y" "$N" "$*" >&2; }
die() {
  printf '%serror:%s %s\n' "${R:-}" "${N:-}" "$*" >&2
  exit 1
}
need() { command -v "$1" >/dev/null 2>&1 || die "this installer needs '$1'; install it and run again"; }

# ask "Question?" y|n — succeeds on yes. The answer is read from the terminal,
# not stdin, because stdin is this script when it's piped into bash.
ask() {
  local question="$1" def="$2" hint answer
  if [ "$def" = y ]; then hint="[Y/n]"; else hint="[y/N]"; fi
  if [ -z "$TTY" ]; then
    printf '%s %s %s(%s)%s\n' "$question" "$hint" "$D" "$def" "$N"
    [ "$def" = y ]
    return
  fi
  while true; do
    printf '%s?%s %s %s ' "$B" "$N" "$question" "$hint" >"$TTY"
    IFS= read -r answer <"$TTY" || answer=""
    case "${answer:-$def}" in
      [Yy] | [Yy][Ee][Ss]) return 0 ;;
      [Nn] | [Nn][Oo]) return 1 ;;
    esac
    printf 'Please answer y or n.\n' >"$TTY"
  done
}

is_root() { [ "$(id -u)" -eq 0 ]; }
can_sudo() { is_root || command -v sudo >/dev/null 2>&1; }
as_root() { if is_root; then "$@"; else sudo "$@"; fi; }
# in_dir CMD...: run a command that writes to the install directory.
in_dir() { if [ "${NEED_SUDO:-0}" = 1 ]; then as_root "$@"; else "$@"; fi; }

# ------------------------------------------------------------- platform ---

detect_platform() {
  case "$(uname -s)" in
    Linux) OS=linux ;;
    Darwin) OS=darwin ;;
    MINGW* | MSYS* | CYGWIN*)
      die "on Windows, run this in PowerShell instead:
  irm https://raw.githubusercontent.com/$REPO/main/scripts/install.ps1 | iex" ;;
    *) die "no Orchard build for $(uname -s); builds exist for Linux, macOS and Windows" ;;
  esac

  case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "no Orchard build for a $(uname -m) CPU; builds exist for amd64 (x86-64) and arm64" ;;
  esac

  # A shell under Rosetta reports x86_64 on Apple silicon; use the native build.
  if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
    ARCH=arm64
  fi

  ASSET="orchard-$OS-$ARCH"
  step "Detected $OS/$ARCH"
}

resolve_version() {
  if [ -n "$VERSION" ]; then
    case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
    return
  fi
  # Following github.com's /releases/latest redirect needs no API token and
  # isn't subject to the API's rate limit.
  local url
  url="$("${CURL[@]}" -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" || true)"
  VERSION="${url##*/tag/}"
  if [ -z "$VERSION" ] || [ "$VERSION" = "$url" ]; then
    VERSION="$("${CURL[@]}" -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
      sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1 || true)"
  fi
  [ -n "$VERSION" ] || die "could not find the latest release of $REPO (is it published?)"
  step "Latest release is $VERSION"
}

# -------------------------------------------------------------- binary ---

# bin_version BIN: the version BIN reports, or nothing. Releases before v1.4.0
# don't know --version and start the server instead, so give up after a few
# seconds and stop it.
bin_version() {
  local out="$TMP/version.out" pid i
  : >"$out"
  "$1" --version >"$out" 2>/dev/null </dev/null &
  pid=$!
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.2
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    return 0
  fi
  wait "$pid" 2>/dev/null || true
  head -n 1 "$out" | grep -E '^v[0-9]' || true
}

choose_install_dir() {
  local existing
  existing="$(command -v orchard 2>/dev/null || true)"
  # Upgrade a release install in place; leave a development build alone.
  if [ -z "$INSTALL_DIR" ] && [ -n "$existing" ] &&
    [ "$(bin_version "$existing")" != dev ]; then
    INSTALL_DIR="$(dirname "$existing")"
  fi

  if [ -z "$INSTALL_DIR" ]; then
    if is_root; then
      INSTALL_DIR=/usr/local/bin
    elif can_sudo && ask "Install Orchard for all users in /usr/local/bin (uses sudo)? Answer n for ~/.local/bin." y; then
      INSTALL_DIR=/usr/local/bin
    else
      INSTALL_DIR="$HOME/.local/bin"
    fi
  fi

  CURRENT=""
  if [ -x "$INSTALL_DIR/orchard" ]; then
    CURRENT="$(bin_version "$INSTALL_DIR/orchard")"
    step "Found Orchard ${CURRENT:-(unknown version)} at $INSTALL_DIR/orchard"
  fi
}

install_binary() {
  if [ "${CURRENT:-}" = "$VERSION" ]; then
    step "Orchard $VERSION is already installed at $BIN"
    UPGRADED=0
    return
  fi

  local base="https://github.com/$REPO/releases/download/$VERSION"
  step "Downloading $ASSET $VERSION"
  "${CURL[@]}" -fL --retry 3 --progress-bar -o "$TMP/$ASSET" "$base/$ASSET" ||
    die "download failed: $base/$ASSET"
  "${CURL[@]}" -fsSL --retry 3 -o "$TMP/SHA256SUMS" "$base/SHA256SUMS" ||
    die "download failed: $base/SHA256SUMS"

  local want got
  # Lines look like "<hash>  dist/orchard-linux-amd64"; match on the file name.
  want="$(awk -v a="$ASSET" '{ n = $2; sub(/^.*\//, "", n); sub(/^\*/, "", n); if (n == a) print $1 }' "$TMP/SHA256SUMS")"
  [ -n "$want" ] || die "SHA256SUMS has no entry for $ASSET"
  if command -v sha256sum >/dev/null 2>&1; then
    got="$(sha256sum "$TMP/$ASSET" | cut -d' ' -f1)"
  else
    got="$(shasum -a 256 "$TMP/$ASSET" | cut -d' ' -f1)"
  fi
  [ "$got" = "$want" ] || die "checksum mismatch for $ASSET; not installing it"
  step "Checksum verified"

  chmod 0755 "$TMP/$ASSET"
  got="$(bin_version "$TMP/$ASSET")"
  # Empty means a release older than v1.4.0, which can't report its version.
  [ -z "$got" ] || [ "$got" = "$VERSION" ] || die "the downloaded binary reports version '$got', expected $VERSION"

  NEED_SUDO=0
  if ! mkdir -p "$INSTALL_DIR" 2>/dev/null || [ ! -w "$INSTALL_DIR" ]; then
    can_sudo || die "cannot write to $INSTALL_DIR and sudo isn't available; try --dir ~/.local/bin"
    NEED_SUDO=1
    in_dir mkdir -p "$INSTALL_DIR"
  fi
  local kept=""
  if [ -f "$BIN" ]; then in_dir cp -p "$BIN" "$BIN.previous"; kept=" (the previous binary is at $BIN.previous)"; fi
  # Copy beside the target and rename over it: a running binary is replaced
  # cleanly, and a failed copy never leaves a half-written orchard behind.
  in_dir cp "$TMP/$ASSET" "$BIN.new"
  in_dir chmod 0755 "$BIN.new"
  in_dir mv -f "$BIN.new" "$BIN"
  UPGRADED=1
  step "Installed Orchard $VERSION to $BIN$kept"

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) warn "$INSTALL_DIR is not on your PATH; add it, e.g. in ~/.profile: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
  esac
}

# ---------------------------------------------------------- shared bits ---

ask_listen_addr() {
  if ask "Make Orchard reachable from other devices on your network? Answer n to keep it on this machine only." n; then
    LISTEN="0.0.0.0:$PORT"
    LAN=1
  else
    LISTEN="127.0.0.1:$PORT"
    LAN=0
  fi
}

lan_ip() {
  case "$OS" in
    linux) hostname -I 2>/dev/null | awk '{ print $1 }' ;;
    darwin) ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || true ;;
  esac
}

wait_healthy() {
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS -m 2 -o /dev/null "http://127.0.0.1:$PORT/api/v1/health" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

print_urls() {
  say ""
  say "${B}Orchard is running.${N} Open ${B}http://127.0.0.1:$PORT${N}"
  if [ "${LAN:-0}" = 1 ]; then
    local ip
    ip="$(lan_ip)"
    [ -z "$ip" ] || say "From other devices: ${B}http://$ip:$PORT${N} (allow port $PORT through your firewall if needed)"
  fi
  say "On first visit, create the administrator account, then add a library."
}

finish_manual() {
  say ""
  say "${B}Done.${N} Start Orchard with: $BIN"
  say "Then open http://127.0.0.1:$PORT and create the administrator account."
}

# set_env FILE KEY VALUE [as_root]: set KEY=VALUE in an env file, keeping
# everything else in it. Creates the file (with a short header) if missing.
set_env() {
  local file="$1" key="$2" value="$3" sudo="${4:-}" new="$TMP/env.new"
  if [ -n "$sudo" ]; then as_root cat "$file" 2>/dev/null >"$new" || true; else cat "$file" 2>/dev/null >"$new" || true; fi
  if [ ! -s "$new" ]; then
    cat >"$new" <<'EOF'
# Orchard settings, read when the service starts. Restart it after editing.
# Every ORCHARD_* setting is listed in the README; for example:
#   ORCHARD_DATA_DIR=/path/to/data
#   ORCHARD_BACKUP_DIR=/path/to/backups
EOF
  fi
  grep -v "^$key=" "$new" >"$new.tmp" || true
  printf '%s=%s\n' "$key" "$value" >>"$new.tmp"
  if [ -n "$sudo" ]; then
    as_root mkdir -p "$(dirname "$file")"
    as_root cp "$new.tmp" "$file"
  else
    mkdir -p "$(dirname "$file")"
    cp "$new.tmp" "$file"
  fi
}

# ------------------------------------------------------------- systemd ---

setup_systemd() {
  if [ ! -d /run/systemd/system ] || ! command -v systemctl >/dev/null 2>&1; then
    say ""
    warn "systemd isn't running on this machine, so no service was set up"
    finish_manual
    return
  fi

  local system_unit=/etc/systemd/system/orchard.service
  local user_unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/orchard.service"

  say ""
  if ! ask "Set up a systemd service so Orchard starts automatically?" y; then
    # Upgrading under an existing service: offer to put the new binary to work.
    if [ "${UPGRADED:-0}" = 1 ]; then
      if [ -f "$system_unit" ] && ask "Restart the existing Orchard service to run $VERSION?" y; then
        as_root systemctl restart orchard.service
      elif [ -f "$user_unit" ] && ! is_root && ask "Restart the existing Orchard user service to run $VERSION?" y; then
        systemctl --user restart orchard.service
      fi
    fi
    finish_manual
    return
  fi

  ask_listen_addr

  if is_root; then
    systemd_system
  elif can_sudo && ask "Run it as a system service that starts at boot (uses sudo)? Answer n for a service in your user session." y; then
    systemd_system
  else
    systemd_user
  fi
}

systemd_system() {
  local unit=/etc/systemd/system/orchard.service env=/etc/orchard/orchard.env
  local run_user extra=""

  # Run as the person installing, so Orchard can read the libraries in their
  # home directory and keeps its data in their ~/.local/share/orchard, as
  # when they run it by hand. Installing from a root shell, use a dedicated
  # system account instead.
  if is_root; then run_user="${SUDO_USER:-}"; else run_user="$(id -un)"; fi
  if [ -z "$run_user" ] || [ "$run_user" = root ]; then
    run_user=orchard
    if ! id orchard >/dev/null 2>&1; then
      step "Creating system user 'orchard'"
      as_root useradd --system --home-dir /var/lib/orchard --no-create-home \
        --shell "$(command -v nologin || echo /bin/false)" orchard
    fi
    extra="StateDirectory=orchard"
    set_env "$env" ORCHARD_DATA_DIR /var/lib/orchard sudo
    say "Orchard will run as the 'orchard' user; give it read access to your library folders."
  fi

  set_env "$env" ORCHARD_HTTP_ADDR "$LISTEN" sudo
  as_root chown "root:$(id -gn "$run_user")" "$env"
  as_root chmod 0640 "$env"

  step "Writing $unit (runs as $run_user)"
  cat >"$TMP/orchard.service" <<EOF
[Unit]
Description=Orchard personal library
Documentation=https://github.com/$REPO
After=network-online.target local-fs.target remote-fs.target
Wants=network-online.target

[Service]
Type=simple
User=$run_user
EnvironmentFile=-$env
ExecStart=$BIN
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
$extra

[Install]
WantedBy=multi-user.target
EOF
  as_root cp "$TMP/orchard.service" "$unit"
  as_root chmod 0644 "$unit"
  as_root systemctl daemon-reload
  as_root systemctl enable orchard.service >/dev/null 2>&1
  as_root systemctl restart orchard.service

  if wait_healthy; then
    print_urls
  else
    warn "the service didn't answer on port $PORT yet; check: sudo systemctl status orchard"
  fi
  say ""
  say "Manage it with:  sudo systemctl {status|restart|stop} orchard"
  say "Logs:            sudo journalctl -u orchard -f"
  say "Settings:        $env"
}

systemd_user() {
  if ! systemctl --user show-environment >/dev/null 2>&1; then
    warn "no systemd user session here (try logging in directly rather than with su)"
    finish_manual
    return
  fi
  local dir="${XDG_CONFIG_HOME:-$HOME/.config}"
  local unit="$dir/systemd/user/orchard.service" env="$dir/orchard/orchard.env"

  set_env "$env" ORCHARD_HTTP_ADDR "$LISTEN"
  chmod 0600 "$env"

  step "Writing $unit"
  mkdir -p "$(dirname "$unit")"
  cat >"$unit" <<EOF
[Unit]
Description=Orchard personal library
Documentation=https://github.com/$REPO
After=network-online.target

[Service]
Type=simple
EnvironmentFile=-$env
ExecStart=$BIN
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable orchard.service >/dev/null 2>&1
  systemctl --user restart orchard.service

  # A user service normally runs only while you're logged in. Lingering
  # starts it at boot and keeps it up after you log out.
  if [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null || true)" != yes ]; then
    if ask "Start Orchard at boot and keep it running when you log out (enables lingering)?" y; then
      loginctl enable-linger "$(id -un)" 2>/dev/null ||
        { can_sudo && as_root loginctl enable-linger "$(id -un)"; } ||
        warn "could not enable lingering; Orchard will run only while you're logged in"
    fi
  fi

  if wait_healthy; then
    print_urls
  else
    warn "the service didn't answer on port $PORT yet; check: systemctl --user status orchard"
  fi
  say ""
  say "Manage it with:  systemctl --user {status|restart|stop} orchard"
  say "Logs:            journalctl --user -u orchard -f"
  say "Settings:        $env"
}

# ------------------------------------------------------------- launchd ---

setup_launchd() {
  local label=local.orchard
  local plist="$HOME/Library/LaunchAgents/$label.plist" log="$HOME/Library/Logs/orchard.log"
  local domain="gui/$(id -u)"

  say ""
  if is_root; then
    warn "run the installer without sudo to have Orchard start when you log in"
    finish_manual
    return
  fi
  if ! ask "Start Orchard automatically when you log in (launchd agent)?" y; then
    if [ "${UPGRADED:-0}" = 1 ] && [ -f "$plist" ] && ask "Restart the existing Orchard agent to run $VERSION?" y; then
      launchctl kickstart -k "$domain/$label" || true
    fi
    finish_manual
    return
  fi

  ask_listen_addr
  step "Writing $plist"
  mkdir -p "$(dirname "$plist")" "$(dirname "$log")"
  cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key><array><string>$BIN</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>ORCHARD_HTTP_ADDR</key><string>$LISTEN</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>$log</string>
  <key>StandardErrorPath</key><string>$log</string>
</dict>
</plist>
EOF
  launchctl bootout "$domain/$label" 2>/dev/null || true
  launchctl bootstrap "$domain" "$plist"

  if wait_healthy; then
    print_urls
  else
    warn "Orchard didn't answer on port $PORT yet; check $log"
  fi
  say ""
  say "Restart:  launchctl kickstart -k $domain/$label"
  say "Stop:     launchctl bootout $domain/$label"
  say "Logs:     $log"
  say "Settings: edit $plist, then restart"
}

main "$@"
