# NanoKVM-Pro · AllMyKVM edition

## This fork: AllMyKVM — an AllMyStuff mesh appliance

This fork of [sipeed/NanoKVM-Pro](https://github.com/sipeed/NanoKVM-Pro) turns the device into **AllMyKVM**, a first-class appliance in the [AllMyStuff](https://allmystuff.works) ecosystem. Everything below this section is upstream Sipeed documentation and still applies.

- **AllMyStuff branding** — web UI renamed AllMyKVM in every locale, restyled in AllMyStuff's design language (deep-violet dark theme, `#f11ea1` magenta accent, Inter font), with the AllMyStuff app icon as the favicon.
- **Pure-Go mesh bridge** (`server/service/mesh/`) paired with a bundled [MyOwnMesh](https://myownmesh.net) daemon (Rust, pinned at `v0.2.40` in `.myownmesh-rev`; aarch64-musl build, run as a systemd `myownmesh.service` unit from `packaging/systemd/`).
- **LAN-first claiming** — an unclaimed device advertises on the mDNS-only `allmystuff-local-claim-v1` rendezvous mesh (no relays, no wall clock needed — works pre-NTP), so a fresh KVM auto-appears in the claim sheet of any AllMyStuff app on the same LAN; WAN claiming stays off unless `publicClaims: true`.
- **Zero-login access from anywhere** — the web UI tunnels over the mesh "sites" plane (no port forwarding or VPN), and mesh roster membership *is* the authentication for mesh viewers.
- **Full KVM-node lifecycle** — presence advertising (NodeProfile with `kvm`/`sites` capability tags), fleet membership, attach/detach to the machine it controls (renames itself `KVM-<label>`), owner-curated mesh membership, remote restart, and unclaim (factory-reset of the mesh identity).
- **CEC hand raise** (`server/service/mesh/cec.go`) — the KVM can raise a hand on the [CEC Support](https://github.com/mrjeeves/CECSupport) help queue (a `SupportPresence` beacon on the `cecsupport-clients` mesh, exactly like a CEC customer), so a technician sees the device needs help along with its 9-digit support number; the KVM auto-approves the technician who answers (only while it's still asking). Raise/lower from the web UI's Mesh tab, the `/api/mesh/help/*` endpoints, or a **tap of the USR button** (`server/service/button/`; on by default, `mesh.handRaise` in `server.yaml`). The USR button (gpio-98) is owned by the closed firmware (kvm_ui's `LinuxKeyMonitor`) rather than exposed as an evdev node, so the watcher co-reads its live level from debugfs (the `gpio:<n>` input mode); the firmware still toggles the inside screen on a tap, which is harmless.
- **usbnet internet sharing** — the KVM NATs its own uplink to the USB-tethered host (`usbnet-share.service`).

Details in [docs/MESH.md](docs/MESH.md) · companion app: [allmystuff.works](https://allmystuff.works) · mesh tech: [myownmesh.net](https://myownmesh.net)

> **⚠️ Maintainers — mirrored source, one deliberate divergence.** `server/service/mesh` and `server/service/button` are kept as verbatim copies shared with the PCIe [NanoKVM](https://github.com/mrjeeves/NanoKVM) repo, **except** `server/service/button/button.go`: this Pro repo adds the `gpio:<n>` USR-button mode (the Pro's USR button is gpio-98, owned by the closed firmware, not an evdev node — the non-pro board has neither). **Do not blindly copy `button.go` between the two repos** or you'll silently drop that mode — reconcile changes by hand. (See the banner at the top of `button.go`.)

---

> ## Code Availability
>
> - [x] **Frontend** (Released)
> - [x] **Backend** (Released)
> - [ ] **Support** (in development)

## Introduction

NanoKVM-Pro is the continuation of NanoKVM, inheriting the extreme compactness and powerful expandability of the NanoKVM series as an IP-KVM product.
It has made a significant leap in performance, making it more suitable for remote working scenarios.

To meet different user needs, NanoKVM-Pro offers two forms: NanoKVM-Desk and NanoKVM-ATX:

![NanoKVM-Pro Desktop and ATX versions side by side](https://wiki.sipeed.com/hardware/assets/NanoKVM/pro/introduce/combine.png)

- **NanoKVM-Desk** is the desktop version of NanoKVM-Pro, featuring an anodized matte metal shell. The front panel has a 1.47-inch touchscreen that displays core KVM information and allows for easy hardware function settings or can be used as a mini secondary screen, providing a more tactile user experience with the left-side infinite knob.

- **NanoKVM-ATX** is the internal version of NanoKVM-Pro, equipped with half-height/full-height brackets for installation inside a case. It allows for easier installation for host users with built-in USB cables and power control interfaces. Remote control can be achieved via external HDMI, network, and USB connections.

NanoKVM-Pro uses the AX630 as its main control core, featuring an ARM 1.2G dual-core A53 CPU. The external 1GB LPDDR4 memory provides strong computing support for remote desktop connections. It has built-in HDMI loop-out and capture chips, offering up to 4K60FPS HDMI loop-out and 4K45FPS video capture. Thanks to AX630's efficient and powerful image processing architecture, NanoKVM-Pro can transmit high-resolution images with very low latency, with typical delays as low as 60ms at 2K resolution.

## Specifications

| Product       | NanoKVM-Pro | NanoKVM      | GxxKVM      | JxxKVM      |
|---------------|----------|--------------|-------------|-------------|
| Main Control  | AX630C   | SG2002       | RV1126      | RV1106      |
| Core          | <2xA53@1.2G> | <1xC906@1.0G>  | <4xA7@1.5G>   | <1xA7@1.2G>   |
| Memory        | 1G LPDDR4X | 256M DDR3    | 1G DDR3     | 256M DDR3   |
| Storage       | 32G eMMC | 32G microSD  | 8G eMMC     | 16G eMMC    |
| System        | NanoKVM+PIKVM | NanoKVM      | GxxKVM      | JxxKVM      |
| Resolution    | 4K@45fps | 1080P@60fps | 4K@30fps, 2K@60fps | 1080P@60fps |
| HDMI Loop-Out | 4K Loop-Out | ×            | ×           | ×           |
| Video Encoding | MJPG/H264 | MJPG/H264    | MJPG/H264   | MJPG/H264   |
| Audio Transmission | ✓        | ×            | ✓           | ×           |
| UEFI/BIOS Support | ✓        | ✓            | ✓           | ✓           |
| Simulated USB Keyboard/Mouse | ✓ | ✓          | ✓           | ✓           |
| Simulated USB ISO | ✓        | ✓            | ✓           | ✓           |
| IPMI          | ✓        | ✓            | ✓           | ×           |
| Wake-on-LAN (WOL) | ✓        | ✓            | ✓           | ✓           |
| WebSSH        | ✓        | ✓            | ✓           | ✓           |
| Custom Scripts | ✓        | ✓            | ×           | ×           |
| Serial Terminal | 2 Channels | 2 Channels   | None        | 1 Channel   |
| Storage Performance | 32G eMMC 300MB/s | 32G MicroSD 12MB/s | 8G eMMC 120MB/s | 8G eMMC 60MB/s |
| Ethernet      | 1000M    | 100M         | 1000M       | 100M        |
| Internal Form Factor | Optional ATX version | Optional PCIe version | ×           | ×           |
| WiFi          | Optional WiFi6 | Optional WiFi6 | ×           | ×           |
| MicroSD Expansion | ✓        | ×            | ×           | ×           |
| ATX Power Control | ✓        | ✓            | +15$        | +10$        |
| Display       | 1.47-inch 320x172 LCD<br>0.96-inch 128x64 OLED | 0.96-inch 128x64 OLED | None | 1.66-inch 280x240 |
| Additional Features | Synchronized LED effects, Smart Assistant | –        | –           | –           |
| Power Consumption | 0.6A@5V  | 0.2A@5V      | 0.4A@5V     | 0.2A@5V     |
| Power Input   | USB-C/PoE | USB-C/PoE/PCIe | USB-C       | USB-C       |
| Dimensions     | 65x65x28mm | 40x36x36mm   | 80x60x7.5mm | 60x6x24-30mm |

## Where to buy

- [AliExpress](https://www.aliexpress.com/item/1005010048471263.html)
- [Pre-sale Page](https://sipeed.com/nanokvm/pro)

## 💬 Community & Support

- [Discord](https://discord.gg/V4sAZ9XWpN)
- QQ group: 703230713
- email: [support@sipeed.com](mailto:support@sipeed.com)
- [FAQ](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM_Pro/faq.html)

## 📜 License

This project is licensed under the GPL-3.0 License - see the LICENSE file for details.
