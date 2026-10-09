#!/bin/bash
# Package bin/ builds with the web assets: ./release.sh <version>  -> release/etcdkeeper-<version>-linux_<arch>.tar.gz + SHA256SUMS
set -euo pipefail
cd "$(dirname "$0")"
ver=${1:?Release version is empty.}
./build.sh "$ver"
mkdir -p release
rm -f release/SHA256SUMS
for arch in amd64 arm64; do
    name=x86_64; [ $arch = arm64 ] && name=arm64
    out=etcdkeeper-$ver-linux_$name.tar.gz
    tar --owner=0 --group=0 -czf release/$out -C bin/linux_$arch etcdkeeper -C ../.. assets LICENSE README.md
    (cd release && sha256sum $out >> SHA256SUMS)
    echo "release $out"
done
