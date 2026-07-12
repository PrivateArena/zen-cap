package annotation

import (
	"image"
	"image/color"
	"math"
)

type Command interface {
	apply(layer *image.RGBA, cfg Config)
}

type StrokeCmd struct {
	Points []image.Point
	Color  color.RGBA
	Thick  int
	Tool   Tool
}

func (c *StrokeCmd) apply(layer *image.RGBA, cfg Config) {
	switch c.Tool {
	case Doodle:
		for i := 1; i < len(c.Points); i++ {
			drawLine(layer, c.Points[i-1].X, c.Points[i-1].Y, c.Points[i].X, c.Points[i].Y, c.Color, c.Thick)
		}
	case Rect:
		if len(c.Points) >= 2 {
			drawRect(layer, c.Points[0].X, c.Points[0].Y, c.Points[1].X, c.Points[1].Y, c.Color, c.Thick)
		}
	case Circle:
		if len(c.Points) >= 2 {
			dx := c.Points[1].X - c.Points[0].X
			dy := c.Points[1].Y - c.Points[0].Y
			r := int(math.Sqrt(float64(dx*dx + dy*dy)))
			if r > 0 {
				drawCircle(layer, c.Points[0].X, c.Points[0].Y, r, c.Color, c.Thick)
			}
		}
	}
}

type TextCmd struct {
	Anchor    image.Point
	Text      string
	Color     color.RGBA
	FontScale int
}

func (c *TextCmd) apply(layer *image.RGBA, cfg Config) {
	bg := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	drawHUDTextScaled(layer, c.Text, c.Anchor.X, c.Anchor.Y, c.Color, bg, c.FontScale)
}

type UndoLog struct {
	commands []Command
}

func (u *UndoLog) Push(cmd Command) {
	u.commands = append(u.commands, cmd)
}

func (u *UndoLog) Pop() Command {
	if len(u.commands) == 0 {
		return nil
	}
	cmd := u.commands[len(u.commands)-1]
	u.commands = u.commands[:len(u.commands)-1]
	return cmd
}

func (u *UndoLog) Replay(layer *image.RGBA, cfg Config) {
	for _, cmd := range u.commands {
		cmd.apply(layer, cfg)
	}
}

func (u *UndoLog) Len() int {
	return len(u.commands)
}
