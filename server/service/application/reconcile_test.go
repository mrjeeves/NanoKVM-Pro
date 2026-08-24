package application

import (
	"os"
	"path/filepath"
	"testing"
)

// bundleWithDaemon builds a temp "extracted bundle" dir carrying a myownmesh
// binary with the given content.
func bundleWithDaemon(t *testing.T, root, content string) string {
	t.Helper()
	dir := filepath.Join(root, "bundle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "myownmesh"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func installedDaemon(t *testing.T, root, content string) string {
	t.Helper()
	p := filepath.Join(root, "system", "bin", "myownmesh")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInstallDaemonFromBundleReplacesChanged(t *testing.T) {
	root := t.TempDir()
	bundleDir := bundleWithDaemon(t, root, "new-daemon")
	dst := installedDaemon(t, root, "old-daemon")

	changed, err := installDaemonFromBundle(bundleDir, dst)
	if err != nil {
		t.Fatalf("installDaemonFromBundle: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for a differing daemon")
	}
	if got := read(t, dst); got != "new-daemon" {
		t.Errorf("daemon = %q, want new-daemon", got)
	}
	for _, leftover := range []string{"myownmesh.new", "myownmesh.old"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(dst), leftover)); !os.IsNotExist(err) {
			t.Errorf("leftover %s not cleaned up", leftover)
		}
	}
}

func TestInstallDaemonFromBundleFreshInstall(t *testing.T) {
	root := t.TempDir()
	bundleDir := bundleWithDaemon(t, root, "the-daemon")
	dst := filepath.Join(root, "system", "bin", "myownmesh") // nothing installed

	changed, err := installDaemonFromBundle(bundleDir, dst)
	if err != nil {
		t.Fatalf("installDaemonFromBundle: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true when no daemon was installed")
	}
	if got := read(t, dst); got != "the-daemon" {
		t.Errorf("daemon = %q, want the-daemon", got)
	}
}

func TestInstallDaemonFromBundleUnchanged(t *testing.T) {
	root := t.TempDir()
	bundleDir := bundleWithDaemon(t, root, "same-daemon")
	dst := installedDaemon(t, root, "same-daemon")

	changed, err := installDaemonFromBundle(bundleDir, dst)
	if err != nil {
		t.Fatalf("installDaemonFromBundle: %v", err)
	}
	if changed {
		t.Error("changed = true, want false for a byte-identical daemon")
	}
	if _, err := os.Stat(dst + ".old"); !os.IsNotExist(err) {
		t.Errorf("an unchanged daemon should not create a backup")
	}
}

func TestInstallDaemonFromBundleNoDaemonInBundle(t *testing.T) {
	root := t.TempDir()
	bundleDir := filepath.Join(root, "bundle")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := installedDaemon(t, root, "keep-me")

	changed, err := installDaemonFromBundle(bundleDir, dst)
	if err != nil {
		t.Fatalf("installDaemonFromBundle: %v", err)
	}
	if changed {
		t.Error("changed = true, want false when the bundle has no daemon")
	}
	if got := read(t, dst); got != "keep-me" {
		t.Errorf("daemon = %q, want keep-me (untouched)", got)
	}
}

func TestAtomicSwapFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.WriteFile(src, []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Over an existing dst.
	dst := filepath.Join(root, "nested", "dst")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicSwapFile(src, dst); err != nil {
		t.Fatalf("atomicSwapFile over existing: %v", err)
	}
	if got := read(t, dst); got != "payload" {
		t.Errorf("dst = %q, want payload", got)
	}
	if _, err := os.Stat(dst + ".old"); !os.IsNotExist(err) {
		t.Errorf("backup not cleaned up")
	}
	if _, err := os.Stat(dst + ".new"); !os.IsNotExist(err) {
		t.Errorf("stage not cleaned up")
	}

	// Onto a path with no existing dst (dir auto-created).
	fresh := filepath.Join(root, "brand", "new", "dst")
	if err := atomicSwapFile(src, fresh); err != nil {
		t.Fatalf("atomicSwapFile fresh: %v", err)
	}
	if got := read(t, fresh); got != "payload" {
		t.Errorf("fresh dst = %q, want payload", got)
	}
}

