#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — lab probe: how does unix_chkpwd behave on Debian 13?
# (ADR-0018: the first admin proves ownership with the machine account.)
#
# Creates a throwaway account, checks passwords through unix_chkpwd the way
# nbhelper would (root, password + NUL on stdin), then deletes the account.
# Run as root inside the lab: wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-chkpwd.sh
set -u
export COLUMNS=512 LINES=50

U=cwnsb-chkpwd-probe
PW='Tr0ub4dor & "3" é ü $HOME'

cleanup() { userdel -r "$U" >/dev/null 2>&1; }
trap cleanup EXIT

main() {
  [ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
  local bin
  bin=$(command -v unix_chkpwd || ls /usr/sbin/unix_chkpwd /sbin/unix_chkpwd 2>/dev/null | head -1)
  echo "== unix_chkpwd: ${bin:-MISSING}"
  [ -n "$bin" ] || exit 1
  ls -l "$bin"
  dpkg -S "$bin" 2>/dev/null || dpkg -S "$(readlink -f "$bin")" 2>/dev/null

  cleanup
  useradd --create-home --shell /bin/bash "$U"
  printf '%s:%s\n' "$U" "$PW" | chpasswd
  echo "== hash prefix: $(getent shadow "$U" | cut -d: -f2 | cut -c1-4)"

  check() { # check <label> <user> <password>
    local start rc
    start=$(date +%s%N)
    printf '%s\0' "$3" | "$bin" "$2" nonull >/dev/null 2>&1
    rc=$?
    printf '%-34s exit=%-3s %5d ms\n' "$1" "$rc" $((($(date +%s%N) - start) / 1000000))
  }

  check "right password" "$U" "$PW"
  check "wrong password" "$U" "nope"
  check "empty password" "$U" ""
  check "right password + trailing newline" "$U" "$PW"$'\n'
  check "unknown user" "cwnsb-nobody-here" "x"
  passwd -l "$U" >/dev/null
  check "locked account, right password" "$U" "$PW"
  passwd -u "$U" >/dev/null
  check "unlocked again, right password" "$U" "$PW"
  usermod -p '*' "$U"
  check "no password set ('*')" "$U" ""

  echo "== without the NUL terminator (password + EOF):"
  usermod -p "$(getent shadow root | cut -d: -f2)" "$U" >/dev/null 2>&1 # any hash, then reset
  printf '%s:%s\n' "$U" "$PW" | chpasswd
  start=$(date +%s%N); printf '%s' "$PW" | "$bin" "$U" nonull >/dev/null 2>&1; echo "no NUL, right password: exit=$?"

  echo "== groups of the account: $(id -nG "$U")"
  echo "== journal (last lines mentioning unix_chkpwd):"
  journalctl -q --no-pager -n 200 2>/dev/null | grep -i "chkpwd\|$U" | tail -5
}

main "$@" </dev/null
exit 0
