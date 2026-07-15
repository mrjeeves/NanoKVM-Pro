# NanoKVM-Pro — build & deploy the device with the native AllMyStuff bridge.
#
# `just build-pro` produces a COMPLETE device build:
#   server/NanoKVM-Server         the Go server (with the mesh bridge)
#   dist/myownmesh                the MyOwnMesh daemon, pinned in .myownmesh-rev
#   web/dist                      the web UI bundle the device serves — built
#                                 here, NOT the firmware's stock SPA (which goes
#                                 blank when served over the mesh "sites" tunnel)
#
# The server builds inside a linux/amd64 Docker image (Go + the ARM GNU aarch64
# cross toolchain, baked by docker/Dockerfile). The toolchain is an x86_64 Linux
# binary, so building through Docker is what lets `just build-pro` work on a Mac
# (it runs under Rosetta/QEMU) — the native toolchain can't execute on macOS.
# `just setup-pro` builds that image (and sets up a Docker runtime on a Mac).
# The daemon is NOT built here — it's the prebuilt
# `myownmesh-linux-aarch64-musl.tar.gz` from the MyOwnMesh release pinned in
# .myownmesh-rev, downloaded and staged for you. (MyOwnMesh cross-compiles it
# with cargo-zigbuild; a NanoKVM-Pro never builds Rust.)
#
# The Pro is a systemd (Ubuntu aarch64) device, so the daemon runs as a
# `myownmesh.service` unit rather than a busybox init script.
#
# For local testing you don't need the daemon here at all: run a `myownmesh
# serve` you already have and point the bridge at its control socket (set
# mesh.home / MYOWNMESH_HOME) — see docs/MESH.md.

set shell := ["bash", "-cu"]

daemon_dst := "dist/myownmesh"
mom_repo := "https://github.com/mrjeeves/MyOwnMesh"
nanokvm_repo := "https://github.com/mrjeeves/NanoKVM-Pro"
unit_src := "packaging/systemd/myownmesh.service"
prestart_src := "packaging/systemd/myownmesh-prestart.sh"
image := "nanokvm-pro-builder"
web_image := "nanokvm-pro-web-builder"
platform := "linux/amd64"

# The Go packages this fork owns and that build & test without the on-device C
# libs (libkvm / libopus): the mesh bridge, the hand-raise button watcher, and
# config. The rest of the server is upstream device glue that only links in the
# builder image, so the quality recipes below scope to these — they run on any
# dev machine (no Docker, no cross toolchain, no device libs). `go_pure_dirs` is
# the same set as plain paths for gofmt (which takes dirs, not `./...` patterns).
go_pure_pkgs := "./config/... ./service/mesh/... ./service/button/..."
go_pure_dirs := "config service/mesh service/button"

default: help

help:
    @just --list

# ── Development: format, vet, and test the Go server ───────────────────────────
# The app-repo dev loop (fmt / fmt-check / lint / test / check), scoped to the
# CGO-free Go packages (config, service/mesh, service/button) so it runs on any
# dev machine — no Docker, no cross toolchain, no device libs. Mirrors the
# AllMyStuff / CEC Support Justfiles.

# Format this fork's Go packages in place.
fmt:
    @cd server && gofmt -w {{go_pure_dirs}}

# Fail if any of this fork's Go files isn't gofmt-clean (the formatting gate).
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    cd server
    unformatted="$(gofmt -l {{go_pure_dirs}})"
    if [ -n "$unformatted" ]; then
      echo "❌ gofmt needs to run on:" >&2
      echo "$unformatted" >&2
      exit 1
    fi
    echo "OK — gofmt clean"

# Vet the CGO-free packages (Go's `go vet` — the analog of the app repos' clippy lint).
lint:
    @cd server && go vet {{go_pure_pkgs}}

# Unit-test the CGO-free packages (the mesh bridge + hand-raise button).
test:
    @cd server && go test {{go_pure_pkgs}}

# Everything the local dev gate runs: gofmt check + go vet + go test on the
# CGO-free packages. Mirrors the app repos' `just check`.
[doc("Run the full local dev gate: gofmt check + go vet + go test (CGO-free pkgs).")]
check: fmt-check lint test

