package magnifier

import (
	"encoding/binary"
	"fmt"
	"image"
	"syscall"
	"unsafe"

	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

// Linux IPC constants (not defined in stdlib syscall package for all arches).
const (
	ipcPrivate = 0
	ipcRmid    = 0
)

// capturer is the internal interface for screen region capture.
type capturer interface {
	// capture reads a screen region (x, y, w, h) in root coordinates
	// and returns it as an RGBA image.
	capture(x, y, w, h int) (*image.RGBA, error)
	close()
}

// ----- MIT-SHM capturer -----

// shmCapturer uses the MIT-SHM X extension for zero-copy screen reads.
// This is the primary capture path; ~3-5× faster than XGetImage.
type shmCapturer struct {
	xu      *xgbutil.XUtil
	seg     shm.Seg
	shmid   int
	size    int
	dataPtr []byte // mmap'd shared memory
}

func newSHMCapturer(xu *xgbutil.XUtil, maxW, maxH int) (*shmCapturer, error) {
	conn := xu.Conn()
	if err := shm.Init(conn); err != nil {
		return nil, fmt.Errorf("MIT-SHM not available: %w", err)
	}

	size := maxW * maxH * 4 // BGRA, 32bpp

	// Allocate System V shared memory segment.
	shmid, _, errno := syscall.Syscall(syscall.SYS_SHMGET,
		uintptr(ipcPrivate),
		uintptr(size),
		uintptr(0600))
	if errno != 0 {
		return nil, fmt.Errorf("shmget failed: %w", errno)
	}

	// Attach the segment into our address space.
	addr, _, errno := syscall.Syscall(syscall.SYS_SHMAT, shmid, 0, 0)
	if errno != 0 {
		syscall.Syscall(syscall.SYS_SHMCTL, shmid, uintptr(ipcRmid), 0)
		return nil, fmt.Errorf("shmat failed: %w", errno)
	}
	dataPtr := (*[1 << 30]byte)(unsafe.Pointer(addr))[:size:size]

	// Tell the X server about the segment.
	segID, err := shm.NewSegId(conn)
	if err != nil {
		detachShm(addr, shmid)
		return nil, fmt.Errorf("shm.NewSegId: %w", err)
	}
	if err := shm.AttachChecked(conn, segID, uint32(shmid), false).Check(); err != nil {
		detachShm(addr, shmid)
		return nil, fmt.Errorf("shm.Attach: %w", err)
	}

	// Mark the segment for deletion so it is released when no longer attached.
	syscall.Syscall(syscall.SYS_SHMCTL, shmid, uintptr(ipcRmid), 0)

	return &shmCapturer{
		xu:      xu,
		seg:     segID,
		shmid:   int(shmid),
		size:    size,
		dataPtr: dataPtr,
	}, nil
}

func (c *shmCapturer) capture(x, y, w, h int) (*image.RGBA, error) {
	root := c.xu.RootWin()

	reply, err := shm.GetImage(c.xu.Conn(),
		xproto.Drawable(root),
		int16(x), int16(y),
		uint16(w), uint16(h),
		0xFFFFFFFF,
		byte(xproto.ImageFormatZPixmap),
		c.seg, 0,
	).Reply()
	if err != nil {
		return nil, fmt.Errorf("shm.GetImage: %w", err)
	}
	_ = reply // data is already in c.dataPtr

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	pixCount := w * h
	for i := 0; i < pixCount; i++ {
		base := i * 4
		b := c.dataPtr[base]
		g := c.dataPtr[base+1]
		r := c.dataPtr[base+2]
		// a := c.dataPtr[base+3]  // ignored; X always returns 0xFF for BGRA from screen
		img.Pix[base] = r
		img.Pix[base+1] = g
		img.Pix[base+2] = b
		img.Pix[base+3] = 0xFF
	}
	return img, nil
}

func (c *shmCapturer) close() {
	shm.Detach(c.xu.Conn(), c.seg)
	addr := uintptr(unsafe.Pointer(&c.dataPtr[0]))
	syscall.Syscall(syscall.SYS_SHMDT, addr, 0, 0)
}

func detachShm(addr uintptr, shmid uintptr) {
	syscall.Syscall(syscall.SYS_SHMDT, addr, 0, 0)
	syscall.Syscall(syscall.SYS_SHMCTL, shmid, uintptr(ipcRmid), 0)
}

// ----- XGetImage fallback capturer -----

// xgetCapturer uses xproto.GetImage. Slower than SHM but universally available
// (virtual machines, remote X sessions, etc.).
type xgetCapturer struct {
	xu *xgbutil.XUtil
}

func newXGetCapturer(xu *xgbutil.XUtil) *xgetCapturer {
	return &xgetCapturer{xu: xu}
}

func (c *xgetCapturer) capture(x, y, w, h int) (*image.RGBA, error) {
	root := c.xu.RootWin()

	reply, err := xproto.GetImage(c.xu.Conn(),
		xproto.ImageFormatZPixmap,
		xproto.Drawable(root),
		int16(x), int16(y),
		uint16(w), uint16(h),
		0xFFFFFFFF,
	).Reply()
	if err != nil {
		return nil, fmt.Errorf("xproto.GetImage: %w", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	data := reply.Data
	pixCount := w * h
	for i := 0; i < pixCount && (i+1)*4 <= len(data); i++ {
		base := i * 4
		// X returns ZPixmap as BGRA on little-endian.
		img.Pix[base] = data[base+2]   // R
		img.Pix[base+1] = data[base+1] // G
		img.Pix[base+2] = data[base]   // B
		img.Pix[base+3] = 0xFF
	}
	return img, nil
}

func (c *xgetCapturer) close() {}

// ----- factory -----

// newCapturer tries MIT-SHM first; falls back to XGetImage automatically.
func newCapturer(xu *xgbutil.XUtil, maxW, maxH int) capturer {
	if cap, err := newSHMCapturer(xu, maxW, maxH); err == nil {
		return cap
	}
	return newXGetCapturer(xu)
}

// clampRegion ensures (x, y, w, h) fits entirely within (0,0)-(screenW,screenH).
func clampRegion(x, y, w, h, screenW, screenH int) (int, int, int, int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x+w > screenW {
		x = screenW - w
	}
	if y+h > screenH {
		y = screenH - h
	}
	if x < 0 {
		x = 0
		w = screenW
	}
	if y < 0 {
		y = 0
		h = screenH
	}
	return x, y, w, h
}

// sourceViewport calculates the source region (in root coordinates) to capture
// for a given zoom level and cursor position. The source is centred on the
// cursor and shrinks as zoom increases, so performance improves at high zoom.
func sourceViewport(cursorX, cursorY int, zoom float64, mon MonitorGeometry) (srcX, srcY, srcW, srcH int) {
	srcW = int(float64(mon.W) / zoom)
	srcH = int(float64(mon.H) / zoom)
	if srcW < 1 {
		srcW = 1
	}
	if srcH < 1 {
		srcH = 1
	}

	// Centre on cursor
	srcX = cursorX - srcW/2
	srcY = cursorY - srcH/2

	// Clamp within monitor bounds
	if srcX < mon.X {
		srcX = mon.X
	}
	if srcY < mon.Y {
		srcY = mon.Y
	}
	if srcX+srcW > mon.X+mon.W {
		srcX = mon.X + mon.W - srcW
	}
	if srcY+srcH > mon.Y+mon.H {
		srcY = mon.Y + mon.H - srcH
	}
	return
}

// byteOrder is used to detect endianness for BGRA decoding.
var hostLittleEndian = func() bool {
	var v uint16 = 0x0102
	b := (*[2]byte)(unsafe.Pointer(&v))
	return b[0] == 0x02
}()

// u32LE reads a uint32 little-endian from b.
func u32LE(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
