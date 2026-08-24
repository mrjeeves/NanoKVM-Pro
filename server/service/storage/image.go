package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
)

var imageMountMu sync.Mutex

const (
	imageDirectory       = "/data"
	sdCardDirectory      = "/sdcard"
	mountDevice          = "/sys/kernel/config/usb_gadget/g0/configs/c.1/mass_storage.disk0/lun.0/file"
	cdromFlag            = "/sys/kernel/config/usb_gadget/g0/configs/c.1/mass_storage.disk0/lun.0/cdrom"
	roFlag               = "/sys/kernel/config/usb_gadget/g0/configs/c.1/mass_storage.disk0/lun.0/ro"
	usbDisk              = "/boot/usb.disk0"
	virtualMediaMetadata = "/data/.allmystuff-virtual-media.json"
	usbGadgetUDC         = "/sys/kernel/config/usb_gadget/g0/UDC"
	usbUDCClass          = "/sys/class/udc"
)

type virtualMediaState struct {
	Source string `json:"source"`
	Label  string `json:"label"`
	File   string `json:"file"`
}

func persistVirtualMedia(req proto.MountImageReq) {
	if req.File == "" {
		_ = os.Remove(virtualMediaMetadata)
		return
	}
	if req.Source == "" {
		return
	}
	data, err := json.Marshal(virtualMediaState{Source: req.Source, Label: req.Label, File: req.File})
	if err == nil {
		_ = os.WriteFile(virtualMediaMetadata, data, 0o600)
	}
}

func (s *Service) GetImages(c *gin.Context) {
	var rsp proto.Response
	var images []string

	for _, directory := range []string{imageDirectory, sdCardDirectory} {
		err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() {
				name := strings.ToLower(info.Name())
				if strings.HasSuffix(name, ".iso") || strings.HasSuffix(name, ".img") {
					images = append(images, path)
				}
			}

			return nil
		})

		if err != nil {
			log.Errorf("failed to get images: %s", err)
		}
	}

	rsp.OkRspWithData(c, &proto.GetImagesRsp{
		Files: images,
	})
	log.Debugf("get images success, total %d", len(images))
}

func (s *Service) MountImage(c *gin.Context) {
	var req proto.MountImageReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	// A direct mount/unmount replaces any lazy provider. The provider performs
	// this request in the same USB restart, then releases FUSE after the gadget
	// no longer has the virtual file open.
	handled, err := s.remote.replaceActive(req)
	if !handled {
		err = mountImage(req)
	}
	if err != nil {
		log.Errorf("mount image %s failed: %s", req.File, err)
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	if !handled {
		persistVirtualMedia(req)
	}
	rsp.OkRsp(c)
	log.Debugf("mount image %s success", req.File)
}

func mountImage(req proto.MountImageReq) (retErr error) {
	imageMountMu.Lock()
	defer imageMountMu.Unlock()

	// This function unbinds and rebinds the UDC as a matter of course. Tell the
	// watchdog, or the "not attached" it sees mid-mount is indistinguishable
	// from the dead link it exists to repair — and it would recover a gadget
	// that is being deliberately rebuilt underneath it. Marked again on the way
	// out, so the settle window covers the re-enumeration too, not just the
	// teardown.
	noteUSBGadgetMutated()
	defer noteUSBGadgetMutated()

	// usbdev.sh recreates every gadget function, including /dev/hidg*. Close
	// the old descriptors around the entire operation and wait for the new ones
	// before allowing input traffic to resume.
	h := hid.GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		if err := ensureUSBGadgetBound(); err != nil {
			if retErr == nil {
				retErr = fmt.Errorf("recover USB gadget: %w", err)
			} else {
				retErr = fmt.Errorf("%v; recover USB gadget: %w", retErr, err)
			}
		}
		time.Sleep(500 * time.Millisecond)
		h.OpenNoLock()
		h.Unlock()
	}()

	if req.File == "" {
		if err := os.WriteFile(mountDevice, []byte("\n"), 0o666); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("unmount image failed: %w", err)
		}
		if err := restartUSB(0); err != nil {
			return fmt.Errorf("unmount image failed: %w", err)
		}
		time.Sleep(3 * time.Second)
		return nil
	}

	if err := restartUSB(1); err != nil {
		_ = ensureUSBGadgetBound()
		return fmt.Errorf("mount image failed: %w", err)
	}
	rollback := func(cause error) error {
		// A failed media change must still leave keyboard, mouse, and networking
		// as a valid composite gadget. Removing the incomplete storage function is
		// safer than leaving usbdev.sh's half-created LUN attached.
		if err := restartUSB(0); err != nil {
			_ = ensureUSBGadgetBound()
			return fmt.Errorf("%v; restore USB gadget without failed media: %w", cause, err)
		}
		return cause
	}
	cdrom := "0"
	readOnly := "0"
	if req.Cdrom {
		cdrom, readOnly = "1", "1"
	}
	if req.ReadOnly {
		readOnly = "1"
	}
	if err := os.WriteFile(cdromFlag, []byte(cdrom), 0o666); err != nil {
		return rollback(fmt.Errorf("set cdrom flag failed: %w", err))
	}
	if err := os.WriteFile(roFlag, []byte(readOnly), 0o666); err != nil {
		return rollback(fmt.Errorf("set ro flag failed: %w", err))
	}
	if err := os.WriteFile(mountDevice, []byte(req.File), 0o666); err != nil {
		return rollback(fmt.Errorf("mount image failed: %w", err))
	}
	// restartUSB creates/enables the mass-storage function before its final
	// media flags and backing file can be assigned. Rebind once those values are
	// in place so firmware enumerates the requested CD-ROM/disk, not the empty
	// or previous LUN that existed during the script restart.
	if err := rebindUSBGadget(); err != nil {
		return rollback(fmt.Errorf("mount image failed: %w", err))
	}
	time.Sleep(3 * time.Second)
	return nil
}

