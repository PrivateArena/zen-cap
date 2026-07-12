package annotation

import (
	"image"
	"image/color"
	"image/draw"
	"sync"
	"sync/atomic"
	"time"
)

type Annotator struct {
	mu    sync.Mutex
	base  *image.RGBA
	layer *image.RGBA
	log   UndoLog
	tool  Tool
	cfg   Config
	dirty atomic.Bool

	doodling       bool
	doodleStart    image.Point
	doodleLast     image.Point
	doodlePoints   []image.Point
	textActive     bool
	textAnchor     image.Point
	textBuffer     string
	lastRightClick time.Time
}

func NewAnnotator(base *image.RGBA, cfg Config) *Annotator {
	w, h := 0, 0
	if base != nil {
		b := base.Bounds()
		w, h = b.Dx(), b.Dy()
	}
	layer := image.NewRGBA(image.Rect(0, 0, w, h))
	return &Annotator{
		base:  base,
		layer: layer,
		cfg:   cfg,
	}
}

func (a *Annotator) HandleEvent(ev InputEvent) (consumed bool, needsRedraw bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch ev.Kind {
	case Press:
		return a.handlePress(ev)
	case Release:
		return a.handleRelease(ev)
	case Motion:
		return a.handleMotion(ev)
	case Key:
		return a.handleKey(ev)
	}
	return false, false
}

func (a *Annotator) handlePress(ev InputEvent) (bool, bool) {
	if ev.Button == 3 {
		now := time.Now()
		if now.Sub(a.lastRightClick) < 300*time.Millisecond {
			a.textActive = true
			a.textAnchor = image.Point{X: ev.X, Y: ev.Y}
			a.textBuffer = ""
			a.dirty.Store(true)
			return true, true
		}
		a.lastRightClick = now

		a.doodling = true
		a.doodleStart = image.Point{X: ev.X, Y: ev.Y}
		a.doodleLast = a.doodleStart
		a.doodlePoints = []image.Point{a.doodleStart}

		if ev.Mods&0x01 != 0 {
			a.tool = Rect
		} else if ev.Mods&0x04 != 0 {
			a.tool = Circle
		} else {
			a.tool = Doodle
		}
		return true, false
	}
	return false, false
}

func (a *Annotator) handleRelease(ev InputEvent) (bool, bool) {
	if ev.Button == 3 && a.doodling {
		a.doodling = false
		endPt := image.Point{X: ev.X, Y: ev.Y}
		a.doodlePoints = append(a.doodlePoints, endPt)

		cmd := &StrokeCmd{
			Points: a.doodlePoints,
			Color:  a.cfg.Color,
			Thick:  int(a.cfg.BrushThickness),
			Tool:   a.tool,
		}
		cmd.apply(a.layer, a.cfg)
		a.log.Push(cmd)
		a.dirty.Store(true)
		return true, true
	}
	return false, false
}

func (a *Annotator) handleMotion(ev InputEvent) (bool, bool) {
	if a.doodling {
		pt := image.Point{X: ev.X, Y: ev.Y}
		a.doodlePoints = append(a.doodlePoints, pt)
		if a.tool == Doodle {
			pink := a.cfg.Color
			drawLine(a.layer, a.doodleLast.X, a.doodleLast.Y, pt.X, pt.Y, pink, int(a.cfg.BrushThickness))
			a.dirty.Store(true)
		}
		a.doodleLast = pt
		return true, true
	}
	return false, false
}

func (a *Annotator) handleKey(ev InputEvent) (bool, bool) {
	if a.textActive {
		switch ev.KeyStr {
		case "\r", "\n", "Return", "Enter":
			if len(a.textBuffer) > 0 {
				cmd := &TextCmd{
					Anchor:    a.textAnchor,
					Text:      a.textBuffer,
					Color:     a.cfg.Color,
					FontScale: a.cfg.FontScale,
				}
				cmd.apply(a.layer, a.cfg)
				a.log.Push(cmd)
				a.dirty.Store(true)
			}
			a.textActive = false
			a.textBuffer = ""
			return true, true
		case "Escape":
			a.textActive = false
			a.textBuffer = ""
			return true, true
		case "BackSpace":
			if len(a.textBuffer) > 0 {
				a.textBuffer = a.textBuffer[:len(a.textBuffer)-1]
			}
			return true, true
		default:
			if ev.Keycode == 22 {
				if len(a.textBuffer) > 0 {
					a.textBuffer = a.textBuffer[:len(a.textBuffer)-1]
				}
				return true, true
			}
			if len(ev.KeyStr) == 1 && ev.KeyStr[0] >= 32 && ev.KeyStr[0] <= 126 {
				a.textBuffer += ev.KeyStr
			}
			return true, true
		}
	}

	if (ev.KeyStr == "z" || ev.KeyStr == "Z") && (ev.Mods&0x04 != 0) {
		a.undo()
		return true, true
	}

	if (ev.KeyStr == "equal" || ev.KeyStr == "plus" || ev.KeyStr == "+") && (ev.Mods&0x04 != 0) {
		a.cfg.FontScale++
		a.cfg.BrushThickness += 2
		return true, true
	}
	if (ev.KeyStr == "minus" || ev.KeyStr == "hyphen" || ev.KeyStr == "-") && (ev.Mods&0x04 != 0) {
		if a.cfg.FontScale > 1 {
			a.cfg.FontScale--
		}
		if a.cfg.BrushThickness > 2 {
			a.cfg.BrushThickness -= 2
		}
		return true, true
	}

	return false, false
}

func (a *Annotator) undo() {
	cmd := a.log.Pop()
	if cmd == nil {
		return
	}
	for i := range a.layer.Pix {
		a.layer.Pix[i] = 0
	}
	a.log.Replay(a.layer, a.cfg)
	a.dirty.Store(true)
}

func (a *Annotator) GetLayer() *image.RGBA {
	a.mu.Lock()
	defer a.mu.Unlock()
	dst := image.NewRGBA(a.layer.Bounds())
	copy(dst.Pix, a.layer.Pix)
	return dst
}

func (a *Annotator) GetComposite() *image.RGBA {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := a.layer.Bounds()
	dst := image.NewRGBA(b)
	if a.base != nil {
		draw.Draw(dst, b, a.base, b.Min, draw.Src)
	}
	draw.Draw(dst, b, a.layer, b.Min, draw.Over)
	return dst
}

func (a *Annotator) IsDirty() bool {
	return a.dirty.Load()
}

func (a *Annotator) ClearDirty() {
	a.dirty.Store(false)
}

func (a *Annotator) Config() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *Annotator) SetColor(c color.RGBA) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Color = c
}

func (a *Annotator) Undo() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.undo()
}

func (a *Annotator) IsDoodling() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.doodling
}

func (a *Annotator) IsTextActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.textActive
}

func (a *Annotator) TextBuffer() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.textBuffer
}

func (a *Annotator) TextAnchor() image.Point {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.textAnchor
}

func (a *Annotator) DoodleLast() image.Point {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.doodleLast
}

func (a *Annotator) HasCommitted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.log.Len() > 0
}

func (a *Annotator) GetBase() *image.RGBA {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.base
}

func (a *Annotator) DoodleStart() image.Point {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.doodleStart
}

func (a *Annotator) Tool() Tool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tool
}
