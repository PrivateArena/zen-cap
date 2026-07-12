package overlay

import (
	"time"

	"github.com/jezek/xgb/xproto"
)

func (ov *X11Overlay) runRenderLoop() {
	defer ov.wg.Done()
	ticker := time.NewTicker(time.Second / time.Duration(ov.cfg.TargetFPS))
	defer ticker.Stop()

	for {
		select {
		case <-ov.stop:
			return
		case <-ticker.C:
			if !ov.ann.IsDirty() {
				continue
			}
			composite := ov.ann.GetComposite()
			ov.ann.ClearDirty()
			bgra := imageToBGRA(composite)
			uploadImageChunked(ov.xu, xproto.Drawable(ov.bufPix), ov.gc, ov.depth,
				ov.cfg.Width, ov.cfg.Height, bgra)
			xproto.CopyArea(ov.xu.Conn(),
				xproto.Drawable(ov.bufPix),
				xproto.Drawable(ov.win),
				ov.gc, 0, 0, 0, 0,
				uint16(ov.cfg.Width), uint16(ov.cfg.Height),
			)
		}
	}
}