func rebindUSBGadget() error {
	controller, err := usbController()
	if err != nil {
		return err
	}
	if err := os.WriteFile(usbGadgetUDC, []byte("\n"), 0o666); err != nil {
		return fmt.Errorf("unbind USB gadget: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	return bindUSBGadget(controller)
}

func ensureUSBGadgetBound() error {
	data, err := os.ReadFile(usbGadgetUDC)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) != "" {
		return nil
	}
	controller, err := usbController()
	if err != nil {
		return err
	}
	return bindUSBGadget(controller)
}

func usbController() (string, error) {
	if data, err := os.ReadFile(usbGadgetUDC); err == nil {
		if controller := strings.TrimSpace(string(data)); controller != "" {
			return controller, nil
		}
	}
	controllers, err := os.ReadDir(usbUDCClass)
	if err != nil {
		return "", fmt.Errorf("list USB controllers: %w", err)
	}
	if len(controllers) == 0 {
		return "", fmt.Errorf("no USB device controller is available")
	}
	return controllers[0].Name(), nil
}

func bindUSBGadget(controller string) error {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if err := os.WriteFile(usbGadgetUDC, []byte(controller), 0o666); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("bind USB gadget to %s: %w", controller, lastErr)
}

func (s *Service) GetMountedImage(c *gin.Context) {
	var rsp proto.Response
	var data proto.GetMountedImageRsp

	if _, err := os.Stat(mountDevice); err != nil {
		rsp.OkRspWithData(c, &data)
		return
	}

	if content, err := os.ReadFile(mountDevice); err != nil {
		rsp.ErrRsp(c, -2, "read failed")
		return
	} else {
		data.File = strings.ReplaceAll(string(content), "\n", "")
	}

	if content, err := os.ReadFile(cdromFlag); err == nil {
		data.Cdrom = strings.ReplaceAll(string(content), "\n", "") == "1"
	}

	if content, err := os.ReadFile(roFlag); err == nil {
		data.ReadOnly = strings.ReplaceAll(string(content), "\n", "") == "1"
	}

	rsp.OkRspWithData(c, data)
}

func (s *Service) UploadImage(ctx *gin.Context) {
	var rsp proto.Response

	// 1. Get form data
	form, err := ctx.MultipartForm()
	if err != nil {
		log.Errorf("read form data failed: %s", err.Error())
		rsp.ErrRsp(ctx, -1, "Failed to read form data")
		return
	}
	defer form.RemoveAll()

	chunkIndexStr := ctx.PostForm("chunkIndex")
	chunkSizeStr := ctx.PostForm("chunkSize")
	totalChunksStr := ctx.PostForm("totalChunks")

	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		rsp.ErrRsp(ctx, -2, "Invalid chunk index")
		return
	}

	chunkSize, err := strconv.Atoi(chunkSizeStr)
	if err != nil {
		rsp.ErrRsp(ctx, -3, "Invalid chunk size")
		return
	}

	totalChunks, err := strconv.Atoi(totalChunksStr)
	if err != nil {
		rsp.ErrRsp(ctx, -4, "Invalid total chunks count")
		return
	}

	if chunkIndex < 0 {
		rsp.ErrRsp(ctx, -2, "Invalid chunk index")
		return
	}

	if chunkSize <= 0 {
		rsp.ErrRsp(ctx, -3, "Invalid chunk size")
		return
	}

	if totalChunks <= 0 {
		rsp.ErrRsp(ctx, -4, "Invalid total chunks count")
		return
	}

	if chunkIndex >= totalChunks {
		rsp.ErrRsp(ctx, -2, "Invalid chunk index")
		return
	}

	files := form.File["file"]
	if len(files) == 0 {
		rsp.ErrRsp(ctx, -5, "No file data found")
		return
	}

	fileHeader := files[0]
	filename := sanitizeFileName(fileHeader.Filename)
	if filename == "" {
		rsp.ErrRsp(ctx, -6, "Invalid filename")
		return
	}

	targetPath := filepath.Join(imageDirectory, filename)
	finalFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Errorf("open/create file failed: %s", err.Error())
		rsp.ErrRsp(ctx, -1, "File operation failed")
		return
	}
	defer finalFile.Close()

	offset := int64(chunkSize * chunkIndex)
	if _, err := finalFile.Seek(offset, 0); err != nil {
		log.Errorf("file seek failed: %s", err.Error())
		rsp.ErrRsp(ctx, -2, "File seek failed")
		return
	}

	srcFile, err := fileHeader.Open()
	if err != nil {
		log.Errorf("open uploaded file failed: %s", err.Error())
		rsp.ErrRsp(ctx, -3, "Failed to read uploaded file")
		return
	}
	defer srcFile.Close()

	bufSize := min(chunkSize, 10*1024*1024) //buffer 10MB
	buf := make([]byte, bufSize)

	written, err := io.CopyBuffer(finalFile, srcFile, buf)
	if err != nil {
		log.Errorf("write file failed: %s", err.Error())
		rsp.ErrRsp(ctx, -4, "File write failed")
		return
	}

	log.Infof("Successfully uploaded chunk %d/%d of %s (%d bytes)", chunkIndex+1, totalChunks, filename, written)

	rsp.OkRspWithData(ctx, gin.H{
		"status":      "chunk_uploaded",
		"filename":    filename,
		"chunkIndex":  chunkIndex,
		"totalChunks": totalChunks,
		"written":     written,
	})
}

