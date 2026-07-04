# NanoKVM-Pro — build & deploy the device with the native AllMyStuff bridge.
#
# `just build-pro` produces a COMPLETE device build:
#   server/NanoKVM-Server         the Go server (with the mesh bridge)
#   dist/myownmesh                the MyOwnMesh daemon, pinned in .myownmesh-rev
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

set shell := ["bash", "-uc"]

daemon_dst := "dist/myownmesh"
mom_repo := "https://github.com/mrjeeves/MyOwnMesh"
nanokvm_repo := "https://github.com/mrjeeves/NanoKVM-Pro"
unit_src := "packaging/systemd/myownmesh.service"
prestart_src := "packaging/systemd/myownmesh-prestart.sh"
image := "nanokvm-pro-builder"
platform := "linux/amd64"

default: help

help:
    @just --list

# One-time: get a Docker runtime going and build the builder image (Go + the ARM
# aarch64 cross toolchain baked in — see docker/Dockerfile). On a Mac this
# installs/starts Colima and enables amd64 emulation so the x86_64-Linux cross
# toolchain runs (the native toolchain is an x86_64 Linux ELF that can't execute
# on macOS at all). Idempotent: re-run any time. Mirrors the NanoKVM's setup-risc.
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

# Complete device build: the server + the pinned daemon, staged for deploy.
build-pro: build-server daemon

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
    echo "OK -> server/NanoKVM-Server + {{daemon_dst}}"
    echo "Now: just deploy <device-ip>   (or use 'just install <device-ip>')"

# Fetch the prebuilt device bundle (server + daemon) and deploy to a device.
install ip VERSION="latest": (fetch VERSION)
    @just deploy {{ip}}

# Bump the advertised version, commit, push, then push the `vX.Y.Z` tag to
# trigger the release workflow.
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
    echo "  It publishes nanokvm-pro-mesh-aarch64.tar.gz (server + pinned daemon)."
    echo "  Then: just install <device-ip>   (downloads that bundle and deploys)"

# Copy the complete device build (server + daemon + systemd unit) to a device
# and (re)start the services. The Pro is systemd, so we install the unit into
# /etc/systemd/system, daemon-reload, enable+start myownmesh, then restart the
# server so its bridge connects to the freshly-started daemon socket.
deploy ip:
    #!/usr/bin/env bash
    set -euo pipefail
    test -f server/NanoKVM-Server && test -f "{{daemon_dst}}" || { echo "❌ build first: just build-pro"; exit 1; }
    echo "==> deploying to {{ip}}…"
    ssh root@{{ip}} 'mkdir -p /kvmapp/system/bin'
    scp "{{daemon_dst}}"        root@{{ip}}:/kvmapp/system/bin/myownmesh
    scp "{{prestart_src}}"      root@{{ip}}:/kvmapp/system/bin/myownmesh-prestart.sh
    scp "{{unit_src}}"          root@{{ip}}:/etc/systemd/system/myownmesh.service
    scp server/NanoKVM-Server   root@{{ip}}:/kvmapp/server/NanoKVM-Server
    ssh root@{{ip}} 'chmod +x /kvmapp/system/bin/myownmesh /kvmapp/system/bin/myownmesh-prestart.sh /kvmapp/server/NanoKVM-Server && systemctl daemon-reload && systemctl enable --now myownmesh && systemctl restart nanokvm'
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
    @rm -rf server/NanoKVM-Server {{daemon_dst}}
    @echo "removed build outputs"
