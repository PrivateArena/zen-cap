package main

import (
	"fmt"
	"strconv"

	"zen-cap/pkg/config"
	"zen-cap/pkg/display"
)

// Helper X11 queries

func listScreens() error {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return err
	}
	defer dm.Close()

	screens, err := dm.GetScreens()
	if err != nil {
		return err
	}

	fmt.Printf("%-6s %-15s %s\n", "Index", "Name", "Geometry")
	for _, s := range screens {
		fmt.Printf("%-6d %-15s %dx%d at (%d,%d)\n", s.Index, s.Name, s.Geometry.Width, s.Geometry.Height, s.Geometry.X, s.Geometry.Y)
	}
	return nil
}

func listWindows() error {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return err
	}
	defer dm.Close()

	windows, err := dm.GetWindows()
	if err != nil {
		return err
	}

	fmt.Printf("%-12s %-20s %s\n", "Window ID", "Class", "Title")
	for _, w := range windows {
		fmt.Printf("0x%08x   %-20s %s\n", w.ID, w.Class, w.Title)
	}
	return nil
}

func getScreenInfo(idx int) (display.Screen, error) {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return display.Screen{}, err
	}
	defer dm.Close()

	screens, err := dm.GetScreens()
	if err != nil {
		return display.Screen{}, err
	}

	if idx < 0 || idx >= len(screens) {
		return display.Screen{}, fmt.Errorf("screen index %d out of range (found %d screens)", idx, len(screens))
	}
	return screens[idx], nil
}

func getActiveWindowInfo() (display.Window, error) {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return display.Window{}, err
	}
	defer dm.Close()

	win, err := dm.GetActiveWindow()
	if err != nil {
		return display.Window{}, err
	}
	return *win, nil
}

func parseWindowID(str string) (uint32, error) {
	// Parse window ID as hex or decimal
	var id uint64
	var err error
	if len(str) > 2 && str[:2] == "0x" {
		id, err = strconv.ParseUint(str[2:], 16, 32)
	} else {
		id, err = strconv.ParseUint(str, 10, 32)
	}
	if err != nil {
		return 0, fmt.Errorf("invalid window ID %q: %w", str, err)
	}
	return uint32(id), nil
}

func sendNotification(title, message string) {
	config.SendNotification(title, message)
}
