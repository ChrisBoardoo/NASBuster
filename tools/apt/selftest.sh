#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — end-to-end test of the APT repository chain, on the PC.
#
#   tools/apt/selftest.sh              # from Git Bash, at the repository root, Docker running
#   tools/apt/selftest.sh --real-key   # rehearsal: sign with the REAL subkey (asks for its
#                                      # passphrase) and check with the real public key
#
# Takes the newest nasbuster_*_amd64.deb and nasbuster_*_arm64.deb of dist/ (build them
# first: scripts/build.ps1 -Arch <arch> -Package), then:
#   1. builds the repository with tools/apt/build-repo.sh in debian:trixie;
#   2. signs it with scripts/sign-release.sh and a THROWAWAY key (the real key
#      is never used by tests);
#   3. checks it with the real apt (tools/apt/check-repo.sh, debian:trixie);
#   4. checks that the real public key refuses it (signed by the wrong key).
# With --real-key, steps 2 and 3 use the real subkey and public key instead, and
# step 4 is skipped: a rehearsal of the release signature, nothing is published.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
IMAGE=debian:trixie
export MSYS_NO_PATHCONV=1 # Git Bash: keep container paths as they are

win() { cygpath -w "$1"; } # Docker Desktop wants Windows paths for volumes

main() {
	local real=
	[ "${1:-}" = --real-key ] && real=1
	cd "$ROOT"
	local amd arm
	amd=$(ls -t dist/nasbuster_*_amd64.deb 2>/dev/null | head -1)
	arm=$(ls -t dist/nasbuster_*_arm64.deb 2>/dev/null | head -1)
	[ -n "$amd" ] && [ -n "$arm" ] || { echo "build both packages first (scripts/build.ps1 -Arch amd64|arm64 -Package)" >&2; exit 1; }
	echo "packages: $(basename "$amd"), $(basename "$arm")"

	# Globals, not locals: the EXIT trap runs after main has returned.
	work=$(mktemp -d)
	gh="$HOME/.cwnsb-selftest" # short path: gpg-agent sockets are limited to about 108 characters
	trap 'GNUPGHOME="$gh" gpgconf --kill gpg-agent 2>/dev/null; rm -rf "$work" "$gh"' EXIT
	mkdir -p "$work/debs"
	cp "$amd" "$arm" "$work/debs/"

	echo "== 1. build the repository (debian:trixie)"
	docker run --rm -v "$(win "$ROOT"):/src:ro" -v "$(win "$work"):/work" "$IMAGE" bash -c '
		set -e
		apt-get update -qq && apt-get install -y -qq --no-install-recommends apt-utils >/dev/null
		bash /src/tools/apt/build-repo.sh /work/debs /work/site https://example.invalid/NasBuster
		# Pool manifest, as release.yml writes it: "<sha256>  <tag> <file>".
		cd /work/debs && for f in nasbuster_*.deb; do printf "%s  %s %s\n" "$(sha256sum < "$f" | cut -d" " -f1)" vSELFTEST "$f"; done > /work/site/pool.sha256'

	local version
	version=$(awk '/^Version:/ {print $2; exit}' "$work/site/dists/stable/main/binary-amd64/Packages")

	if [ -n "$real" ]; then
		echo "== 2. sign it with the REAL signing subkey (passphrase from the password manager)"
		bash scripts/sign-release.sh --dir "$work/site"
		echo "== 3. check it with apt (debian:trixie), trusting packaging/apt/ only"
		docker run --rm -v "$(win "$ROOT"):/src:ro" -v "$(win "$work"):/work:ro" "$IMAGE" \
			bash /src/tools/apt/check-repo.sh /work/site /src/packaging/apt/nasbuster-archive-keyring.asc "$version"
		echo
		echo "rehearsal OK: the real key signs, and apt accepts it (nothing was published)"
		return
	fi

	echo "== 2. sign it with a throwaway key"
	rm -rf "$gh" && mkdir -p "$gh" && chmod 700 "$gh"
	printf 'selftest-passphrase' >"$work/pass.txt"
	GNUPGHOME="$gh" gpg --batch --quiet --pinentry-mode loopback --passphrase-file "$work/pass.txt" \
		--quick-generate-key "NasBuster selftest (throwaway)" ed25519 cert 1d
	local fpr sub
	fpr=$(GNUPGHOME="$gh" gpg --list-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
	GNUPGHOME="$gh" gpg --batch --quiet --pinentry-mode loopback --passphrase-file "$work/pass.txt" \
		--quick-add-key "$fpr" ed25519 sign 1d
	sub=$(GNUPGHOME="$gh" gpg --list-keys --with-colons --with-subkey-fingerprints | awk -F: '/^fpr:/ {n++; if (n == 2) {print $10; exit}}')
	GNUPGHOME="$gh" gpg --armor --export "$fpr" >"$work/test-keyring.asc"
	CWNSB_GNUPGHOME="$gh" CWNSB_SIGNING_KEY="$sub" CWNSB_TRUSTED_KEYRING="$work/test-keyring.asc" \
		CWNSB_PASSPHRASE_FILE="$work/pass.txt" CWNSB_YES=1 \
		bash scripts/sign-release.sh --dir "$work/site"

	echo "== 3. check it with apt (debian:trixie), trusting the throwaway key"
	docker run --rm -v "$(win "$ROOT"):/src:ro" -v "$(win "$work"):/work:ro" "$IMAGE" \
		bash /src/tools/apt/check-repo.sh /work/site /work/test-keyring.asc "$version"

	echo "== 4. the real public key must refuse a repository signed by another key"
	if docker run --rm -v "$(win "$ROOT"):/src:ro" -v "$(win "$work"):/work:ro" "$IMAGE" \
		bash /src/tools/apt/check-repo.sh /work/site /src/packaging/apt/nasbuster-archive-keyring.asc >"$work/wrong.log" 2>&1; then
		cat "$work/wrong.log"
		echo "FAIL: a repository signed by another key was accepted" >&2
		exit 1
	fi
	grep -E '^(E|W|Err):' "$work/wrong.log" | head -3 | sed 's/^/  apt said: /' || true
	echo "ok: refused with the real key"
	echo
	echo "selftest OK"
}

main "$@"
exit 0
