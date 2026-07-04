# check=skip=FromPlatformFlagConstDisallowed
# Build NanoKVM-Server (with the mesh bridge) for the AX630C on ANY host.
#
# Pinned to linux/amd64 because the ARM GNU cross toolchain (support/scripts/
# config.ini) is an x86_64 *Linux* binary. On a native amd64 Linux host this is
# a no-op; on Apple Silicon / an ARM Mac it runs under Rosetta/QEMU — which is
# the whole point: the native toolchain is an x86_64 Linux ELF that cannot
# execute on macOS at all, so `just build-pro` on a Mac must go through here.
# Mirrors the NanoKVM's docker/Dockerfile.
FROM --platform=linux/amd64 golang:1.25 AS builder

# Optional proxy passthrough for locked-down build networks (harmless if unset).
ARG HTTP_PROXY
ARG HTTPS_PROXY

# build-essential provides `ar` (deb extraction); zstd is REQUIRED because
# toolchain_setup.sh's install_libopus extracts the jammy libopus .deb whose
# data member is data.tar.zst (Ubuntu switched dpkg to zstd in 21.10). Without
# zstd that extraction fails *silently* and opus/opus.h never lands in the
# sysroot, breaking the CGO build (service/stream/opus) later.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       patchelf python3 xz-utils zstd curl ca-certificates file build-essential \
    && rm -rf /var/lib/apt/lists/*

# Bake the ARM aarch64 cross toolchain + the libopus sysroot by running Sipeed's
# own setup script against a copy of support/. It installs into
# /opt/nanokvm-pro/support/toolchains and writes toolchain.ini with ABSOLUTE
# compiler paths pointing there — so a build with the repo mounted elsewhere
# (/work) still resolves this baked toolchain. Baking it here means `build-pro`
# never re-downloads and never hits the toolchain-setup interactivity.
COPY support /opt/nanokvm-pro/support
RUN cd /opt/nanokvm-pro/support/scripts && ./toolchain_setup.sh \
    && test -f /opt/nanokvm-pro/support/toolchains/toolchain.ini \
    # Assert the libopus-dev headers actually landed in the sysroot. install_libopus
    # ignores its own extraction failures (returns 0 even when nothing is copied),
    # so verify here — otherwise a missing zstd would ship a broken image that only
    # fails much later at the CGO opus build.
    && { ls /opt/nanokvm-pro/support/toolchains/*/aarch64-none-linux-gnu/libc/usr/include/opus/opus.h >/dev/null 2>&1 \
         || { echo "ERROR: libopus-dev headers (opus/opus.h) not injected into the sysroot — deb extraction failed"; exit 1; }; }

WORKDIR /work
