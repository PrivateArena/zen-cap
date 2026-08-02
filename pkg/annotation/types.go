package annotation

import (
	"image"
	"image/color"
)

type Tool int

const (
	Doodle Tool = iota
	Rect
	Circle
	Text
)

type Config struct {
	BrushThickness uint32
	FontScale      int
	Color          color.RGBA
}

type EventKind int

const (
	Press EventKind = iota
	Release
	Motion
	Key
)

type InputEvent struct {
	Kind    EventKind
	X, Y    int
	Button  int
	Mods    uint16
	KeyStr  string
	Keycode uint8
}

func DefaultConfig() Config {
	return Config{
		BrushThickness: 4,
		FontScale:      4,
		Color:          color.RGBA{R: 255, G: 0, B: 127, A: 255},
	}
}

func (e InputEvent) Point() image.Point {
	return image.Point{X: e.X, Y: e.Y}
}
