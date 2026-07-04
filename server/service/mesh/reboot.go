package mesh

import (
	"fmt"
	"os/exec"
	"strings"
)

// restartDevice asks the OS to reboot this machine — the action behind an
// owner's AppControl restart_device. Mirrors AllMyStuff's node/src/reboot.rs:
// the reboot is asked of the OS, never forced from here; attempts run in order
// and the first acceptance wins; if all refuse, the caller gets every reason —
// a refusal must be visible, not a silent nothing-happened. Blocking (waits on
// each command), so call it off the event-stream goroutine.
//
// The attempt order covers both worlds this binary runs in: systemd on the
// AX630C appliance (the `systemctl reboot` attempt lands first) and a busybox
// dev box (`reboot`, `shutdown` maybe absent).
func restartDevice() error {
	attempts := [][]string{
		{"systemctl", "reboot"},
		{"shutdown", "-r", "now"},
		{"reboot"},
	}
	var refusals []string
	for _, a := range attempts {
		if err := exec.Command(a[0], a[1:]...).Run(); err == nil {
			return nil
		} else {
			refusals = append(refusals, fmt.Sprintf("%s: %s", a[0], err))
		}
	}
	return fmt.Errorf("the OS refused the reboot (%s)", strings.Join(refusals, "; "))
}

// serverUnit is the systemd unit the NanoKVM-Pro server runs under — the same
// name the stock nanokvmpro deb installs, and the one the device's own docs use
// (`systemctl restart nanokvm`). restartServer targets it.
const serverUnit = "nanokvm"

// restartServer relaunches NanoKVM-Server onto the same build — AppControl
// "restart", one step lighter than a device reboot. The Pro is systemd (Ubuntu
// aarch64), so systemd owns the process and we ask it to restart the unit; the
// child is detached (Start, not Run) because the restart kills this very
// process — waiting on it would be waiting on our own funeral. We can't use the
// blocking D-Bus utils.RestartService for the same reason: the restart tears
// down the caller mid-call. Confirmation is the fresh process's presence advert,
// exactly like the Rust node's sink.restart() path.
func restartServer() error {
	cmd := exec.Command("systemctl", "restart", serverUnit)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("systemctl restart %s: %w", serverUnit, err)
	}
	// Detach: the child outlives us. Release rather than Wait so a zombie
	// isn't left if systemctl somehow returns before killing us.
	return cmd.Process.Release()
}
