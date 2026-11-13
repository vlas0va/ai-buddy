#!/usr/bin/env bash
# Разрешить зависимости Go и положить их в vendor (запускать на машине С сетью, например Git Bash в Windows).
# vendor/ коммитится: тогда сборкам Docker и машинам жюри не нужен доступ к прокси модулей Go.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GOTOOLCHAIN=local GOFLAGS=-mod=mod
TARGET_GO=1.24   # Dockerfile, CI and the cloud workspace build with Go 1.24
go mod tidy
# Более новая зависимость (часто косвенная, только для тестов) может поднять директиву go. `go get go@1.24` понижает
# именно эти модули до версий, которые ещё поддерживают Go 1.24.
go get "go@${TARGET_GO}.0" toolchain@none
go mod tidy
go mod vendor
go_line="$(grep -E '^go [0-9]' go.mod | awk '{print $2}')"
echo "go directive: $go_line ($(go version | awk '{print $3}') used)"
case "$go_line" in
  1.24|1.24.*) echo "OK: compatible with Go 1.24 toolchains (Dockerfile, CI)";;
  *) echo "WARNING: go directive is $go_line, expected ${TARGET_GO}.x — send this output to the backend owner"; exit 1;;
esac
echo "vendored modules: $(grep -c '^# ' vendor/modules.txt)"
