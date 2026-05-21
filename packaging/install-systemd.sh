#!/bin/sh
set -eu

install -d -m 0755 /etc/fvc
install -d -m 0755 /usr/bin
install -m 0644 packaging/systemd/fvcd.service /etc/systemd/system/fvcd.service

if [ -x ./fvc ]; then
  install -m 0755 ./fvc /usr/bin/fvc
fi
if [ -x ./fvcd ]; then
  install -m 0755 ./fvcd /usr/bin/fvcd
fi
if [ -x ./fvc-build-agent ]; then
  install -m 0755 ./fvc-build-agent /usr/bin/fvc-build-agent
fi
if [ -x ./fvc-init ]; then
  install -m 0755 ./fvc-init /usr/bin/fvc-init
fi

if [ -f packaging/systemd/fvcd.env.example ] && [ ! -f /etc/fvc/fvcd.env ]; then
  install -m 0644 packaging/systemd/fvcd.env.example /etc/fvc/fvcd.env
fi

if ! getent group fvc >/dev/null 2>&1; then
  groupadd --system fvc
fi

systemctl daemon-reload
systemctl enable fvcd.service
