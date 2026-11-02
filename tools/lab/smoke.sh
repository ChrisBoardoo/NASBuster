#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — end-to-end smoke test of the installed daemon in the lab.
# Exercises the real HTTPS server, sessions, CSRF, the root helper over its
# socket (SO_PEERCRED), real lsblk/udevadm/smartctl/hostnamectl/timedatectl.
#
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/smoke.sh
set -uo pipefail
export COLUMNS=512 LINES=50 # procps: "screen size is bogus" under wsl.exe

if [ "$(hostname)" != "cwnsb-lab" ] || [ "$(id -u)" -ne 0 ]; then
  echo "refusing: run as root inside the cwnsb-lab distro only" >&2
  exit 1
fi

BASE="https://127.0.0.1:8443/api/v1"
LAB_USER="admin"
LAB_PASSWORD="lab-password-123" # lab only, never used anywhere real
work="$(mktemp -d)"
jar="$work/cookies"
fails=0
trap 'for l in "${loops[@]:-}"; do [ -n "$l" ] && losetup -d "$l"; done; rm -rf "$work" /var/lib/cwnsb-lab/smoke-*.img' EXIT

pass() { printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fails=$((fails + 1)); }
csrf() { awk '$6 == "cwnsb_csrf" { print $7 }' "$jar"; }

# req METHOD PATH [JSON] -> prints "STATUS BODY"
req() {
  local method=$1 path=$2 body=${3:-}
  local args=(-sk -o "$work/body" -w '%{http_code}' -b "$jar" -c "$jar" -X "$method" -H "X-CSRF-Token: $(csrf)")
  if [ -n "$body" ]; then args+=(-H 'Content-Type: application/json' --data "$body"); fi
  local code
  code=$(curl "${args[@]}" "$BASE$path")
  printf '%s %s' "$code" "$(cat "$work/body")"
}

