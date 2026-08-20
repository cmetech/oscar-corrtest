#!/bin/sh
set -eu

if [ "$#" -ne 2 ] || [ ! -d "$1" ] || [ -z "$2" ]; then
  printf '%s\n' 'usage: check-package.sh <dist-directory> <version>' >&2
  exit 2
fi

for architecture in amd64 arm64; do
  archive="$1/oscar-corrtest_$2_linux_$architecture.tar.gz"
  if [ ! -f "$archive" ]; then
    printf 'missing package: %s\n' "$archive" >&2
    exit 1
  fi
  listing=$(tar -tzf "$archive")
  for path in \
    oscar-corrtest/bin/oscar-corrtest \
    oscar-corrtest/README.md \
    oscar-corrtest/docs/operator.md \
    oscar-corrtest/docs/builtins.md \
    oscar-corrtest/docs/live-qualification.md \
    oscar-corrtest/docs/schema/correlation-scenario.schema.json \
    oscar-corrtest/packaging/oscar-corrtest.service \
    oscar-corrtest/Containerfile
  do
    printf '%s\n' "$listing" | grep -qx "$path"
  done
done
