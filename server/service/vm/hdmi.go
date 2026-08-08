package vm

import (
	"os"
	"strings"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/viewer"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	LT6911Power        = "/proc/lt6911_info/power"
	LT6911HdmiPower    = "/proc/lt6911_info/hdmi_power"
	LT6911LoopoutPower = "/proc/lt6911_info/loopout_power"
)

// HdmiCaptureActive reports the receiver's live power state — the same value
// GetHdmiCapture hands the web UI. Used to seed the viewer lease at startup so
// it inherits the operator's setting rather than overriding it.
func HdmiCaptureActive() bool {
	enabled, err := isHdmiEnabled(LT6911Power)
	if err != nil {
		// Unreadable /proc: assume the receiver may be used, matching the
		// pre-lease behaviour of coming up live.
		return true
	}
	return enabled
}

// SetHdmiCaptureActive is the hardware seam used by the viewer lease manager
// as well as the HTTP setting. It changes live state only; it does not invent a
// second persisted preference behind the existing KVM UI.
func SetHdmiCaptureActive(enabled bool) error {
	status := "off"
	if enabled {
		status = "on"
	}
	return os.WriteFile(LT6911Power, []byte(status), 0644)
}

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

	if err := SetHdmiCaptureActive(req.Enabled); err != nil {
		rsp.ErrRsp(c, -2, "failed to set HDMI capture status")
		return
	}
	// The UI reads this setting back off live /proc state, so the lease has to
	// respect it: enabling re-arms on-demand activation, disabling pins the
	// receiver off however many viewers connect. Note keeps the lease's cached
	// view in step with the write we just made, so the next lease transition
	// is not skipped as a redundant no-op.
	viewer.Note(req.Enabled)
	viewer.SetAllowed(req.Enabled)

	rsp.OkRsp(c)
	log.Debugf("set HDMI capture status: %t", req.Enabled)
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
