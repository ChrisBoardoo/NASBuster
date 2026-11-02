# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — test the Debian package on a FRESH Debian 13 (WSL2),
# like a brand-new Raspberry Pi OS would see it. The distro is disposable:
# created, used, then unregistered (pass -Keep to inspect it afterwards).
#
#   .\scripts\build.ps1 -Arch amd64 -Package
#   .\tools\lab\pkgtest.ps1
[CmdletBinding()]
param([switch]$Keep)
$ErrorActionPreference = 'Stop'
$name = 'cwnsb-pkgtest'
$tar = Join-Path $env:TEMP 'cwnsb-pkgtest-rootfs.tar'
$dest = Join-Path $env:LOCALAPPDATA $name

if (((wsl -l -q) -replace "`0", '') -contains $name) { wsl --unregister $name | Out-Null }
docker create --name cwnsb-pkgtest-rootfs debian:trixie-slim | Out-Null
docker export cwnsb-pkgtest-rootfs -o $tar
docker rm cwnsb-pkgtest-rootfs | Out-Null
New-Item -ItemType Directory -Force $dest | Out-Null
wsl --import $name $dest $tar --version 2 | Out-Null
Remove-Item $tar

# The container image has no init system; a real Raspberry Pi OS boots systemd.
wsl -d $name -u root -- apt-get update -qq
wsl -d $name -u root -- apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus
# systemd as PID 1, own hostname (the in-distro script checks it). Copied from
# a file: piping text from PowerShell into wsl is not reliable.
wsl -d $name -u root -- cp /mnt/h/Work/repos/NASBuster/tools/lab/pkgtest-wsl.conf /etc/wsl.conf
wsl --terminate $name
try {
    wsl -d $name -- bash /mnt/h/Work/repos/NASBuster/tools/lab/pkgtest-inside.sh
    $code = $LASTEXITCODE
} finally {
    if (-not $Keep) { wsl --unregister $name | Out-Null }
}
exit $code
