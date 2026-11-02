# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — build on Linux (CI, lab). On Windows use scripts/build.ps1.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X github.com/crosswax/nasbuster/internal/buildinfo.Version=$(VERSION)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)'

.PHONY: all web build-arm64 build-amd64 package-arm64 package-amd64 test test-integration vet vuln clean

all: web build-arm64

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build-arm64:
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/linux-arm64/ ./cmd/nasbusterd ./cmd/nbhelper

build-amd64:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/linux-amd64/ ./cmd/nasbusterd ./cmd/nbhelper

DEB_VERSION ?= 0.1.0~dev
NFPM := go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0

package-arm64: build-arm64
	mkdir -p dist bin/pkg && cp bin/linux-arm64/nasbusterd bin/linux-arm64/nbhelper bin/pkg/ && CWNSB_ARCH=arm64 CWNSB_VERSION=$(DEB_VERSION) $(NFPM) package -f packaging/nfpm.yaml -p deb -t dist/

package-amd64: build-amd64
	mkdir -p dist bin/pkg && cp bin/linux-amd64/nasbusterd bin/linux-amd64/nbhelper bin/pkg/ && CWNSB_ARCH=amd64 CWNSB_VERSION=$(DEB_VERSION) $(NFPM) package -f packaging/nfpm.yaml -p deb -t dist/

test:
	go test ./...

# Real kernel tests (loop devices, Btrfs, mdadm). Root, lab or CI runner only.
test-integration:
	go test -c -tags integration -o bin/storage.integration.test ./internal/storage/
	cd internal/storage && sudo env CWNSB_LAB=1 ../../bin/storage.integration.test -test.v

vet:
	go vet ./...
	gofmt -l . | (! grep .)

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

clean:
	rm -rf bin
