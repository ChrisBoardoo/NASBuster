#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — what Docker from Debian 13 really does on a Btrfs pool
# (ADR-0013): packages, storage driver, data-root on the pool, Compose JSON
# outputs, memory. Lab only; purges Docker and cleans up afterwards.
#   wsl -d cwnsb-lab -u root -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-docker.sh
set -uo pipefail
export COLUMNS=512 LINES=50
[ "$(hostname)" = "cwnsb-lab" ] && [ "$(id -u)" -eq 0 ] || { echo "lab only" >&2; exit 1; }

dir=/var/lib/cwnsb-lab/probe-docker
mnt=/mnt/cwnsb-probe-docker
mkdir -p "$dir" "$mnt"
loops=()
cleanup() {
  docker compose -p cwnsbprobe -f "$dir/compose.yaml" down -v >/dev/null 2>&1
  docker rm -f probe-web >/dev/null 2>&1
  systemctl stop docker.socket docker.service containerd.service >/dev/null 2>&1
  umount "$mnt" 2>/dev/null
  for l in "${loops[@]}"; do losetup -d "$l" 2>/dev/null; done
  DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq docker.io docker-cli docker-compose containerd runc tini >/dev/null 2>&1
  DEBIAN_FRONTEND=noninteractive apt-get autoremove -y -qq >/dev/null 2>&1
  rm -rf /etc/docker /var/lib/docker /var/lib/containerd "$dir" "$mnt"
}
trap cleanup EXIT
run() { echo "\$ $*"; "$@"; echo "[exit $?]"; }
rss() { ps -C "$1" -o rss= | awk '{ s += $1 } END { printf "%.1f MB", s / 1024 }'; }

echo "================ install (Debian packages, no recommends) ================"
mem_before=$(free -m | awk '/^Mem:/ { print $3 }')
time DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends docker.io docker-cli docker-compose >/dev/null 2>&1
echo "install exit $?"
dpkg-query -W -f '${Package} ${Version}\n' docker.io docker-cli docker-compose containerd runc
run docker compose version
ls /usr/libexec/docker/cli-plugins/ 2>&1 | sed 's/^/cli-plugins: /'
systemctl is-enabled docker.service docker.socket containerd.service 2>&1 | paste -sd' ' | sed 's/^/enabled: /'

echo "================ Btrfs mirror as data-root ================"
for i in 1 2; do
  truncate -s 4G "$dir/d$i.img"
  LOOP=$(losetup --find --show "$dir/d$i.img"); loops+=("$LOOP")
done
mkfs.btrfs -q -d raid1 -m raid1 "${loops[@]}"
mount -o noatime,compress=zstd:1 "${loops[0]}" "$mnt"
btrfs subvolume create "$mnt/.docker" >/dev/null
systemctl stop docker.socket docker.service >/dev/null 2>&1
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<JSON
{ "data-root": "$mnt/.docker", "log-driver": "local", "log-opts": { "max-size": "10m", "max-file": "3" } }
JSON
run systemctl start docker.service
run docker info --format '{{json .Driver}} {{json .DockerRootDir}} {{json .CgroupDriver}} {{json .CgroupVersion}} {{json .LoggingDriver}} {{json .SecurityOptions}}'
echo "dockerd: $(rss dockerd), containerd: $(rss containerd)"

echo "================ a container on the pool ================"
run docker run -d --name probe-web -p 18080:80 nginx:alpine
sleep 2
curl -s -o /dev/null -w 'http %{http_code}\n' http://127.0.0.1:18080/
run docker ps --format json
echo "btrfs subvolumes under data-root: $(btrfs subvolume list -o "$mnt/.docker" 2>/dev/null | wc -l)"
du -sh "$mnt/.docker" 2>/dev/null
echo "dockerd: $(rss dockerd), containerd: $(rss containerd), shims: $(rss containerd-shim-runc-v2)"
docker rm -f probe-web >/dev/null

echo "================ compose ================"
mkdir -p "$dir/data"
cat > "$dir/compose.yaml" <<YAML
services:
  web:
    image: nginx:alpine
    ports: ["18081:80"]
    volumes: ["$dir/data:/usr/share/nginx/html:ro"]
    restart: unless-stopped
YAML
echo "hello from a compose app" > "$dir/data/index.html"
run docker compose -p cwnsbprobe -f "$dir/compose.yaml" up -d
sleep 2
curl -s http://127.0.0.1:18081/
run docker compose ls --all --format json
run docker compose -p cwnsbprobe ps --all --format json
run docker compose -p cwnsbprobe -f "$dir/compose.yaml" config --quiet
echo "services: [x" > "$dir/bad.yaml"
run docker compose -p bad -f "$dir/bad.yaml" config --quiet
run docker compose -p cwnsbprobe -f "$dir/compose.yaml" down

echo "================ memory, engine idle ================"
echo "dockerd: $(rss dockerd), containerd: $(rss containerd)"
mem_after=$(free -m | awk '/^Mem:/ { print $3 }')
echo "system used: ${mem_before} MB -> ${mem_after} MB"
echo "PROBE DONE"
exit 0
