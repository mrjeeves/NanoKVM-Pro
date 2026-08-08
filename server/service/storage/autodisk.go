package storage

import (
	"os"
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"
)

// kvmDriveImage is the KVM's own USB drive — the small FAT volume labelled
// "CEC KVM" carrying the CEC Support launcher and our icon, written by the
// updater (service/application/usbdisk.go).
const (
	kvmDriveImage = "/data/usbdisk.img"
	// lunFile is the gadget's backing-file attribute, under functions/ rather
	// than the configs/ symlink AttachDefaultDrive writes through — same
	// attribute either way.
	lunFile = mountDevice
)

// AttachDefaultDrive puts our drive in front of the attached machine at startup,
// so the KVM always presents something the user can open — the whole point of
// the drive is to surface our files, and a drive nobody sees does not do that.
//
// It only ever fills an EMPTY slot. Virtual media shares this one LUN, so a
// mounted ISO always wins: this runs at startup, and MountImage overwrites the
// backing file afterwards exactly as it does today. Unmounting media leaves the
// slot empty again, and the next boot re-attaches the drive.
//
// Deliberately not called on unmount: yanking the ISO out from under a host that
// is mid-install and instantly re-enumerating a different volume in its place is
// worse than a moment with no drive.
//
// Best-effort and silent on a device without the gadget (a dev box, or a Pro
// whose USB stack has not composed disk0 yet) — this is a convenience, and it
// must never be the reason the server fails to start.
func AttachDefaultDrive() {
	if _, err := os.Stat(kvmDriveImage); err != nil {
		return // no drive installed yet (a bundle from before it shipped)
	}
	if _, err := os.Stat(mountDevice); err != nil {
		return // no mass-storage LUN on this device
	}

	current, err := os.ReadFile(mountDevice)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(current)) != "" {
		return // something is already mounted; leave it alone
	}

	// Same sequence MountImage uses: point the LUN at the image, then let the
	// gadget script re-enumerate so the host sees it appear.
	if err := os.WriteFile(mountDevice, []byte(kvmDriveImage), 0o666); err != nil {
		log.Debugf("kvm drive: attach %s: %s", kvmDriveImage, err)
		return
	}
	// A writable disk, not a CD-ROM: the customer can drop files on it, and the
	// launcher is a file to run rather than an installer to autorun off media.
	_ = os.WriteFile(cdromFlag, []byte("0"), 0o666)
	_ = os.WriteFile(roFlag, []byte("0"), 0o666)

	if _, err := os.Create(usbDisk); err != nil {
		log.Debugf("kvm drive: create %s: %s", usbDisk, err)
		return
	}
	if err := exec.Command("sh", "-c", "/dev/shm/kvmapp/scripts/usbdev.sh restart").Run(); err != nil {
		log.Debugf("kvm drive: usbdev restart: %s", err)
		return
	}
	log.Infof("kvm drive attached (%s)", kvmDriveImage)
}

// DetachDriveBacking releases the drive image so it can be replaced, and
// reports whether it did.
//
// This MUST happen before the updater swaps the image. installUsbDisk stages
// and renames, and the gadget has that exact path open as its backing store —
// replacing the inode underneath a live LUN leaves the host mid-transaction
// with a block device whose identity changed, which is what surfaces on Windows
// as "USB device not recognized". Detaching first turns that into an ordinary
// media-removed event, which every OS already knows how to handle.
//
// Returns false when there is nothing composed, or when the LUN points at
// something that is not ours — virtual media the operator mounted is theirs,
// and a drive refresh must never eject it.
func DetachDriveBacking() bool {
	if _, err := os.Stat(lunFile); err != nil {
		return false
	}
	current, err := os.ReadFile(lunFile)
	if err != nil {
		return false
	}
	if strings.TrimSpace(string(current)) != kvmDriveImage {
		return false
	}
	if err := os.WriteFile(lunFile, []byte("\n"), 0o666); err != nil {
		log.Warnf("usb drive: detach before refresh: %s", err)
		return false
	}
	return true
}

// ReattachDriveBacking points the LUN back at the drive image. Pair it with
// DetachDriveBacking on every path, including the ones where the install did
// nothing — leaving the host with no media because an update decided not to
// rewrite the drive would be a worse bug than the one this avoids.
func ReattachDriveBacking() {
	if _, err := os.Stat(lunFile); err != nil {
		return
	}
	if !driveImageReady(kvmDriveImage) {
		log.Warnf("usb drive: image is not a filesystem after refresh; leaving the LUN empty")
		return
	}
	if err := os.WriteFile(lunFile, []byte(kvmDriveImage), 0o666); err != nil {
		log.Warnf("usb drive: re-attach after refresh: %s", err)
		return
	}
	log.Infof("usb drive: re-attached after refresh")
}

// driveImageReady reports whether path is a file carrying a FAT boot signature.
// Presence is not readiness: handing a host a half-written volume makes Windows
// demand the customer format their KVM, which is worse than no drive at all.
func driveImageReady(path string) bool {
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