expect() { # expect NAME "STATUS BODY" WANT_STATUS [GREP]
  local name=$1 out=$2 want=$3 pattern=${4:-}
  local code=${out%% *} body=${out#* }
  if [ "$code" != "$want" ]; then
    fail "$name (HTTP $code, want $want): ${body:0:200}"
  elif [ -n "$pattern" ] && ! grep -q -- "$pattern" <<<"$body"; then
    fail "$name (missing $pattern): ${body:0:200}"
  else
    pass "$name"
  fi
}

echo "== Lab disks"
loops=()
for i in 1 2; do
  truncate -s 256M "/var/lib/cwnsb-lab/smoke-$i.img"
  loops+=("$(losetup --find --show "/var/lib/cwnsb-lab/smoke-$i.img")")
done
echo "  created ${loops[*]}"

echo "== Bootstrap"
expect "health" "$(req GET /health)" 200 '"status":"ok"'
expect "bootstrap sets CSRF cookie" "$(req GET /bootstrap)" 200 '"labMode":true'
[ -n "$(csrf)" ] && pass "csrf cookie present" || fail "csrf cookie present"

echo "== Setup or login"
if [ -f /var/lib/cwnsb/setup-code ]; then
  expect "setup checks (public before setup)" "$(req GET /setup/checks)" 200 '"id":"bootDevice"'
  expect "wrong setup code refused" "$(req POST /setup/admin '{"setupCode":"AAAA-AAAA","username":"x","password":"xxxxxxxxxxxx"}')" 403 'setup.invalid_code'
  code=$(cat /var/lib/cwnsb/setup-code)
  expect "first admin created" "$(req POST /setup/admin "{\"setupCode\":\"$code\",\"username\":\"$LAB_USER\",\"password\":\"$LAB_PASSWORD\",\"language\":\"fr\"}")" 201 '"username":"admin"'
  [ ! -f /var/lib/cwnsb/setup-code ] && pass "setup code file removed" || fail "setup code file removed"
else
  expect "login" "$(req POST /auth/login "{\"username\":\"$LAB_USER\",\"password\":\"$LAB_PASSWORD\"}")" 200 '"username":"admin"'
fi
expect "setup checks closed after setup" "$(req GET /setup/checks)" 403 'setup.already_done'

echo "== Security"
# Double submit: an attacker cannot read the cookie, so the header won't match it.
put_with() { # put_with TOKEN [ORIGIN] -> HTTP status
  local extra=()
  if [ -n "${2:-}" ]; then extra=(-H "Origin: $2"); fi
  curl -sk -o /dev/null -w '%{http_code}' -b "$jar" -X PUT -H "X-CSRF-Token: $1" "${extra[@]}" \
    -H 'Content-Type: application/json' --data '{"currentStep":"disks"}' "$BASE/onboarding"
}
[ "$(put_with forged)" = 403 ] && pass "CSRF header not matching cookie refused" || fail "CSRF header not matching cookie refused"
[ "$(put_with "$(csrf)" https://evil.example)" = 403 ] && pass "cross-origin write refused" || fail "cross-origin write refused"
hdrs=$(curl -sk -D - -o /dev/null "https://127.0.0.1:8443/")
grep -qi "content-security-policy: default-src 'self'" <<<"$hdrs" && pass "CSP header" || fail "CSP header"

echo "== System"
expect "system info" "$(req GET /system/info)" 200 '"hostname":"cwnsb-lab"'
expect "system checks" "$(req GET /system/checks)" 200 '"id":"memory"'
expect "metrics" "$(req GET /system/metrics)" 200 '"history":'
expect "timezones" "$(req GET /system/timezones)" 200 'Europe/Paris'
expect "identity via helper (hostnamectl + timedatectl)" "$(req PUT /system/identity '{"hostname":"cwnsb-lab","timezone":"Europe/Paris"}')" 200 '"timezone":"Europe/Paris"'
expect "invalid hostname refused" "$(req PUT /system/identity '{"hostname":"Bad Name","timezone":"Europe/Paris"}')" 400 'validation.hostname'
events=$(curl -sk -N --max-time 3 -b "$jar" "$BASE/events" 2>/dev/null)
grep -q '^event: metrics' <<<"$events" && pass "SSE metrics stream" || fail "SSE metrics stream"

echo "== Storage"
disks=$(req GET /storage/disks)
expect "disks listed" "$disks" 200 '"disks":'
for l in "${loops[@]}"; do
  id="lab-${l#/dev/}"
  grep -q "\"id\":\"$id\"[^}]*\"role\":\"available\"" <<<"${disks#* }" && pass "$id available" || fail "$id available"
done
grep -q '"role":"protected"' <<<"${disks#* }" && pass "non-lab disks protected" || fail "non-lab disks protected"
if grep -q '"role":"available"' <<<"$(sed 's/},{/}\n{/g' <<<"${disks#* }" | grep -v '"transport":"loop"')"; then
  fail "SAFETY: a non-loop disk is available"
else
  pass "no non-loop disk available"
fi
expect "SMART on lab loop" "$(req GET "/storage/disks/lab-${loops[0]#/dev/}/smart")" 200 '"reason":"not_supported"'
wwn=$(grep -o '"id":"wwn-[^"]*"' <<<"${disks#* }" | head -1 | cut -d'"' -f4)
if [ -n "$wwn" ]; then
  expect "SMART via root helper on $wwn" "$(req GET "/storage/disks/$wwn/smart")" 200 '"available":'
fi
expect "unknown disk" "$(req GET /storage/disks/wwn-0xdeadbeef/smart)" 404 'storage.disk_not_found'

echo "== Pools (real partitioning, Btrfs RAID1 and systemd mount on lab loops)"
pool_loops=() # left in place on purpose: tools/lab/reset.sh cleans pools up
for i in 1 2; do
  img="/var/lib/cwnsb-lab/smoke-pool-$RANDOM-$i.img"
  truncate -s 1G "$img"
  pool_loops+=("$(losetup --find --show --partscan "$img")")
done
udevadm settle
pa="lab-${pool_loops[0]#/dev/}" pb="lab-${pool_loops[1]#/dev/}"
pool="smoke$RANDOM"
expect "pool plan (no side effect)" "$(req POST /storage/pools/plan "{\"layout\":\"mirror\",\"disks\":[\"$pa\",\"$pb\"]}")" 200 '"usableBytes":'
expect "parity refused with 2 disks" "$(req POST /storage/pools/plan "{\"layout\":\"parity1\",\"disks\":[\"$pa\",\"$pb\"]}")" 400 'validation.disk_count'
expect "wrong confirmation refused" "$(req POST /storage/pools "{\"name\":\"$pool\",\"layout\":\"mirror\",\"disks\":[\"$pa\",\"$pb\"],\"confirm\":\"nope\"}")" 400 'validation.confirm'
expect "mirror pool created and mounted" "$(req POST /storage/pools "{\"name\":\"$pool\",\"layout\":\"mirror\",\"disks\":[\"$pa\",\"$pb\"],\"confirm\":\"$pool\"}")" 201 '"mounted":true'
if echo "CrossWax NasBuster" > "/srv/cwnsb/$pool/hello.txt" && [ "$(cat "/srv/cwnsb/$pool/hello.txt")" = "CrossWax NasBuster" ]; then
  pass "data written to the pool"
else
  fail "data written to the pool"
fi
expect "pool status healthy" "$(req GET "/storage/pools/$pool")" 200 '"health":"ok"'
expect "scrub started" "$(req POST "/storage/pools/$pool/scrub")" 202 '"started":true'
expect "members cannot be reused" "$(req POST /storage/pools "{\"name\":\"other$RANDOM\",\"layout\":\"mirror\",\"disks\":[\"$pa\",\"$pb\"],\"confirm\":\"x\"}")" 400 'validation.confirm'
other="other$RANDOM"
expect "members cannot be reused (confirmed)" "$(req POST /storage/pools "{\"name\":\"$other\",\"layout\":\"mirror\",\"disks\":[\"$pa\",\"$pb\"],\"confirm\":\"$other\"}")" 409 'storage.disk_not_available'

echo "== Users and SMB shares (real accounts, real Samba, real smbclient)"
suffix=$RANDOM
alice="sma$suffix" bob="smb$suffix" share="smoke$suffix"
pwa="alice long password" pwb="bob long password"
expect "NAS user (rw) created" "$(req POST /users "{\"name\":\"$alice\",\"password\":\"$pwa\"}")" 201 "\"name\":\"$alice\""
expect "NAS user (ro) created" "$(req POST /users "{\"name\":\"$bob\",\"password\":\"$pwb\"}")" 201 "\"name\":\"$bob\""
expect "system account refused" "$(req POST /users '{"name":"root","password":"long enough pw"}')" 400 'validation.user_name'
expect "share created on the pool" "$(req POST /shares "{\"name\":\"$share\",\"pool\":\"$pool\",\"access\":[{\"user\":\"$alice\",\"mode\":\"rw\"},{\"user\":\"$bob\",\"mode\":\"ro\"}]}")" 201 "\"path\":\"/srv/cwnsb/$pool/$share\""
sleep 1
payload="$work/payload.txt"
echo "CrossWax NasBuster over SMB3" > "$payload"
smb() { smbclient "//127.0.0.1/$share" -U "$1%$2" -m SMB3 -c "$3" >"$work/smb.out" 2>&1; }
smb "$alice" "$pwa" "put $payload hello.txt" && pass "rw user writes over SMB3" || fail "rw user writes over SMB3: $(tail -1 "$work/smb.out")"
smb "$bob" "$pwb" "get hello.txt $work/got.txt" && pass "ro user reads over SMB3" || fail "ro user reads over SMB3: $(tail -1 "$work/smb.out")"
if smb "$bob" "$pwb" "put $payload nope.txt" || ! grep -q ACCESS_DENIED "$work/smb.out"; then fail "ro user cannot write"; else pass "ro user cannot write"; fi
smb "$alice" "wrong password" "ls" && fail "wrong password refused" || pass "wrong password refused"
expect "recycle bin turned on" "$(req PUT "/shares/$share/recycle" '{"enabled":true,"days":30}')" 200 '"recycle":true'
smb "$alice" "$pwa" "put $payload trash-me.txt" && smb "$alice" "$pwa" "del trash-me.txt" \
  && [ -f "/srv/cwnsb/$pool/$share/#recycle/trash-me.txt" ] && pass "deleted over SMB, kept in #recycle" || fail "deleted over SMB, kept in #recycle: $(ls -la "/srv/cwnsb/$pool/$share/" 2>&1 | head -5)"
grep -q '^map to guest = never\|map to guest = never' /etc/samba/smb.conf && pass "smb.conf: no guest access" || fail "smb.conf: no guest access"
expect "sessions endpoint" "$(req GET /shares/sessions)" 200 '"sessions":'
tm="tm$suffix"
expect "Time Machine share created" "$(req POST /shares "{\"name\":\"$tm\",\"pool\":\"$pool\",\"access\":[{\"user\":\"$alice\",\"mode\":\"rw\"}],\"timeMachine\":true,\"timeMachineMaxGiB\":100}")" 201 '"timeMachine":true'
sleep 2
if command -v avahi-browse >/dev/null; then
  # -k: raw service types (without it, -p prints descriptions such as "Apple TimeMachine")
  adv=$(avahi-browse -artpk -t 2>/dev/null | grep '^=' | grep '_adisk._tcp' | grep -c "adVN=$tm")
  [ "$adv" -gt 0 ] && pass "Time Machine advertised over mDNS" || fail "Time Machine advertised over mDNS"
  avahi-browse -artpk -t 2>/dev/null | grep '^=' | grep -q '_smb._tcp' && pass "SMB advertised over mDNS" || fail "SMB advertised over mDNS"
fi
if systemctl list-unit-files wsdd2.service >/dev/null 2>&1; then
  [ "$(systemctl is-active wsdd2)" = active ] && pass "wsdd2 (Windows network discovery) running" || fail "wsdd2 running"
fi
expect "user deleted (access removed first)" "$(req DELETE "/users/$bob")" 204
smb "$bob" "$pwb" "ls" && fail "deleted user locked out" || pass "deleted user locked out"

echo "== Disk failure (real degraded mount and replacement, through daemon and helper)"
fl=()
for i in 1 2 3; do
  img="/var/lib/cwnsb-lab/smoke-fail-$RANDOM-$i.img"
  truncate -s 1G "$img"
  fl+=("$(losetup --find --show --partscan "$img")")
done
udevadm settle
fa="lab-${fl[0]#/dev/}" fb="lab-${fl[1]#/dev/}" fc="lab-${fl[2]#/dev/}"
fpool="fail$RANDOM"
expect "mirror for the failure test" "$(req POST /storage/pools "{\"name\":\"$fpool\",\"layout\":\"mirror\",\"disks\":[\"$fa\",\"$fb\"],\"confirm\":\"$fpool\"}")" 201 '"mounted":true'
head -c 8M /dev/urandom > "/srv/cwnsb/$fpool/before.bin"
sum_before=$(md5sum < "/srv/cwnsb/$fpool/before.bin")
sync
systemctl stop "srv-cwnsb-$fpool.mount"
losetup -d "${fl[1]}"          # the USB disk is gone at the next boot
btrfs device scan --forget >/dev/null 2>&1
udevadm settle
expect "missing disk reported" "$(req GET "/storage/pools/$fpool")" 200 '"canMountDegraded":true'
expect "degraded mount needs the name" "$(req POST "/storage/pools/$fpool/mount-degraded" '{"confirm":"nope"}')" 400 'validation.confirm'
expect "degraded mount" "$(req POST "/storage/pools/$fpool/mount-degraded" "{\"confirm\":\"$fpool\"}")" 200 '"mountedDegraded":true'
head -c 4M /dev/urandom > "/srv/cwnsb/$fpool/during.bin" && sync
sum_during=$(md5sum < "/srv/cwnsb/$fpool/during.bin")
expect "restore refused while a disk is missing" "$(req POST "/storage/pools/$fpool/restore-redundancy")" 409 'storage.disk_missing'
expect "replace started" "$(req POST "/storage/pools/$fpool/replace" "{\"old\":\"$fb\",\"new\":\"$fc\",\"confirm\":\"$fpool\"}")" 202 '"method":"btrfs-replace"'
for _ in $(seq 1 120); do
  out=$(req GET "/storage/pools/$fpool")
  grep -q '"state":"finished"' <<<"$out" && break
  sleep 0.5
done
grep -q '"state":"finished"' <<<"$out" && pass "replace finished" || fail "replace finished: ${out:0:300}"
grep -q '"mixedProfiles":true' <<<"$out" && pass "single chunks detected after replace" || fail "single chunks detected after replace"
restore=$(req POST "/storage/pools/$fpool/restore-redundancy")
case "${restore%% *}" in
  202|409) pass "redundancy restore (or already running): ${restore%% *}" ;;
  *) fail "redundancy restore: ${restore:0:200}" ;;
esac
for _ in $(seq 1 120); do
  out=$(req GET "/storage/pools/$fpool")
  grep -q '"health":"ok"' <<<"$out" && break
  sleep 0.5
done
grep -q '"health":"ok"' <<<"$out" && pass "pool healthy again" || fail "pool healthy again: ${out:0:300}"
[ "$(md5sum < "/srv/cwnsb/$fpool/before.bin")" = "$sum_before" ] && [ "$(md5sum < "/srv/cwnsb/$fpool/during.bin")" = "$sum_during" ] \
  && pass "data intact" || fail "data intact"
grep -q "\"members\":\[\"$fa\",\"$fc\"\]" <<<"$out" && pass "members recorded" || fail "members recorded: ${out:0:200}"

echo "== Alerts & notifications"
expect "alerts list" "$(req GET /alerts)" 200 '"alerts":'
expect "plain http to the internet refused" "$(req PUT /notifications '{"ntfy":{"enabled":true,"url":"http://ntfy.sh/x"},"webhook":{"enabled":false,"url":""}}')" 400 'validation.url'
expect "ntfy channel saved" "$(req PUT /notifications '{"ntfy":{"enabled":true,"url":"https://ntfy.invalid/cwnsb-smoke","token":"tk_smoke_secret"},"webhook":{"enabled":false,"url":""}}')" 200 '"tokenSet":true'
out=$(req GET /notifications)
case "$out" in *tk_smoke_secret*) fail "token never returned" ;; *) pass "token never returned" ;; esac
expect "test send reports the failure" "$(req POST /notifications/test)" 200 '"ok":false'
expect "notifications disabled again" "$(req PUT /notifications '{"ntfy":{"enabled":false,"url":"","token":""},"webhook":{"enabled":false,"url":""}}')" 200 '"tokenSet":false'

