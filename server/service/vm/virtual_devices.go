package vm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	virtualNetwork     = "/boot/usb.ncm"
	virtualNetworkFlag = "/sys/kernel/config/usb_gadget/g0/configs/c.1/ncm.usb0"

	virtualMic     = "/boot/usb.uac2"
	virtualMicFlag = "/sys/kernel/config/usb_gadget/g0/configs/c.1/uac2.usb0"

	virtualDiskSDCard = "/boot/usb.disk1.sd"
	virtualDiskEmmc   = "/boot/usb.disk1.emmc"
	virtualDiskFlag   = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk1/lun.0/file"
)

const (
	scriptUsbDev    = "/kvmapp/scripts/usbdev.sh"
	scriptMountEmmc = "/kvmcomm/scripts/mount_emmc.py"
	// scriptUsbNet shares the KVM's uplink internet with the USB-tethered host
	// (usb0 → uplink NAT). Shipped by the image overlay; best-effort (always
	// exits 0), so chaining it onto a toggle can't fail the toggle.
	scriptUsbNet = "/usr/local/bin/usbnet-share.sh"
	// scriptUsbDhcp hands the USB-tethered host an address. Without it the
	// gadget comes up and the host's USB adapter sits unaddressed, so the KVM's
	// own usb0 address is unreachable from the one machine that should always
	// be able to see it. Best-effort like the above (always exits 0).
	scriptUsbDhcp = "/usr/local/bin/usbdhcp.sh"
)

const (
	sdCardFlag = "/dev/mmcblk1"
	emmcFlag   = "/exfat.img"

	diskTypeSDCard = "sdcard"
	diskTypeEmmc   = "emmc"
)

type VirtualDevice interface {
	IsMounted() bool
	Mount() error
	Unmount() error
}

func (s *Service) GetVirtualDevice(c *gin.Context) {
	var rsp proto.Response

	rsp.OkRspWithData(c, &proto.GetVirtualDeviceRsp{
		IsNetworkEnabled: isVirtualNetworkMounted(),
		IsMicEnabled:     isVirtualMicMounted(),
		MountedDisk:      getMountedDiskType(),
		IsSdCardExist:    isSdCardPresent(),
		IsEmmcExist:      isEmmcImagePresent(),
	})

	log.Debug("get virtual device success")
}

func (s *Service) UpdateVirtualDevice(c *gin.Context) {
	var req proto.UpdateVirtualDeviceReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid argument")
		return
	}

	commands, err := resolveDeviceCommands(req.Device, req.Type)
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}

	if err := executeWithHidLock(commands); err != nil {
		log.Errorf("failed to execute virtual device commands: %s", err)
		rsp.ErrRsp(c, -3, "failed to mount device")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("update virtual device %s success", req.Device)
}

func (s *Service) RefreshVirtualDevice(c *gin.Context) {
	var req proto.RefreshVirtualDeviceReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil || req.Device != "emmc" {
		rsp.ErrRsp(c, -1, "invalid argument")
		return
	}

	err := exec.Command(scriptMountEmmc, "restart").Run()
	if err != nil {
		log.Errorf("failed to restart emmc image: %v", err)
		rsp.ErrRsp(c, -2, "failed to refresh")
		return
	}

	rsp.OkRsp(c)
	log.Debug("refresh virtual device success")
}

func resolveDeviceCommands(device, diskType string) ([]string, error) {
	switch device {
	case "network":
		return getNetworkCommands(), nil
	case "mic":
		return getMicCommands(), nil
	case "disk":
		return getDiskCommands(diskType)
	default:
		return nil, fmt.Errorf("invalid device: %s", device)
	}
}

func getNetworkCommands() []string {
	// isVirtualNetworkMounted reflects the CURRENT (pre-toggle) gadget state, so
	// "not mounted" means this call is turning the network ON.
	enabling := !isVirtualNetworkMounted()

	cmd := []string{
		scriptUsbDev + " stop",
	}

	if enabling {
		cmd = append(cmd, "touch "+virtualNetwork)
	} else {
		cmd = append(cmd, "rm -rf "+virtualNetwork)
	}

	cmd = append(cmd, scriptUsbDev+" start")

	// Bring internet sharing up/down with the gadget so the tether extends the
	// host's connectivity instead of black-holing its default route.
	// Address the host BEFORE sharing an uplink with it: the masquerade rule is
	// scoped to usb0's subnet, which only exists once the interface is up and
	// addressed.
	if enabling {
		cmd = append(cmd, scriptUsbDhcp+" start", scriptUsbNet+" start")
	} else {
		cmd = append(cmd, scriptUsbNet+" stop", scriptUsbDhcp+" stop")
	}
	return cmd
}

func getMicCommands() []string {
	cmd := []string{
		scriptUsbDev + " stop",
	}

	if !isVirtualMicMounted() {
		cmd = append(cmd, "touch "+virtualMic)
	} else {
		cmd = append(cmd, "rm -rf "+virtualMic)
	}

	cmd = append(cmd, scriptUsbDev+" start")
	return cmd
}

