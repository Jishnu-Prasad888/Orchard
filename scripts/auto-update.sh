#!/bin/sh
# Keep an installed Orchard binary on the latest published GitHub release.
#
#   ./scripts/auto-update.sh                  update now, if a newer release exists
#   ./scripts/auto-update.sh check            show installed and latest versions
#   ./scripts/auto-update.sh install [opts]   check for updates on a schedule
#   ./scripts/auto-update.sh uninstall        stop checking on a schedule
#   ./scripts/auto-update.sh config           show the settings in use
#
# install options (each is saved in the config file, so later runs use it):
#
#   --at HH:MM          time of day for the check (default 03:30, local time)
#   --every N           check every N days instead of every day (default 1)
#   --bin PATH          the orchard binary to keep updated (default: `orchard`
#                       on PATH)
#   --restart CMD       command that restarts Orchard after an update, e.g.
#                       "systemctl --user restart orchard" (default: none —
#                       the new version runs from the next restart)
#   --prerelease        follow pre-releases as well
#
# The schedule is a systemd user timer where systemd is available, or a
# crontab line otherwise. Settings live in
# ${XDG_CONFIG_HOME:-~/.config}/orchard/auto-update.conf, a plain shell file
# you can also edit by hand:
#
#   ORCHARD_BIN=/usr/local/bin/orchard
#   ORCHARD_UPDATE_AT=03:30
#   ORCHARD_UPDATE_EVERY_DAYS=1
#   ORCHARD_UPDATE_RESTART="systemctl --user restart orchard"
#   ORCHARD_UPDATE_PRERELEASE=0
#   ORCHARD_UPDATE_REPO=Jishnu-Prasad888/Orchard
#
# An update downloads the release binary for this OS and CPU, checks it
# against the release's SHA256SUMS, makes sure it runs, and only then swaps it
# in. The binary it replaced is kept beside it as `<bin>.previous`, so going
# back is one `mv`. A development build (version "dev") is never replaced.
set -eu

CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/orchard"
CONFIG_FILE="$CONFIG_DIR/auto-update.conf"

# Settings come from the environment first, then the config file, then these
# defaults.
env_bin="${ORCHARD_BIN-}" env_at="${ORCHARD_UPDATE_AT-}" env_every="${ORCHARD_UPDATE_EVERY_DAYS-}"
env_restart="${ORCHARD_UPDATE_RESTART-}" env_pre="${ORCHARD_UPDATE_PRERELEASE-}" env_repo="${ORCHARD_UPDATE_REPO-}"
ORCHARD_BIN=""
ORCHARD_UPDATE_AT="03:30"
ORCHARD_UPDATE_EVERY_DAYS=1
ORCHARD_UPDATE_RESTART=""
ORCHARD_UPDATE_PRERELEASE=0
ORCHARD_UPDATE_REPO="Jishnu-Prasad888/Orchard"
# shellcheck disable=SC1090
if [ -f "$CONFIG_FILE" ]; then . "$CONFIG_FILE"; fi
ORCHARD_BIN="${env_bin:-$ORCHARD_BIN}"
ORCHARD_UPDATE_AT="${env_at:-$ORCHARD_UPDATE_AT}"
ORCHARD_UPDATE_EVERY_DAYS="${env_every:-$ORCHARD_UPDATE_EVERY_DAYS}"
ORCHARD_UPDATE_RESTART="${env_restart:-$ORCHARD_UPDATE_RESTART}"
ORCHARD_UPDATE_PRERELEASE="${env_pre:-$ORCHARD_UPDATE_PRERELEASE}"
ORCHARD_UPDATE_REPO="${env_repo:-$ORCHARD_UPDATE_REPO}"

UNIT="orchard-auto-update"
CRON_MARK="# orchard-auto-update"

