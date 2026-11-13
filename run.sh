#!/usr/bin/env bash
set -euo pipefail

BIN="${BIN:-zen-cap}"
PKG="${PKG:-.}"
BUILD_TAGS="${BUILD_TAGS:-fts5}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
FFMPEG_ROOT="${ZEN_CAP_FFMPEG:-$SCRIPT_DIR/ffmpeg8}"

if [ ! -d "$FFMPEG_ROOT/include" ] || [ ! -d "$FFMPEG_ROOT/lib/pkgconfig" ]; then
	echo "error: FFmpeg bundle not found at '$FFMPEG_ROOT'" >&2
	echo "Set ZEN_CAP_FFMPEG to the ffmpeg8 directory, or run build.sh from the repo root." >&2
	exit 1
fi

PKG_CONFIG_PATH="$FFMPEG_ROOT/lib/pkgconfig" \
CGO_CFLAGS="-I$FFMPEG_ROOT/include" \
CGO_LDFLAGS="-L$FFMPEG_ROOT/lib -Wl,-rpath,\$ORIGIN/ffmpeg8/lib -Wl,-rpath,$FFMPEG_ROOT/lib -Wl,--disable-new-dtags" \

ARGS=()
if [[ "${1:-}" == "--" ]]; then
  shift
  ARGS=("$@")
fi

INCLUDE_EXT=(go mod sum yaml yml toml)
EXCLUDE_DIR=(assets tmp npm vendor build frontend/node_modules internal/cfg/data .git)
EXCLUDE_FILE=(yaml)
EXCLUDE_REGEX=(_test\.go)
DELAY_MS="${RUN_WATCH_DELAY_MS:-4000}"
POLL_MS="${RUN_WATCH_POLL_MS:-1000}"

if [[ -n "${RUN_WATCH_INCLUDE_EXT:-}" ]]; then IFS=' ,' read -r -a INCLUDE_EXT <<< "$RUN_WATCH_INCLUDE_EXT"; fi
if [[ -n "${RUN_WATCH_EXCLUDE_DIR:-}" ]]; then IFS=' ,' read -r -a EXCLUDE_DIR <<< "$RUN_WATCH_EXCLUDE_DIR"; fi
if [[ -n "${RUN_WATCH_EXCLUDE_FILE:-}" ]]; then IFS=' ,' read -r -a EXCLUDE_FILE <<< "$RUN_WATCH_EXCLUDE_FILE"; fi
if [[ -n "${RUN_WATCH_EXCLUDE_REGEX:-}" ]]; then IFS=' ,' read -r -a EXCLUDE_REGEX <<< "$RUN_WATCH_EXCLUDE_REGEX"; fi

NAME_ARGS=()
for ext in "${INCLUDE_EXT[@]}"; do NAME_ARGS+=( -o -name "*.$ext" ); done
if [[ "${#NAME_ARGS[@]}" -eq 0 ]]; then NAME_ARGS=( -o -name '*' ); fi
NAME_ARGS=( "${NAME_ARGS[@]:1}" )

EXCLUDE_ARGS=()
for d in "${EXCLUDE_DIR[@]}"; do EXCLUDE_ARGS+=( ! -path "./$d/*" ); done
for f in "${EXCLUDE_FILE[@]}"; do EXCLUDE_ARGS+=( ! -name "$f" ); done
for re in "${EXCLUDE_REGEX[@]}"; do EXCLUDE_ARGS+=( ! -regex ".*$re.*" ); done

src_hash() {
  find . -type f \( "${NAME_ARGS[@]}" \) "${EXCLUDE_ARGS[@]}" \
    -print0 2>/dev/null | sort -z | xargs -0 stat --format='%Y %n' 2>/dev/null | md5sum | cut -d' ' -f1
}

# --- Preserve the script's original stdin/stdout/stderr on separate fds.
# THIS IS THE FIX: bash redirects an async command's (`cmd &`) stdin to
# /dev/null automatically when job control is off (true inside any script),
# no matter what fd 0 currently is. stdout/stderr are NOT affected by this
# rule, only stdin — which is why output looked fine in earlier attempts
# but the terminal commander never received input. We capture real stdin
# as fd 5 here, then explicitly attach the backgrounded server to it.
exec 3>&1 4>&2 5<&0

SERVER_PID=""
SERVER_DEAD_AT=""

start_server() {
  echo "🚀 Starting ${BIN} service ${ARGS[*]}" >&3
  if [[ -r /dev/tty ]] && [[ -t 0 || -e /dev/tty ]]; then
    "./${BIN}" service "${ARGS[@]}" </dev/tty >&3 2>&4 &
  else
    "./${BIN}" service "${ARGS[@]}" <&5 >&3 2>&4 &
  fi
  SERVER_PID=$!
  SERVER_DEAD_AT=""
}

