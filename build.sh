#!/bin/sh
# zen-cap build script.
#
# zen-cap links against a private FFmpeg 8 build (see pkg/av) so the binary is
# self-contained and does NOT depend on the distro's FFmpeg libraries. By
# default the bundle is expected next to this script at ./ffmpeg8, but the
# location can be overridden with the ZEN_CAP_FFMPEG environment variable.
#
#   ZEN_CAP_FFMPEG=/path/to/ffmpeg8 ./build.sh
#
# The ffmpeg8/ directory layout must contain:
#   ffmpeg8/include/pkgconfig/...   (headers used at compile time)
#   ffmpeg8/lib/pkgconfig/...       (pkg-config .pc files)
#   ffmpeg8/lib/*.so                (shared libs shipped next to the binary)
#
# ABI coupling (review M2): go-astiav v0.41.0 is a cgo binding generated
# against a specific FFmpeg API version. The private bundle's headers/libraries
# MUST stay compatible with that pinned go-astiav version. Do not swap the
# bundle for a distro ffmpeg-dev package without checking ABI compatibility.
#
# Decision (C3): we keep the bundled-FFmpeg approach (option "a" in the
# review) rather than switching to static linking or the distro's ffmpeg-dev.
# This preserves exact codec/version control while the fixed $ORIGIN rpath
# makes the bundle relocatable. --disable-new-dtags is kept deliberately so the
# bundled libs win over any system FFmpeg (DT_RPATH), unoverridable by
# LD_LIBRARY_PATH.
#
# Minimum Go version: 1.24.0 (go.mod). Older-Go distros (Ubuntu 22.04/Debian 12)
# cannot rebuild this module (review M7); ship the prebuilt binary instead.

set -e

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
go build -o zen-cap .