#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — check a signed APT repository site with the real apt.
#
#   tools/apt/check-repo.sh <site-dir> <trusted-keyring.asc> [expected-version]
#
# Run as root in a throwaway debian:trixie container (apt verifies with sqv
# there, as on a Raspberry Pi OS 13). Never on a real system: it rewrites the
# apt sources. The trusted keyring must come from the repository checkout
# (packaging/apt/), never from the site being checked.
#
# Checks: the signature is accepted, the package is downloadable with the
# expected version and installable (simulated), and apt refuses the repository
# when an index is modified or when the signature is missing.
set -euo pipefail

fail() { echo "FAIL: $*" >&2; exit 1; }

use_repo() { # <dir>
	rm -f /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/cwnsb.sources
	cat >/etc/apt/sources.list.d/cwnsb.sources <<EOF
Types: deb
URIs: file://$1
Suites: stable
Components: main
Signed-By: /usr/share/keyrings/cwnsb-check.asc
EOF
	rm -rf /var/lib/apt/lists/*
}

main() {
	local site keyring expected
	site=$(cd "$1" && pwd)
	keyring=$2
	expected=${3:-}
	[ "$(id -u)" = 0 ] || fail "run as root, in a throwaway container"
	[ -f /.dockerenv ] || [ -n "${CWNSB_CHECK_ANYWAY:-}" ] || fail "not in a container: refusing to rewrite apt sources"
	[ -f "$site/dists/stable/InRelease" ] || fail "no InRelease: the repository is not signed"

	install -m 0644 "$keyring" /usr/share/keyrings/cwnsb-check.asc
	# Debian's own sources stay: samba & co. must resolve for the simulated install.
	use_repo "$site"
	apt-get update --error-on=any >/tmp/update.log 2>&1 || { cat /tmp/update.log; fail "apt-get update refused the repository"; }
	echo "ok: signature accepted by apt ($(apt-get --version | head -1))"

	local candidate
	candidate=$(apt-cache policy cwnsb | awk '/Candidate:/ {print $2}')
	[ -n "$candidate" ] && [ "$candidate" != "(none)" ] || fail "cwnsb not found in the repository"
	[ -z "$expected" ] || [ "$candidate" = "$expected" ] || fail "candidate is $candidate, expected $expected"
	echo "ok: candidate version $candidate"

	(cd /tmp && apt-get download -q "cwnsb=$candidate" >/dev/null) || fail "download failed"
	echo "ok: package downloaded and its hash matched the index"
	apt-get install -s -q cwnsb >/tmp/install.log 2>&1 || { cat /tmp/install.log; fail "simulated install failed"; }
	echo "ok: install simulation resolves every dependency"

	# Negative checks, on copies of the site.
	local bad=/tmp/cwnsb-tampered
	rm -rf "$bad" && cp -r "$site" "$bad"
	# Both forms: apt may fetch either Packages or Packages.gz.
	printf 'Package: evil\n' >>"$bad/dists/stable/main/binary-amd64/Packages"
	gzip -9 -n -f -k "$bad/dists/stable/main/binary-amd64/Packages"
	use_repo "$bad"
	if apt-get update --error-on=any >/tmp/bad.log 2>&1; then fail "a modified Packages index was accepted"; fi
	echo "ok: a modified index is refused"

	rm -rf "$bad" && cp -r "$site" "$bad"
	rm -f "$bad/dists/stable/InRelease" "$bad/dists/stable/Release.gpg"
	use_repo "$bad"
	if apt-get update --error-on=any >/tmp/bad.log 2>&1; then fail "an unsigned repository was accepted"; fi
	echo "ok: an unsigned repository is refused"

	rm -rf "$bad"
	use_repo "$site"
	echo "repository OK"
}

main "$@"
exit 0
