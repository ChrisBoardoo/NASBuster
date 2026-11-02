# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — build from Windows (PowerShell 7).
#
#   .\scripts\build.ps1                  # web UI + linux/arm64 binaries (Raspberry Pi)
#   .\scripts\build.ps1 -Arch amd64      # linux/amd64 binaries (WSL lab)
#   .\scripts\build.ps1 -SkipWeb         # reuse the existing web/dist
#   .\scripts\build.ps1 -Test            # also run the Go tests first
#   .\scripts\build.ps1 -Package         # also build the Debian package in dist\
#
# Output: bin\linux-<arch>\nasbusterd and bin\linux-<arch>\nbhelper
[CmdletBinding()]
param(
    [ValidateSet('arm64', 'amd64')]
    [string]$Arch = 'arm64',
    [switch]$SkipWeb,
    [switch]$Test,
    [switch]$Package
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

# Debian package version. It must grow with every build, so that a newer package
# always upgrades an older one. A git hash is not a version: "04dcf52" sorts
# above "0.1.0" for dpkg. The hash only follows the timestamp, as a trace.
function Get-DebVersion([string]$described) {
    if ($described -match '^v?(\d+\.\d+\.\d+)$') { return $Matches[1] } # clean release tag
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddHHmm')
    $hash = git rev-parse --short HEAD 2>$null
    $trace = if ($hash) { ".g$hash" } else { '' }
    if ($described -match '-dirty$') { $trace += '.dirty' }
    if ($described -match '^v?(\d+\.\d+\.\d+)-') { return "$($Matches[1])+dev$stamp$trace" } # after a tag
    return "0.1.0~dev$stamp$trace" # before the first release
}
Push-Location $root
try {
    if ($Test) {
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    }
    if (-not $SkipWeb) {
        Push-Location web
        try {
            pnpm install --frozen-lockfile
            if ($LASTEXITCODE -ne 0) { throw 'pnpm install failed' }
            pnpm build
            if ($LASTEXITCODE -ne 0) { throw 'pnpm build failed' }
        } finally { Pop-Location }
    }

    $version = (git describe --tags --always --dirty 2>$null)
    if (-not $version) { $version = '0.1.0-dev' }
    $out = Join-Path 'bin' "linux-$Arch"
    New-Item -ItemType Directory -Force $out | Out-Null

    $env:GOOS = 'linux'; $env:GOARCH = $Arch; $env:CGO_ENABLED = '0'
    try {
        go build -trimpath -ldflags "-s -w -X github.com/crosswax/nasbuster/internal/buildinfo.Version=$version" -o "$out/" ./cmd/nasbusterd ./cmd/nbhelper
        if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
    }
    # Out-String + Write-Host: table output must not leak into callers' pipelines.
    Get-ChildItem $out | Select-Object Name, @{ n = 'MB'; e = { [math]::Round($_.Length / 1MB, 1) } } |
        Format-Table -AutoSize | Out-String | Write-Host
    Write-Host "Built $version for linux/$Arch in $out"

    if ($Package) {
        $debVersion = Get-DebVersion $version
        New-Item -ItemType Directory -Force dist, bin\pkg | Out-Null
        Copy-Item "$out\nasbusterd", "$out\nbhelper" bin\pkg\ -Force # nfpm paths are fixed
        $env:CWNSB_ARCH = $Arch; $env:CWNSB_VERSION = $debVersion
        try {
            go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0 package -f packaging/nfpm.yaml -p deb -t dist/
            if ($LASTEXITCODE -ne 0) { throw 'nfpm failed' }
        } finally {
            Remove-Item Env:CWNSB_ARCH, Env:CWNSB_VERSION
        }
    }
} finally {
    Pop-Location
}
