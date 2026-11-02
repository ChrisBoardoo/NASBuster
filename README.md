<p align="center">
  <img src="logo/transparent/nasbuster-256.png" width="128" alt="NasBuster logo">
</p>

# CrossWax NasBuster

> A lightweight, modern NAS for Raspberry Pi: real RAID, snapshots, Docker, guided setup.

**English** · [Français](README_FR.md)

**Status: public beta (v0.1).** Test it on a Pi that does not hold your only copy of anything.

NasBuster sits between OpenMediaVault and TrueNAS:

- **It installs on top of a standard OS** (Raspberry Pi OS Lite 64-bit, Debian), like OpenMediaVault. No locked-down appliance.
- **Data integrity like TrueNAS**: checksums, scheduled scrubs, self-healing mirrors.
- **Tiny footprint**: one Go service of about 20 MB of RAM, no Python, PHP or Node on the Pi.
- **Step-by-step setup** from a browser, in English or French.

## Features

- **Storage**: Btrfs single disk, mirror and RAID10; parity with mdadm RAID5/6 under Btrfs.
- **Disk failures**: degraded mount, guided replacement, automatic return to full redundancy.
- **SD card loss**: existing pools are imported after a reinstall, and the configuration is backed up on every pool and restored from there.
- **Shares**: SMB with per-user rights, hourly snapshots exposed as Windows *Previous Versions*, Time Machine, recycle bin, network discovery.
- **Import**: copy data from an old NAS disk or a USB drive, read-only.
- **Apps**: Docker installed on demand with its data on a pool, Compose apps, a small catalog (Portainer CE, Uptime Kuma).
- **Alerts**: pool health, power, temperature and SMART, sent once per change by ntfy or webhook.
- **HTTPS** with a self-signed certificate whose fingerprint is shown.

## Requirements

- Raspberry Pi 4 or 5 with a 64-bit OS: Raspberry Pi OS Lite 64-bit or Debian 12 or 13 (tested on Debian 13 "Trixie"). amd64 machines work too.
- Data disks over USB or NVMe. The SD card only holds the system. On a Pi 4, put USB disks behind a powered hub.
- Port 443 free: NasBuster serves its web interface there.

## Recommended hardware

A quiet, compact two-disk mirror (1 TB usable). The links go to Amazon France.

| Item | Suggested model | What to look for |
|---|---|---|
| Raspberry Pi 5, 8 GB | [Raspberry Pi 5, 8 GB](https://link.amazon/B0j62lUhG) | 4 GB is enough for NasBuster alone; 8 GB leaves room for Docker apps. |
| Official 27 W power supply (USB-C) | [Raspberry Pi 27 W USB-C](https://link.amazon/B02qCPOOx) | It announces 5 A over USB-PD; with a weaker one, the Pi limits its USB ports, and two SSDs may run short of power. |
| Dual M.2 NVMe HAT for the Pi 5 | [Geekworm X1004](https://link.amazon/B02NGxn3v) | Two slots on the Pi's single PCIe lane, shared by both disks (about 400 MB/s in total, more than gigabit Ethernet). Check that your SSD is on the HAT maker's compatibility list. |
| 2 × NVMe SSD, 1 TB | [Lexar NM610 Pro, 1 TB](https://link.amazon/B0aGh7h9u) (× 2) | Two identical disks for a Btrfs mirror: if one dies, nothing is lost. |
| Case with a fan | [ElectroCookie aluminium case](https://link.amazon/B00yQkwFB) | Keep active cooling: a hot Pi 5 slows down. Check that the case fits the Pi 5 with the HAT on. |
| microSD card, 32 GB | [Lexar microSDHC, 32 GB](https://link.amazon/B03NvZonO) | Holds the system only; your data never goes on it. A known brand matters more than speed. |

*These are Amazon affiliate links: as an Amazon Associate, the project earns a small commission on qualifying purchases, at no extra cost to you. It helps pay for test hardware.*

## Install

Packages come from a signed APT repository: <https://chrisboardoo.github.io/NASBuster/>.

**1. Install the system**

Write Raspberry Pi OS Lite (64-bit) on the microSD card with [Raspberry Pi Imager](https://www.raspberrypi.com/software/). In its settings, set a user and a password, and turn on SSH. Start the Pi, then connect to it over SSH.

**2. Get the signing key and check it**

```bash
sudo curl -fsSL -o /usr/share/keyrings/nasbuster-archive-keyring.asc https://chrisboardoo.github.io/NASBuster/nasbuster-archive-keyring.asc
gpg --show-keys /usr/share/keyrings/nasbuster-archive-keyring.asc
```

The fingerprint must read exactly:

```text
6E4A B0CB FCC4 E45D EC76  A4B4 5282 C4BC 9717 0E72
```

If it differs, stop: someone may be serving a fake key. The package keeps this key up to date afterwards.

**3. Add the repository**

```bash
sudo tee /etc/apt/sources.list.d/nasbuster.sources >/dev/null <<'EOF'
Types: deb
URIs: https://chrisboardoo.github.io/NASBuster
Suites: stable
Components: main
Signed-By: /usr/share/keyrings/nasbuster-archive-keyring.asc
EOF
```

**4. Install**

```bash
sudo apt update
sudo apt install nasbuster
```

**5. Set it up**

Open `https://<your-pi>/` in a browser. The certificate is self-signed, so the browser warns once. To be sure you reach your own Pi, compare the fingerprint it shows with:

```bash
sudo nasbusterd -print-tls-fingerprint
```

The setup then guides you: you prove you own the machine with the Pi's own login account (a member of the `sudo` group), create the NAS administrator, then a pool, a user and a first share.

## Update

```bash
sudo apt update && sudo apt upgrade
```

## Uninstall

```bash
sudo apt remove nasbuster    # stops NasBuster, keeps everything
sudo apt purge nasbuster     # also deletes its settings and web accounts
```

Neither command touches your pools, shares, their data or the NAS user accounts.

## Build from source

Go and pnpm are needed. On Linux:

```bash
make web package-arm64    # dist/nasbuster_<version>_arm64.deb
make test
```

Every release tag in this repository holds the exact source of the packages published with it.

## Support the project

NasBuster is free, with nothing locked. If it is useful to you, you can buy the project a coffee on [Ko-fi](https://ko-fi.com/crosswax): it pays for test hardware.

## License

[GNU Affero General Public License v3.0](LICENSE) or later.

The NasBuster logo (the ghost) is not covered by the AGPL: it has its own license, in [logo/LICENSE.txt](logo/LICENSE.txt). In short, use it freely to talk about NasBuster, but a fork needs its own logo.
