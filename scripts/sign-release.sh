#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — sign the APT repository of a release, on the PC (ADR-0024).
#
# GitHub builds the packages and the unsigned repository (release workflow) and
# leaves them in a DRAFT release. This script, run from Git Bash on the PC:
#   1. downloads the unsigned indexes of the draft (apt-repo.tar.gz);
#   2. checks that Release, Packages and the pool manifest agree;
#   3. shows what is about to be signed and asks for confirmation;
#   4. signs Release (InRelease, Release.gpg) with the signing subkey;
#   5. verifies both signatures with the public key of this checkout;
#   6. uploads them and publishes the release, which triggers the deployment
#      of the repository to GitHub Pages (publish workflow).
#
#   scripts/sign-release.sh --tag v0.1.0        # the real thing (needs gh, logged in)
#   scripts/sign-release.sh --dir <site-dir>    # sign a local site (tests), no GitHub
#
# Environment (defaults in brackets):
#   CWNSB_GNUPGHOME       keyring holding the signing subkey [~/.cwnsb-gnupg]
#   CWNSB_SIGNING_KEY     subkey fingerprint [60C9ADBAF28CD18AC687E53CA6850A6B0E1D5F94]
#   CWNSB_TRUSTED_KEYRING public key to verify with [packaging/apt/nasbuster-archive-keyring.asc]
#   CWNSB_PUBLIC_REPO     GitHub repository of the releases [ChrisBoardoo/NASBuster]
#   CWNSB_PASSPHRASE_FILE read the passphrase from this file (tests only) [prompt]
#   CWNSB_YES=1           do not ask for confirmation (tests only)
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
GNUPGHOME=${CWNSB_GNUPGHOME:-$HOME/.cwnsb-gnupg}
KEY=${CWNSB_SIGNING_KEY:-60C9ADBAF28CD18AC687E53CA6850A6B0E1D5F94}
TRUSTED=${CWNSB_TRUSTED_KEYRING:-$ROOT/packaging/apt/nasbuster-archive-keyring.asc}
REPO=${CWNSB_PUBLIC_REPO:-ChrisBoardoo/NASBuster}
export GNUPGHOME

