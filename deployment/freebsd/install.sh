#!/bin/sh
set -eu

DEPLOYMENT_FILES_PATH="deployment/freebsd"

PREFIX="${PREFIX:-/usr/local}"
BINDIR="${PREFIX}/bin"
RCDIR="${PREFIX}/etc/rc.d"
NEWSYSLOGDIR="${PREFIX}/etc/newsyslog.conf.d"
PIDFILE="${PIDFILE:-/var/run/podserve.pid}"
LOGFILE="${LOGFILE:-/var/log/podserve.log}"

BINARY="podserve"
SERVICE_USER="podserve"
SERVICE_GROUP="podserve"
USER_UID="917"
GROUP_GID="917"

if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root" >&2
  exit 1
fi

echo "===> Creating group"
if ! pw groupshow "${SERVICE_GROUP}" >/dev/null 2>&1; then
  pw groupadd "${SERVICE_GROUP}" -g "${GROUP_GID}"
fi
echo "===> Creating user"
if ! pw usershow "${SERVICE_USER}" >/dev/null 2>&1; then
  pw useradd "${SERVICE_USER}" \
    -u "${USER_UID}" \
    -g "${SERVICE_GROUP}" \
    -c "podserve service account" \
    -s /usr/sbin/nologin \
    -d /nonexistent
fi

echo "===> Installing binary and rc script"
install -m 555 -o root -g wheel "${BINARY}" "${BINDIR}/${BINARY}"
install -m 555 -o root -g wheel "${DEPLOYMENT_FILES_PATH}"/rc.in "${RCDIR}/podserve"
sed -i '' \
  -e "s|@@PREFIX@@|${PREFIX}|g" \
  -e "s|@@PIDFILE@@|${PIDFILE}|g" \
  -e "s|@@LOGFILE@@|${LOGFILE}|g" \
  "${RCDIR}/podserve"

echo "===> Installing log rotation config"
mkdir -p "${NEWSYSLOGDIR}"
install -m 644 -o root -g wheel "${DEPLOYMENT_FILES_PATH}/newsyslog.conf.in" "${NEWSYSLOGDIR}/podserve.conf"
sed -i '' \
  -e "s|@@PIDFILE@@|${PIDFILE}|g" \
  -e "s|@@LOGFILE@@|${LOGFILE}|g" \
  -e "s|@@SERVICE_USER@@|${SERVICE_USER}|g" \
  -e "s|@@SERVICE_GROUP@@|${SERVICE_GROUP}|g" \
  "${NEWSYSLOGDIR}/podserve.conf"


echo
echo "===> Installation done. Enable with:"
echo "sysrc podserve_enable=YES"
echo "sysrc podserve_args=\"-logFormat json -externalUrl https://example.com/podcast -port 4343 -title \\\\\\\"My Podcast\\\\\\\" -dir /var/www/podcast\""
echo "service podserve start"
