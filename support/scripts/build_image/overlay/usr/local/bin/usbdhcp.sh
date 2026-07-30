#!/bin/sh
# usbdhcp.sh — give the USB-tethered host an address on the virtual network.
#
# The gadget gives the KVM a usb0 interface, but nothing was ever giving the
# machine on the other end of the cable an IP. Its USB ethernet adapter came up
# unaddressed — or self-assigned 169.254/16 — so it had no route to the KVM's
# usb0 address and could not reach it at all. That made the one path meant to
# work when the device has NO network the one path that never worked.
#
# The image has been provisioning /var/lib/misc/udhcpd.usb0.leases all along,
# with no config and nothing to start: this is the rest of that.
#
# Deliberately hands out an address and netmask ONLY — no router, no DNS. The
# host keeps its own default route and resolver, so plugging a KVM in can never
# black-hole its internet (the failure usbnet-share.sh exists to undo). Sharing
# the KVM's uplink stays opt-in, and lives there.
#
# Idempotent and best-effort: always exits 0, so nothing chained onto it can
# fail on a missing DHCP server or an interface that hasn't appeared yet.

IF=usb0
CONF=/etc/udhcpd.usb0.conf
PIDFILE=/var/run/udhcpd.usb0.pid
LEASES=/var/lib/misc/udhcpd.usb0.leases
FALLBACK_PREFIX=10.201.83

network_enabled() {
    [ -e /boot/usb.ncm ] || [ -e /boot/usb.rndis0 ]
}

# usb_prefix — the /24 usb0 already carries (e.g. "10.55.7"), or empty. Honoured
# ahead of anything we'd pick ourselves: the base image may address this
# interface, and two writers fighting over it would be worse than either.
usb_prefix() {
    ip -o -4 addr show "$IF" 2>/dev/null | awk '{print $4; exit}' | cut -d/ -f1 |
        awk -F. 'NF==4 { print $1"."$2"."$3 }'
}

start() {
    network_enabled || { echo "usb-dhcp: virtual network off; nothing to serve"; return 0; }

    # The gadget may still be enumerating on a fresh bring-up.
    i=0
    while [ ! -e "/sys/class/net/$IF" ] && [ "$i" -lt 5 ]; do
        sleep 1
        i=$((i + 1))
    done
    [ -e "/sys/class/net/$IF" ] || { echo "usb-dhcp: $IF not present; skipping"; return 0; }

    ip link set "$IF" up 2>/dev/null

    prefix=$(usb_prefix)
    if [ -z "$prefix" ]; then
        prefix=$FALLBACK_PREFIX
        echo "usb-dhcp: $IF has no address; assigning $prefix.1/24"
        ip addr add "$prefix.1/24" dev "$IF" 2>/dev/null
    fi

    mkdir -p /var/lib/misc
    [ -e "$LEASES" ] || : > "$LEASES"

    stop_quiet

    # udhcpd is what the provisioned lease file was written for; dnsmasq is the
    # fallback because a Debian rootfs won't always carry busybox's applet. Both
    # are told to serve addresses and nothing else.
    if command -v udhcpd >/dev/null 2>&1; then
        {
            echo "start $prefix.100"
            echo "end $prefix.200"
            echo "interface $IF"
            echo "pidfile $PIDFILE"
            echo "lease_file $LEASES"
            echo "option subnet 255.255.255.0"
            echo "option lease 864000"
        } > "$CONF"
        udhcpd -S "$CONF" 2>/dev/null
        echo "usb-dhcp: serving $prefix.100-$prefix.200 on $IF (udhcpd)"
    elif command -v dnsmasq >/dev/null 2>&1; then
        # --bind-interfaces + --except-interface keeps this off every other link,
        # so it can't answer DHCP or DNS for the LAN the KVM is also on.
        dnsmasq --pid-file="$PIDFILE" \
            --interface="$IF" --bind-interfaces --except-interface=lo \
            --no-hosts --no-resolv --port=0 \
            --dhcp-range="$prefix.100,$prefix.200,255.255.255.0,24h" \
            --dhcp-option=3 --dhcp-option=6 \
            --dhcp-authoritative 2>/dev/null
        echo "usb-dhcp: serving $prefix.100-$prefix.200 on $IF (dnsmasq)"
    else
        echo "usb-dhcp: no udhcpd or dnsmasq; the tethered host will get no address"
    fi
}

stop_quiet() {
    if [ -e "$PIDFILE" ]; then
        kill "$(cat "$PIDFILE")" 2>/dev/null
        rm -f "$PIDFILE"
    fi
}

stop() {
    stop_quiet
    echo "usb-dhcp: stopped"
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; start ;;
    *)       echo "usage: $0 {start|stop|restart}" ;;
esac
exit 0
