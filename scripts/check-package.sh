#!/bin/sh
set -eu

if [ "$#" -ne 2 ] || [ ! -d "$1" ] || [ -z "$2" ]; then
  printf '%s\n' 'usage: check-package.sh <dist-directory> <version>' >&2
  exit 2
fi

required_members='oscar-corrtest/bin/OSCAR_CORRTEST_BINARY
oscar-corrtest/README.md
oscar-corrtest/docs/operator.md
oscar-corrtest/docs/builtins.md
oscar-corrtest/docs/live-qualification.md
oscar-corrtest/docs/schema/correlation-scenario.schema.json
oscar-corrtest/packaging/oscar-corrtest.service
oscar-corrtest/Containerfile'

for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os_name=${platform%/*}
  architecture=${platform#*/}
  extension=tar.gz
  binary=oscar-corrtest
  if [ "$os_name" = windows ]; then
    extension=zip
    binary=oscar-corrtest.exe
  fi
  archive="$1/oscar-corrtest_$2_${os_name}_${architecture}.${extension}"
  if [ ! -f "$archive" ]; then
    printf 'missing package: %s\n' "$archive" >&2
    exit 1
  fi
  if [ "$os_name" = windows ]; then
    listing=$(GOWORK=off go run ./cmd/package-zip --list "$archive")
  else
    listing=$(tar -tzf "$archive")
  fi
  printf '%s\n' "$required_members" | while IFS= read -r path; do
    path=$(printf '%s\n' "$path" | sed "s|OSCAR_CORRTEST_BINARY|$binary|")
    printf '%s\n' "$listing" | grep -qx "$path"
  done
done