if [ "${CWNSB_SMOKE_APPS:-0}" = 1 ]; then
  echo "== Applications (real Docker through the daemon; needs Internet)"
  waitjob() { # waitjob JOB -> last body
    local out=""
    for _ in $(seq 1 600); do
      out=$(req GET "/jobs/$1")
      grep -q '"state":"\(succeeded\|failed\)"' <<<"$out" && break
      sleep 1
    done
    printf '%s' "$out"
  }
  expect "engine absent" "$(req GET /apps/engine)" 200 '"installed":false'
  expect "engine install started" "$(req POST /apps/engine/install "{\"pool\":\"$pool\"}")" 202 '"started":true'
  out=$(waitjob engine)
  grep -q '"state":"succeeded"' <<<"$out" && pass "engine installed" || fail "engine installed: ${out:0:400}"
  expect "engine active, data on the pool" "$(req GET /apps/engine)" 200 "\"pool\":\"$pool\""
  [ ! -e /var/lib/docker ] && pass "nothing written to the system disk" || fail "nothing written to the system disk"
  compose='services:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"18096:80\"\n'
  expect "broken compose refused" "$(req POST /apps "{\"name\":\"broken\",\"pool\":\"$pool\",\"compose\":\"services: [\"}")" 400 'apps.compose_invalid'
  expect "app deployed" "$(req POST /apps "{\"name\":\"smokeweb\",\"pool\":\"$pool\",\"compose\":\"$compose\"}")" 202 '"name":"smokeweb"'
  out=$(waitjob app-smokeweb)
  grep -q '"state":"succeeded"' <<<"$out" && pass "app started" || fail "app started: ${out:0:400}"
  [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18096/)" = 200 ] && pass "app answers" || fail "app answers"
  expect "app listed running" "$(req GET /apps)" 200 '"status":"running(1)"'
  expect "app stopped" "$(req POST /apps/smokeweb/stop)" 200 '"ok":true'
  expect "app logs" "$(req GET '/apps/smokeweb/logs?tail=20')" 200 '"logs":'
  expect "app removed with its data" "$(req DELETE /apps/smokeweb '{"confirm":"smokeweb","deleteData":true}')" 204
  [ ! -e "/srv/cwnsb/$pool/.apps/smokeweb" ] && pass "app data gone" || fail "app data gone"
  # No engine removal in the API yet: purge by hand so that the pool can be destroyed.
  systemctl stop docker.socket docker.service containerd.service cwnsb-job-engine.service >/dev/null 2>&1
  systemctl reset-failed cwnsb-job-engine.service >/dev/null 2>&1
  DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq docker.io docker-cli docker-compose containerd runc tini >/dev/null 2>&1
  DEBIAN_FRONTEND=noninteractive apt-get autoremove -y -qq >/dev/null 2>&1
  rm -rf /etc/docker /etc/systemd/system/docker.service.d /var/lib/containerd
  systemctl daemon-reload
  btrfs subvolume delete "/srv/cwnsb/$pool/.docker" >/dev/null 2>&1
  rmdir "/srv/cwnsb/$pool/.apps" 2>/dev/null
