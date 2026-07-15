package button

import (
	"testing"
	"time"
)

func newDetector() *detector {
	return &detector{tapMax: tapMax, resetHold: resetHold}
}

// A real debugfs dump from a NanoKVM-Pro. gpio-98 is the USR button
// (LinuxKeyMonitor3); it reads "hi" released and "lo" pressed.
const proGPIODump = `gpiochip2: GPIOs 64-95, parent: platform/6000000.gpio, 6000000.gpio:
 gpio-74  (                    |sysfs               ) in  hi
 gpio-82  (                    |LT86102UXC_HDMI_RXI ) in  hi IRQ

gpiochip3: GPIOs 96-127, parent: platform/6001000.gpio, 6001000.gpio:
 gpio-97  (                    |rotary@0            ) in  hi IRQ
 gpio-98  (                    |LinuxKeyMonitor3    ) in  lo
`

func TestParseGPIOLevel(t *testing.T) {
	if lvl, err := parseGPIOLevel(proGPIODump, 98); err != nil || lvl != "lo" {
		t.Fatalf("gpio-98: got %q, %v; want lo", lvl, err)
	}
	// A line with a trailing IRQ flag still yields the level (field after dir).
	if lvl, err := parseGPIOLevel(proGPIODump, 82); err != nil || lvl != "hi" {
		t.Fatalf("gpio-82: got %q, %v; want hi", lvl, err)
	}
	// gpio-9 must not match gpio-98 (prefix guard).
	if _, err := parseGPIOLevel(proGPIODump, 9); err == nil {
		t.Fatal("gpio-9 should be absent, not matched against gpio-98")
	}
	if _, err := parseGPIOLevel(proGPIODump, 500); err == nil {
		t.Fatal("absent line should error")
	}
}

func TestGPIOLineFromDevice(t *testing.T) {
	if n, ok := gpioLineFromDevice("gpio:98"); !ok || n != 98 {
		t.Fatalf("gpio:98 → %d,%v; want 98,true", n, ok)
	}
	for _, dev := range []string{"/dev/input/event0", "gpio:", "gpio:-1", "gpio:abc", "event1"} {
		if _, ok := gpioLineFromDevice(dev); ok {
			t.Fatalf("%q should not parse as a gpio spec", dev)
		}
	}
}

// press feeds a down then an up separated by hold, and returns the gesture the
// release produced.
func press(d *detector, downAt time.Time, hold time.Duration) gesture {
	d.feed(valueDown, downAt)
	return d.feed(valueUp, downAt.Add(hold))
}

func TestTapRaisesHand(t *testing.T) {
	d := newDetector()
	t0 := time.Unix(1_000_000, 0)
	for i, hold := range []time.Duration{10 * time.Millisecond, 200 * time.Millisecond, tapMax} {
		if g := press(d, t0.Add(time.Duration(i)*time.Second), hold); g != gestureTap {
			t.Errorf("hold %s: got %v, want gestureTap", hold, g)
		}
	}
}

func TestLongHoldResets(t *testing.T) {
	d := newDetector()
	t0 := time.Unix(1_000_000, 0)
	if g := press(d, t0, resetHold); g != gestureReset {
		t.Fatalf("hold == resetHold: got %v, want gestureReset", g)
	}
	if g := press(d, t0.Add(30*time.Second), resetHold+3*time.Second); g != gestureReset {
		t.Fatalf("hold > resetHold: got %v, want gestureReset", g)
	}
}

func TestMediumHoldIgnored(t *testing.T) {
	d := newDetector()
	t0 := time.Unix(1_000_000, 0)
	// Between tapMax and resetHold — formerly the firmware's WiFi gesture, now
	// deliberately nothing.
	for i, hold := range []time.Duration{tapMax + time.Millisecond, 3 * time.Second, resetHold - time.Millisecond} {
		if g := press(d, t0.Add(time.Duration(i)*time.Minute), hold); g != gestureNone {
			t.Errorf("hold %s: got %v, want gestureNone", hold, g)
		}
	}
}

func TestUpWithoutDownIgnored(t *testing.T) {
	d := newDetector()
	if g := d.feed(valueUp, time.Unix(1_000_000, 0)); g != gestureNone {
		t.Fatalf("stray release: got %v, want gestureNone", g)
	}
}

func TestAutorepeatIgnored(t *testing.T) {
	d := newDetector()
	t0 := time.Unix(1_000_000, 0)
	d.feed(valueDown, t0)
	for i := 0; i < 3; i++ {
		if g := d.feed(valueRepeat, t0.Add(time.Duration(i+1)*time.Second)); g != gestureNone {
			t.Fatal("autorepeat should never produce a gesture")
		}
	}
	// A tap-length total hold still resolves to a tap on release.
	if g := d.feed(valueUp, t0.Add(300*time.Millisecond)); g != gestureTap {
		t.Fatalf("release after autorepeat: got %v, want gestureTap", g)
	}
}
