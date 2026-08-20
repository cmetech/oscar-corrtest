#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ ! -d "$1" ]; then
  printf '%s\n' 'usage: check-package.sh <dist-directory>' >&2
  exit 2
fi

for archive in "$1"/*.tar.gz; do
  listing=$(tar -tzf "$archive")
  for path in \
    oscar-corrtest/bin/oscar-corrtest \
    oscar-corrtest/README.md \
    oscar-corrtest/docs/operator.md \
    oscar-corrtest/docs/builtins.md \
    oscar-corrtest/docs/schema/correlation-scenario.schema.json \
    oscar-corrtest/packaging/oscar-corrtest.service \
    oscar-corrtest/Containerfile
  do
    printf '%s\n' "$listing" | grep -qx "$path"
  done
done
