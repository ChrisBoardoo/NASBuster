#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — build the APT repository site from a directory of .deb files.
#
#   tools/apt/build-repo.sh <dir-with-debs> <out-site-dir> <base-url>
#
# Writes an UNSIGNED repository: the Release file is signed afterwards on the
# PC (scripts/sign-release.sh, ADR-0024), never here. Needs apt-ftparchive
# (package apt-utils): runs on the GitHub runner or in a debian:trixie container.
#
# Layout (one suite "stable", one component "main", amd64 and arm64):
#   pool/main/n/nasbuster/nasbuster_<version>_<arch>.deb
#   dists/stable/main/binary-<arch>/Packages{,.gz}
#   dists/stable/Release          (to sign: InRelease, Release.gpg)
#   nasbuster-archive-keyring.asc     (public key, a copy of packaging/apt/)
#   index.html                    (install instructions)
set -euo pipefail

main() {
	local debs=$1 out=$2 base=${3%/}
	local here root
	here=$(cd "$(dirname "$0")" && pwd)
	root=$(cd "$here/../.." && pwd)

	shopt -s nullglob
	local files=("$debs"/nasbuster_*.deb)
	((${#files[@]})) || { echo "no nasbuster_*.deb in $debs" >&2; exit 1; }

	rm -rf "$out"
	mkdir -p "$out/pool/main/n/nasbuster"
	cp "${files[@]}" "$out/pool/main/n/nasbuster/"
	cd "$out"

	local arch
	for arch in amd64 arm64; do
		mkdir -p "dists/stable/main/binary-$arch"
		apt-ftparchive --arch "$arch" packages pool >"dists/stable/main/binary-$arch/Packages"
		gzip -9 -n -k "dists/stable/main/binary-$arch/Packages"
	done

	# Written outside dists/stable first: the Release file must not list itself.
	apt-ftparchive \
		-o APT::FTPArchive::Release::Origin="CrossWax NasBuster" \
		-o APT::FTPArchive::Release::Label="NasBuster" \
		-o APT::FTPArchive::Release::Suite="stable" \
		-o APT::FTPArchive::Release::Codename="stable" \
		-o APT::FTPArchive::Release::Architectures="amd64 arm64" \
		-o APT::FTPArchive::Release::Components="main" \
		-o APT::FTPArchive::Release::Description="CrossWax NasBuster, a lightweight NAS for Raspberry Pi" \
		release dists/stable >Release.tmp
	mv Release.tmp dists/stable/Release

	cp "$root/packaging/apt/nasbuster-archive-keyring.asc" nasbuster-archive-keyring.asc
	sed "s|@BASE_URL@|$base|g" "$here/site/index.html" >index.html
	touch .nojekyll # GitHub Pages: serve files as they are

	echo "repository built in $out"
	grep -h '^Version:' dists/stable/main/binary-*/Packages | sort -u | sed 's/^/  /'
}

main "$@"
exit 0
