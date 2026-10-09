#!/bin/bash
# Build static etcdkeeper binaries inside the golang container (no local Go needed).
# Usage: ./build.sh [version]   (default: git describe)   -> bin/linux_amd64/etcdkeeper, bin/linux_arm64/etcdkeeper
set -euo pipefail
cd "$(dirname "$0")"
ver=${1:-$(git describe --tags --always --dirty)}
GO_IMAGE=${GO_IMAGE:-golang:1.26}
mkdir -p bin .gocache
for arch in amd64 arm64; do
    docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/src/.gocache/build -e GOMODCACHE=/src/.gocache/mod \
        -e GOTOOLCHAIN=local -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=$arch \
        -v "$PWD:/src" -w /src/src/etcdkeeper "$GO_IMAGE" \
        go build -trimpath -ldflags "-s -w -X main.version=$ver" -o /src/bin/linux_$arch/etcdkeeper .
    echo "build etcdkeeper $ver GOOS=linux GOARCH=$arch ok"
done
