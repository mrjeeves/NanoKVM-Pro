// Package button watches the device's physical user button — the BOOT button on
// the PCIe NanoKVM, the USR button on the Pro — and drives the CEC hand raise
// from it.
//
// The button is an evdev node (default /dev/input/event0) that emits EV_KEY
// events. On the PCIe NanoKVM the on-device screen firmware (the C++ kvm_system
// app) also reads this node and, out of the box, reacts to presses: a short
// press cycles the OLED, a long press (>=1.5s) toggles a WiFi hotspot, and a
// very-long press (>=9s) resets the account. To make the button do ONE obvious
// thing — raise a hand — we take exclusive ownership of the node with EVIOCGRAB
// (Config.Grab), so the firmware stops seeing presses, and this watcher becomes
// the sole handler:
//
//	tap  (< tapMax)     → toggle the CEC hand raise
//	hold (>= resetHold) → OnFactoryReset (we re-implement the firmware's own
//	                      hold-to-reset, since the grab took it away)
//	anything in between → ignored
//
// The OLED *display* keeps working (that's a separate firmware thread); only the
// button's firmware gestures are suppressed. If the grab fails we fall back to
// co-reading: hand raise still works, but the firmware gestures fire too.
package button

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// evdev input_event constants. On 64-bit Linux (riscv64 and aarch64 are both
// 64-bit, little-endian) struct input_event is: struct timeval{sec i64, usec
// i64} (16) + type u16 (2) + code u16 (2) + value i32 (4) = 24 bytes. We only
// read type/code/value; timing comes from the wall clock at read.
const (
	eventSize   = 24
	evKey       = 0x01 // EV_KEY
	valueUp     = 0    // key released
	valueDown   = 1    // key pressed
	valueRepeat = 2    // autorepeat (ignored)
	// eviocgrab is EVIOCGRAB = _IOW('E', 0x90, int): take exclusive ownership of
	// an evdev node so no other reader (the screen firmware) sees its events.
	// Released automatically when the fd is closed.
	eviocgrab = 0x40044590
)

// Gesture thresholds — mirror the on-device firmware's own key timings
// (kvm_system include/config.h: KEY_LONG_PRESS=1500, KEY_LONGLONG_PRESS=9000)
// so the button keeps the same feel: a quick tap is a tap, and the familiar
// long hold still resets.
const (
	tapMax        = 1500 * time.Millisecond // <= this on release → hand raise
	resetHold     = 9 * time.Second         // >= this on release → factory reset
	reopenBackoff = 5 * time.Second
)

// GPIO-poll mode constants. When Device is "gpio:<n>" the watcher polls the
// line's live level from the kernel's debugfs gpio dump instead of reading an
// evdev node — for a button the on-device firmware owns via the gpiochip chardev
// (the Pro's USR button, held by kvm_ui as "LinuxKeyMonitor"), which libgpiod
// can't request but debugfs still exposes. gpioActiveLowPressed maps the level
// to a press (these buttons idle high, pulled low when pressed).
const (
	debugfsGPIO          = "/sys/kernel/debug/gpio"
	gpioPollInterval     = 40 * time.Millisecond
	gpioActiveLowPressed = "lo"
	gpioDeviceSpecPrefix = "gpio:"
)

// Toggler is the hand-raise action a tap drives. *mesh.Bridge satisfies it
// (ToggleHand); kept as an interface so this package doesn't import mesh and
// stays trivially testable.
type Toggler interface {
	ToggleHand() (raised bool, err error)
}

// Config controls the watcher.
type Config struct {
	// Enabled turns the watcher on. Device is the evdev node to read; KeyCode,
	// when non-zero, restricts to a specific evdev key code (0 = any key, right
	// for a single-button device).
	Enabled bool
	Device  string
	KeyCode int
	// Grab takes exclusive ownership of the node (EVIOCGRAB) so the on-device
	// firmware stops reacting to the button and this watcher becomes its sole
	// handler. Best-effort: on failure we co-read instead (hand raise still
	// works; the firmware gestures also still fire).
	Grab bool
	// OnFactoryReset, if set, runs when the button is held >= resetHold. Wire it
	// alongside Grab so the grabbed device keeps a working hold-to-reset.
	OnFactoryReset func()
}

// Watch starts the button watcher in a background goroutine and returns
// immediately. It is a no-op when disabled or misconfigured, so callers can
// invoke it unconditionally.
func Watch(cfg Config, toggler Toggler) {
	if !cfg.Enabled {
		log.Info("button: hand-raise button disabled")
		return
	}
	if cfg.Device == "" {
		log.Warn("button: hand-raise enabled but no input device configured; not watching")
		return
	}
	if toggler == nil {
		log.Warn("button: no hand-raise handler; not watching")
		return
	}
	go run(cfg, toggler)
}

// run watches the configured button, reopening with backoff on error. Device
// "gpio:<n>" selects the debugfs GPIO-poll path; anything else is an evdev node.
func run(cfg Config, toggler Toggler) {
	if line, ok := gpioLineFromDevice(cfg.Device); ok {
		log.Infof("button: watching GPIO %d via %s (tap = hand raise)", line, debugfsGPIO)
		for {
			watchGPIO(cfg, toggler, line)
			time.Sleep(reopenBackoff)
		}
	}
	log.Infof("button: watching %s (tap = hand raise, hold %s = factory reset)", cfg.Device, resetHold)
	for {
		watchOnce(cfg, toggler)
		time.Sleep(reopenBackoff)
	}
}

