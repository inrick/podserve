#!/bin/sh
set -eu

PREFIX="${PREFIX:-/usr/local}"
SERVICE_USER="podserve"
SERVICE_GROUP="podserve"

if [ "$(id -u)" -ne 0 ]; then
    echo "This script must be run as root" >&2
    exit 1
fi

echo "===> Stopping service"
service podserve stop 2>/dev/null || true

echo "===> Removing files"
rm -f "${PREFIX}/bin/podserve"
rm -f "${PREFIX}/etc/rc.d/podserve"
rm -f "${PREFIX}/etc/newsyslog.conf.d/podserve.conf"

echo "===> Removing user"
pw usershow "${SERVICE_USER}" >/dev/null 2>&1 && pw userdel "${SERVICE_USER}"
echo "===> Removing group"
pw groupshow "${SERVICE_GROUP}" >/dev/null 2>&1 && pw groupdel "${SERVICE_GROUP}"

echo
echo "Left in place, remove manually if desired:"
echo "    /var/log/podserve.log"
echo "Also remove podserve_* lines from /etc/rc.conf."
