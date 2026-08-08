package vm

import (
	"os"
	"strings"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	LT6911Power        = "/proc/lt6911_info/power"
	LT6911HdmiPower    = "/proc/lt6911_info/hdmi_power"
	LT6911LoopoutPower = "/proc/lt6911_info/loopout_power"
)

func (s *Service) GetHdmiCapture(c *gin.Context) {
	var rsp proto.Response

	enabled, err := isHdmiEnabled(LT6911Power)
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to get HDMI capture status")
		return
	}

	rsp.OkRspWithData(c, &proto.GetHdmiCaptureRsp{
		Enabled: enabled,
	})
	log.Debugf("get HDMI capture status: %t", enabled)
}

func (s *Service) SetHdmiCapture(c *gin.Context) {
	var req proto.SetHdmiCaptureReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	status := "off"
	if req.Enabled {
		status = "on"
	}

	if err := os.WriteFile(LT6911Power, []byte(status), 0644); err != nil {
		rsp.ErrRsp(c, -2, "failed to set HDMI capture status")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("set HDMI capture status: %s", status)
}

func (s *Service) GetHdmiPassthrough(c *gin.Context) {
	var rsp proto.Response

	enabled, err := isHdmiEnabled(LT6911LoopoutPower)
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to get HDMI passthrough status")
		return
	}

	rsp.OkRspWithData(c, &proto.GetHdmiPassthroughRsp{
		Enabled: enabled,
	})
	log.Debugf("get HDMI passthrough status: %t", enabled)
}

func (s *Service) SetHdmiPassthrough(c *gin.Context) {
	var req proto.SetHdmiPassthroughReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	var err error
	if req.Enabled {
		err = enableHdmiPassthrough()
	} else {
		err = disableHdmiPassthrough()
	}

	if err != nil {
		rsp.ErrRsp(c, -2, "failed to set HDMI passthrough status")
		return
	}

	time.Sleep(10 * time.Millisecond)

	rsp.OkRsp(c)
	log.Debugf("set HDMI passthrough status: %t", req.Enabled)
}

func isHdmiEnabled(flag string) (bool, error) {
	content, err := os.ReadFile(flag)
	if err != nil {
		return false, err
	}

	enabled := strings.TrimSpace(string(content)) == "on"
	return enabled, nil
}

func enableHdmiPassthrough() error {
	if err := os.WriteFile(LT6911HdmiPower, []byte("0"), 0644); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(LT6911LoopoutPower, []byte("1"), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(LT6911HdmiPower, []byte("1"), 0644); err != nil {
		return err
	}
	return nil
}

func disableHdmiPassthrough() error {
	if err := os.WriteFile(LT6911LoopoutPower, []byte("0"), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(LT6911HdmiPower, []byte("0"), 0644); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(LT6911HdmiPower, []byte("1"), 0644); err != nil {
		return err
	}
	return nil
}

// RestoreHdmiPower asserts the HDMI capture path ON, and reports whether it had
// to change anything.
//
// This exists because of a mess we made. On-demand HDMI (reverted) powered the
// LT6911 down when nobody was watching, and this repo has never had a startup
// path that turns it back on — before that feature nothing ever turned it off,
// so nothing needed to. Reverting the feature restored that state faithfully
// and left every device the lease had powered down still dark: a server restart
// is what an update performs, and a restart alone was never going to write this
// file. Only a full reboot (the driver reloads at its default) or a manual trip
// through the web UI's capture toggle would.
//
// Asserting it at every start is also just correct on its own terms. The
// operator's setting lives in this same /proc attribute, so there is no
// persisted preference to override — a KVM that comes up with its capture path
// dark is a KVM with no picture, and the only way to know is to look.
func RestoreHdmiPower() (changed bool, err error) {
	was, readErr := isHdmiEnabled(LT6911Power)
	if writeErr := os.WriteFile(LT6911Power, []byte("on"), 0644); writeErr != nil {
		return false, writeErr
	}
	// An unreadable attribute is reported as a change: "we wrote it and cannot
	// prove it was already right" is the honest answer, and it only affects a
	// log line.
	return readErr != nil || !was, nil
}
