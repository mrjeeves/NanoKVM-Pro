# NanoKVM-Pro · CEC KVM edition

## This fork: CEC KVM — an AllMyStuff mesh appliance

This fork of [sipeed/NanoKVM-Pro](https://github.com/sipeed/NanoKVM-Pro) turns the device into **CEC KVM**, a first-class appliance in the [AllMyStuff](https://allmystuff.works) ecosystem. Everything below this section is upstream Sipeed documentation and still applies.

- **CEC branding** — web UI renamed CEC KVM in every locale, with the CEC "critical error" mark as the favicon and login logo (`web/public/sipeed.ico` — upstream filename kept, ours bytes). The palette stays AllMyStuff's (deep-violet dark theme, `#f11ea1` magenta accent, Inter font) — CEC is moving to that scheme, so the two match by design rather than by omission.
- **Pure-Go mesh bridge** (`server/service/mesh/`) paired with a bundled [MyOwnMesh](https://myownmesh.net) daemon (Rust, pinned at `v0.3.20` in `.myownmesh-rev`; aarch64-musl build, run as a systemd `myownmesh.service` unit from `packaging/systemd/`).
- **LAN-first claiming** — an unclaimed device advertises on the mDNS-only `allmystuff-local-claim-v1` rendezvous mesh (no relays, no wall clock needed — works pre-NTP), so a fresh KVM auto-appears in the claim sheet of any AllMyStuff app on the same LAN; WAN claiming stays off unless `publicClaims: true`.
- **Zero-login access from anywhere** — the web UI tunnels over the mesh "sites" plane (no port forwarding or VPN), and mesh roster membership *is* the authentication for mesh viewers.
- **Firmware updates from our own channel** — the stock Sipeed update (a `dpkg` install that would clobber our mesh server build) is removed; **Settings → Update** installs our GitHub-released bundle instead. Reached over the mesh it needs no device password (mesh-roster membership authorizes it); on the LAN the normal KVM login applies. See [`docs/MESH.md`](docs/MESH.md).
- **Full KVM-node lifecycle** — presence advertising (NodeProfile with `kvm`/`sites` capability tags), fleet membership, attach/detach to the machine it controls (renames itself `KVM-<label>`), owner-curated mesh membership, remote restart, and unclaim (factory-reset of the mesh identity).
- **CEC support number** — share the number shown in the Mesh tab and approve the incoming request in the web UI, CECSupport, or AllMyStuff. The device button and web approval button approve the current request, or open a five-minute window for exactly one new request. Press again to refresh the window. Approval grants three hours of access; reconnects do not extend it. The countdown, pending requests and active access are shown in the web UI.
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
