package hid

import (
	"errors"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Hid struct {
	g0         *os.File
	g1         *os.File
	g2         *os.File
	kbMutex    sync.Mutex
	mouseMutex sync.Mutex
}

const (
	HID0 = "/dev/hidg0" // Keyboard
	HID1 = "/dev/hidg1" // Mouse (Relative Mode)
	HID2 = "/dev/hidg2" // Touchpad (Absolute Mode)
)

var (
	hid     *Hid
	hidOnce sync.Once
)

func GetHid() *Hid {
	hidOnce.Do(func() {
		hid = &Hid{}
	})
	return hid
}

func (h *Hid) Lock() {
	h.kbMutex.Lock()
	h.mouseMutex.Lock()
}

func (h *Hid) Unlock() {
	h.kbMutex.Unlock()
	h.mouseMutex.Unlock()
}

func (h *Hid) OpenNoLock() {
	var err error
	h.CloseNoLock()

	h.g0, err = os.OpenFile(HID0, os.O_WRONLY, 0o666)
	if err != nil {
		log.Errorf("open %s failed: %s", HID0, err)
	}

	h.g1, err = os.OpenFile(HID1, os.O_WRONLY, 0o666)
	if err != nil {
		log.Errorf("open %s failed: %s", HID1, err)
	}

	h.g2, err = os.OpenFile(HID2, os.O_WRONLY, 0o666)
	if err != nil {
		log.Errorf("open %s failed: %s", HID2, err)
	}
}

func (h *Hid) CloseNoLock() {
	for _, file := range []*os.File{h.g0, h.g1, h.g2} {
		if file != nil {
			_ = file.Sync()
			_ = file.Close()
		}
	}
}

func (h *Hid) Open() {
	h.kbMutex.Lock()
	defer h.kbMutex.Unlock()
	h.mouseMutex.Lock()
	defer h.mouseMutex.Unlock()

	h.CloseNoLock()

	h.OpenNoLock()
}

func (h *Hid) Close() {
	h.kbMutex.Lock()
	defer h.kbMutex.Unlock()
	h.mouseMutex.Lock()
	defer h.mouseMutex.Unlock()

	h.CloseNoLock()
}

// ensureKeyboard lazily opens the keyboard gadget if no descriptor is open yet.
// The web WS path calls Open() when a client connects, but the native (mesh)
// input path never does — so without this, its first write hits a nil g0, which
// returns os.ErrInvalid (not ErrClosed, so the reopen branch below never fires)
// and every keystroke is silently dropped. Caller holds kbMutex.
func (h *Hid) ensureKeyboard() {
	if h.g0 != nil {
		return
	}
	var err error
	if h.g0, err = os.OpenFile(HID0, os.O_WRONLY, 0o666); err != nil {
		log.Errorf("open %s failed: %s", HID0, err)
	}
}

// ensureRelMouse lazily opens the relative-mouse gadget (see ensureKeyboard).
// Caller holds mouseMutex.
func (h *Hid) ensureRelMouse() {
	if h.g1 != nil {
		return
	}
	var err error
	if h.g1, err = os.OpenFile(HID1, os.O_WRONLY, 0o666); err != nil {
		log.Errorf("open %s failed: %s", HID1, err)
	}
}

// ensureAbsMouse lazily opens the absolute-mouse gadget (see ensureKeyboard).
// Caller holds mouseMutex.
func (h *Hid) ensureAbsMouse() {
	if h.g2 != nil {
		return
	}
	var err error
	if h.g2, err = os.OpenFile(HID2, os.O_WRONLY, 0o666); err != nil {
		log.Errorf("open %s failed: %s", HID2, err)
	}
}

func (h *Hid) WriteHid0(data []byte) {
	deadline := time.Now().Add(8 * time.Millisecond)

	h.kbMutex.Lock()
	h.ensureKeyboard()
	_ = h.g0.SetWriteDeadline(deadline)
	_, err := h.g0.Write(data)
	h.kbMutex.Unlock()

	if err != nil {
		switch {
		case errors.Is(err, os.ErrClosed):
			log.Errorf("hid already closed, reopen it...")
			h.OpenNoLock()
		case errors.Is(err, os.ErrDeadlineExceeded):
			log.Debugf("write to %s timeout", HID0)
		default:
			log.Errorf("write to %s failed: %s", HID0, err)
		}
		return
	}

	log.Debugf("write to %s: %v", HID0, data)
}

func (h *Hid) WriteHid1(data []byte) {
	deadline := time.Now().Add(8 * time.Millisecond)

	h.mouseMutex.Lock()
	h.ensureRelMouse()
	_ = h.g1.SetWriteDeadline(deadline)
	_, err := h.g1.Write(data)
	h.mouseMutex.Unlock()

	if err != nil {
		switch {
		case errors.Is(err, os.ErrClosed):
			log.Errorf("hid already closed, reopen it...")
			h.OpenNoLock()
		case errors.Is(err, os.ErrDeadlineExceeded):
			log.Debugf("write to %s timeout", HID1)
		default:
			log.Errorf("write to %s failed: %s", HID1, err)
		}
		return
	}

	log.Debugf("write to %s: %v", HID1, data)
}

func (h *Hid) WriteHid2(data []byte) {
	deadline := time.Now().Add(8 * time.Millisecond)

	h.mouseMutex.Lock()
	h.ensureAbsMouse()
	_ = h.g2.SetWriteDeadline(deadline)
	_, err := h.g2.Write(data)
	h.mouseMutex.Unlock()

	if err != nil {
		switch {
		case errors.Is(err, os.ErrClosed):
			log.Errorf("hid already closed, reopen it...")
			h.OpenNoLock()
		case errors.Is(err, os.ErrDeadlineExceeded):
			log.Debugf("write to %s timeout", HID2)
		default:
			log.Errorf("write to %s failed: %s", HID2, err)
		}
		return
	}

	log.Debugf("write to %s: %v", HID2, data)
}
