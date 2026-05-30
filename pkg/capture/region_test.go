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

	t.Run("Successful Shift Right-Click Rectangle Drawing", func(t *testing.T) {
		TestHookWindowID = 0
		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		go func() {
			img, err := InteractiveSelectRegion(dummyImg)
			resChan <- result{img: img, err: err}
		}()

		var winID uint32
		for i := 0; i < 50; i++ {
			if TestHookWindowID != 0 {
				winID = TestHookWindowID
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if winID == 0 {
			t.Fatal("window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed client connection: %v", err)
		}
		defer clientXu.Conn().Close()

		// 1. Simulate Shift + Right click drag and drop from (120, 120) to (180, 180)
		startX, startY := 120, 120
		endX, endY := 180, 180

		// Send Right ButtonPress with Shift modifier
		press := xproto.ButtonPressEvent{
			Detail:     3, // Right click
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(startX),
			RootY:      int16(startY),
			EventX:     int16(startX),
			EventY:     int16(startY),
			State:      xproto.ModMaskShift,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(press.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send MotionNotify
		motion := xproto.MotionNotifyEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask3 | xproto.ModMaskShift,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonMotion, string(motion.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send ButtonRelease
		release := xproto.ButtonReleaseEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask3 | xproto.ModMaskShift,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(release.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// 2. Crop selection surrounding the rectangle: (100, 100) to (200, 200)
		pressCrop := xproto.ButtonPressEvent{
			Detail:     1, // Left click
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      100,
			RootY:      100,
			EventX:     100,
			EventY:     100,
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(pressCrop.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		releaseCrop := xproto.ButtonReleaseEvent{
			Detail:     1,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      200,
			RootY:      200,
			EventX:     200,
			EventY:     200,
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(releaseCrop.Bytes()))
		clientXu.Conn().Sync()

		select {
		case res := <-resChan:
			if res.err != nil {
				t.Fatalf("rectangle draw crop failed: %v", res.err)
			}
			// Verify pink color exists in the cropped image
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
				t.Error("expected to find neon-pink rectangle color inside crop, but found none")
			} else {
				t.Log("Successfully verified Shift+Right-Click rectangle drawing!")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for result")
		}
	})

	t.Run("Successful Ctrl Right-Click Circle Drawing", func(t *testing.T) {
		TestHookWindowID = 0
		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		go func() {
			img, err := InteractiveSelectRegion(dummyImg)
			resChan <- result{img: img, err: err}
		}()

		var winID uint32
		for i := 0; i < 50; i++ {
			if TestHookWindowID != 0 {
				winID = TestHookWindowID
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if winID == 0 {
			t.Fatal("window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed client connection: %v", err)
		}
		defer clientXu.Conn().Close()

		// 1. Simulate Ctrl + Right click drag and drop from (150, 150) to (180, 180)
		startX, startY := 150, 150
		endX, endY := 180, 180

		press := xproto.ButtonPressEvent{
			Detail:     3, // Right click
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(startX),
			RootY:      int16(startY),
			EventX:     int16(startX),
			EventY:     int16(startY),
			State:      xproto.ModMaskControl,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(press.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		motion := xproto.MotionNotifyEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask3 | xproto.ModMaskControl,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonMotion, string(motion.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		release := xproto.ButtonReleaseEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask3 | xproto.ModMaskControl,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(release.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// 2. Crop selection surrounding: (100, 100) to (200, 200)
		pressCrop := xproto.ButtonPressEvent{
			Detail:     1, // Left click
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      100,
			RootY:      100,
			EventX:     100,
			EventY:     100,
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(pressCrop.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		releaseCrop := xproto.ButtonReleaseEvent{
			Detail:     1,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      200,
			RootY:      200,
			EventX:     200,
			EventY:     200,
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(releaseCrop.Bytes()))
		clientXu.Conn().Sync()

		select {
		case res := <-resChan:
			if res.err != nil {
				t.Fatalf("circle draw crop failed: %v", res.err)
			}
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
				t.Error("expected to find neon-pink circle color inside crop, but found none")
			} else {
				t.Log("Successfully verified Ctrl+Right-Click circle drawing!")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for result")
		}
	})

	t.Run("Successful Double Right-Click Text Typing", func(t *testing.T) {
		TestHookWindowID = 0
		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		go func() {
			img, err := InteractiveSelectRegion(dummyImg)
			resChan <- result{img: img, err: err}
		}()

		var winID uint32
		for i := 0; i < 50; i++ {
			if TestHookWindowID != 0 {
				winID = TestHookWindowID
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if winID == 0 {
			t.Fatal("window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed client connection: %v", err)
		}
		defer clientXu.Conn().Close()

		// 1. Simulate two Right clicks at (150, 150) within 100ms
		clickX, clickY := 150, 150
		for k := 0; k < 2; k++ {
			press := xproto.ButtonPressEvent{
				Detail:     3, // Right click
				Time:       xproto.TimeCurrentTime,
				Root:       screen.Root,
				Event:      xproto.Window(winID),
				Child:      xproto.WindowNone,
				RootX:      int16(clickX),
				RootY:      int16(clickY),
				EventX:     int16(clickX),
				EventY:     int16(clickY),
				State:      0,
				SameScreen: true,
			}
			xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(press.Bytes()))
			clientXu.Conn().Sync()
			time.Sleep(30 * time.Millisecond)

			release := xproto.ButtonReleaseEvent{
				Detail:     3,
				Time:       xproto.TimeCurrentTime,
				Root:       screen.Root,
				Event:      xproto.Window(winID),
				Child:      xproto.WindowNone,
				RootX:      int16(clickX),
				RootY:      int16(clickY),
				EventX:     int16(clickX),
				EventY:     int16(clickY),
				State:      0,
				SameScreen: true,
			}
			xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(release.Bytes()))
			clientXu.Conn().Sync()
			time.Sleep(30 * time.Millisecond)
		}

		// Wait for Text Mode event processing
		time.Sleep(100 * time.Millisecond)

		keybind.Initialize(clientXu)

		// 2. Simulate typing "1" then "Enter" / "Return"
		kcs := keybind.StrToKeycodes(clientXu, "1")
		if len(kcs) == 0 {
			t.Fatal("could not locate keycode for '1'")
		}
		oneKeycode := kcs[0]

		pressKey := xproto.KeyPressEvent{
			Detail:     xproto.Keycode(oneKeycode),
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
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskKeyPress, string(pressKey.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		// Send Return key
		retKcs := keybind.StrToKeycodes(clientXu, "Return")
		if len(retKcs) == 0 {
			retKcs = keybind.StrToKeycodes(clientXu, "Enter")
		}
		if len(retKcs) == 0 {
			t.Fatal("could not locate Return/Enter keycode")
		}
		retKeycode := retKcs[0]

		pressReturn := xproto.KeyPressEvent{
			Detail:     xproto.Keycode(retKeycode),
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
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskKeyPress, string(pressReturn.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(100 * time.Millisecond)

		// 3. Crop selection surrounding the typed text
		pressCrop := xproto.ButtonPressEvent{
			Detail:     1, // Left click
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      100,
			RootY:      100,
			EventX:     100,
			EventY:     100,
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(pressCrop.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		releaseCrop := xproto.ButtonReleaseEvent{
			Detail:     1,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      200,
			RootY:      200,
			EventX:     200,
			EventY:     200,
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(releaseCrop.Bytes()))
		clientXu.Conn().Sync()

		select {
		case res := <-resChan:
			if res.err != nil {
				t.Fatalf("text typing crop failed: %v", res.err)
			}
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
				t.Error("expected to find neon-pink text color inside crop, but found none")
			} else {
				t.Log("Successfully verified double right-click text input and typing flow!")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for result")
		}
	})

	t.Run("Successful Ctrl+Z Undo Annotation", func(t *testing.T) {
		TestHookWindowID = 0
		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		go func() {
			img, err := InteractiveSelectRegion(dummyImg)
			resChan <- result{img: img, err: err}
		}()

		var winID uint32
		for i := 0; i < 50; i++ {
			if TestHookWindowID != 0 {
				winID = TestHookWindowID
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if winID == 0 {
			t.Fatal("window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed client connection: %v", err)
		}
		defer clientXu.Conn().Close()

		// 1. Draw a rectangle shape
		startX, startY := 120, 120
		endX, endY := 180, 180

		press := xproto.ButtonPressEvent{
			Detail:     3, // Right click
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(startX),
			RootY:      int16(startY),
			EventX:     int16(startX),
			EventY:     int16(startY),
			State:      xproto.ModMaskShift,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(press.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		motion := xproto.MotionNotifyEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask3 | xproto.ModMaskShift,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonMotion, string(motion.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		release := xproto.ButtonReleaseEvent{
			Detail:     3,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      int16(endX),
			RootY:      int16(endY),
			EventX:     int16(endX),
			EventY:     int16(endY),
			State:      xproto.ButtonMask3 | xproto.ModMaskShift,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(release.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(100 * time.Millisecond)

		// 2. Simulate Ctrl+Z keypress to undo
		keybind.Initialize(clientXu)
		zKcs := keybind.StrToKeycodes(clientXu, "z")
		if len(zKcs) == 0 {
			t.Fatal("could not locate keycode for 'z'")
		}
		zKeycode := zKcs[0]

		pressUndo := xproto.KeyPressEvent{
			Detail:     xproto.Keycode(zKeycode),
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      0,
			RootY:      0,
			EventX:     0,
			EventY:     0,
			State:      xproto.ModMaskControl,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskKeyPress, string(pressUndo.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(100 * time.Millisecond)

		// 3. Crop selection surrounding: (100, 100) to (200, 200)
		pressCrop := xproto.ButtonPressEvent{
			Detail:     1, // Left click
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      100,
			RootY:      100,
			EventX:     100,
			EventY:     100,
			State:      0,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(pressCrop.Bytes()))
		clientXu.Conn().Sync()
		time.Sleep(50 * time.Millisecond)

		releaseCrop := xproto.ButtonReleaseEvent{
			Detail:     1,
			Time:       xproto.TimeCurrentTime,
			Root:       screen.Root,
			Event:      xproto.Window(winID),
			Child:      xproto.WindowNone,
			RootX:      200,
			RootY:      200,
			EventX:     200,
			EventY:     200,
			State:      xproto.ButtonMask1,
			SameScreen: true,
		}
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonRelease, string(releaseCrop.Bytes()))
		clientXu.Conn().Sync()

		select {
		case res := <-resChan:
			if res.err != nil {
				t.Fatalf("crop failed after undo: %v", res.err)
			}
			// Verify pink color does NOT exist in the cropped image (since it was undone!)
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
			if foundPink {
				t.Error("expected pink rectangle annotation to be undone and removed from the cropped image, but found pink pixels")
			} else {
				t.Log("Successfully verified Ctrl+Z annotation Undo flow!")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for result")
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
