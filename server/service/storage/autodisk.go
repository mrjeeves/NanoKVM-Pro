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
const kvmDriveImage = "/data/usbdisk.img"

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
