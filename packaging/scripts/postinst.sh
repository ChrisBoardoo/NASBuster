#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — after install or upgrade: service account, services,
# then tell the user how to finish the setup in the browser.
#
# Works with and without a running systemd: in a chroot or an image build
# (pi-gen) the units are only enabled for the next boot.
set -e

systemd-sysusers cwnsb.conf
mkdir -p /srv/cwnsb

if [ ! -d /run/systemd/system ]; then
  deb-systemd-helper enable nbhelper.socket >/dev/null || true
  deb-systemd-helper enable nasbusterd.service >/dev/null || true
  echo "CrossWax NasBuster installed; it will start at the next boot."
  exit 0
fi

systemctl daemon-reload
systemctl enable --now nbhelper.socket
# An upgrade must not leave the previous helper serving (seen on the Pi 5: it
# stays up as long as the UI keeps calling it). Its socket starts the new one
# at the next call; long operations run in their own units (ADR-0026).
systemctl stop nbhelper.service 2>/dev/null || true
systemctl enable nasbusterd.service
systemctl restart nasbusterd.service

# First install: the daemon writes the setup code within a second or two.
i=0
while [ ! -f /var/lib/cwnsb/setup-code ] && [ "$i" -lt 15 ]; do
  sleep 1
  i=$((i + 1))
done
if [ -f /var/lib/cwnsb/setup-code ]; then
  host=$(hostname)
  # The account that ran "sudo apt install", if any: it proves ownership (ADR-0018).
  account=""
  case "${SUDO_USER:-}" in
    "" | root | *[!a-z0-9_-]*) ;;
    *) account=" ($SUDO_USER)" ;;
  esac
  echo ""
  echo "  CrossWax NasBuster is running."
  echo "  Open https://$host.local (or https://<this device's IP address>)"
  echo "  and sign in with this machine's account$account, the one you created"
  echo "  in Raspberry Pi Imager or when installing Debian."
  echo ""
  echo "  No password on that account? Use this setup code instead:"
  echo ""
  echo "      $(cat /var/lib/cwnsb/setup-code)"
  echo ""
  # The browser will warn about the self-signed certificate (ADR-0019): this
  # fingerprint, seen here on the machine itself, lets the user check it.
  i=0
  while [ ! -f /var/lib/cwnsb/tls/cert.pem ] && [ "$i" -lt 10 ]; do
    sleep 1
    i=$((i + 1))
  done
  if fingerprint=$(/usr/bin/nasbusterd -print-tls-fingerprint 2>/dev/null); then
    echo "  Your browser will warn about the certificate: the NAS signs it itself."
    echo "  Check that its SHA-256 fingerprint is this one before going on:"
    echo ""
    echo "      $fingerprint"
    echo ""
  fi
fi
exit 0