stop_server() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "⏹  Stopping ${BIN} (pid ${SERVER_PID})" >&3
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    for i in $(seq 1 30); do
      kill -0 "$SERVER_PID" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$SERVER_PID" 2>/dev/null; then
      echo "⚠️  ${BIN} did not exit after 3s — sending SIGKILL" >&3
      kill -9 "$SERVER_PID" 2>/dev/null || true
      for i in $(seq 1 20); do
        kill -0 "$SERVER_PID" 2>/dev/null || break
        sleep 0.1
      done
      if kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "🚨 ${BIN} is zombie — killing run.sh self" >&3
        kill -TERM "$$" 2>/dev/null || true
        sleep 1
        kill -9 "$$" 2>/dev/null || true
        return
      fi
    fi
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  SERVER_PID=""
  SERVER_DEAD_AT=""
}

build_and_run() {
  SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
  export FFMPEG_ROOT="${ZEN_CAP_FFMPEG:-$SCRIPT_DIR/ffmpeg8}"

  if [ ! -d "$FFMPEG_ROOT/include" ] || [ ! -d "$FFMPEG_ROOT/lib/pkgconfig" ]; then
    echo "error: FFmpeg bundle not found at '$FFMPEG_ROOT'" >&2
    echo "Set ZEN_CAP_FFMPEG to the ffmpeg8 directory, or run build.sh from the repo root." >&2
    exit 1
  fi

  export PKG_CONFIG_PATH="$FFMPEG_ROOT/lib/pkgconfig"
  export CGO_CFLAGS="-I$FFMPEG_ROOT/include"
  export CGO_LDFLAGS="-L$FFMPEG_ROOT/lib -Wl,-rpath,\$ORIGIN/ffmpeg8/lib -Wl,-rpath,$FFMPEG_ROOT/lib -Wl,--disable-new-dtags"
  echo "🔨 Building ${BIN}..." >&3
  if ! go build -o "${BIN}.new" "${PKG}"; then
    echo "❌ Build failed — keeping the last good binary running" >&4
    rm -f "${BIN}.new"
    return
  fi
  mv -f "${BIN}.new" "${BIN}"
  echo "✅ Build complete" >&3
  stop_server
  start_server
}

if [[ "${WATCH:-1}" == "0" ]]; then
  go build -o "${BIN}" "${PKG}"
  exec "./${BIN}" "${ARGS[@]}"
fi

if [[ ! -f config.json ]]; then
  echo "❌ config.json not found — run this from the project root" >&2
  exit 1
fi

printf '%s\n' \
  "👀 Watching for changes (Ctrl+C to stop) — poll ${POLL_MS}ms, debounce ${DELAY_MS}ms" \
  "   include_ext : ${INCLUDE_EXT[*]}" \
  "   exclude_dir : ${EXCLUDE_DIR[*]}" \
  "   exclude_file: ${EXCLUDE_FILE[*]}" \
  "   exclude_regex: ${EXCLUDE_REGEX[*]}" \
  >&3

cleanup() {
  stop_server
  exit 0
}
trap cleanup INT TERM EXIT

build_and_run
LAST_HASH="$(src_hash)"

while true; do
  if [[ -n "${SERVER_PID:-}" ]] && ! kill -0 "$SERVER_PID" 2>/dev/null; then
    if [[ -z "${SERVER_DEAD_AT:-}" ]]; then
      SERVER_DEAD_AT=$(date +%s)
      echo "⚠️  ${BIN} died unexpectedly (pid ${SERVER_PID})" >&3
    else
      now=$(date +%s)
      if (( now - SERVER_DEAD_AT > 10 )); then
        echo "🚨 ${BIN} dead for >10s — killing run.sh" >&3
        kill -TERM "$$" 2>/dev/null || true
        sleep 1
        kill -9 "$$" 2>/dev/null || true
      fi
    fi
  fi
  HASH="$(src_hash)"
  if [[ "$HASH" != "$LAST_HASH" ]]; then
    echo "" >&3
    echo "🔍 Change detected" >&3
    sleep "$(( (DELAY_MS + 999) / 1000 ))"
    HASH="$(src_hash)"
    if [[ "$HASH" != "$LAST_HASH" ]]; then
      LAST_HASH="$HASH"
      build_and_run
    fi
  fi
  sleep "$(( (POLL_MS + 999) / 1000 ))"
done