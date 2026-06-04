package capture

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/xevent"
)

// NotationState manages live shape, doodle, and text annotations.
type NotationState struct {
	rgbaImg            *image.RGBA
	history            []*image.RGBA
	doodling           bool
	lastDoodleX        int
	lastDoodleY        int
	annoTool           string // "doodle", "rect", "circle"
	textInputActive    bool
	textInputX         int
	textInputY         int
	textInputBuffer    string
	lastRightClickTime time.Time
	fontScale          int
	brushThickness     uint32
}

// NewNotationState initializes a new annotation manager on top of a base image.
func NewNotationState(img *image.RGBA, brushThickness uint32, fontScale int) *NotationState {
	return &NotationState{
		rgbaImg:        img,
		annoTool:       "doodle",
		fontScale:      fontScale,
		brushThickness: brushThickness,
		history:        []*image.RGBA{copyImage(img)},
	}
}

// GetImage returns the current active RGBA image (with annotations burned in).
func (ns *NotationState) GetImage() *image.RGBA {
	return ns.rgbaImg
}

// IsTextInputActive checks if the user is currently typing text.
func (ns *NotationState) IsTextInputActive() bool {
	return ns.textInputActive
}

// IsDoodling checks if the user is currently dragging to draw an annotation.
func (ns *NotationState) IsDoodling() bool {
	return ns.doodling
}

// BrushThickness returns the current brush thickness.
func (ns *NotationState) BrushThickness() uint32 {
	return ns.brushThickness
}

// HandleButtonPress processes right-clicks for starting doodles or enabling text entry.
func (ns *NotationState) HandleButtonPress(ev xevent.ButtonPressEvent, cropDragging bool, currX, currY int) bool {
	if ev.Detail == 3 { // Right Mouse Button -> Annotate/Doodle
		if !cropDragging && !ns.textInputActive {
			now := time.Now()
			if now.Sub(ns.lastRightClickTime) < 300*time.Millisecond {
				// Double Right Click -> Text input mode
				ns.textInputActive = true
				ns.textInputX = int(ev.EventX)
				ns.textInputY = int(ev.EventY)
				ns.textInputBuffer = ""
				return true
			}
			ns.lastRightClickTime = now

			// Normal right-click annotation setup
			ns.doodling = true
			ns.lastDoodleX = int(ev.EventX)
			ns.lastDoodleY = int(ev.EventY)

			// Select shape tool based on modifiers
			if ev.State&xproto.ModMaskShift != 0 {
				ns.annoTool = "rect"
			} else if ev.State&xproto.ModMaskControl != 0 {
				ns.annoTool = "circle"
			} else {
				ns.annoTool = "doodle"
			}
			return true
		}
	}
	return false
}

// HandleButtonRelease finishes drawing a rectangle or circle and saves history.
func (ns *NotationState) HandleButtonRelease(
	xu *xgbutil.XUtil,
	bgPixmapID xproto.Pixmap,
	pinkGCID xproto.Gcontext,
	ev xevent.ButtonReleaseEvent,
) bool {
	if ev.Detail == 3 && ns.doodling {
		ns.doodling = false
		cx := int(ev.EventX)
		cy := int(ev.EventY)
		pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}

		if ns.annoTool == "rect" {
			x1 := int(math.Min(float64(ns.lastDoodleX), float64(cx)))
			y1 := int(math.Min(float64(ns.lastDoodleY), float64(cy)))
			w := int(math.Abs(float64(cx - ns.lastDoodleX)))
			h := int(math.Abs(float64(cy - ns.lastDoodleX))) // Keep aspect aspect-ratio bounds
			_ = h                                             // We want exact bounding box
			h = int(math.Abs(float64(cy - ns.lastDoodleY)))

			if w > 0 && h > 0 {
				// 1. Burn into Go image permanently
				drawRect(ns.rgbaImg, ns.lastDoodleX, ns.lastDoodleY, cx, cy, pinkColorRGBA, int(ns.brushThickness))
				// 2. Burn into X11 background pixmap
				rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
				xproto.PolyRectangle(xu.Conn(), xproto.Drawable(bgPixmapID), pinkGCID, []xproto.Rectangle{rect})
			}
		} else if ns.annoTool == "circle" {
			dx := cx - ns.lastDoodleX
			dy := cy - ns.lastDoodleY
			r := int(math.Sqrt(float64(dx*dx + dy*dy)))
			if r > 0 {
				// 1. Burn into Go image permanently
				drawCircle(ns.rgbaImg, ns.lastDoodleX, ns.lastDoodleY, r, pinkColorRGBA, int(ns.brushThickness))
				// 2. Burn into X11 background pixmap
				arc := xproto.Arc{
					X:      int16(ns.lastDoodleX - r),
					Y:      int16(ns.lastDoodleY - r),
					Width:  uint16(r * 2),
					Height: uint16(r * 2),
					Angle1: 0,
					Angle2: 360 * 64,
				}
				xproto.PolyArc(xu.Conn(), xproto.Drawable(bgPixmapID), pinkGCID, []xproto.Arc{arc})
			}
		}

		// Push state to history after completing doodle, rectangle, or circle annotation!
		ns.history = append(ns.history, copyImage(ns.rgbaImg))
		return true
	}
	return false
}

