#!/bin/sh

PKG_CONFIG_PATH=/media/jang/home/Deve/zen-cap/ffmpeg8/lib/pkgconfig CGO_CFLAGS="-I/media/jang/home/Deve/zen-cap/ffmpeg8/include" CGO_LDFLAGS="-L/media/jang/home/Deve/zen-cap/ffmpeg8/lib -Wl,-rpath,'\$ORIGIN/ffmpeg8/lib' -Wl,-rpath,/media/jang/home/Deve/zen-cap/ffmpeg8/lib -Wl,--disable-new-dtags" go build -o zen-cap .