# One-time: get a Docker runtime going and build the builder image (Go + the ARM
# aarch64 cross toolchain baked in — see docker/Dockerfile). On a Mac this
# installs/starts Colima and enables amd64 emulation so the x86_64-Linux cross
# toolchain runs (the native toolchain is an x86_64 Linux ELF that can't execute
# on macOS at all). Idempotent: re-run any time. Mirrors the NanoKVM's setup-risc.
[doc("Bootstrap Docker (Colima on a Mac) + build the builder image. Run once.")]
setup-pro:
    #!/usr/bin/env bash
    set -euo pipefail
    # 1. Ensure a working Docker daemon (any runtime — Colima, Docker Desktop,
    #    Linux dockerd).
    if ! docker info >/dev/null 2>&1; then
      case "$(uname -s)" in
        Darwin)
          command -v brew >/dev/null || { echo "❌ Install Homebrew first: https://brew.sh"; exit 1; }
          command -v colima >/dev/null || { echo "==> installing colima (lightweight Linux VM)…"; brew install colima; }
          command -v docker >/dev/null || { echo "==> installing the docker CLI…"; brew install docker; }
          if ! colima status >/dev/null 2>&1; then
            echo "==> starting colima (first boot takes a minute)…"
            # vz + Rosetta runs the amd64 toolchain image fast on Apple Silicon;
            # falls back to a plain start (qemu) on older macOS/Intel.
            colima start --vm-type=vz --vz-rosetta 2>/dev/null || colima start
          fi
          # Make sure linux/amd64 images (the ARM cross toolchain) can run.
          docker run --privileged --rm tonistiigi/binfmt --install amd64 >/dev/null 2>&1 || true
          ;;
        Linux)
          echo "❌ Docker isn't available. Install it (e.g. 'sudo apt-get install -y docker.io',"
          echo "   add yourself to the 'docker' group) or see https://docs.docker.com/engine/install/, then re-run."
          exit 1 ;;
        *)
          echo "❌ Unsupported OS for auto-setup — install a Docker-compatible runtime and re-run."; exit 1 ;;
      esac
      docker info >/dev/null 2>&1 || { echo "❌ Docker still not reachable after setup."; exit 1; }
    fi
    echo "==> Docker runtime OK"
    # 2. Build the builder image (Go + baked ARM aarch64 toolchain + libopus).
    echo "==> building the builder image (first run downloads the toolchain)…"
    docker build --platform={{platform}} \
      --build-arg HTTP_PROXY="${HTTP_PROXY:-}" --build-arg HTTPS_PROXY="${HTTPS_PROXY:-}" \
      -t {{image}} -f docker/Dockerfile .
    echo "OK — now: just build-pro"

# Build just the Go server (with the mesh bridge) inside the builder image.
# Output: server/NanoKVM-Server. The repo is mounted at /work; the baked
# toolchain.ini (absolute paths into the image) is copied in so build.sh resolves
# the baked cross compiler, then the output is chown'd back to the caller.
[doc("Build just the Go server (mesh bridge) in the builder image.")]
build-server:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! docker image inspect {{image}} >/dev/null 2>&1; then
      echo "==> builder image missing — running setup-pro first…"
      just setup-pro
    fi
    echo "==> building NanoKVM-Server (in {{image}})…"
    # bash -c (NOT -lc): a login shell re-sources /etc/profile, which resets PATH
    # and drops /usr/local/go/bin (the golang image puts Go on PATH via ENV, not
    # profile) — that made build.sh report "Go is not installed". Also prepend
    # Go's bin explicitly so it's found regardless of the shell's profile.
    docker run --rm --platform={{platform}} \
      -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
      -v "$(pwd):/work" {{image}} bash -c '
        set -e
        export PATH="/usr/local/go/bin:${PATH}"
        mkdir -p /work/support/toolchains
        cp /opt/nanokvm-pro/support/toolchains/toolchain.ini /work/support/toolchains/toolchain.ini
        cd /work/server && ./build.sh
        chown "${HOST_UID}:${HOST_GID}" NanoKVM-Server 2>/dev/null || true
      '
    test -f server/NanoKVM-Server && echo "OK -> server/NanoKVM-Server"

