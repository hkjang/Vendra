#!/bin/sh
set -eu

version="${1#v}"
if [ -z "$version" ]; then
  echo "usage: $0 <version>" >&2
  exit 2
fi

image="vendra:v${version}"
# The archive also carries vendra:latest so compose.yaml can name the image
# without a version in it. A version written into compose has to be edited on
# every upgrade, and when it is not, `docker compose up` reaches for a registry
# that an air-gapped install does not have.
rolling="vendra:latest"
archive="dist/vendra-v${version}.tar.gz"
mkdir -p dist
docker build \
  --build-arg "VERSION=${version}" \
  --build-arg "COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  --build-arg "BUILD_TIME=$(date -u +%FT%TZ)" \
  -t "$image" .
docker tag "$image" "$rolling"
docker save "$image" "$rolling" | gzip -9 > "$archive"
echo "$archive"
