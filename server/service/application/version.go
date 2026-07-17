package application

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// githubLatestAPI is our release channel's "latest release" endpoint. We read
// the tag name to tell the Update tab whether a newer build is out — the same
// channel the Update handler installs from, never cdn.sipeed.com.
const githubLatestAPI = "https://api.github.com/repos/mrjeeves/NanoKVM-Pro/releases/latest"

// GetVersion reports the running firmware version and, best-effort, the latest
// version on our release channel (so the Update tab can show "up to date" vs
// "update available"). A failed latest-lookup (no internet, rate limit) just
// echoes the current version so the tab reads as current rather than nagging.
func (s *Service) GetVersion(c *gin.Context) {
	var rsp proto.Response

	currentVersion := getCurrentVersion()

	latestVersion := currentVersion
	if latest, err := latestChannelVersion(); err == nil && latest != "" {
		latestVersion = latest
	}

	rsp.OkRspWithData(c, &proto.GetVersionRsp{
		Current: currentVersion,
		Latest:  latestVersion,
	})
	log.Debugf("current version: %s, latest version: %s", currentVersion, latestVersion)
}

func getCurrentVersion() string {
	defaultVersion := "v1.0.0"

	versionFile := filepath.Join(AppDir, "version")
	content, err := os.ReadFile(versionFile)
	if err != nil {
		return defaultVersion
	}

	version := strings.ReplaceAll(string(content), "\n", "")
	if version == "" {
		return defaultVersion
	}

	return version
}

// latestChannelVersion asks GitHub for our newest release's tag. The Pro's
// current version is stored with a leading "v" (see getCurrentVersion), so the
// tag is returned as-is for a clean semver compare on the client.
func latestChannelVersion() (string, error) {
	req, err := http.NewRequest("GET", githubLatestAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}
	return strings.TrimSpace(release.TagName), nil
}