// HandleMotionNotify handles freehand drawing when doodling.
func (ns *NotationState) HandleMotionNotify(
	xu *xgbutil.XUtil,
	bgPixmapID xproto.Pixmap,
	pinkGCID xproto.Gcontext,
	ev xevent.MotionNotifyEvent,
) bool {
	if ns.doodling {
		cx := int(ev.EventX)
		cy := int(ev.EventY)

		if ns.annoTool == "doodle" {
			// 1. Draw line in background pixmap using X11 Neon Pink GC
			xproto.PolyLine(
				xu.Conn(),
				xproto.CoordModeOrigin,
				xproto.Drawable(bgPixmapID),
				pinkGCID,
				[]xproto.Point{
					{X: int16(ns.lastDoodleX), Y: int16(ns.lastDoodleY)},
					{X: int16(cx), Y: int16(cy)},
				},
			)

			// 2. Draw line on Go Image to burn the annotation into the final capture
			pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}
			drawLine(ns.rgbaImg, ns.lastDoodleX, ns.lastDoodleY, cx, cy, pinkColorRGBA, int(ns.brushThickness))

			ns.lastDoodleX = cx
			ns.lastDoodleY = cy
			return true
		} else {
			// Shape tools (rect / circle): just update preview coordinates
			return true
		}
	}
	return false
}

// HandleKeyPress manages text typing, backspace, committing, undos, and scaling changes.
func (ns *NotationState) HandleKeyPress(
	xu *xgbutil.XUtil,
	bgPixmapID xproto.Pixmap,
	gcID xproto.Gcontext,
	depth byte,
	screenWidth, screenHeight int,
	pinkGCID xproto.Gcontext,
	keyStr string,
	keycode xproto.Keycode,
	mods uint16,
) (handled bool, redraw bool) {
	if ns.textInputActive {
		// Check for Enter / Return
		if keyStr == "\r" || keyStr == "\n" || keyStr == "Return" || keyStr == "Enter" {
			// Commit text!
			if len(ns.textInputBuffer) > 0 {
				pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}
				// Burn into Go image permanently with fontScale
				drawHUDTextScaled(ns.rgbaImg, ns.textInputBuffer, ns.textInputX, ns.textInputY, pinkColorRGBA, color.Black, ns.fontScale)
				// Push to history before uploading!
				ns.history = append(ns.history, copyImage(ns.rgbaImg))
				// Redraw background pixmap to include the committed text
				bgra := imageToBGRA(ns.rgbaImg)
				uploadImageChunked(xu, xproto.Drawable(bgPixmapID), gcID, depth, screenWidth, screenHeight, bgra)
			}
			ns.textInputActive = false
			ns.textInputBuffer = ""
			return true, true
		}

		// Check for Escape (Cancel typing)
		if keyStr == "Escape" {
			ns.textInputActive = false
			ns.textInputBuffer = ""
			return true, true
		}

		// Check for Backspace
		if keyStr == "BackSpace" || keycode == 22 {
			if len(ns.textInputBuffer) > 0 {
				ns.textInputBuffer = ns.textInputBuffer[:len(ns.textInputBuffer)-1]
			}
			return true, true
		}

		// Capture printable characters
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			ns.textInputBuffer += keyStr
		}
		return true, true
	}

	// --- Normal Mode keys ---

	// Undo: Ctrl+Z
	if (keyStr == "z" || keyStr == "Z") && (mods&xproto.ModMaskControl != 0) {
		if len(ns.history) > 1 {
			ns.history = ns.history[:len(ns.history)-1]
			ns.rgbaImg = copyImage(ns.history[len(ns.history)-1])
			// Sync to background X11 pixmap
			bgra := imageToBGRA(ns.rgbaImg)
			uploadImageChunked(xu, xproto.Drawable(bgPixmapID), gcID, depth, screenWidth, screenHeight, bgra)
			return true, true
		}
		return true, false
	}

	// Adjust brush thickness and font scale: Ctrl+Plus or Ctrl+Minus
	if (keyStr == "equal" || keyStr == "plus" || keyStr == "+") && (mods&xproto.ModMaskControl != 0) {
		ns.fontScale++
		ns.brushThickness += 2
		// Update X11 GC line width
		xproto.ChangeGC(xu.Conn(), pinkGCID, xproto.GcLineWidth, []uint32{ns.brushThickness})
		return true, true
	}
	if (keyStr == "minus" || keyStr == "hyphen" || keyStr == "-") && (mods&xproto.ModMaskControl != 0) {
		if ns.fontScale > 1 {
			ns.fontScale--
		}
		if ns.brushThickness > 2 {
			ns.brushThickness -= 2
		}
		// Update X11 GC line width
		xproto.ChangeGC(xu.Conn(), pinkGCID, xproto.GcLineWidth, []uint32{ns.brushThickness})
		return true, true
	}

	return false, false
}

