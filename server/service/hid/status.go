package hid

import (
	"NanoKVM-Server/proto"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	ModeNormal  = "normal"
	ModeHidOnly = "hid-only"
	HidOnlyFlag = "/dev/shm/tmp/hid_only"
	USBScript   = "/kvmapp/scripts/usbdev.sh"
)

var hidModeCmdMap = map[string]string{
	ModeNormal:  "restart",
	ModeHidOnly: "hid-only",
}

func (s *Service) GetHidMode(c *gin.Context) {
	var rsp proto.Response

	mode := getHidMode()

	rsp.OkRspWithData(c, &proto.GetHidModeRsp{
		Mode: mode,
	})
	log.Debugf("get hid mode: %s", mode)
}

func (s *Service) SetHidMode(c *gin.Context) {
	var req proto.SetHidModeReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	arg, ok := hidModeCmdMap[req.Mode]
	if !ok {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}

	if mode := getHidMode(); req.Mode == mode {
		rsp.OkRsp(c)
		return
	}

	h := GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	if err := exec.Command("bash", USBScript, arg).Run(); err != nil {
		rsp.ErrRsp(c, -3, "failed to set hid mode")
		log.Errorf("Failed to execute script: %v: %s", USBScript, err)
		return
	}

	time.Sleep(3 * time.Second)

	rsp.OkRsp(c)
	log.Debugf("set hid mode: %s", req.Mode)
}

func (s *Service) Reset(c *gin.Context) {
	var rsp proto.Response

	if err := ResetGadget(); err != nil {
		rsp.ErrRsp(c, -1, "failed to reset")
		log.Errorf("reset hid failed: %s", err)
		return
	}

	rsp.OkRsp(c)
	log.Debugf("reset hid success")
}

// ResetGadget rebuilds the USB gadget with usbdev.sh, in the current HID mode,
// closing the HID descriptors across the operation and reopening them after —
// the script recreates /dev/hidg*, so a descriptor held open across it refers
// to a function that no longer exists.
//
// Factored out of the Reset handler so the USB supervisor can take the same
// path a user pressing "reset" takes, rather than growing a second, subtly
// different way to rebuild the same gadget.
func ResetGadget() error {
	mode := getHidMode()
	arg, ok := hidModeCmdMap[mode]
	if !ok {
		return fmt.Errorf("invalid hid mode: %s", mode)
	}

	h := GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	if err := exec.Command("bash", USBScript, arg).Run(); err != nil {
		return fmt.Errorf("run %s %s: %w", USBScript, arg, err)
	}

	// usbdev.sh returns before the gadget has finished re-enumerating; the
	// descriptors are not there yet if we reopen immediately.
	time.Sleep(3 * time.Second)
	return nil
}

func getHidMode() string {
	_, err := os.Stat(HidOnlyFlag)
	if err != nil {
		return ModeNormal
	}
	return ModeHidOnly
}