# Build the web UI bundle (the React/vite SPA the device serves) into web/dist.
#
# WHY this is built and shipped (the NanoKVM never bothered): the device serves
# this SPA over BOTH its HTTPS LAN port AND the AllMyStuff mesh "sites" tunnel,
# where the viewer maps it to http://localhost:<port>. The firmware's stock Pro
# SPA renders on its own https://<ip> origin but goes BLANK at that tunnel origin
# — so we ship OUR build, which is origin-relative (vite base '/') and renders
# through the tunnel byte-for-byte identically to direct access. Built in a
# node:22 image (vite 7 needs Node >=20) so a Mac without Node still builds it;
# the output is plain JS, so there's no amd64 pin here (native arch = same bytes,
# and faster). The web-builder image bakes node-gyp's toolchain (python3/g++) —
# see docker/web.Dockerfile for why an optional `ws` addon forces that.
[doc("Build the web UI bundle (carries the Mesh tab) into web/dist.")]
build-web:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! docker info >/dev/null 2>&1; then
      echo "==> Docker not running — running setup-pro first (bootstraps Docker)…"
      just setup-pro
    fi
    if ! docker image inspect {{web_image}} >/dev/null 2>&1; then
      echo "==> building the web-builder image (node:22 + node-gyp toolchain)…"
      docker build -t {{web_image}} -f docker/web.Dockerfile docker
    fi
    echo "==> building the web bundle (vite) in {{web_image}}…"
    docker run --rm \
      -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
      -v "$(pwd)/web:/web" -w /web {{web_image}} bash -c '
        set -e
        pnpm install --frozen-lockfile
        pnpm run build
        chown -R "${HOST_UID}:${HOST_GID}" dist node_modules 2>/dev/null || true
      '
    test -f web/dist/index.html && echo "OK -> web/dist"

# Complete device build: the server + the pinned daemon + the web bundle, staged
# for deploy. The web bundle is part of the payload now — the mesh tunnel needs
# our origin-relative build, not the firmware's stock SPA.
[doc("Build a complete device image: server + web UI + pinned daemon.")]
build-pro: build-server daemon build-web

# The daemon is never built here — MyOwnMesh cross-compiles + publishes it, and
# this fails with a clear pointer (not a wrong build) if the pinned release has
# no aarch64-musl asset yet.
#
# Download the pinned MyOwnMesh daemon release and stage it for deploy.
daemon:
    #!/usr/bin/env bash
    set -euo pipefail
    rev="$(cat .myownmesh-rev)"
    dst="{{daemon_dst}}"; mkdir -p "$(dirname "$dst")"
    asset="myownmesh-linux-aarch64-musl.tar.gz"
    url="{{mom_repo}}/releases/download/${rev}/${asset}"
    sha() { if command -v sha256sum >/dev/null; then sha256sum -c "$1"; else shasum -a 256 -c "$1"; fi; }
    tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
    echo "==> daemon pinned at ${rev}: ${url}"
    if ! curl -fsSL "$url" -o "$tmp/$asset"; then
      echo "❌ no ${asset} published at ${rev}." >&2
      echo "   The aarch64-musl daemon asset ships from MyOwnMesh's release pipeline (the" >&2
      echo "   daemon-aarch64-musl job). Cut a MyOwnMesh release that includes it (just release" >&2
      echo "   <ver>), then set .myownmesh-rev to that tag. Or build it yourself: in a MyOwnMesh" >&2
      echo "   checkout run 'just build-aarch64-musl' and copy" >&2
      echo "   target/aarch64-unknown-linux-musl/release/myownmesh to ${dst}." >&2
      exit 1
    fi
    if curl -fsSL "$url.sha256" -o "$tmp/$asset.sha256"; then
      echo "    verifying sha256…"; ( cd "$tmp" && sha "$asset.sha256" )
    else
      echo "    (no .sha256 published; skipping integrity check)"
    fi
    tar -xzf "$tmp/$asset" -C "$(dirname "$dst")"
    chmod +x "$dst"
    echo "OK (release ${rev}) -> $dst"

# Print the pinned MyOwnMesh daemon revision.
daemon-rev:
    @cat .myownmesh-rev

# ── Download-only path: deploy a release with NO local build ───────────────────
#
# `just install <device-ip>` fetches the prebuilt device bundle (server + the
# pinned daemon, in one NanoKVM-Pro release asset) and deploys it. Nothing is
# compiled locally.