fi

echo "== Configuration backup and restore (through the daemon and its restart by systemd)"
expect "configuration backed up on the pools" "$(req POST /system/config-backup)" 201 '"pools":\['
backups=$(req GET /system/config-backups)
expect "backups listed" "$backups" 200 '"backups":\[{'
bname=$(grep -o '"name":"[0-9T-]*Z"' <<<"${backups#* }" | head -1 | cut -d'"' -f4)
bpool=$(grep -o '"pool":"[a-z0-9-]*"' <<<"${backups#* }" | head -1 | cut -d'"' -f4)
[ -f "/srv/cwnsb/$bpool/.cwnsb/backup/$bname/config.db" ] && pass "backup on the pool" || fail "backup on the pool"
expect "restore needs confirmation" "$(req POST /system/config-restore "{\"pool\":\"$bpool\",\"name\":\"$bname\",\"confirm\":\"no\"}")" 400 'validation.confirm'
restarts=$(systemctl show nasbusterd -p NRestarts --value)
expect "configuration restored" "$(req POST /system/config-restore "{\"pool\":\"$bpool\",\"name\":\"$bname\",\"confirm\":\"restore\"}")" 202 '"restart":true'
sleep 2
for _ in $(seq 1 30); do
  [ "$(systemctl show nasbusterd -p NRestarts --value)" != "$restarts" ] && [ "$(curl -sk -o /dev/null -w '%{http_code}' "$BASE/health")" = 200 ] && break
  sleep 1