die() { echo "error: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "$1 not found${2:+ ($2)}"; }

# Every file listed under SHA256 in Release must exist with that hash and size.
check_release() { # <dists/stable>
	local d=$1 n=0 hash size name
	while read -r hash size name; do
		[ -f "$d/$name" ] || die "Release lists $name, which is missing"
		[ "$(sha256sum <"$d/$name" | cut -d' ' -f1)" = "$hash" ] || die "hash mismatch for $name"
		[ "$(wc -c <"$d/$name" | tr -d ' ')" = "$size" ] || die "size mismatch for $name"
		n=$((n + 1))
	done < <(awk '/^SHA256:/ {on=1; next} /^[^ ]/ {on=0} on {print $1, $2, $3}' "$d/Release")
	[ "$n" -gt 0 ] || die "Release lists no SHA256 entries"
	echo "  Release: $n index files, hashes and sizes match"
}

# Each package of the indexes must appear in the pool manifest with the same hash.
check_manifest() { # <site-or-extract-dir>
	local dir=$1 file hash n=0
	[ -f "$dir/pool.sha256" ] || { echo "  (no pool.sha256: local site, skipped)"; return; }
	while read -r file hash; do
		grep -q "^$hash  .* $(basename "$file")$" "$dir/pool.sha256" ||
			die "$(basename "$file") is in Packages but not in pool.sha256 with the same hash"
		n=$((n + 1))
	done < <(awk '/^Filename:/ {f=$2} /^SHA256:/ {print f, $2}' "$dir"/dists/stable/main/binary-*/Packages)
	echo "  pool.sha256: the $n packages of the indexes are in the manifest"
}

summary() { # <dir>
	local dir=$1
	echo
	[ -f "$dir/build-info.txt" ] && sed 's/^/  /' "$dir/build-info.txt"
	echo "  Packages in the repository:"
	awk '/^Package:/ {p=$2} /^Version:/ {v=$2} /^Architecture:/ {a=$2} /^SHA256:/ {printf "    %s %s %s  sha256 %s…\n", p, v, a, substr($2, 1, 16)}' \
		"$dir"/dists/stable/main/binary-*/Packages | sort -u
	echo "  Release date: $(awk -F': ' '/^Date:/ {print $2}' "$dir/dists/stable/Release")"
	echo
}

sign() { # <dists/stable>
	local d=$1 pass
	gpg --list-secret-keys --with-colons "$KEY" 2>/dev/null | grep -q '^ssb:' ||
		die "signing subkey $KEY not in $GNUPGHOME (tutorial 09: reinstall it from the password manager)"
	if [ -n "${CWNSB_PASSPHRASE_FILE:-}" ]; then
		pass=$(cat "$CWNSB_PASSPHRASE_FILE")
	else
		read -rsp "Passphrase of the signing key: " pass </dev/tty
		echo
	fi
	rm -f "$d/InRelease" "$d/Release.gpg"
	gpg --batch --quiet --pinentry-mode loopback --passphrase-fd 3 --local-user "$KEY!" \
		--digest-algo SHA512 --clearsign -o "$d/InRelease" "$d/Release" 3<<<"$pass"
	gpg --batch --quiet --pinentry-mode loopback --passphrase-fd 3 --local-user "$KEY!" \
		--digest-algo SHA512 --armor --detach-sign -o "$d/Release.gpg" "$d/Release" 3<<<"$pass"
	pass=
	echo "  signed: InRelease, Release.gpg"
}

verify() { # <dists/stable>
	local d=$1 kr
	kr=$(mktemp)
	gpg --batch --quiet --dearmor <"$TRUSTED" >"$kr"
	gpgv --keyring "$kr" "$d/InRelease" 2>/dev/null || die "InRelease does not verify with $TRUSTED"
	gpgv --keyring "$kr" "$d/Release.gpg" "$d/Release" 2>/dev/null || die "Release.gpg does not verify with $TRUSTED"
	# InRelease must carry exactly the Release that was checked.
	gpg --batch --quiet --decrypt "$d/InRelease" 2>/dev/null | cmp -s - "$d/Release" || die "InRelease content differs from Release"
	rm -f "$kr"
	echo "  verified with $(basename "$TRUSTED"): both signatures are good"
}

# Release notes: the "## X.Y.Z" section of the PRIVATE CHANGELOG.md (the file
# itself never ships, ADR-0021), then how to install. The text becomes public.
release_notes() { # <X.Y.Z>
	local section
	section=$(awk -v v="$1" '/^## / {h=$2; gsub(/[][v]/, "", h); on=(h==v); next} on' "$ROOT/CHANGELOG.md" 2>/dev/null || true)
	if [ -n "$(printf '%s' "$section" | tr -d '[:space:]')" ]; then
		printf '%s\n' "$section"
	else
		echo "NasBuster v$1."
	fi
	printf '\n---\nInstall or update with apt: https://chrisboardoo.github.io/NASBuster/\n'
}

confirm() {
	[ "${CWNSB_YES:-}" = 1 ] && return
	local answer
	read -rp "Sign this repository? [y/N] " answer </dev/tty
	[ "$answer" = y ] || [ "$answer" = Y ] || die "cancelled, nothing signed"
}

main() {
	local mode=${1:-} arg=${2:-}
	need gpg "Git Bash ships it"
	need gpgv
	need sha256sum
	case "$mode" in
	--dir)
		[ -d "$arg/dists/stable" ] || die "$arg has no dists/stable"
		echo "Local site $arg"
		check_release "$arg/dists/stable"
		check_manifest "$arg"
		summary "$arg"
		confirm
		sign "$arg/dists/stable"
		verify "$arg/dists/stable"
		;;
	--tag)
		[[ "$arg" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "--tag takes vX.Y.Z"
		need gh "winget install GitHub.cli, then gh auth login"
		local work draft
		draft=$(gh release view "$arg" -R "$REPO" --json isDraft --jq .isDraft) || die "no release $arg in $REPO"
		[ "$draft" = true ] || die "$arg is already published: nothing to sign"
		work=$(mktemp -d)
		trap 'rm -rf "$work"' EXIT
		gh release download "$arg" -R "$REPO" -p apt-repo.tar.gz -D "$work"
		tar -xzf "$work/apt-repo.tar.gz" -C "$work"
		echo "Draft release $arg of $REPO"
		check_release "$work/dists/stable"
		check_manifest "$work"
		summary "$work"
		confirm
		sign "$work/dists/stable"
		verify "$work/dists/stable"
		gh release upload "$arg" -R "$REPO" --clobber "$work/dists/stable/InRelease" "$work/dists/stable/Release.gpg"
		release_notes "${arg#v}" >"$work/notes.md"
		echo "  release notes (public):"
		sed 's/^/    | /' "$work/notes.md"
		gh release edit "$arg" -R "$REPO" --notes-file "$work/notes.md" --draft=false
		echo
		echo "Published $arg. The publish workflow now checks the repository with apt and deploys it:"
		echo "  gh run watch -R $REPO \$(gh run list -R $REPO -w publish-apt.yml -L 1 --json databaseId --jq '.[0].databaseId')"
		;;
	*)
		sed -n '3,25p' "$0" | sed 's/^# \{0,1\}//'
		exit 2
		;;
	esac
}

main "$@"
exit 0
