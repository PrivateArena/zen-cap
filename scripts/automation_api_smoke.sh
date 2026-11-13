#!/usr/bin/env bash
# Smoke test for the /automation HTTP API (plan step A14).
#
# NOT `set -e`: every step must run so the summary at the end is a real report.
# Failures are counted and the exit status is non-zero if any step failed.
#
#	BASE=http://localhost:4444 bash scripts/automation_api_smoke.sh
#
# Steps that inject input (click, scroll) do run — point BASE at a daemon whose
# DISPLAY is a scratch Xvfb when you do not want them on your own desktop:
#
#	Xvfb :99 -screen 0 1280x800x24 &
#	DISPLAY=:99 LD_LIBRARY_PATH=$PWD/ffmpeg8/lib ./zen-cap service &
#	BASE=http://localhost:4444 bash scripts/automation_api_smoke.sh
set -uo pipefail

BASE="${BASE:-http://localhost:4444}"
LAST_STEP="startup"
CODE=000
BODY=

if ! command -v jq >/dev/null 2>&1; then
	echo "FAIL: jq is required (every response in this script is parsed with it)"
	exit 1
fi

# fail <what broke> [response body]. The first failure is the interesting one, so it
# prints the offending body and stops here, naming the step that broke: that name is
# what makes a red run actionable.
fail() {
	echo "FAIL: [$LAST_STEP] $1"
	[ -n "${2:-}" ] && echo "        response: $2"
	exit 1
}

# req METHOD PATH [JSON] -> sets $CODE (HTTP status) and $BODY.
# Both have to be globals: `body=$(req …)` would run the call in a subshell, and
# the status of the last request is the one thing every step needs to see.
req() {
	local method="$1" path="$2" data="${3:-}"
	local tmp
	tmp=$(mktemp)
	if [ -n "$data" ]; then
		CODE=$(curl -sS -o "$tmp" -w '%{http_code}' -X "$method" -H 'Content-Type: application/json' \
			--max-time 120 "$BASE$path" -d "$data" 2>/dev/null) || CODE=000
	else
		CODE=$(curl -sS -o "$tmp" -w '%{http_code}' -X "$method" --max-time 120 "$BASE$path" 2>/dev/null) || CODE=000
	fi
	BODY=$(cat "$tmp")
	rm -f "$tmp"
}

pass() { echo "ok:   $1"; }

# ---------------------------------------------------------------------------
# 0. Readiness. ./run.sh restarts the daemon on every save, so "is it up yet"
#    is a question the script has to keep asking rather than assuming.
# ---------------------------------------------------------------------------
ready=""
for _ in $(seq 1 30); do
	req GET /automation/actions
	body=$BODY
	if [ "$CODE" = "200" ] && printf '%s' "$body" | jq -e '.actions | length > 0' >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 1
done
if [ -z "$ready" ]; then
	echo "FAIL: $BASE/automation/actions never answered (is 'zen-cap service' running on X11?)"
	exit 1
fi
pass "daemon is up; $(printf '%s' "$body" | jq -r '.actions | length') actions published"

# ---------------------------------------------------------------------------
# 1. Enumerate windows and pick one to work on.
# ---------------------------------------------------------------------------
LAST_STEP="GET /automation/windows"
req GET /automation/windows
body=$BODY
[ "$CODE" = "200" ] || fail "windows listing returned HTTP $CODE" "$body"

