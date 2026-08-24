# CEC KVM changelog (NanoKVM-Pro)

The release history of **this fork** — Critical Error Computing's AllMyStuff /
CEC integration built on top of Sipeed's NanoKVM-Pro. Entries below are our own
`vX.Y.Z` releases (the version the device advertises on the mesh and the one the
Update tab installs — `server/buildinfo`, never the Sipeed base image's
`/kvmapp/version`).

When a release re-bases onto a newer upstream Sipeed firmware, the new upstream
baseline is called out inline, so our version and the Sipeed version underneath
it never drift silently apart. Sipeed's full upstream changelog is preserved
verbatim in [`CHANGELOG.upstream.md`](CHANGELOG.upstream.md).

## Unreleased

- **MyOwnMesh daemon pinned to v0.3.9** (`.myownmesh-rev`, was `v0.3.3`) — six
  releases of connection work reach the device. TURN now falls back to TCP and
  TLS when a network blocks plain UDP relay, mDNS endpoint dialing backs off
  instead of hammering LAN candidates that never answer, relay admission and
  reconnect no longer flap a peer between rescued and dropped, and signaling
  identity is re-verified on recovery. The daemon also silences upstream's
  stale-TURN `ChannelData` error storm at source — a restarted public relay
  could otherwise flood the journal with it while relaying was perfectly
  healthy. Settings → Update installs the new daemon and restarts
  `myownmesh.service` on the sha256 compare, so it reaches the fleet over the
  mesh rather than needing an on-site `just deploy`.

- **Remote viewers pick up the new TURN fallbacks with no bridge change.** The
  venue ICE union (`server/service/mesh/venue.go`) is a deliberately defensive
  subset of the daemon's config schema, so the added
  `turn:…?transport=tcp` and `turns:…:5349?transport=tcp` URLs flow straight
  through `config_show` into the browser's `RTCIceServer` list. The bridge
  keeps omitting `stun_servers`/`turn_servers` on network add, which is what
  lets a daemon-side default change like this one apply at all.

- **The daemon's config schema migrates v2 → v3 on first start**, rewriting the
  built-in reference relay into the three-URL UDP/TCP/TLS set. Automatic and
  idempotent, and it touches only that exact old default: the LAN-only claim
  mesh's explicit `turn_servers: []` opt-out and any operator-supplied relay
  are left alone. **Downgrade caveat:** a v3 config is rejected outright by a
  daemon older than v0.3.5 (`config version 3 is from a newer build`), and
  `serve` exits on that error, so rolling back to a Pro build pinned at
  ≤ v0.3.4 leaves `myownmesh.service` restart-looping until
  `/data/myownmesh/config.json` is deleted (the prestart script writes a fresh
  minimal one, and the bridge re-adds its networks on the next handshake).
  This matters more on the Pro than on the NanoKVM: the daemon's own updater
  is pinned off (`MYOWNMESH_AUTOUPDATE=0`), so the config only ever moves
  forward when the release pipeline moves the pin.

- **`myownmesh.service` rate-limits its own journal.** `SyslogIdentifier=`
  plus `LogRateLimitIntervalSec=5min` / `LogRateLimitBurst=100` match what
  MyOwnMesh's own `service install` has rendered since v0.3.4, and keep any
  future log storm off a rootfs the journal shares with everything else. The
  known stale-TURN firehose is already fixed in the daemon; this is the
  second line of defence. The unit is not carried in the OTA bundle, so this
  one lands on a `just deploy` or a reflash rather than through
  Settings → Update.

- **A failed or interrupted install-media mount can no longer take down USB.**
  Every gadget rebuild now closes and reopens HID around the operation, retries
  UDC binding, and rolls a failed media function back to a valid composite
  gadget. Server startup also rebinds a blank UDC and ejects dead FUSE media
  left by an interrupted stream, so installing this update repairs a KVM that
  an older build already left without USB.

