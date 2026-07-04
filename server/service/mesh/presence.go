package mesh

import (
	"bufio"
	"math/rand"
	"os"
	"strconv"
	"strings"

	"NanoKVM-Server/config"
)

// deviceInfo is a CGO-free snapshot of the host's identity + hardware thumbnail.
// We deliberately gather it here (os/proc reads) rather than importing
// server/service/vm, which pulls in CGO/libkvm via config hardware + common.
type deviceInfo struct {
	hostname string
	summary  InventorySummary
}

// gatherDeviceInfo reads hostname and a hardware thumbnail from /proc and /etc.
// Everything is best-effort: a missing file just leaves a field empty.
func gatherDeviceInfo() deviceInfo {
	return deviceInfo{
		hostname: readHostname(),
		summary: InventorySummary{
			OS:          "linux",
			CPU:         readCPUModel(),
			RAMBytes:    readTotalRAMBytes(),
			DeviceCount: 1, // the KVM appliance itself
		},
	}
}

func readHostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	if raw, err := os.ReadFile("/etc/hostname"); err == nil {
		return strings.TrimSpace(string(raw))
	}
	return ""
}

func readCPUModel() string {
	if v := scanCPUInfo(); v != "" {
		return v
	}
	// aarch64 kernels (the AX630C) usually expose none of the cpuinfo keys
	// scanCPUInfo looks for — /proc/cpuinfo lists only "processor"/"BogoMIPS"/
	// "CPU part". Fall back to the device-tree model so the graph card shows
	// something concrete instead of an empty CPU field.
	for _, p := range []string{"/proc/device-tree/model", "/sys/firmware/devicetree/base/model"} {
		if raw, err := os.ReadFile(p); err == nil {
			// device-tree strings are NUL-terminated; trim the NUL and spaces.
			if v := strings.TrimSpace(strings.TrimRight(string(raw), "\x00")); v != "" {
				return v
			}
		}
	}
	return ""
}

func scanCPUInfo() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		// x86 uses "model name", many ARM/RISC-V kernels use "Hardware",
		// "uarch", or "isa". Take the first informative one.
		for _, key := range []string{"model name", "Hardware", "uarch", "cpu model", "isa"} {
			if strings.HasPrefix(line, key) {
				if i := strings.Index(line, ":"); i >= 0 {
					if v := strings.TrimSpace(line[i+1:]); v != "" {
						return v
					}
				}
			}
		}
	}
	return ""
}

func readTotalRAMBytes() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					return kb * 1024 // MemTotal is in kB
				}
			}
		}
	}
	return 0
}

// webPort returns the port the KVM web UI listens on, per config.
func webPort(conf *config.Config) uint16 {
	if conf.Proto == "https" {
		return uint16(conf.Port.Https)
	}
	return uint16(conf.Port.Http)
}

// webScheme returns the URL scheme AllMyStuff should speak to the tunneled web
// UI. It is ALWAYS "http", independent of the device's own LAN proto: the sites
// plane is a transparent layer-4 tunnel, and siteHost.serveHTTP serves each
// tunneled connection as plaintext in-process HTTP through the gin engine — TLS
// is never terminated on the tunnel. The AllMyStuff viewer opens
// "<scheme>://localhost:<localPort>" against its local end of that tunnel, so
// the scheme must match what the tunnel actually speaks (plaintext), not what
// the KVM's direct LAN listener uses. This matters on the Pro because it
// defaults to proto=https; advertising "https" here would make the viewer's
// browser attempt TLS against a plaintext proxy and "Open KVM" would fail.
func webScheme(conf *config.Config) string {
	return "http"
}

// siteID is the SiteAdvert id (and KvmAdvert.web) for our web UI — "tcp:<port>",
// mirroring the scan's ListeningService.id convention.
func siteID(port uint16) string {
	return "tcp:" + strconv.Itoa(int(port))
}

// attachmentLabel is the display label an attached KVM takes: KVM-<target's
// label>, or "" when unattached (callers then use their own default). The
// target's label is resolved LIVE, preferring the attached node's current
// presence label (from the peer-label cache, so the name tracks a rename and
// self-heals a claim/attach that landed before the target's label was known),
// then the label baked at attach time, then a short canonical id as a last
// resort. A bridge method (not a free function) so it can read the cache; used
// by both the presence advert and the daemon identity so the two never
// disagree.
func (b *Bridge) attachmentLabel() string {
	snap := b.state.snapshot()
	if snap.AttachedTo == "" {
		return ""
	}
	target := b.peerLabel(snap.AttachedTo)
	if target == "" {
		target = snap.AttachedLabel
	}
	if target == "" {
		target = pubkeyPart(snap.AttachedTo)
		if len(target) > 10 {
			target = target[:10]
		}
	}
	return "KVM-" + target
}

// buildProfile assembles the presence NodeProfile from device info, config, and
// the current persisted state. nodeID is our daemon device id; version is the
// NanoKVM application version; boot is the random per-run boot id; joiningMesh
// is this device's derived joining mesh; meshes is every network id currently
// joined (fleet included); attachedLabel is the resolved KVM-<target> display
// name ("" when unattached), computed by the bridge so it can consult the live
// peer-label cache.
func buildProfile(nodeID string, conf *config.Config, dev deviceInfo, st *State, version string, boot uint64, joiningMesh string, meshes []string, attachedLabel string) NodeProfile {
	port := webPort(conf)
	id := siteID(port)
	snap := st.snapshot()

	// Display name on the graph: KVM-<attached machine> once attached, else
	// the configured brand name ("CEC-KVM" by default), falling back to
	// hostname then node id if explicitly cleared.
	label := attachedLabel
	if label == "" {
		label = conf.Mesh.Name
	}
	if label == "" {
		label = dev.hostname
	}
	if label == "" {
		label = nodeID
	}

	var owner *string
	if snap.Owner != "" {
		o := snap.Owner
		owner = &o
	}

	var attached *string
	if snap.AttachedTo != "" {
		a := snap.AttachedTo
		attached = &a
	}

	return NodeProfile{
		Protocol:     ProtocolVersion,
		Node:         nodeID,
		Label:        label,
		Hostname:     dev.hostname,
		Summary:      dev.summary,
		Capabilities: []Capability{}, // none in v1 — the tunneled web UI carries everything
		Owner:        owner,
		Claimable:    snap.Claimable,
		Boot:         boot,
		Features:     []string{FeatureKVM, FeatureSites},
		Sites: []SiteAdvert{{
			ID:       id,
			Label:    "KVM Web UI",
			Port:     port,
			Scheme:   webScheme(conf),
			Loopback: false,
		}},
		Version:    version,
		FleetName:  snap.FleetName,
		FleetOwner: snap.FleetName, // a fleet is named for its owner; track it
		Kvm: &KvmAdvert{
			AttachedTo:  attached,
			Web:         id,
			JoiningMesh: joiningMesh,
			Meshes:      meshes,
		},
	}
}

// newBootID mints a random per-run boot id (never 0, which means "older peer").
func newBootID() uint64 {
	b := rand.Uint64()
	if b == 0 {
		b = 1
	}
	return b
}

// readFileTrim reads a file and trims surrounding whitespace/newlines.
func readFileTrim(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
