#!/bin/sh
set -eu

version="${1#v}"
if [ -z "$version" ]; then
  echo "usage: $0 <version>" >&2
  exit 2
fi

image="vendra:v${version}"
archive="dist/vendra-v${version}.tar.gz"
mkdir -p dist
docker build \
  --build-arg "VERSION=${version}" \
  --build-arg "COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  --build-arg "BUILD_TIME=$(date -u +%FT%TZ)" \
  -t "$image" .
docker save "$image" | gzip -9 > "$archive"
echo "$archive"