func getDiskCommands(diskType string) ([]string, error) {
	// For emmc, ensure image exists before mounting
	if diskType == diskTypeEmmc {
		if err := ensureEmmcImageExists(); err != nil {
			return nil, err
		}
	}

	cmd := []string{
		"rm -f " + virtualDiskSDCard,
		"rm -f " + virtualDiskEmmc,
	}

	// add mount command
	if getMountedDiskType() != diskType {
		if diskType == diskTypeSDCard {
			cmd = append(cmd, "touch "+virtualDiskSDCard)
		} else if diskType == diskTypeEmmc {
			cmd = append(cmd, "touch "+virtualDiskEmmc)
		}
	}

	cmd = append(cmd, scriptUsbDev+" restart")
	return cmd, nil
}

func ensureEmmcImageExists() error {
	if isFileExists(emmcFlag) {
		return nil
	}

	result, err := utils.RunShell(scriptMountEmmc + " start")
	if err != nil {
		return fmt.Errorf("failed to create emmc image: %v (exit code: %d, output: %s)",
			err, result.ExitCode, result.Stdout)
	}

	return nil
}

func executeWithHidLock(commands []string) error {
	h := hid.GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	result, err := utils.RunShell(commands...)
	if err != nil {
		return fmt.Errorf("command failed: %v (exit code: %d, stdout: %s, stderr: %s)",
			err, result.ExitCode, result.Stdout, result.Stderr)
	}

	return nil
}

func getMountedDiskType() string {
	if !isFileExists(virtualDiskFlag) {
		return ""
	}

	content, err := os.ReadFile(virtualDiskFlag)
	if err != nil {
		return ""
	}

	disk := strings.TrimSpace(string(content))

	switch disk {
	case sdCardFlag, "/dev/mmcblk1p1":
		return diskTypeSDCard
	case emmcFlag:
		return diskTypeEmmc
	default:
		return ""
	}
}

func isVirtualNetworkMounted() bool {
	return isFileExists(virtualNetworkFlag)
}

func isVirtualMicMounted() bool {
	return isFileExists(virtualMicFlag)
}

func isSdCardPresent() bool {
	return isFileExists(sdCardFlag)
}

func isEmmcImagePresent() bool {
	return isFileExists(emmcFlag)
}

func isFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// usbNetAutoMarker records, under the mesh home dir, that the USB network was
// auto-enabled for first claim. Its presence — not the flag file's — is what
// makes that a one-time act.
const usbNetAutoMarker = ".usbnet-auto"

// EnsureUsbNetworkForClaim brings the USB network gadget up on a device nobody
// has claimed yet, so the machine it is physically plugged into can reach it
// over the cable and claim it there.
//
// This is the setup path for an appliance with no network: the USB gadget needs
// no LAN, no router, no DHCP server and no Wi-Fi credentials, and the KVM's web
// server already binds every interface, so the full API answers on it. Without
// this a factory-fresh device is only reachable by first getting it onto a
// network — the thing you often need the KVM to help you do.
//
// Deliberately once per device, tracked by a marker rather than by the flag
// file. The flag's absence can't distinguish "never configured" from
// "deliberately turned off", so keying on it would put the gadget back every
// boot and overrule an operator who switched it off. The marker is written only
// after the gadget is actually up, so a failed attempt retries next boot
// instead of silently giving up. It lives under the mesh home dir, which a
// reflash wipes — a re-imaged device should get the setup path again.
//
// It never turns the gadget OFF. Claiming over the USB link is the whole point,
// and disabling on claim would cut the very connection the claim arrived on.
//
// Best-effort and self-silencing: every failure is logged and returns.
func EnsureUsbNetworkForClaim(claimed bool, stateDir string) {
	if claimed || stateDir == "" {
		return
	}
	marker := filepath.Join(stateDir, usbNetAutoMarker)
	if _, err := os.Stat(marker); err == nil {
		return
	}

	// Already on (an image that ships it, or a flag placed by hand on the SD
	// card): nothing to do, but record it so we never reconsider.
	if !isVirtualNetworkMounted() {
		// Write the flag ONLY; do NOT recompose the running gadget. Tearing one
		// down is not something the usbdev script can actually do — it unbinds
		// the UDC and leaves the configfs tree standing — so "enabling" live
		// mutates a composite the attached host is using rather than rebuilding
		// it, and that can take its keyboard and mouse out. Breaking the KVM's
		// whole reason for existing to enable a convenience is a bad trade.
		//
		// The gadget is built cleanly once per boot from these flags, before
		// this server starts, so writing the flag is enough. This image already
		// ships overlay/boot/usb.ncm, so a factory device never reaches here.
		if err := os.WriteFile(virtualNetwork, nil, 0o644); err != nil {
			log.Warnf("usb network auto-enable: write %s: %s", virtualNetwork, err)
			return
		}
		log.Infof("usb network enabled for first claim — takes effect on the next boot (the running gadget is left alone on purpose)")
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		log.Warnf("usb network auto-enable: create %s: %s", stateDir, err)
		return
	}
	if err := os.WriteFile(marker, []byte("1\n"), 0o644); err != nil {
		log.Warnf("usb network auto-enable: write %s: %s", marker, err)
	}
}