log() { printf '%s orchard-update: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
die() {
  log "error: $*" >&2
  exit 1
}

script_path() {
  case "$0" in
    /*) printf '%s\n' "$0" ;;
    *) printf '%s/%s\n' "$(cd "$(dirname "$0")" && pwd)" "$(basename "$0")" ;;
  esac
}

fetch() { # fetch URL [OUTFILE]
  if command -v curl >/dev/null 2>&1; then
    if [ $# -ge 2 ]; then curl -fsSL --retry 3 -o "$2" "$1"; else curl -fsSL --retry 3 "$1"; fi
  elif command -v wget >/dev/null 2>&1; then
    if [ $# -ge 2 ]; then wget -q -O "$2" "$1"; else wget -q -O - "$1"; fi
  else
    die "need curl or wget"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

resolve_bin() {
  if [ -z "$ORCHARD_BIN" ]; then
    ORCHARD_BIN="$(command -v orchard 2>/dev/null || true)"
  fi
  [ -n "$ORCHARD_BIN" ] || die "no orchard binary found; pass --bin PATH or set ORCHARD_BIN in $CONFIG_FILE"
  [ -x "$ORCHARD_BIN" ] || die "$ORCHARD_BIN is not an executable file"
}

installed_version() { "$ORCHARD_BIN" --version 2>/dev/null | head -n 1 || true; }

latest_tag() {
  if [ "$ORCHARD_UPDATE_PRERELEASE" = "1" ]; then
    url="https://api.github.com/repos/$ORCHARD_UPDATE_REPO/releases?per_page=1"
  else
    url="https://api.github.com/repos/$ORCHARD_UPDATE_REPO/releases/latest"
  fi
  fetch "$url" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1
}

# newer A B: succeeds when version A is newer than version B.
newer() {
  a="${1#v}"
  b="${2#v}"
  [ "$a" != "$b" ] && [ "$(printf '%s\n%s\n' "$a" "$b" | sort -V | tail -n 1)" = "$a" ]
}

asset_name() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "no release build for CPU $(uname -m)" ;;
  esac
  case "$os" in
    linux | darwin) printf 'orchard-%s-%s\n' "$os" "$arch" ;;
    *) die "no release build for OS $os" ;;
  esac
}

cmd_check() {
  resolve_bin
  printf 'installed: %s (%s)\n' "$(installed_version)" "$ORCHARD_BIN"
  printf 'latest:    %s (%s)\n' "$(latest_tag)" "$ORCHARD_UPDATE_REPO"
}

cmd_update() {
  resolve_bin
  current="$(installed_version)"
  [ -n "$current" ] || die "$ORCHARD_BIN does not report a version (too old for --version?)"
  if [ "$current" = "dev" ]; then
    log "$ORCHARD_BIN is a development build; leaving it alone"
    return 0
  fi
  tag="$(latest_tag)"
  [ -n "$tag" ] || die "could not find the latest release of $ORCHARD_UPDATE_REPO"
  if ! newer "$tag" "$current"; then
    log "up to date ($current)"
    return 0
  fi

  dir="$(dirname "$ORCHARD_BIN")"
  [ -w "$dir" ] || die "cannot write to $dir; run as a user who can, or install orchard somewhere you own"

  # Only one update at a time (a manual run racing the timer).
  lock="$dir/.orchard-update.lock"
  mkdir "$lock" 2>/dev/null || die "another update is running (remove $lock if not)"
  tmp="$dir/.orchard-update.$$"
  trap 'rm -rf "$lock" "$tmp" "$tmp.sums"' EXIT INT TERM

  asset="$(asset_name)"
  base="https://github.com/$ORCHARD_UPDATE_REPO/releases/download/$tag"
  log "updating $current -> $tag"
  fetch "$base/$asset" "$tmp" || die "download of $asset failed"
  fetch "$base/SHA256SUMS" "$tmp.sums" || die "download of SHA256SUMS failed"
  want="$(awk -v a="$asset" '{ n = $2; sub(/^.*\//, "", n); sub(/^\*/, "", n); if (n == a) print $1 }' "$tmp.sums")"
  [ -n "$want" ] || die "SHA256SUMS has no entry for $asset"
  [ "$(sha256_of "$tmp")" = "$want" ] || die "checksum mismatch for $asset; not installing it"
  chmod 0755 "$tmp"
  got="$("$tmp" --version 2>/dev/null | head -n 1 || true)"
  [ "$got" = "$tag" ] || die "downloaded binary reports version '$got', expected $tag"

  cp -p "$ORCHARD_BIN" "$ORCHARD_BIN.previous"
  mv -f "$tmp" "$ORCHARD_BIN"
  log "installed $tag (the previous binary is at $ORCHARD_BIN.previous)"

  if [ -n "$ORCHARD_UPDATE_RESTART" ]; then
    log "restarting: $ORCHARD_UPDATE_RESTART"
    sh -c "$ORCHARD_UPDATE_RESTART" || die "restart command failed"
  else
    log "restart Orchard to run the new version"
  fi
}

write_config() {
  mkdir -p "$CONFIG_DIR"
  {
    echo "# Written by scripts/auto-update.sh install; edit freely."
    printf 'ORCHARD_BIN=%s\n' "$(quote "$ORCHARD_BIN")"
    printf 'ORCHARD_UPDATE_AT=%s\n' "$(quote "$ORCHARD_UPDATE_AT")"
    printf 'ORCHARD_UPDATE_EVERY_DAYS=%s\n' "$(quote "$ORCHARD_UPDATE_EVERY_DAYS")"
    printf 'ORCHARD_UPDATE_RESTART=%s\n' "$(quote "$ORCHARD_UPDATE_RESTART")"
    printf 'ORCHARD_UPDATE_PRERELEASE=%s\n' "$(quote "$ORCHARD_UPDATE_PRERELEASE")"
    printf 'ORCHARD_UPDATE_REPO=%s\n' "$(quote "$ORCHARD_UPDATE_REPO")"
  } >"$CONFIG_FILE"
}

# quote VALUE: VALUE as a single-quoted shell word.
quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

have_systemd_user() {
  command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1
}

