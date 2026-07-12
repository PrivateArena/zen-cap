package overlay

import (
	"time"
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
			ov.render()
		}
	}
}
