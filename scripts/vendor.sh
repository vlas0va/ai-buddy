#!/usr/bin/env bash
# Resolve Go dependencies and vendor them (run on a machine WITH network, e.g. Git Bash on Windows).
# vendor/ is committed: Docker builds and the jury's machines then need no access to the Go module proxy.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GOTOOLCHAIN=local GOFLAGS=-mod=mod
TARGET_GO=1.24   # Dockerfile, CI and the cloud workspace build with Go 1.24
go mod tidy
# A newer dependency (often an indirect test-only one) may raise the go directive.
# `go get go@1.24` downgrades exactly those modules to versions that still support Go 1.24.
go get "go@${TARGET_GO}.0" toolchain@none
go mod tidy
go mod vendor
go_line="$(grep -E '^go [0-9]' go.mod | awk '{print $2}')"
echo "go directive: $go_line ($(go version | awk '{print $3}') used)"
case "$go_line" in
  1.24|1.24.*) echo "OK: compatible with Go 1.24 toolchains (Dockerfile, CI)";;
  *) echo "WARNING: go directive is $go_line, expected ${TARGET_GO}.x — send this output to Claude"; exit 1;;
esac
echo "vendored modules: $(grep -c '^# ' vendor/modules.txt)"
