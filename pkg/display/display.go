// [VERIFIED]
package display

type Geometry struct {
	X      int
	Y      int
	Width  int
	Height int
}

type Screen struct {
	Index    int
	Name     string
	Geometry Geometry
}

type Window struct {
	ID       uint32
	Title    string
	Class    string
	Geometry Geometry
}

type DisplayManager interface {
	GetScreens() ([]Screen, error)
	GetWindows() ([]Window, error)
	GetActiveWindow() (*Window, error)
	Close()
}