func (s *Service) DeleteImage(c *gin.Context) {
	var req proto.DeleteImageReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	filename := strings.ToLower(req.File)
	validPrefix := strings.HasPrefix(filename, imageDirectory) || strings.HasPrefix(filename, sdCardDirectory)
	validSuffix := strings.HasSuffix(filename, ".iso") || strings.HasSuffix(filename, ".img")

	if !validPrefix || !validSuffix {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}

	if err := os.Remove(req.File); err != nil {
		rsp.ErrRsp(c, -3, "remove file failed")
		log.Errorf("failed to remove file %s: %s", req.File, err)
		return
	}

	rsp.OkRsp(c)
	log.Debugf("delete image %s success", req.File)
}

func restartUSB(flag int) error {
	if flag == 0 {
		if err := os.Remove(usbDisk); err != nil && !os.IsNotExist(err) {
			log.Errorf("remove %s failed: %s", usbDisk, err)
			return err
		}
	} else if flag == 1 {
		file, err := os.Create(usbDisk)
		if err != nil {
			log.Errorf("create file failed: %s", err)
			return err
		}
		defer file.Close()
	}

	cmd := "/dev/shm/kvmapp/scripts/usbdev.sh restart"
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		log.Errorf("failed to run %s: %s", cmd, err)
		return err
	}

	return nil
}

// sanitizeFileName cleans up potentially dangerous characters in filenames
func sanitizeFileName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == ' ' || r == '-' || r == '_' || r == '.':
			return r
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			return r
		default:
			return '_'
		}
	}, name)
}