// gpioLineFromDevice parses a "gpio:<n>" spec into a global GPIO line number.
// Anything else (an evdev path) returns ok=false.
func gpioLineFromDevice(device string) (int, bool) {
	if !strings.HasPrefix(device, gpioDeviceSpecPrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(device[len(gpioDeviceSpecPrefix):]))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// watchGPIO polls a GPIO line's live level from debugfs and drives the same
// tap/hold detector as the evdev path. Used for a button the firmware owns via
// the gpiochip chardev — we can't request the line with libgpiod while it's
// held, but debugfs still reports the live level, so we co-read it. The firmware
// keeps reacting to the press; we add the hand raise. (Grab is meaningless here
// — there's no exclusive-ownership handle for a polled line — so it's ignored.)
func watchGPIO(cfg Config, toggler Toggler, line int) {
	det := &detector{tapMax: tapMax, resetHold: resetHold}
	var pressed, have bool
	ticker := time.NewTicker(gpioPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		level, err := readGPIOLevel(line)
		if err != nil {
			log.Warnf("button: read GPIO %d: %s (retrying)", line, err)
			return // back off and retry from run()
		}
		now := level == gpioActiveLowPressed
		if !have {
			pressed, have = now, true
			continue
		}
		if now == pressed {
			continue
		}
		pressed = now
		value := int32(valueUp)
		if now {
			value = valueDown
		}
		switch det.feed(value, time.Now()) {
		case gestureTap:
			raised, err := toggler.ToggleHand()
			if err != nil {
				log.Errorf("button: hand-raise toggle failed: %s", err)
			} else if raised {
				log.Info("button: hand raised")
			} else {
				log.Info("button: hand lowered")
			}
		case gestureReset:
			if cfg.OnFactoryReset != nil {
				log.Warn("button: held past the reset threshold — running factory reset")
				cfg.OnFactoryReset()
			}
		}
	}
}

// readGPIOLevel returns "hi" or "lo" for global GPIO line n, read from the
// kernel's debugfs gpio dump — which reports the live level even for a line
// another process holds. Lines look like:
//
//	gpio-98  (                    |LinuxKeyMonitor3    ) in  lo
//
// after the "(...)" consumer block come <direction> <level> [flags].
func readGPIOLevel(n int) (string, error) {
	data, err := os.ReadFile(debugfsGPIO)
	if err != nil {
		return "", err
	}
	return parseGPIOLevel(string(data), n)
}

// parseGPIOLevel extracts the "hi"/"lo" level of line n from a debugfs gpio
// dump. Split out for testing.
func parseGPIOLevel(dump string, n int) (string, error) {
	tag := fmt.Sprintf("gpio-%d ", n) // trailing space so gpio-9 never matches gpio-98
	for _, ln := range strings.Split(dump, "\n") {
		s := strings.TrimSpace(ln)
		if !strings.HasPrefix(s, tag) {
			continue
		}
		rest := s
		if i := strings.LastIndex(s, ")"); i >= 0 {
			rest = s[i+1:]
		}
		fields := strings.Fields(rest)
		if len(fields) >= 2 {
			return fields[1], nil // fields[0]=in/out, fields[1]=hi/lo
		}
		return "", fmt.Errorf("gpio-%d: no level in %q", n, s)
	}
	return "", fmt.Errorf("gpio-%d not present in the gpio dump", n)
}

// watchOnce opens (and optionally grabs) the device and reads events until an
// error, then returns so the caller retries after a backoff.
func watchOnce(cfg Config, toggler Toggler) {
	f, err := os.Open(cfg.Device)
	if err != nil {
		log.Warnf("button: open %s: %s (will retry)", cfg.Device, err)
		return
	}
	defer func() { _ = f.Close() }()

	if cfg.Grab {
		if err := unix.IoctlSetInt(int(f.Fd()), eviocgrab, 1); err != nil {
			log.Warnf("button: could not grab %s (%s); co-reading — firmware gestures will still fire", cfg.Device, err)
		} else {
			log.Infof("button: grabbed %s — this watcher now owns the button", cfg.Device)
		}
	}

	det := &detector{tapMax: tapMax, resetHold: resetHold}
	buf := make([]byte, eventSize)
	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			log.Warnf("button: read %s: %s (reopening)", cfg.Device, err)
			return
		}
		etype := binary.LittleEndian.Uint16(buf[16:18])
		code := binary.LittleEndian.Uint16(buf[18:20])
		value := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if etype != evKey {
			continue
		}
		if cfg.KeyCode != 0 && int(code) != cfg.KeyCode {
			continue
		}
		switch det.feed(value, time.Now()) {
		case gestureTap:
			raised, err := toggler.ToggleHand()
			if err != nil {
				log.Errorf("button: hand-raise toggle failed: %s", err)
			} else if raised {
				log.Info("button: hand raised")
			} else {
				log.Info("button: hand lowered")
			}
		case gestureReset:
			if cfg.OnFactoryReset != nil {
				log.Warn("button: held past the reset threshold — running factory reset")
				cfg.OnFactoryReset()
			}
		}
	}
}

type gesture int

const (
	gestureNone gesture = iota
	gestureTap
	gestureReset
)

// detector maps a press (down→up) to a gesture by its hold duration, split out
// for testing. Feed it evdev key values with the wall-clock time of the event.
type detector struct {
	tapMax    time.Duration
	resetHold time.Duration

	downAt time.Time
}

func (d *detector) feed(value int32, at time.Time) gesture {
	switch value {
	case valueDown:
		d.downAt = at
	case valueUp:
		if d.downAt.IsZero() {
			return gestureNone
		}
		hold := at.Sub(d.downAt)
		d.downAt = time.Time{}
		switch {
		case hold >= d.resetHold:
			return gestureReset
		case hold <= d.tapMax:
			return gestureTap
		default:
			return gestureNone
		}
	case valueRepeat:
		// ignore autorepeat while a key is held
	}
	return gestureNone
}