cmd_install() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --at) ORCHARD_UPDATE_AT="${2:?--at needs HH:MM}"; shift 2 ;;
      --every) ORCHARD_UPDATE_EVERY_DAYS="${2:?--every needs a number of days}"; shift 2 ;;
      --bin) ORCHARD_BIN="${2:?--bin needs a path}"; shift 2 ;;
      --restart) ORCHARD_UPDATE_RESTART="${2?--restart needs a command}"; shift 2 ;;
      --prerelease) ORCHARD_UPDATE_PRERELEASE=1; shift ;;
      *) die "unknown option $1 (see the top of this script)" ;;
    esac
  done
  echo "$ORCHARD_UPDATE_AT" | grep -Eq '^([01][0-9]|2[0-3]):[0-5][0-9]$' || die "--at wants HH:MM (24-hour), got $ORCHARD_UPDATE_AT"
  echo "$ORCHARD_UPDATE_EVERY_DAYS" | grep -Eq '^[1-9][0-9]*$' || die "--every wants a whole number of days"
  resolve_bin
  write_config

  self="$(script_path)"
  hour="${ORCHARD_UPDATE_AT%%:*}"
  minute="${ORCHARD_UPDATE_AT##*:}"
  if have_systemd_user; then
    units="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
    mkdir -p "$units"
    cat >"$units/$UNIT.service" <<EOF
[Unit]
Description=Update Orchard to its latest release

[Service]
Type=oneshot
ExecStart=/bin/sh $self update
EOF
    if [ "$ORCHARD_UPDATE_EVERY_DAYS" = "1" ]; then
      when="*-*-* $ORCHARD_UPDATE_AT:00"
    else
      # systemd calendars can't say "every N days"; this is every N days of
      # the month, which comes close.
      when="*-*-1/$ORCHARD_UPDATE_EVERY_DAYS $ORCHARD_UPDATE_AT:00"
    fi
    cat >"$units/$UNIT.timer" <<EOF
[Unit]
Description=Check for a new Orchard release

[Timer]
OnCalendar=$when
Persistent=true
RandomizedDelaySec=10min

[Install]
WantedBy=timers.target
EOF
    systemctl --user daemon-reload
    systemctl --user enable --now "$UNIT.timer"
    log "scheduled with systemd ($when); see: systemctl --user list-timers $UNIT.timer"
    log "logs: journalctl --user -u $UNIT.service"
  elif command -v crontab >/dev/null 2>&1; then
    day="*"
    [ "$ORCHARD_UPDATE_EVERY_DAYS" = "1" ] || day="*/$ORCHARD_UPDATE_EVERY_DAYS"
    line="${minute#0} ${hour#0} $day * * /bin/sh $self update >>$CONFIG_DIR/auto-update.log 2>&1 $CRON_MARK"
    { crontab -l 2>/dev/null | grep -vF "$CRON_MARK" || true; echo "$line"; } | crontab -
    log "scheduled with cron: $line"
  else
    die "neither systemd (user) nor cron is available to schedule updates"
  fi
  log "settings saved to $CONFIG_FILE"
}

cmd_uninstall() {
  removed=0
  units="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  if [ -f "$units/$UNIT.timer" ]; then
    systemctl --user disable --now "$UNIT.timer" 2>/dev/null || true
    rm -f "$units/$UNIT.timer" "$units/$UNIT.service"
    systemctl --user daemon-reload 2>/dev/null || true
    removed=1
  fi
  if command -v crontab >/dev/null 2>&1 && crontab -l 2>/dev/null | grep -qF "$CRON_MARK"; then
    crontab -l 2>/dev/null | grep -vF "$CRON_MARK" | crontab -
    removed=1
  fi
  if [ "$removed" = 1 ]; then log "automatic updates off (settings kept in $CONFIG_FILE)"; else log "no schedule was installed"; fi
}

cmd_config() {
  printf 'config file: %s%s\n' "$CONFIG_FILE" "$([ -f "$CONFIG_FILE" ] || echo ' (not written yet)')"
  printf 'ORCHARD_BIN=%s\n' "${ORCHARD_BIN:-$(command -v orchard 2>/dev/null || echo '(not found)')}"
  printf 'ORCHARD_UPDATE_AT=%s\n' "$ORCHARD_UPDATE_AT"
  printf 'ORCHARD_UPDATE_EVERY_DAYS=%s\n' "$ORCHARD_UPDATE_EVERY_DAYS"
  printf 'ORCHARD_UPDATE_RESTART=%s\n' "${ORCHARD_UPDATE_RESTART:-(none)}"
  printf 'ORCHARD_UPDATE_PRERELEASE=%s\n' "$ORCHARD_UPDATE_PRERELEASE"
  printf 'ORCHARD_UPDATE_REPO=%s\n' "$ORCHARD_UPDATE_REPO"
}

case "${1:-update}" in
  update) cmd_update ;;
  check) cmd_check ;;
  install) shift; cmd_install "$@" ;;
  uninstall) cmd_uninstall ;;
  config) cmd_config ;;
  -h | --help | help) sed -n '2,36p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) die "unknown command $1 (try --help)" ;;
esac
