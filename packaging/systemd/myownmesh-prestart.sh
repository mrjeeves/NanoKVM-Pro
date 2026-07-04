#!/bin/sh
# myownmesh-prestart.sh — ExecStartPre for myownmesh.service on the NanoKVM-Pro.
#
# Prepares the daemon's environment before `myownmesh serve` starts:
#   1. ensures the persistent home ($MYOWNMESH_HOME) exists,
#   2. waits (bounded) for a real wall clock before the daemon joins the mesh,
#   3. pins the daemon's control socket onto the runtime tmpfs.
#
# It takes the control-socket path as $1 so the systemd unit is the single
# source of truth for it (the Pro server's mesh.socket in /etc/kvm/server.yaml
# must match). Everything here is best-effort and bounded: a slow/absent NTP
# path degrades to "come up and recover later" rather than hanging boot.
set -u

CONTROL_SOCKET="${1:?usage: myownmesh-prestart.sh <control-socket-path>}"
HOME_DIR="${MYOWNMESH_HOME:-/data/myownmesh}"
CONFIG="$HOME_DIR/config.json"

mkdir -p "$HOME_DIR"
# The socket's parent (/run/myownmesh) is created by systemd RuntimeDirectory=,
# but create it defensively so a manual `systemctl start` outside that setup
# still works.
mkdir -p "$(dirname "$CONTROL_SOCKET")"

# Clock-before-join. Like the NanoKVM, a Pro may boot without a valid wall clock
# (no/unset RTC): the mesh's relays use TLS (a 1970 clock rejects their certs as
# "not valid yet") and presence/roster state is timestamped/signed, so joining at
# 1970 makes peers treat the node as invalid and the later time jump tears the
# live connection down. The systemd unit already orders us After=time-sync.target
# chrony.service; this is a bounded backstop for the no-RTC cold-boot case.
year="$(date -u +%Y 2>/dev/null || echo 0)"
if [ "$year" -lt 2020 ] 2>/dev/null; then
    echo "myownmesh: clock unset ($(date -u 2>/dev/null)); waiting for time sync…"
    if command -v chronyc >/dev/null 2>&1; then
        # Force a step to the true time now, then wait (bounded) for it to land.
        chronyc -a makestep >/dev/null 2>&1 || true
        chronyc waitsync 25 0 0 1 >/dev/null 2>&1 || true
    else
        _i=0
        while [ "$(date -u +%Y 2>/dev/null || echo 0)" -lt 2020 ] && [ "$_i" -lt 25 ]; do
            sleep 1
            _i=$((_i + 1))
        done
    fi
    echo "myownmesh: clock now $(date -u 2>/dev/null)"
fi

# Pin the daemon's control socket onto the runtime tmpfs. The daemon defaults to
# $MYOWNMESH_HOME/daemon.sock; we move it to the tmpfs path so it never depends
# on the data partition's filesystem (and matches the server's mesh.socket).
# MeshConfig is #[serde(default)], so this minimal config.json loads fine and the
# daemon preserves control_socket across its own saves. The guard matches the
# socket VALUE, not just the key: the daemon serializes "control_socket": null
# when unset, and an operator-edited file can carry a wrong path — either would
# satisfy a key-only check and leave the daemon binding the wrong place forever.
if [ ! -f "$CONFIG" ]; then
    printf '%s\n' "{\"daemon\":{\"control_socket\":\"$CONTROL_SOCKET\"}}" >"$CONFIG"
elif ! grep -q "$CONTROL_SOCKET" "$CONFIG" 2>/dev/null; then
    echo "myownmesh: config.json lost its control_socket pin — resetting to minimal config (old saved as config.json.bad)"
    cp "$CONFIG" "$CONFIG.bad" 2>/dev/null || true
    printf '%s\n' "{\"daemon\":{\"control_socket\":\"$CONTROL_SOCKET\"}}" >"$CONFIG"
fi

exit 0
