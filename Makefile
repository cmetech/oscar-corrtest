SHELL := /bin/sh

BINARY := oscar-corrtest
PKG := ./cmd/oscar-corrtest
BUILD_DIR := bin
DIST_DIR := dist
TOOLS_DIR := $(CURDIR)/.tools
VERSION ?= $(shell git describe --tags --always --dirty --match='v[0-9]*' 2>/dev/null || echo 0.0.0-dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || echo unknown)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct HEAD 2>/dev/null || echo 0)
LDFLAGS := -s -w -X github.com/cmetech/oscar-corrtest/internal/version.Version=$(VERSION) -X github.com/cmetech/oscar-corrtest/internal/version.Commit=$(COMMIT) -X github.com/cmetech/oscar-corrtest/internal/version.BuildDate=$(BUILD_DATE)
GO_BUILD := GOWORK=off CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="$(LDFLAGS)"
GOSEC_VERSION := v2.28.0
GOVULNCHECK_VERSION := v1.6.0

.DEFAULT_GOAL := build

.PHONY: tools fmt-check mod-check archive-mod-check vet security test test-race plan2-gate plan3-gate plan4-gate plan5-gate plan6-gate plan7-gate container-check release-contract-check package-content-check reproducible-check installer-posix-check release-script-check release-gate live-qualification build cross package checksums standalone-check ci-core ci clean

tools:
	mkdir -p "$(TOOLS_DIR)"
	GOBIN="$(TOOLS_DIR)" go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	GOBIN="$(TOOLS_DIR)" go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

fmt-check:
	@files="$$(find . -type f -name '*.go' -not -path './.tools/*')"; \
	unformatted="$$(gofmt -l $$files)"; \
	test -z "$$unformatted" || { printf '%s\n' "$$unformatted"; exit 1; }

mod-check:
	go mod verify
	go mod tidy
	git diff --exit-code -- go.mod go.sum

archive-mod-check:
	go mod verify
	go mod tidy -diff

vet:
	go vet ./...

security:
	"$(TOOLS_DIR)/gosec" ./...
	"$(TOOLS_DIR)/govulncheck" ./...

test:
	go test -count=1 ./...

test-race:
	CGO_ENABLED=1 go test -race -count=1 ./...

plan2-gate:
	go test -count=1 ./internal/config ./internal/domain ./internal/persistence/sqlite ./internal/artifact ./internal/report ./internal/runtime ./internal/web

plan3-gate:
	go test -count=1 ./internal/compiler ./internal/oscar ./internal/runner ./internal/runtime ./internal/evidence ./internal/web

plan4-gate:
	go test -count=1 ./internal/scenario ./internal/compiler ./internal/runner

plan5-gate:
	./scripts/check-release-contract.sh

plan6-gate:
	go test -count=1 ./internal/compiler ./internal/oscar ./internal/runner -run 'Parent|Notification|Builtin'

plan7-gate:
	go test -count=1 ./internal/scenario ./internal/evidence ./internal/artifact ./internal/runtime ./internal/command ./internal/web

container-check:
	grep -q '^FROM scratch$$' Containerfile
	! grep -Eq '(^|:)latest([[:space:]]|$$)' Containerfile
	./scripts/check-release-contract.sh

release-contract-check:
	./scripts/check-release-contract.sh

build:
	mkdir -p "$(BUILD_DIR)"
	$(GO_BUILD) -o "$(BUILD_DIR)/$(BINARY)" $(PKG)

cross:
	mkdir -p "$(BUILD_DIR)/linux_amd64" "$(BUILD_DIR)/linux_arm64" "$(BUILD_DIR)/darwin_amd64" "$(BUILD_DIR)/darwin_arm64" "$(BUILD_DIR)/windows_amd64"
	GOOS=linux GOARCH=amd64 $(GO_BUILD) -o "$(BUILD_DIR)/linux_amd64/$(BINARY)" $(PKG)
	GOOS=linux GOARCH=arm64 $(GO_BUILD) -o "$(BUILD_DIR)/linux_arm64/$(BINARY)" $(PKG)
	GOOS=darwin GOARCH=amd64 $(GO_BUILD) -o "$(BUILD_DIR)/darwin_amd64/$(BINARY)" $(PKG)
	GOOS=darwin GOARCH=arm64 $(GO_BUILD) -o "$(BUILD_DIR)/darwin_arm64/$(BINARY)" $(PKG)
	GOOS=windows GOARCH=amd64 $(GO_BUILD) -o "$(BUILD_DIR)/windows_amd64/$(BINARY).exe" $(PKG)

package: cross
	mkdir -p "$(DIST_DIR)"
	./scripts/package.sh "$(VERSION)" linux amd64 "$(BUILD_DIR)/linux_amd64/$(BINARY)" "$(SOURCE_DATE_EPOCH)"
	./scripts/package.sh "$(VERSION)" linux arm64 "$(BUILD_DIR)/linux_arm64/$(BINARY)" "$(SOURCE_DATE_EPOCH)"
	./scripts/package.sh "$(VERSION)" darwin amd64 "$(BUILD_DIR)/darwin_amd64/$(BINARY)" "$(SOURCE_DATE_EPOCH)"
	./scripts/package.sh "$(VERSION)" darwin arm64 "$(BUILD_DIR)/darwin_arm64/$(BINARY)" "$(SOURCE_DATE_EPOCH)"
	./scripts/package.sh "$(VERSION)" windows amd64 "$(BUILD_DIR)/windows_amd64/$(BINARY).exe" "$(SOURCE_DATE_EPOCH)"

checksums: package
	@set -eu; \
	files='$(BINARY)_$(VERSION)_darwin_amd64.tar.gz $(BINARY)_$(VERSION)_darwin_arm64.tar.gz $(BINARY)_$(VERSION)_linux_amd64.tar.gz $(BINARY)_$(VERSION)_linux_arm64.tar.gz $(BINARY)_$(VERSION)_windows_amd64.zip'; \
	cd "$(DIST_DIR)"; \
	for file in $$files; do test -f "$$file"; done; \
	if command -v sha256sum >/dev/null 2>&1; then \
		for file in $$files; do sha256sum "$$file"; done > SHA256SUMS; \
	else \
		for file in $$files; do shasum -a 256 "$$file"; done > SHA256SUMS; \
	fi

package-content-check: package
	./scripts/check-package.sh "$(DIST_DIR)" "$(VERSION)"

reproducible-check:
	./scripts/check-reproducible.sh

installer-posix-check:
	$(MAKE) clean package checksums VERSION=v0.0.0
	sh scripts/test-install-posix.sh v0.0.0

release-script-check:
	sh scripts/test-release.sh

standalone-check:
	bash scripts/test-standalone.sh

ci-core:
	$(MAKE) fmt-check
	$(MAKE) mod-check
	$(MAKE) vet
	$(MAKE) security
	$(MAKE) plan2-gate
	$(MAKE) test
	$(MAKE) test-race
	$(MAKE) build

ci:
	$(MAKE) tools
	$(MAKE) ci-core
	$(MAKE) standalone-check
	$(MAKE) package
	$(MAKE) checksums

release-gate: ci
	$(MAKE) plan3-gate plan4-gate plan5-gate plan6-gate plan7-gate
	$(MAKE) container-check package-content-check reproducible-check

live-qualification:
	./scripts/live-qualification.sh

clean:
	if test -d "$(BUILD_DIR)"; then find "$(BUILD_DIR)" -mindepth 1 -delete; fi
	if test -d "$(DIST_DIR)"; then find "$(DIST_DIR)" -mindepth 1 -delete; fi