// DrawPreview draws transient shapes, circles, and active text entry indicators.
func (ns *NotationState) DrawPreview(
	xu *xgbutil.XUtil,
	bufPixmapID xproto.Pixmap,
	pinkGCID xproto.Gcontext,
	gcID xproto.Gcontext,
	depth byte,
	currX, currY int,
) {
	// Draw preview of active shape drawing
	if ns.doodling && (ns.annoTool == "rect" || ns.annoTool == "circle") {
		x1 := int(math.Min(float64(ns.lastDoodleX), float64(currX)))
		y1 := int(math.Min(float64(ns.lastDoodleY), float64(currY)))
		w := int(math.Abs(float64(currX - ns.lastDoodleX)))
		h := int(math.Abs(float64(currY - ns.lastDoodleY)))

		if w > 0 && h > 0 {
			if ns.annoTool == "rect" {
				rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
				xproto.PolyRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), pinkGCID, []xproto.Rectangle{rect})
			} else if ns.annoTool == "circle" {
				dx := currX - ns.lastDoodleX
				dy := currY - ns.lastDoodleY
				r := int(math.Sqrt(float64(dx*dx + dy*dy)))
				if r > 0 {
					arc := xproto.Arc{
						X:      int16(ns.lastDoodleX - r),
						Y:      int16(ns.lastDoodleY - r),
						Width:  uint16(r * 2),
						Height: uint16(r * 2),
						Angle1: 0,
						Angle2: 360 * 64,
					}
					xproto.PolyArc(xu.Conn(), xproto.Drawable(bufPixmapID), pinkGCID, []xproto.Arc{arc})
				}
			}
		}
	}

	// Draw real-time blinking text input box with scale adjustment
	if ns.textInputActive {
		cursor := "_"
		if time.Now().UnixNano()/500000000%2 == 0 {
			cursor = " "
		}
		textToShow := ns.textInputBuffer + cursor
		textW := len(textToShow)*6*ns.fontScale + 6
		textH := 7*ns.fontScale + 4
		textImg := image.NewRGBA(image.Rect(0, 0, textW, textH))
		pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}

		for dy := 0; dy < textH; dy++ {
			for dx := 0; dx < textW; dx++ {
				textImg.Set(dx, dy, color.Black)
			}
		}
		drawStringScaled(textImg, textToShow, 3, 2, pinkColorRGBA, ns.fontScale)

		textBGRA := imageToBGRA(textImg)
		xproto.PutImage(
			xu.Conn(),
			xproto.ImageFormatZPixmap,
			xproto.Drawable(bufPixmapID),
			gcID,
			uint16(textW),
			uint16(textH),
			int16(ns.textInputX),
			int16(ns.textInputY),
			0,
			depth,
			textBGRA,
		)
	}
}
