package application

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

// The KVM's own USB drive: a small FAT volume, labelled "CEC KVM", carrying the
// CEC Support launcher and our icon. It exists to put OUR files in front of the
// machine the KVM is plugged into — the web UI's "Desktop App" link only helps
// someone already at a browser who knows where to go, and the machine on the
// other end of the cable often is not that.
//
// Built in CI by scripts/build-usbdisk.sh (the same script `just usbdisk` runs)
// and shipped in the release bundle. Building it on the device would put mkfs in
// the boot path of a board that has better things to do, and a half-written
// image is a drive Windows asks the customer to format.
const (
	usbDiskImage = "/data/usbdisk.img"
	// usbDiskStamp records the sha256 of the packed image the current drive was
	// written from. It is what makes the drive a MANAGED artifact rather than a
	// one-shot: without it, a device provisioned once would keep its original
	// launcher for good and never see a fix to it.
	usbDiskStamp = "/data/.usbdisk.stamp"
)

// Indirected so tests can point them somewhere writable.
var (
	usbDiskImageForTest = usbDiskImage
	usbDiskStampForTest = usbDiskStamp
)

// installUsbDisk lays down the release's prebuilt USB drive image when the
// device has no drive, or has one written from a different build. Reports
// whether it wrote one.
//
// The stamp comparison is deliberately on the PACKED image: it is the artifact
// the bundle actually carries, so an unchanged release is a byte-identical
// compare and costs one hash of a ~90 KB file, no decompression.
//
// Best-effort throughout: a failure here leaves the device without our drive,
// which is a missing convenience, not a broken KVM, and must never fail an
// update that has already swapped in a working server.
func installUsbDisk(bundleDir string) bool {
	src := filepath.Join(bundleDir, "usbdisk.img.gz")
	if !isFile(src) {
		return false // a bundle from an older release
	}

	want, err := sha256File(src)
	if err != nil {
		log.Warnf("update: usb drive: hash bundle image: %s", err)
		return false
	}
	if current, err := os.ReadFile(usbDiskStampForTest); err == nil &&
		string(current) == want && isFile(usbDiskImageForTest) {
		return false // already this build
	}

	dst := usbDiskImageForTest
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		log.Warnf("update: usb drive: %s", err)
		return false
	}

	// Stage and rename, so an interrupted write can never become the drive.
	// A file that exists but was never finished still passes a size check, and
	// a drive like that is one Windows asks the customer to format — worse than
	// no drive at all.
	stage := dst + ".new"
	if err := gunzipTo(src, stage); err != nil {
		_ = os.Remove(stage)
		log.Warnf("update: usb drive: %s", err)
		return false
	}
	if !looksFormatted(stage) {
		_ = os.Remove(stage)
		log.Warnf("update: usb drive image in the bundle isn't a filesystem; leaving the drive alone")
		return false
	}
	if err := os.Rename(stage, dst); err != nil {
		_ = os.Remove(stage)
		log.Warnf("update: usb drive: %s", err)
		return false
	}

	// Stamp last: a crash between the rename and this leaves a good drive with
	// a stale stamp, so the next update rewrites it. The reverse order would
	// leave a stamp claiming a drive that was never written.
	if err := os.WriteFile(usbDiskStampForTest, []byte(want), 0o644); err != nil {
		log.Warnf("update: usb drive: write stamp: %s", err)
	}
	log.Infof("update: USB drive written from this release")
	return true
}

// looksFormatted reports whether path carries a FAT boot signature. Presence is
// not readiness: a zero-filled or half-written file is a drive the host will
// demand be formatted, so it must never be handed to one.
func looksFormatted(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var sig [2]byte
	if _, err := f.ReadAt(sig[:], 510); err != nil {
		return false
	}
	return sig[0] == 0x55 && sig[1] == 0xAA
}

// gunzipTo decompresses src to dst.
func gunzipTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	zr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, zr); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