# Download the device bundle (latest release, or VERSION): server + daemon.
fetch VERSION="latest":
    #!/usr/bin/env bash
    set -euo pipefail
    sha() { if command -v sha256sum >/dev/null; then sha256sum -c "$1"; else shasum -a 256 -c "$1"; fi; }
    asset="nanokvm-pro-mesh-aarch64.tar.gz"
    if [ "{{VERSION}}" = "latest" ]; then
      url="{{nanokvm_repo}}/releases/latest/download/${asset}"
    else
      url="{{nanokvm_repo}}/releases/download/{{VERSION}}/${asset}"
    fi
    tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
    echo "==> device bundle ({{VERSION}}): ${url}"
    if ! curl -fsSL "$url" -o "$tmp/$asset"; then
      echo "❌ no ${asset} at {{VERSION}}. Cut a NanoKVM-Pro release (just release X.Y.Z) so CI publishes it," >&2
      echo "   or build locally with 'just build-pro'." >&2
      exit 1
    fi
    if curl -fsSL "$url.sha256" -o "$tmp/$asset.sha256"; then
      echo "    verifying sha256…"; ( cd "$tmp" && sha "$asset.sha256" )
    else
      echo "    (no .sha256 published; skipping integrity check)"
    fi
    mkdir -p server "$(dirname "{{daemon_dst}}")"
    tar -xzf "$tmp/$asset" -C "$tmp"
    cp "$tmp/NanoKVM-Server" server/NanoKVM-Server
    cp "$tmp/myownmesh"      "{{daemon_dst}}"
    chmod +x server/NanoKVM-Server "{{daemon_dst}}"
    rm -rf web/dist && mkdir -p web/dist && cp -a "$tmp/web/." web/dist/
    echo "OK -> server/NanoKVM-Server + {{daemon_dst}} + web/dist"
    echo "Now: just deploy <device-ip>   (or use 'just install <device-ip>')"

# Fetch the prebuilt device bundle (server + daemon) and deploy to a device.
install ip VERSION="latest": (fetch VERSION)
    @just deploy {{ip}}

# Bump the advertised version, commit, push, then push the `vX.Y.Z` tag to
# trigger the release workflow.
[doc("Cut a release: bump version, commit, push, tag (triggers the CI bundle).")]
release VERSION:
    #!/usr/bin/env bash
    set -euo pipefail
    ./scripts/bump-version.sh "{{VERSION}}"
    if ! git diff --quiet server/service/mesh/bridge.go web/package.json; then
      git add server/service/mesh/bridge.go web/package.json
      git commit -m "chore(release): {{VERSION}}"
    fi
    git push
    git tag "v{{VERSION}}"
    git push origin "v{{VERSION}}"
    echo ""
    echo "✓ pushed tag v{{VERSION}} — the release workflow is building the device bundle."
    echo "  It publishes nanokvm-pro-mesh-aarch64.tar.gz (server + web + pinned daemon)."
    echo "  Then: just install <device-ip>   (downloads that bundle and deploys)"

