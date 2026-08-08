package application

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// packedDrive writes a bundle containing a gzipped image whose 510th byte pair
// is the FAT boot signature, with body distinguishing one build from another.
func packedDrive(t *testing.T, dir, body string) string {
	t.Helper()
	img := make([]byte, 1024)
	copy(img, body)
	img[510], img[511] = 0x55, 0xAA

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(img); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "usbdisk.img.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// point installUsbDisk at a temp dir instead of /data.
func redirect(t *testing.T) (image, stamp string) {
	t.Helper()
	dir := t.TempDir()
	image, stamp = filepath.Join(dir, "usbdisk.img"), filepath.Join(dir, ".usbdisk.stamp")
	oldI, oldS := usbDiskImageForTest, usbDiskStampForTest
	usbDiskImageForTest, usbDiskStampForTest = image, stamp
	t.Cleanup(func() { usbDiskImageForTest, usbDiskStampForTest = oldI, oldS })
	return image, stamp
}

func TestInstallUsbDiskWritesThenSkipsTheSameBuild(t *testing.T) {
	bundle := t.TempDir()
	packedDrive(t, bundle, "build-one")
	image, stamp := redirect(t)

	if !installUsbDisk(bundle) {
		t.Fatal("first install should write the drive")
	}
	if _, err := os.Stat(image); err != nil {
		t.Fatalf("drive not written: %v", err)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatalf("stamp not written: %v", err)
	}

	// An unchanged release must not rewrite the drive — that is an SD-card
	// write on every routine update for no reason.
	if installUsbDisk(bundle) {
		t.Fatal("second install of the same build should be a no-op")
	}
}

// The whole reason the stamp exists: a device provisioned once must still pick
// up a fixed launcher, instead of keeping its original drive for good.
func TestInstallUsbDiskRefreshesWhenTheBuildChanges(t *testing.T) {
	bundle := t.TempDir()
	packedDrive(t, bundle, "build-one")
	image, _ := redirect(t)

	if !installUsbDisk(bundle) {
		t.Fatal("first install should write the drive")
	}
	packedDrive(t, bundle, "build-two")
	if !installUsbDisk(bundle) {
		t.Fatal("a changed build should rewrite the drive")
	}

	got, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, []byte("build-two")) {
		t.Fatalf("drive still holds the old build: %q", got[:16])
	}
}

// A bundle whose image is not a filesystem must leave whatever is there alone.
// Handing a host a half-written volume makes Windows demand the customer format
// their KVM, which is worse than no drive at all.
func TestInstallUsbDiskRejectsAnUnformattedImage(t *testing.T) {
	bundle := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(make([]byte, 1024)) // all zeros: no 55AA signature
	_ = zw.Close()
	if err := os.WriteFile(filepath.Join(bundle, "usbdisk.img.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	image, _ := redirect(t)
	if err := os.WriteFile(image, []byte("existing drive"), 0o644); err != nil {
		t.Fatal(err)
	}

	if installUsbDisk(bundle) {
		t.Fatal("an unformatted bundle image must not be installed")
	}
	got, _ := os.ReadFile(image)
	if string(got) != "existing drive" {
		t.Fatalf("existing drive was clobbered: %q", got)
	}
	if _, err := os.Stat(image + ".new"); !os.IsNotExist(err) {
		t.Fatal("staging file left behind")
	}
}

// A bundle from before the drive shipped is not an error.
func TestInstallUsbDiskIgnoresABundleWithoutOne(t *testing.T) {
	redirect(t)
	if installUsbDisk(t.TempDir()) {
		t.Fatal("a bundle with no usbdisk.img.gz should be a quiet no-op")
	}
}
