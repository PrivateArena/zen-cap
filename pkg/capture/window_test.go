// [VERIFIED]
package capture

import (
	"image"
	"os"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

func TestInteractiveSelectWindowAndAbort(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("Skipping test because DISPLAY environment variable is not set")
	}

	xu, err := xgbutil.NewConn()
	if err != nil {
		t.Fatalf("failed to connect to X server: %v", err)
	}
	defer xu.Conn().Close()

	screen := xu.Screen()
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	dummyImg := image.NewRGBA(image.Rect(0, 0, screenWidth, screenHeight))

	t.Run("Abort window selection", func(t *testing.T) {
		TestHookWindowID = 0

		type result struct {
			img image.Image
			err error
		}
		resChan := make(chan result, 1)

		go func() {
			var action string
			img, err := InteractiveSelectWindowExt(dummyImg, &action, nil, nil, nil, nil, nil)
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
			t.Fatal("interactive selection window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed to connect client: %v", err)
		}
		defer clientXu.Conn().Close()

		// Send Right click (Button 3) to abort
		press := xproto.ButtonPressEvent{
			Detail:     3,
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
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(press.Bytes()))
		clientXu.Conn().Sync()

		select {
		case res := <-resChan:
			if res.err == nil {
				t.Fatal("expected error from aborting, got nil")
			}
			t.Logf("Successfully aborted window capture: %v", res.err)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for selection result")
		}
	})
}

func TestInteractiveSelectWindowClassAndAbort(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("Skipping test because DISPLAY environment variable is not set")
	}

	xu, err := xgbutil.NewConn()
	if err != nil {
		t.Fatalf("failed to connect to X server: %v", err)
	}
	defer xu.Conn().Close()

	screen := xu.Screen()
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	dummyImg := image.NewRGBA(image.Rect(0, 0, screenWidth, screenHeight))

	t.Run("Abort window class selection", func(t *testing.T) {
		TestHookWindowID = 0

		type result struct {
			class string
			err   error
		}
		resChan := make(chan result, 1)

		go func() {
			class, err := InteractiveSelectWindowClassExt(dummyImg)
			resChan <- result{class: class, err: err}
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
			t.Fatal("interactive selection window was not created in time")
		}

		time.Sleep(200 * time.Millisecond)

		clientXu, err := xgbutil.NewConn()
		if err != nil {
			t.Fatalf("failed to connect client: %v", err)
		}
		defer clientXu.Conn().Close()

		// Send Right click (Button 3) to abort
		press := xproto.ButtonPressEvent{
			Detail:     3,
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
		xproto.SendEvent(clientXu.Conn(), false, xproto.Window(winID), xproto.EventMaskButtonPress, string(press.Bytes()))
		clientXu.Conn().Sync()

		select {
		case res := <-resChan:
			if res.err == nil {
				t.Fatal("expected error from aborting, got nil")
			}
			t.Logf("Successfully aborted window class selection: %v", res.err)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for selection result")
		}
	})
}
