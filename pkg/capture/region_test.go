package capture

import (
	"image"
	"image/color"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
)

func TestInteractiveSelectRegionAndAbort(t *testing.T) {
	// Skip if we are running in a GUI-less environment without X11 DISPLAY
	if os.Getenv("DISPLAY") == "" {
		t.Skip("Skipping test because DISPLAY environment variable is not set")
	}

	// Connect to X server to get the screen size
	xu, err := xgbutil.NewConn()
	if err != nil {
		t.Fatalf("failed to connect to X server: %v", err)
	}
	defer xu.Conn().Close()

	screen := xu.Screen()
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	// Create a dummy mock screenshot image
	dummyImg := image.NewRGBA(image.Rect(0, 0, screenWidth, screenHeight))

	t.Run("Successful Selection Crop", func(t *testing.T) {
		// Reset hook
		TestHookWindowID = 0

		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		// Start interactive selection in a goroutine
		go func() {
			img, err := InteractiveSelectRegion(dummyImg)
			resChan <- result{img: img, err: err}
		}()

		// Wait for the window to be created and the hook populated
		var winID uint32
		for i := 0; i < 50; i++ {
			if TestHookWindowID != 0 {
				winID = TestHookWindowID
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		if winID == 0 {
			t.Fatal("interactive selection window was not created in time")
		}

		// Wait a small moment to ensure the event loop has started and mapped the window
		time.Sleep(200 * time.Millisecond)

		// Connect separately to send the simulated events
		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed to connect client connection: %v", err)
		}
		defer clientXu.Conn().Close()

		// Coordinates: Drag from (100, 100) to (300, 250) -> width 200, height 150
		startX, startY := 100, 100
		endX, endY := 300, 250

		// Send ButtonPress
		press := xproto.ButtonPressEvent{
			Detail:     1, // Button 1 (Left click)
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(startX),
			RootY:      int16(startY),
			EventX:     int16(startX),
			EventY:     int16(startY),
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(press.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send MotionNotify
		motion := xproto.MotionNotifyEvent{
			Detail:     1,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButton1Motion, string(motion.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send ButtonRelease
		release := xproto.ButtonReleaseEvent{
			Detail:     1, // Button 1
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(release.Bytes()))
		clientXu.Conn().Sync()

		// Get result with a timeout of 3 seconds
		select {
		case res := <-resChan:
			if res.err != nil {
				t.Fatalf("expected successful crop, got error: %v", res.err)
			}
			if res.img == nil {
				t.Fatal("expected cropped image, got nil")
			}
			w := res.img.Bounds().Dx()
			h := res.img.Bounds().Dy()
			if w != 200 || h != 150 {
				t.Errorf("expected cropped dimensions to be 200x150, got %dx%d", w, h)
			}
			t.Logf("Successfully verified selection crop of dimensions: %dx%d", w, h)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for selection result")
		}
	})

	t.Run("Successful Annotation Doodle and Crop", func(t *testing.T) {
		// Reset hook
		TestHookWindowID = 0

		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		// Start interactive selection in a goroutine
		go func() {
			img, err := InteractiveSelectRegion(dummyImg)
			resChan <- result{img: img, err: err}
		}()

		// Wait for window hook
		var winID uint32
		for i := 0; i < 50; i++ {
			if TestHookWindowID != 0 {
				winID = TestHookWindowID
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		if winID == 0 {
			t.Fatal("interactive selection window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		// Connect separately to send simulated events
		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed to connect client connection: %v", err)
		}
		defer clientXu.Conn().Close()

		// 1. Simulate a Right-click freehand doodle inside (150, 150) to (160, 160)
		doodleX, doodleY := 150, 150

		// Send Right ButtonPress (Detail 3)
		pressDoodle := xproto.ButtonPressEvent{
			Detail:     3, // Button 3 (Right click)
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(doodleX),
			RootY:      int16(doodleY),
			EventX:     int16(doodleX),
			EventY:     int16(doodleY),
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(pressDoodle.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send MotionNotify for Doodle
		motionDoodle := xproto.MotionNotifyEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(doodleX + 10),
			RootY:      int16(doodleY + 10),
			EventX:     int16(doodleX + 10),
			EventY:     int16(doodleY + 10),
			State:      xproto.ButtonMask3,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonMotion, string(motionDoodle.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send Right ButtonRelease
		releaseDoodle := xproto.ButtonReleaseEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(doodleX + 10),
			RootY:      int16(doodleY + 10),
			EventX:     int16(doodleX + 10),
			EventY:     int16(doodleY + 10),
			State:      xproto.ButtonMask3,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(releaseDoodle.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// 2. Simulate Left-click crop (100, 100) to (200, 200) encompassing the doodle
		startX, startY := 100, 100
		endX, endY := 200, 200

		// Send ButtonPress
		pressCrop := xproto.ButtonPressEvent{
			Detail:     1, // Button 1 (Left click)
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(startX),
			RootY:      int16(startY),
			EventX:     int16(startX),
			EventY:     int16(startY),
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(pressCrop.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send MotionNotify for Crop
		motionCrop := xproto.MotionNotifyEvent{
			Detail:     1,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonMotion, string(motionCrop.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send ButtonRelease for Crop
		releaseCrop := xproto.ButtonReleaseEvent{
			Detail:     1,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(releaseCrop.Bytes()))
		clientXu.Conn().Sync()

		// Get result with a timeout of 3 seconds
		select {
		case res := <-resChan:
			if res.err != nil {
				t.Fatalf("expected successful crop, got error: %v", res.err)
			}
			if res.img == nil {
				t.Fatal("expected cropped image, got nil")
			}
			w := res.img.Bounds().Dx()
			h := res.img.Bounds().Dy()
			if w != 100 || h != 100 {
				t.Errorf("expected cropped dimensions to be 100x100, got %dx%d", w, h)
			}

			// Scan the cropped image to ensure the neon pink doodle pixel (#ff007f) is burned into it
			foundPink := false
			pinkColor := color.RGBA{R: 255, G: 0, B: 127, A: 255}
			for y := res.img.Bounds().Min.Y; y < res.img.Bounds().Max.Y; y++ {
				for x := res.img.Bounds().Min.X; x < res.img.Bounds().Max.X; x++ {
					r, g, b, a := res.img.At(x, y).RGBA()
					pr, pg, pb, pa := pinkColor.RGBA()
					if r == pr && g == pg && b == pb && a == pa {
						foundPink = true
						break
					}
				}
				if foundPink {
					break
				}
			}

			if !foundPink {
				t.Error("expected to find neon-pink doodle color burned into the cropped image, but none found")
			} else {
				t.Log("Successfully verified neon-pink doodle was burned and captured inside the cropped image bounds!")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for selection result")
		}
	})

	t.Run("Safety Net Emergency Abort", func(t *testing.T) {
		// Reset hook
		TestHookWindowID = 0

		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		// Start interactive selection in a goroutine
		go func() {
			img, err := InteractiveSelectRegion(dummyImg)
			resChan <- result{img: img, err: err}
		}()

		// Wait for window hook
		var winID uint32
		for i := 0; i < 50; i++ {
			if TestHookWindowID != 0 {
				winID = TestHookWindowID
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		if winID == 0 {
			t.Fatal("interactive selection window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		// Connect separately to send the simulated key events
		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed to connect client connection: %v", err)
		}
		defer clientXu.Conn().Close()

		keybind.Initialize(clientXu)

		// Find the keycode for Escape key
		keycodes := keybind.StrToKeycodes(clientXu, "Escape")
		if len(keycodes) == 0 {
			t.Fatal("could not find keycode for Escape")
		}
		escKeycode := keycodes[0]

		// Send Escape KeyPress
		press := xproto.KeyPressEvent{
			Detail:     xproto.Keycode(escKeycode),
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      0,
			RootY:      0,
			EventX:     0,
			EventY:     0,
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskKeyPress, string(press.Bytes()))
		clientXu.Conn().Sync()

		// Get result with a timeout of 3 seconds
		select {
		case res := <-resChan:
			if res.err == nil {
				t.Fatal("expected selection to be aborted with error, but got success")
			}
			if !strings.Contains(res.err.Error(), "aborted by user safety key") {
				t.Errorf("expected safety abort error, got: %v", res.err)
			}
			t.Logf("Successfully verified safety abort with error: %v", res.err)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for selection abort result")
		}
	})
}