done
[ "$(systemctl show nasbusterd -p NRestarts --value)" != "$restarts" ] && pass "daemon restarted by systemd" || fail "daemon restarted by systemd"
ls /var/lib/cwnsb/cwnsb.db.before-restore-* >/dev/null 2>&1 && pass "previous database kept" || fail "previous database kept"
out=$(req GET /system/info)
[ "${out%% *}" = 401 ] && pass "sessions closed by the restore" || fail "sessions closed by the restore (HTTP ${out%% *})"
req GET /bootstrap >/dev/null
expect "login again" "$(req POST /auth/login "{\"username\":\"$LAB_USER\",\"password\":\"$LAB_PASSWORD\"}")" 200 '"username":"admin"'
expect "restored configuration in use" "$(req GET /users)" 200 "\"name\":\"$alice\""

echo "== Lifecycle (delete a share and its data, destroy pools)"
expect "pool with a share cannot be destroyed" "$(req DELETE "/storage/pools/$pool" "{\"confirm\":\"$pool\"}")" 409 'storage.pool_has_shares'
expect "share deleted with its data" "$(req DELETE "/shares/$share" "{\"confirm\":\"$share\"}")" 204
expect "Time Machine share deleted" "$(req DELETE "/shares/$tm" "{\"confirm\":\"$tm\"}")" 204
[ ! -e "/srv/cwnsb/$pool/$share" ] && pass "share subvolume gone" || fail "share subvolume gone"
getent group "cwnsb-share-$share" >/dev/null && fail "share group gone" || pass "share group gone"
grep -q "^\[$share\]" /etc/samba/smb.conf && fail "share out of smb.conf" || pass "share out of smb.conf"
rm -f "/srv/cwnsb/$pool/hello.txt"
expect "empty pool destroyed" "$(req DELETE "/storage/pools/$pool" "{\"confirm\":\"$pool\"}")" 200 '"destroyed":true'
expect "non-empty pool refused" "$(req DELETE "/storage/pools/$fpool" "{\"confirm\":\"$fpool\"}")" 409 'storage.pool_not_empty'
rm -f "/srv/cwnsb/$fpool/before.bin" "/srv/cwnsb/$fpool/during.bin"
expect "failure-test pool destroyed (emptied)" "$(req DELETE "/storage/pools/$fpool" "{\"confirm\":\"$fpool\"}")" 200 '"destroyed":true'
disks=$(req GET /storage/disks)
for id in "$pa" "$pb" "$fa" "$fc"; do
  grep -q "\"id\":\"$id\"[^}]*\"role\":\"available\"" <<<"${disks#* }" && pass "$id available again" || fail "$id available again"
done
[ ! -e "/etc/systemd/system/srv-cwnsb-$pool.mount" ] && pass "mount unit removed" || fail "mount unit removed"

echo "== Onboarding & audit"
expect "onboarding saved" "$(req PUT /onboarding '{"completed":false,"currentStep":"disks","completedSteps":["welcome","health","account","identity"]}')" 200 '"currentStep":"disks"'
expect "audit log" "$(req GET '/audit?limit=200')" 200 '"action":"system.identity"'

echo "== Memory (RSS)"
for p in nasbusterd nbhelper; do
  rss=$(ps -C "$p" -o rss= | awk '{ s += $1 } END { printf "%.1f", s / 1024 }')
  echo "  $p: ${rss} MB"
done

echo
if [ "$fails" -eq 0 ]; then echo "ALL PASSED"; else echo "$fails FAILED"; exit 1; fi