- **Over-the-air updates can now deliver the device-side helpers at all.** The
  helper scripts and their systemd units — `/usr/local/bin/usbdhcp.sh` and
  `usbdhcp.service`, say — shipped only in the image overlay, so they reached a
  device by writing a new SD card and by no other means. An update delivered a
  server that expected them with no way to bring them, which fails silently and
  on exactly the devices already in the field. The bundle now carries an
  `overlay/` tree mirroring where the files live; an update installs it, wires
  the `multi-user.target` wants symlink for any unit that asks for one (the same
  thing the image build does — an offline chroot has no dbus for `systemctl
  enable`), and reloads systemd. What ships is named explicitly in CI rather
  than copied wholesale: the image overlay also holds `/boot/usb.ncm` and a
  MaixPy config, and shipping those in an update would switch a USB interface on
  underneath a running deployment.
- **The startup reconcile covers those helpers too, not just the daemon.** The
  code that performs an update is the code already on the device, so a device
  updating _from_ an older server is updated by that server's updater — which
  installs only the parts it knows about and ignores an `overlay/` it has never
  heard of. The build that adds the overlay therefore can't deliver it during
  the very update that installs it. The startup reconcile, which already fetches
  this exact version's bundle to heal the identical daemon gap, now installs any
  helper that differs as well, closing it in one hop instead of two releases.
  Files are written only when they actually differ, and units are enabled but
  never started: their effects belong to a boot, and one of them reconfigures
  the USB link the update may have arrived over.
- **Firmware updates run off our own version and release channel.** The stock
  Sipeed updater — a `dpkg` install over `/kvmapp` that clobbers our mesh
  server — is removed, both the web UI and the server routes. Settings → Update
  now installs our own GitHub-released bundle
  (`nanokvm-pro-mesh-aarch64.tar.gz`), verified by sha256, and it's
  password-free over the AllMyStuff mesh. The version the updater compares is
  our fork's number (`server/buildinfo`), so a device no longer reads as the
  unrelated upstream `1.x` from `/kvmapp/version`.
- **MyOwnMesh daemon pinned to v0.3.2** (`.myownmesh-rev`) — picks up the
  0.3.2 mesh-connectivity fixes.
- **Over-the-air updates now ship the pinned daemon too.** Settings → Update
  installs the bundled myownmesh and `systemctl restart myownmesh`es it
  whenever the pinned binary actually changed (a sha256 compare), so a
  daemon-side fix reaches the fleet over the mesh — previously an update swapped
  only the server + web and a daemon bump needed an on-site `just deploy`. An
  unchanged daemon is left completely untouched, so an ordinary update never
  disturbs the mesh tunnel; when the daemon does change it is bounced after the
  update response is sent and just before the `nanokvm` restart. Because that
  logic lives in the server, a device updating _from_ an older server can't get
  the daemon during the very update that installs this build — so the new server
  also **reconciles its daemon once on startup**: if the installed binary
  doesn't match the one this release pins, it fetches and swaps it in and
  restarts the daemon, converging the fleet with no on-site deploy. The check
  runs once per version (a marker under the mesh home dir), so ordinary boots do
  nothing.

## 0.1.0

First CEC KVM release — the NanoKVM-Pro as a first-class AllMyStuff mesh
appliance:

- Pure-Go **MyOwnMesh bridge** (`server/service/mesh/`) with a bundled daemon
  pinned in `.myownmesh-rev`, run as a systemd unit.
- **LAN-first claiming** over the mDNS rendezvous mesh; **zero-login** web
  access tunnelled over the mesh "sites" plane.
- Full **KVM-node lifecycle**: presence advertising, fleet membership,
  attach/detach to the machine it controls, remote restart, and unclaim.
- **CEC hand-raise** on the CEC Support help queue — a beacon on the
  `cecsupport-clients` mesh, raised from the web UI or the **USR button**.

_Upstream baseline: Sipeed NanoKVM-Pro **1.2.15**._
