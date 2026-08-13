# Zen-Cap Project Notes

## Running test

```bash
export FFMPEG_ROOT=/media/jang/home/Deve/zen-cap/ffmpeg8; export PKG_CONFIG_PATH="$FFMPEG_ROOT/lib/pkgconfig"; export CGO_CFLAGS="-I$FFMPEG_ROOT/include"; export CGO_LDFLAGS="-L$FFMPEG_ROOT/lib -Wl,-rpath-link,$FFMPEG_ROOT/lib -Wl,-rpath,\$ORIGIN/ffmpeg8/lib -Wl,-rpath,$FFMPEG_ROOT/lib -Wl,--disable-new-dtags"; go test ./pkg/automation/... 2>&1 | tail -20
```
