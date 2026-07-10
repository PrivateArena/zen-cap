package capture

import (
	"fmt"
	"image"
)

// InteractiveAnnotate opens a fullscreen X11 overlay seeded with img, lets
// the user doodle/rect/circle/text-annotate it via NotationState, and
// returns the final image once the user confirms (Enter) or cancels
// (Escape, returns the original img unmodified).
func InteractiveAnnotate(img *image.RGBA, brushThickness uint32, fontScale int) (*image.RGBA, error) {
	// TODO: implement full X11 event loop and window setup using xgbutil.
	return nil, fmt.Errorf("InteractiveAnnotate not yet implemented")
}