// writeOverlay stages a bundle whose overlay/ mirrors where the helpers live.
func writeOverlay(t *testing.T, bundle string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(bundle, "overlay", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const meshTestUnit = "[Unit]\nDescription=MyOwnMesh daemon\n\n[Service]\nExecStart=/kvmapp/system/bin/myownmesh serve\nSyslogIdentifier=myownmesh\n\n[Install]\nWantedBy=multi-user.target\n"

const testUnit = "[Unit]\nDescription=usb dhcp\n\n[Service]\nExecStart=/usr/local/bin/usbdhcp.sh start\n\n[Install]\nWantedBy=multi-user.target\n"

// The Pro's half of the gap, and the wider one: its helper scripts and units
// shipped ONLY in the image overlay, so they reached a device by writing a new
// SD card and by no other means. An update delivered a server that expected
// them with no way to bring them — silently, on exactly the devices already in
// the field.
func TestInstallOverlayPlacesHelpersAndEnablesUnits(t *testing.T) {
	root := t.TempDir()
	bundle := bundleWithDaemon(t, root, "new-daemon")
	writeOverlay(t, bundle, map[string]string{
		"usr/local/bin/usbdhcp.sh":           "#!/bin/sh\nexit 0\n",
		"etc/systemd/system/usbdhcp.service": testUnit,
	})
	daemon := installedDaemon(t, root, "old-daemon")

	target := t.TempDir()
	orig := overlayRootForTest
	overlayRootForTest = target
	defer func() { overlayRootForTest = orig }()

	daemonChanged, helpers, err := installReleaseFromBundle(bundle, daemon)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !daemonChanged {
		t.Error("daemon differed but was not replaced")
	}
	if helpers != 2 {
		t.Fatalf("installed %d helper files, want 2", helpers)
	}

	// The script must land executable: systemd execs it, and a lost +x bit is
	// indistinguishable from a file that never shipped.
	script := filepath.Join(target, "usr/local/bin/usbdhcp.sh")
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatalf("script not installed: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("script mode %v is not executable", fi.Mode().Perm())
	}
	// A unit file must NOT be.
	ufi, err := os.Stat(filepath.Join(target, "etc/systemd/system/usbdhcp.service"))
	if err != nil {
		t.Fatalf("unit not installed: %v", err)
	}
	if ufi.Mode().Perm()&0o111 != 0 {
		t.Errorf("unit mode %v should not be executable", ufi.Mode().Perm())
	}
	// …and it must be wired to run at boot, the same wants symlink the image
	// build writes. Installing the unit without this is a file nothing reads.
	link := filepath.Join(target, systemdWantsDir, "usbdhcp.service")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("unit not enabled at boot: %v", err)
	}

	// Re-running changes nothing: no rewrites onto flash, no duplicate symlink.
	if n, mesh := installOverlay(bundle); n != 0 || mesh {
		t.Errorf("re-install wrote %d files (mesh=%v), want 0, false", n, mesh)
	}
}

// A unit nothing wants at multi-user is one something else pulls in. Enabling
// it anyway would start it in a context its author didn't choose.
func TestInstallOverlaySkipsUnitWithoutWantedBy(t *testing.T) {
	root := t.TempDir()
	bundle := bundleWithDaemon(t, root, "d")
	writeOverlay(t, bundle, map[string]string{
		"etc/systemd/system/oneshot.service": "[Unit]\nDescription=x\n\n[Service]\nExecStart=/bin/true\n",
	})

	target := t.TempDir()
	orig := overlayRootForTest
	overlayRootForTest = target
	defer func() { overlayRootForTest = orig }()

	if n, _ := installOverlay(bundle); n != 1 {
		t.Fatalf("installed %d files, want 1", n)
	}
	if _, err := os.Lstat(filepath.Join(target, systemdWantsDir, "oneshot.service")); err == nil {
		t.Error("a unit with no WantedBy was enabled at boot")
	}
}

// A unit change reaches a device with an already-current daemon binary — the
// case this exists for. systemd reads a unit only when the service starts, so
// reporting the change is what turns a file on disk into a setting in force;
// without it a journal rate limit or an ordering fix would sit inert until
// something else rebooted the box.
func TestInstallReleaseRestartsDaemonForUnitChangeAlone(t *testing.T) {
	root := t.TempDir()
	bundle := bundleWithDaemon(t, root, "same-daemon")
	writeOverlay(t, bundle, map[string]string{
		"etc/systemd/system/myownmesh.service": meshTestUnit,
	})
	// Byte-identical to the bundle's: the daemon step is a no-op, so a restart
	// can only be asked for by the unit.
	daemon := installedDaemon(t, root, "same-daemon")

	target := t.TempDir()
	orig := overlayRootForTest
	overlayRootForTest = target
	defer func() { overlayRootForTest = orig }()

	restart, helpers, err := installReleaseFromBundle(bundle, daemon)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if helpers != 1 {
		t.Fatalf("installed %d helper files, want 1", helpers)
	}
	if !restart {
		t.Error("the mesh unit changed but no daemon restart was asked for")
	}
	if _, err := os.Stat(filepath.Join(target, "etc/systemd/system/myownmesh.service")); err != nil {
		t.Fatalf("mesh unit not installed: %v", err)
	}

	// Converged: a second pass must not ask for another restart, or every
	// reconcile would bounce the mesh tunnel for nothing.
	restart, helpers, err = installReleaseFromBundle(bundle, daemon)
	if err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if helpers != 0 || restart {
		t.Errorf("re-install wrote %d files and asked restart=%v, want 0, false", helpers, restart)
	}
}

// The prestart script is half of the same service: the unit runs it by path at
// ExecStartPre, so a change to it also only lands on a start.
func TestInstallOverlayReportsMeshPrestartChange(t *testing.T) {
	root := t.TempDir()
	bundle := bundleWithDaemon(t, root, "d")
	writeOverlay(t, bundle, map[string]string{
		"kvmapp/system/bin/myownmesh-prestart.sh": "#!/bin/sh\nexit 0\n",
	})

	target := t.TempDir()
	orig := overlayRootForTest
	overlayRootForTest = target
	defer func() { overlayRootForTest = orig }()

	n, mesh := installOverlay(bundle)
	if n != 1 || !mesh {
		t.Fatalf("installed %d files (mesh=%v), want 1, true", n, mesh)
	}
	// It must land executable — systemd execs it, and a lost +x bit fails the
	// unit's ExecStartPre, which takes the whole daemon down with it.
	fi, err := os.Stat(filepath.Join(target, "kvmapp/system/bin/myownmesh-prestart.sh"))
	if err != nil {
		t.Fatalf("prestart not installed: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("prestart mode %v is not executable", fi.Mode().Perm())
	}
}

// A USB helper is NOT a mesh helper: those units reconfigure the link an
// update may have arrived over, so they stay boot-only and must never drag the
// mesh daemon into a restart.
func TestInstallOverlayDoesNotReportUsbHelperAsMesh(t *testing.T) {
	root := t.TempDir()
	bundle := bundleWithDaemon(t, root, "d")
	writeOverlay(t, bundle, map[string]string{
		"usr/local/bin/usbdhcp.sh":           "#!/bin/sh\nexit 0\n",
		"etc/systemd/system/usbdhcp.service": testUnit,
	})

	target := t.TempDir()
	orig := overlayRootForTest
	overlayRootForTest = target
	defer func() { overlayRootForTest = orig }()

	if n, mesh := installOverlay(bundle); n != 2 || mesh {
		t.Fatalf("installed %d files (mesh=%v), want 2, false", n, mesh)
	}
}

// A bundle from an older release has no overlay/. That must be a quiet no-op,
// never something that fails an update which already swapped in a working
// server and web.
func TestInstallOverlayIgnoresBundleWithout(t *testing.T) {
	if n, mesh := installOverlay(t.TempDir()); n != 0 || mesh {
		t.Fatalf("installed %d files from an empty bundle (mesh=%v)", n, mesh)
	}
}
