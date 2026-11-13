# Zen-Cap Project Notes

## Running test

```bash
export FFMPEG_ROOT=/media/jang/home/Deve/zen-cap/ffmpeg8; export PKG_CONFIG_PATH="$FFMPEG_ROOT/lib/pkgconfig"; export CGO_CFLAGS="-I$FFMPEG_ROOT/include"; export CGO_LDFLAGS="-L$FFMPEG_ROOT/lib -Wl,-rpath-link,$FFMPEG_ROOT/lib -Wl,-rpath,\$ORIGIN/ffmpeg8/lib -Wl,-rpath,$FFMPEG_ROOT/lib -Wl,--disable-new-dtags"; go test ./pkg/automation/... 2>&1 | tail -20
```

Those four exports are not optional. Without `-Wl,-rpath` (or an `LD_LIBRARY_PATH`
pointing at `ffmpeg8/lib`) the test binary aborts at load time with
`libavdevice.so.61/62: cannot open shared object file` — the `ffmpeg8` bundle is
built for this tree, and the system FFmpeg is a different soname major.

Live-X tests (`-run Live`) are part of `./pkg/target/...` and `./pkg/automation/...`
and **self-skip when `$DISPLAY` is empty**, so the plain command above is safe on a
headless machine. To run them without disturbing your own pointer or desktop, point
them at a scratch server:

```bash
Xvfb :99 -screen 0 1280x800x24 &
DISPLAY=:99 go test ./pkg/target/... ./pkg/automation/... -run Live -v
```

The tests that need a window manager (EWMH move of a managed window, minimize
verification) skip themselves when `_NET_SUPPORTED` is absent — i.e. under Xvfb.
Run them against your real session once before shipping a window-management change.

## Dev loop (edit → probe)

`./run.sh` is the whole loop: it watches the sources, rebuilds and restarts
`zen-cap service` on change.

- `RUN_WATCH_POLL_MS` (default `1000`) — how often the tree is polled.
- `RUN_WATCH_DELAY_MS` (default `4000`) — debounce after the last change before a
  rebuild starts, so a burst of edits is one build.
- Practically: **edit, wait ~6s, then probe the running service.**
- A build failure keeps the **last good binary running** — check the watcher's
  console for `build failed` rather than trusting that your change is live.
- The watcher excludes `_test.go`, so tests never trigger a restart.
- **Never `go build` by hand** into `./zen-cap`: you will race the watcher over the
  same output file, and a manual build without the rpath flags above produces a
  binary that dies at exec time.

## Coordinates (read before touching capture or input)

A window-scoped run addresses the **client area**: `Screenshot()` grabs the client
drawable (FFmpeg `xcbgrab window_id=`) and input is offset by the client origin
(`TranslateCoordinates(client → root)` in `pkg/target/x11.go`). The decorated
**frame** origin (`DecorGeometry`) is used only by `pkg/display`'s window listing,
which must show what the user sees. Mixing the two is the bug that
`ARCHITECTURE_PLAN_x11-coordinate-and-engine-fixes.md` exists to fix — keep both
sides in client space, and say so in the comment when you change either.