# Copy the complete device build (server + daemon + systemd unit) to a device
# and (re)start the services. The Pro is systemd, so we install the unit into
# /etc/systemd/system, daemon-reload, enable+start myownmesh, then restart the
# server so its bridge connects to the freshly-started daemon socket.
[doc("Copy the built server + daemon + web + systemd unit to a device and restart.")]
deploy ip:
    #!/usr/bin/env bash
    set -euo pipefail
    test -f server/NanoKVM-Server && test -f "{{daemon_dst}}" && test -d web/dist || { echo "❌ build first: just build-pro"; exit 1; }
    echo "==> deploying to {{ip}}…"
    # Bundle the whole payload into ONE tarball → one scp + one ssh (so you type
    # the password twice, not once per file). CRITICAL: the daemon and server run
    # from these exact paths, and Linux refuses to overwrite a *running* executable
    # in place (scp truncates → ETXTBSY, "dest open Failure"). So on the device we
    # unpack to a temp dir and install each binary by writing it alongside its
    # target then rename()-ing over it — a rename replaces a running binary fine
    # (the live process keeps the old inode; the new file takes the path).
    tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
    cp "{{daemon_dst}}"       "$tmp/myownmesh"
    cp server/NanoKVM-Server  "$tmp/NanoKVM-Server"
    cp "{{prestart_src}}"     "$tmp/myownmesh-prestart.sh"
    cp "{{unit_src}}"         "$tmp/myownmesh.service"
    mkdir -p "$tmp/web"
    cp -a web/dist/.          "$tmp/web/"
    tar -czf "$tmp/deploy.tar.gz" -C "$tmp" myownmesh NanoKVM-Server myownmesh-prestart.sh myownmesh.service web
    # Stage on /kvmapp (writable rootfs, same fs as the targets — so the swap is a
    # same-dir rename and there is no tmpfs size limit to worry about).
    scp "$tmp/deploy.tar.gz" root@{{ip}}:/kvmapp/nanokvm-pro-deploy.tar.gz
    # Remote install: unpack, then install each binary by writing it beside its
    # target (same dir = same fs) and rename()-ing over it — safe even while the
    # old binary is executing (the live process keeps the old inode). No single
    # quotes inside this block (it is single-quoted for ssh).
    #
    # Decompress with gzip piped into tar, NOT `tar -xzf`: the Pro's userland has
    # GNU tar today (so -z would work here), but the non-pro's tar is BusyBox,
    # whose applet has no -z. Keep the two deploy recipes on the identical
    # portable form — gzip is universally present and this works on both GNU and
    # BusyBox tar — so a future Pro image on a slimmer rootfs can't regress.
    ssh root@{{ip}} '
      set -e
      d="$(mktemp -d -p /kvmapp)"
      gzip -dc /kvmapp/nanokvm-pro-deploy.tar.gz | tar -xf - -C "$d"
      mkdir -p /kvmapp/system/bin /kvmapp/server
      install_swap() { cp -f "$1" "$2.deploytmp" && chmod +x "$2.deploytmp" && mv -f "$2.deploytmp" "$2"; }
      install_swap "$d/myownmesh"             /kvmapp/system/bin/myownmesh
      install_swap "$d/NanoKVM-Server"        /kvmapp/server/NanoKVM-Server
      install_swap "$d/myownmesh-prestart.sh" /kvmapp/system/bin/myownmesh-prestart.sh
      cp -f "$d/myownmesh.service" /etc/systemd/system/myownmesh.service
      # Web UI bundle: the server serves /kvmapp/server/web (execDir/web) per
      # request, so unlike a running binary it can be replaced freely — but stage
      # into web.new and rename over web so a request never sees a half-copied
      # tree. Same fs (/kvmapp/server), so the rename is atomic.
      rm -rf /kvmapp/server/web.new /kvmapp/server/web.old
      mkdir -p /kvmapp/server/web.new
      cp -a "$d/web/." /kvmapp/server/web.new/
      [ -d /kvmapp/server/web ] && mv /kvmapp/server/web /kvmapp/server/web.old
      mv /kvmapp/server/web.new /kvmapp/server/web
      rm -rf /kvmapp/server/web.old
      rm -rf "$d" /kvmapp/nanokvm-pro-deploy.tar.gz
      systemctl daemon-reload
      systemctl enable myownmesh >/dev/null 2>&1 || true
      systemctl restart myownmesh
      systemctl restart nanokvm
      echo "device: services restarted"
    '
    echo "OK — just verify {{ip}}"

reboot ip:
    @ssh root@{{ip}} reboot || true

# Daemon + bridge: both systemd units, persisted state, and both logs on a device.
verify ip:
    @ssh root@{{ip}} 'echo "--- myownmesh unit ---"; systemctl --no-pager status myownmesh 2>/dev/null | head -n 12 || echo "(no unit)"; echo "--- nanokvm unit ---"; systemctl --no-pager status nanokvm 2>/dev/null | head -n 8 || echo "(no unit)"; echo "--- state (/data/myownmesh) ---"; ls -la /data/myownmesh 2>/dev/null || echo "(none yet)"; echo "--- daemon log (journal) ---"; journalctl -u myownmesh --no-pager -n 30 2>/dev/null || echo "(none yet)"; echo "--- bridge log ---"; tail -n 30 /var/log/nanokvm-mesh.log 2>/dev/null || echo "(none yet)"'

# Reversible undo on a device: stop+disable the daemon, remove the unit + helper.
undeploy ip:
    @ssh root@{{ip}} 'systemctl disable --now myownmesh 2>/dev/null; rm -f /etc/systemd/system/myownmesh.service /kvmapp/system/bin/myownmesh /kvmapp/system/bin/myownmesh-prestart.sh; systemctl daemon-reload; systemctl restart nanokvm' || true

clean-pro:
    @rm -rf server/NanoKVM-Server {{daemon_dst}} web/dist
    @echo "removed build outputs"
