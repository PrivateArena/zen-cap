// [VERIFIED]
package av

import (
	"sync"

	"github.com/asticode/go-astiav"
)

var initOnce sync.Once

// Init initializes the FFmpeg library and registers all input/output devices.
func Init() {
	initOnce.Do(func() {
		astiav.RegisterAllDevices()
	})
}
