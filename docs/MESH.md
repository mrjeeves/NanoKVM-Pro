# Native AllMyStuff mesh integration (NanoKVM-Pro)

The NanoKVM-Pro can join an [AllMyStuff](https://github.com/) cloud mesh as a
first-class **KVM appliance** node — **exactly like the NanoKVM does**. Once
joined it:

- advertises its presence (hardware thumbnail, ownership, fleet, mesh
  memberships) so it shows up in the AllMyStuff graph with the KVM controls;
- tunnels its **own web UI** over the mesh "sites" plane, with the KVM login
  bypassed — mesh roster membership is the authentication;
- supports **claim** (adoption), **fleet** join, **attach/detach** (binding the
  KVM to the machine it controls), owner-curated **mesh membership**
  (`mesh_add`/`mesh_remove`), and **unclaim** (`Release` → factory-reset of the
  mesh identity, back to the joining mesh in claim mode).

This is the same pure-Go bridge the NanoKVM ships (`server/service/mesh`),
ported verbatim — the wire protocol, the frozen joining-mesh/fleet/claim-code
derivations, and the contract fixtures are byte-identical, so a Pro appears in
the graph indistinguishably from a NanoKVM (only its hardware thumbnail reads
AX630C / 1 GB instead of SG2002 / 256 MB). **v1 does not** do native screen/HID
streaming — the tunneled web UI delivers the full KVM experience — so the bridge
imports none of the CGO/libkvm packages and builds & tests on a host
(`go test ./service/mesh/...`).

## What differs from the NanoKVM port

The bridge logic is identical; only the device platform differs, and the port
absorbs exactly these differences:

- **aarch64, not riscv64.** The MyOwnMesh daemon is the static-musl
  `myownmesh-linux-aarch64-musl.tar.gz` asset (not `-riscv64`). See MyOwnMesh's
  `docs/NANOKVM.md`.
- **systemd, not busybox init.** The daemon runs as a `myownmesh.service` unit
  (`packaging/systemd/`), not an `/etc/init.d/S94myownmesh` script.
- **The advertised web-UI scheme is always `http`.** The Pro defaults to
  `proto: https`, but the sites tunnel is a plaintext layer-4 tunnel served
  in-process, so the SiteAdvert scheme is forced to `http` regardless of
  `proto` — otherwise AllMyStuff's "Open KVM" would try TLS against a plaintext
  local proxy. (On the NanoKVM this never surfaced because it defaults to
  `proto: http`.)
- **`restartServer` is `systemctl restart nanokvm`**, not an init script.
- **The joining-mesh name is web-UI-only for now.** The NanoKVM shows it on its
  OLED via a polled file; the Pro's LCD is driven by a separate out-of-repo UI
  daemon with no text/mesh endpoint, so the name lives in the web UI's **Mesh**
  tab. (The bridge still writes `/kvmapp/kvm/mesh_name` best-effort, so a future
  UI-daemon update can pick it up with no bridge change.) The **LAN claim
  sheet** makes a claimable Pro appear on any AllMyStuff machine on the same LAN
  with no name transcription anyway, so this is a convenience, not a
  prerequisite.

## How it works

A separate **MyOwnMesh daemon** runs on the device and owns the WebRTC mesh
transport. The bridge talks to it over a local control socket
(`mesh.socket`, line-delimited JSON). The daemon authenticates every peer
(ed25519 handshake) before any byte reaches the bridge.

On start (when `mesh.enabled`), the bridge:

1. connects to the daemon socket and `events_subscribe`s (capturing its
   `client_id`);
2. `identity_show` → learns this device's node id and derives its **joining
   mesh** (`cec-kvm-xxxxx-xxxxx`, `joining.go` — deterministic from the
   identity, shown in the web UI's Mesh tab);
3. reconciles network membership (`ensureMemberships`): the retired shared
   `cec-backend-client-mesh` is left, an **unclaimed** device joins its joining
   mesh, a **claimed** one its fleet mesh (never the joining mesh — unclaim is
   what returns it there), and owner-added meshes are kept;
4. `channel_subscribe`s the presence / control / media planes on **every**
   joined network and `capabilities_set`s the AllMyStuff marker (`allmystuff`,
   `kvm`, `sites` tags, with the inventory summary + endpoints nested under
   `extra`);
5. broadcasts a `NodeProfile` on the presence plane of every network (and
   re-broadcasts on every state change and on a slow heartbeat). The advert's
   `kvm` block carries `joining_mesh` + `meshes` (the full membership list),
   so the AllMyStuff drawer can render and curate it.

Inbound **control** messages (claim, fleet-key, release, attach/detach,
mesh add/remove, site-route offer) are handled in `control.go`; inbound
**media** `SiteFrame`s are demuxed per route/connection in `sites.go`, each
tunneled browser connection served as in-process HTTP through the gin engine
with `middleware.WithMeshAuth`.

The bridge is **non-fatal**: if the daemon isn't up yet it logs and retries, so
the KVM is never blocked from serving its LAN.

## Ownership, claim, fleets, and the joining mesh

This behaves identically to the NanoKVM (the code is shared). In brief:

- **Claiming is LAN-first, and public-mesh claiming is off by default.** While
  claimable, the device sits on the frozen LAN claim mesh
  (`allmystuff-local-claim-v1`, mDNS-only) — which every AllMyStuff node always
  joins — so a claimable Pro simply **appears in the claim sheet of any
  AllMyStuff machine on the same LAN**, plus its own **joining mesh**
  (`cec-kvm-<5>-<5>`, derived from the daemon identity, shown in the web UI's
  Mesh tab).
- **`publicClaims: true`** (config file only) re-opens the WAN paths: the
  joining mesh keeps its relay venue (claim by id over the internet) and the
  device mints a rotating **claim code** (`amsclaim-<code>`, shown in the web
  UI) for AllMyStuff's **Fleet → "Claim a remote device"** flow. The only HTTP
  mutation exposed is *rotate* (which can only invalidate a code, never enable
  claiming).
- An `Ownership Claim{owner}` (only honored while claimable) records the owner,
  ends claim mode, and — because a KVM is physically wired to the machine that
  claims it — **auto-attaches** to the owner.
- `Ownership FleetKey{key,name,venue}` hands down the shared fleet credential;
  the bridge derives the fleet network id from the key and joins it, then leaves
  the joining mesh once the fleet mesh is carrying the device.
- `Ownership Release` is the **unclaim**: forget owner/attachment/fleet, leave
  every mesh, return to the joining mesh, offer for adoption again.
- `Kvm Attach{node,label}` / `Kvm Detach` re-point or clear the binding; the
  device renames itself **`KVM-<label>`** on its advert and daemon identity.
- `Kvm MeshAdd` / `Kvm MeshRemove` curate mesh memberships (the fleet mesh is
  refused — governed by the fleet key).

All of these are gated on the sender being the device's owner or a fleet
co-member (the mesh authenticates the sender). State (owner, claimable,
attached_to, attached_label, fleet_key, fleet_name) is persisted to
`$MYOWNMESH_HOME/kvm-state.json`.

## Auth bypass

`middleware/jwt.go` exposes `WithMeshAuth(r)` (marks a request context
mesh-authenticated) and `CheckToken` passes for such requests. The site
tunnel wraps every request with it, so mesh-tunneled requests are authenticated
**without a token** while normal LAN/direct requests are unaffected. Mesh roster
membership replaces the KVM login. The Pro's extra loopback and `Authorization:
Bearer` paths are preserved; a tunneled request can never hit the loopback
bypass because its `RemoteAddr` is the mesh route string (a non-IP), so
`ClientIP()` is empty for it.

## Configuration

Add a `mesh` block to `/etc/kvm/server.yaml` (defaults shown):

```yaml
mesh:
  enabled: true
  home: /data/myownmesh              # identity, rosters, kvm-state.json (persistent)
  socket: /run/myownmesh/daemon.sock # control socket — on the runtime tmpfs
  networkId: ""                      # empty = this device's own joining mesh (cec-kvm-…)
  label: CEC KVM Joining Mesh
  relays: []                         # empty = public venue default
  publicClaims: false                # claims over the public mesh (see below)
  daemonBin: /kvmapp/system/bin/myownmesh
```

`networkId` pins a **custom joining mesh** — leave it empty for the derived
per-device `cec-kvm-…` id. A config still carrying the retired shared default
(`cec-backend-client-mesh`) is migrated to empty on load.

`publicClaims` is the device's **claims-over-the-public-mesh policy**, and it is
**strictly device-local**: this config file is the only place it can be set.
There is deliberately no HTTP endpoint and no mesh control message that mutates
it — a remote system (including a mesh-tunneled browser session riding the auth
bypass) must never be able to open a device to public claiming.

**The control socket lives on the runtime tmpfs** (`/run/myownmesh`, created by
the systemd unit's `RuntimeDirectory=`). Unlike the NanoKVM — whose `/data` is
exFAT and cannot `bind()` a Unix socket — the Pro's `/data` is ext4 and could
hold one, but pinning the socket to tmpfs keeps it independent of the data
partition's filesystem and matches the unit. The unit's `ExecStartPre`
(`myownmesh-prestart.sh`) pins the daemon to the same path via
`$home/config.json`; the two must match.

## Packaging and deploy

The Pro is a **systemd** (Ubuntu 22.04 aarch64) device, so the daemon runs as a
unit, not an init script:

- `packaging/systemd/myownmesh.service` — starts `myownmesh serve` before the
  server unit (`nanokvm.service`), with `MYOWNMESH_HOME=/data/myownmesh`,
  `MYOWNMESH_MEDIA_LANES=0` (data-channel only — the KVM never streams media
  over mesh tracks), and `MYOWNMESH_AUTOUPDATE=0` (the release pipeline owns the
  daemon version). `Restart=on-failure` replaces the init script's respawn loop.
- `packaging/systemd/myownmesh-prestart.sh` — `ExecStartPre`: ensures the home
  dir, does a bounded clock-before-join wait (relays use TLS; a cold-boot 1970
  clock rejects certs), and pins the control socket onto tmpfs.

Deploy with the `Justfile` (mirrors the NanoKVM's, adapted to systemd + scp):

```sh
just install <device-ip>          # fetch the prebuilt bundle, then deploy
# or, in two steps:
just fetch                        # download the latest device bundle (server + daemon)
just deploy <device-ip>           # scp server + daemon + unit, then systemctl enable/restart
just verify <device-ip>           # systemctl status + journal for both units
just undeploy <device-ip>         # reversible: disable + remove unit + reboot
```

`fetch` pulls **one** asset from this repo's GitHub release —
`nanokvm-pro-mesh-aarch64.tar.gz`, built by `.github/workflows/release.yml` —
which bundles **both** the NanoKVM-Pro server **and** the MyOwnMesh daemon pinned
in `.myownmesh-rev`. The `.sha256` is verified.

> **OTA caveat.** The Pro's stock OTA installs `nanokvmpro_*_arm64.deb` via
> `dpkg -i`, which overwrites `/kvmapp/server/NanoKVM-Server` with the stock
> (non-mesh) build. Re-run `just deploy` after an OTA to restore the mesh
> server. The daemon binary + unit live outside the stock deb's paths, so an OTA
> leaves them (and the mesh identity/state under `/data/myownmesh`) intact.

### Ordering dependency (`.myownmesh-rev`)

`.myownmesh-rev` must pin a **MyOwnMesh release whose pipeline built the
`myownmesh-linux-aarch64-musl.tar.gz` asset** (the `daemon-aarch64-musl` job).
That job is new — releases before it (≤ v0.2.27) have no such asset. The pin
here (`v0.2.28`) is the first release expected to carry it; update it if
MyOwnMesh's actual release number differs. `just daemon` / the release workflow
fail with a clear pointer (not a wrong build) until a matching release exists.

### Cutting a release

`just release X.Y.Z` bumps the advertised version (`appVersion` in
`server/service/mesh/bridge.go` + `web/package.json`), commits, and pushes the
`vX.Y.Z` tag, which triggers the release workflow. Mirrors MyOwnMesh /
AllMyStuff `just release`.

## Building from source

```sh
just setup-pro             # one-time: start Docker + build the builder image (bakes the toolchain)
just build-pro             # server (in Docker) + pinned daemon (download), one step
just deploy <device-ip>    # scp the server + daemon + unit to a device
```

The server builds inside a `linux/amd64` Docker image (`docker/Dockerfile`) with
Go and the ARM aarch64 cross toolchain baked in — mirroring the NanoKVM's Docker
builder. **This is why it works on a Mac:** the ARM GNU toolchain
(`support/scripts/config.ini`) is an x86_64 *Linux* binary that cannot execute on
macOS natively; in the `linux/amd64` container it runs under Rosetta/QEMU.
`setup-pro` starts a Docker runtime (installing/starting Colima on a Mac) and
builds the image; `build-server` runs `server/build.sh` inside it against the
mounted repo. On a native x86_64 Linux box it's the same image, no emulation.

The daemon is never compiled here: it's downloaded from the MyOwnMesh release
pinned in `.myownmesh-rev` (MyOwnMesh cross-compiles it with cargo-zigbuild — a
NanoKVM-Pro never builds Rust). `just build-server` builds only the server;
`just daemon` only downloads the daemon.

### Testing against an existing daemon

The bridge dials `mesh.socket` and reuses whatever `myownmesh serve` is already
running — it never spawns or builds a daemon. To test on a dev box: run a
`myownmesh serve` you already have, point the bridge at it by setting `mesh.home`
in `server.yaml` (or `MYOWNMESH_HOME`) to that daemon's home, then start
`NanoKVM-Server`; the bridge connects, joins the device's joining mesh, and
advertises the KVM.

## Tunneled video note

The AllMyStuff "Open KVM" flow opens the tunneled web UI. The Pro's default
stream is H264-WebRTC, whose media plane cannot ride the layer-4 sites tunnel;
the tunneled UI should use the tunnel-safe MJPEG (`/api/stream/mjpeg`) or
h264-direct (`/api/stream/h264/direct`) modes, which flow as plain HTTP/WS
through the gin engine. Power/Reset and all non-video controls work over the
tunnel unchanged.

## Tests

```
cd server
go vet ./service/mesh/... ./middleware/...
go test ./service/mesh/...
```

`protocol_test.go` round-trips every wire type; `sites_test.go` covers the
`meshConn` framing; `joining_test.go` freezes the joining-mesh derivation;
`membership_test.go` drives mesh add/remove, unclaim, and the KVM-<label>
renames; `presence_test.go` pins the always-`http` tunnel scheme (the Pro-
specific fix). The contract fixtures under `testdata/contract` are byte-identical
to AllMyStuff's, round-tripped structurally by `contract_test.go`.