# A window smaller than a pointer target cannot be clicked or captured sensibly
# (an unmapped or 1x1 client also makes FFmpeg's window grab fail), so prefer the
# focused window, then any sized window, then work screen-scoped.
WIN=$(printf '%s' "$body" | jq -r '
  def sized: .geometry.w >= 8 and .geometry.h >= 8;
  ((.active_window_id // -1)) as $a
  | ([.windows[] | select(sized) | select(.id == $a)] | if length > 0 then .[0].id else empty end)
  // ([.windows[] | select(sized)] | if length > 0 then .[0].id else empty end)
  // empty')
pass "$(printf '%s' "$body" | jq -r '.count') window(s) listed, target id=${WIN:-<none: screen only>}"

# Window ids come back as decimal numbers, and the API accepts them as decimal or
# 0x… strings, so they can be threaded straight into the next request.
SEL=""
if [ -n "$WIN" ]; then
	SEL="\"window_id\":\"$WIN\","
fi

# ---------------------------------------------------------------------------
# 2. Capture, screen-scoped: the one thing that works on any X server.
# ---------------------------------------------------------------------------
LAST_STEP="POST /automation/screenshot"
shot=/tmp/zcap_smoke_1.png
rm -f "$shot"
req POST /automation/screenshot "$(printf '{"output":"%s"}' "$shot")"
body=$BODY
[ "$CODE" = "200" ] || fail "screenshot returned HTTP $CODE" "$body"
[ -s "$shot" ] || fail "screenshot did not write a non-empty $shot" "$body"
pass "captured $(printf '%s' "$body" | jq -r '.width')x$(printf '%s' "$body" | jq -r '.height') -> $shot"

# ---------------------------------------------------------------------------
# 3. Read back window state (needs a window target).
# ---------------------------------------------------------------------------
if [ -n "$WIN" ]; then
	LAST_STEP="exec window_state"
	req POST /automation/exec "{\"window_id\":\"$WIN\",\"steps\":[{\"action\":\"window_state\"}]}"
	body=$BODY
	[ "$CODE" = "200" ] || fail "window_state returned HTTP $CODE" "$body"
	w=$(printf '%s' "$body" | jq -r '.steps[0].data.geometry.w // 0')
	[ "$w" -gt 0 ] || fail "window_state reported no geometry: $body"
	pass "window 0x$(printf '%x' "$WIN") is ${w}px wide, title=$(printf '%s' "$body" | jq -r '.window_title')"
else
	echo "skip: window_state (no window on this display)"
fi

# ---------------------------------------------------------------------------
# 4. A click, then a capture: the write path plus the trace shape.
# ---------------------------------------------------------------------------
LAST_STEP="exec click+screenshot"
req POST /automation/exec "{$SEL\"steps\":[{\"action\":\"click\",\"x\":2,\"y\":2,\"no_activate\":false},{\"action\":\"screenshot\",\"output\":\"/tmp/zcap_smoke_2.png\"}]}"
body=$BODY
[ "$CODE" = "200" ] || fail "click+screenshot returned HTTP $CODE" "$body"
printf '%s' "$body" | jq -e '.ok == true and (.steps | length) == 2 and .steps[0].ok and .steps[1].ok' >/dev/null || fail "unexpected exec result: $body"
[ -s /tmp/zcap_smoke_2.png ] || fail "the screenshot step wrote nothing"
[ "$(printf '%s' "$body" | jq -r '.steps[0].x')" = "2" ] || fail "the click step did not report its coordinates: $body"
pass "2 steps executed, run_id=$(printf '%s' "$body" | jq -r .run_id), $(printf '%s' "$body" | jq -r '.steps | length') trace entries"

# ---------------------------------------------------------------------------
# 5. Scroll (the action added by this plan).
# ---------------------------------------------------------------------------
LAST_STEP="exec scroll"
req POST /automation/exec "{$SEL\"steps\":[{\"action\":\"scroll\",\"direction\":\"down\",\"amount\":3,\"x\":40,\"y\":40}]}"
body=$BODY
[ "$CODE" = "200" ] || fail "scroll returned HTTP $CODE" "$body"
pass "scroll accepted"

# ---------------------------------------------------------------------------
# 6. The Phase 0 regression tripwire: a wait for something that will never
#    appear must come back as an error, quickly — never as an unbounded loop.
# ---------------------------------------------------------------------------
LAST_STEP="exec wait_until (must time out)"
start=$(date +%s%N)
req POST /automation/exec "{\"steps\":[{\"action\":\"wait_until\",\"wait_for\":\"window\",\"window\":{\"title\":\"definitely-not-a-window\"},\"timeout\":\"1s\"}]}"
body=$BODY
elapsed_ms=$((($(date +%s%N) - start) / 1000000))
case "$CODE" in
2*) fail "a timed-out wait_until answered HTTP $CODE; it must be an error" "$body" ;;
esac
if [ "$elapsed_ms" -gt 5000 ]; then
	fail "wait_until took ${elapsed_ms}ms for a 1s timeout: the poll loop is unbounded" "$body"
fi
pass "wait_until failed in ${elapsed_ms}ms with code=$(printf '%s' "$body" | jq -r '.code')"

# ---------------------------------------------------------------------------
# 7. Abort: must answer even with nothing running, and never queue behind a run.
# ---------------------------------------------------------------------------
LAST_STEP="POST /automation/abort"
req POST /automation/abort '{}'
body=$BODY
[ "$CODE" = "200" ] || fail "abort returned HTTP $CODE" "$body"
aborted=$(printf '%s' "$body" | jq -r '.aborted')
[ "$aborted" -ge 0 ] 2>/dev/null || fail "abort did not report a count" "$body"
pass "abort answered (aborted=$aborted, released_buttons=$(printf '%s' "$body" | jq -r .released_buttons))"

# ---------------------------------------------------------------------------
# 8. State: cheap, and never blocked by the gate.
# ---------------------------------------------------------------------------
LAST_STEP="GET /automation/state"
req GET /automation/state
body=$BODY
[ "$CODE" = "200" ] || fail "state returned HTTP $CODE" "$body"
printf '%s' "$body" | jq -e '.max_run_seconds > 0 and (.screens | length) > 0' >/dev/null || fail "state is incomplete: $body"
pass "state: display=$(printf '%s' "$body" | jq -r .display) deny_api_input=$(printf '%s' "$body" | jq -r .deny_api_input)"

echo
echo "automation API smoke: PASS"
exit 0
