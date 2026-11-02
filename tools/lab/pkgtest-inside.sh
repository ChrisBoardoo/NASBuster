#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — install the .deb on a FRESH Debian 13 and check it
# (run as root inside the disposable cwnsb-pkgtest distro, see pkgtest.ps1).
set -uo pipefail
[ "$(hostname)" = "cwnsb-pkgtest" ] && [ "$(id -u)" -eq 0 ] || { echo "pkgtest distro only" >&2; exit 1; }

fails=0
pass() { printf '  PASS %s\n' "$1"; }
fail() { printf '  FAIL %s\n' "$1"; fails=$((fails + 1)); }
check() { if eval "$2" >/dev/null 2>&1; then pass "$1"; else fail "$1"; fi; }

# Newest build first (by date: version strings do not sort by name).
deb=$(ls -t /mnt/h/Work/repos/NASBuster/dist/nasbuster_*_amd64.deb | head -1)
cp "$deb" /tmp/nasbuster.deb
echo "== Install $(basename "$deb") on a fresh Debian 13"
[ "$(cat /proc/1/comm)" = "systemd" ] && pass "systemd is PID 1 (like a real Pi)" || fail "systemd is PID 1 (like a real Pi)"
# Docker-exported images forbid service starts (policy-rc.d); real systems do not.
rm -f /usr/sbin/policy-rc.d
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl >/dev/null
apt-get install -y /tmp/nasbuster.deb 2>&1 | tail -12

echo "== After install"
check "nasbusterd active" "systemctl is-active --quiet nasbusterd"
check "nbhelper.socket active" "systemctl is-active --quiet nbhelper.socket"
check "service account cwnsb (nologin)" "getent passwd cwnsb | grep -q nologin"
check "setup code written" "test -s /var/lib/cwnsb/setup-code"
sleep 2
check "HTTPS API on :443" "curl -skf https://127.0.0.1/api/v1/health | grep -q ok"
check "web UI served" "curl -skf https://127.0.0.1/ | grep -qi '<div id=\"root\">'"
check "Samba config replaced at startup (no guest)" "grep -q 'Managed by CrossWax' /etc/samba/smb.conf && grep -q 'map to guest = never' /etc/samba/smb.conf"
check "Debian's smb.conf backed up" "test -s /etc/samba/smb.conf.cwnsb-orig"
check "all dependencies present" "command -v btrfs mdadm sgdisk smartctl smbd lsblk rsync"
# samba recommends samba-ad-dc, which pulls winbind into NSS and PAM (ADR-0017).
check "no AD DC nor winbind pulled in" "! dpkg -s samba-ad-dc winbind libpam-winbind 2>/dev/null | grep -q 'Status: install ok'"
check "nmbd stopped (NetBIOS disabled)" "! systemctl is-active --quiet nmbd"
check "NSS without winbind" "! grep -qE '^(passwd|group):.*winbind' /etc/nsswitch.conf"
# The APT repository key, byte for byte against the checkout (gpg is not on a minimal Debian 13).
check "APT repository key installed (ADR-0023)" "cmp -s /usr/share/keyrings/nasbuster-archive-keyring.asc /mnt/h/Work/repos/NASBuster/packaging/apt/nasbuster-archive-keyring.asc"
# The installer shows the certificate's fingerprint (ADR-0019).
check "certificate fingerprint printable" "/usr/bin/nasbusterd -print-tls-fingerprint | grep -Eq '^([0-9A-F]{2}:){31}[0-9A-F]{2}$'"
# With User=root, NoNewPrivileges= and a system-call filter, systemd 257 drops
# CAP_SETUID: apt-get could not switch to _apt, and Docker could not be removed.
helper_caps_full() {
  local p
  p=$(systemctl show -p MainPID --value nbhelper.service)
  [ "$p" != 0 ] && [ "$(awk '/^CapPrm/{print $2}' "/proc/$p/status")" = "$(awk '/^CapBnd/{print $2}' "/proc/$p/status")" ]
}
systemctl start nbhelper.service
check "nbhelper keeps every capability (apt can switch to _apt)" helper_caps_full
mkdir -p /srv/cwnsb/canary && echo keep > /srv/cwnsb/canary/data.txt

echo "== Remove (keeps state), then purge (daemon state only)"
apt-get remove -y -qq nasbuster >/dev/null 2>&1
check "services stopped after remove" "! systemctl is-active --quiet nasbusterd"
check "state kept after remove" "test -d /var/lib/cwnsb"
apt-get purge -y -qq nasbuster >/dev/null 2>&1
check "state removed after purge" "! test -e /var/lib/cwnsb"
check "data under /srv/cwnsb NEVER touched" "grep -q keep /srv/cwnsb/canary/data.txt"

# The package was called cwnsb before v0.1.0 (ADR-0040): a NAS that has it
# switches to nasbuster with apt, keeping its state, its services and its data.
old=$(ls -t /mnt/h/Work/repos/NASBuster/dist/cwnsb_*_amd64.deb 2>/dev/null | head -1)
if [ -n "$old" ]; then
  echo "== Switch from the old package name: $(basename "$old")"
  cp "$old" /tmp/old.deb
  apt-get install -y -qq /tmp/old.deb >/dev/null 2>&1
  check "old cwnsb running" "systemctl is-active --quiet nasbusterd"
  touch /var/lib/cwnsb/switch-canary
  apt-get install -y -qq /tmp/nasbuster.deb >/dev/null 2>&1
  check "cwnsb replaced by nasbuster" "dpkg -s nasbuster | grep -q 'install ok installed' && ! dpkg -s cwnsb 2>/dev/null | grep -q 'install ok installed'"
  check "state kept across the switch" "test -f /var/lib/cwnsb/switch-canary"
  check "nasbusterd active after the switch" "systemctl is-active --quiet nasbusterd"
  check "nbhelper.socket active after the switch" "systemctl is-active --quiet nbhelper.socket"
  sleep 2
  check "HTTPS API after the switch" "curl -skf https://127.0.0.1/api/v1/health | grep -q ok"
  check "data under /srv/cwnsb NEVER touched" "grep -q keep /srv/cwnsb/canary/data.txt"
  apt-get purge -y -qq nasbuster >/dev/null 2>&1
fi

echo
if [ "$fails" -eq 0 ]; then echo "PACKAGE OK"; else echo "$fails FAILED"; exit 1; fi
