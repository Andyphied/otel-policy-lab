#!/usr/bin/env bash
# Developer/CI test dependency only. Neither the CLI nor action invokes this.
set -euo pipefail
version=0.120.0
destination=${1:?Usage: provision-test-collector.sh DESTINATION_DIRECTORY}
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) platform=linux_amd64; checksum=81bf885bc9a86705feb3c113c5a356571390e3601eb651ffcf2b3428f6571adb ;;
  Linux-aarch64|Linux-arm64) platform=linux_arm64; checksum=00b11a5b468455e6ab49d6857e36f5825cb887bf5d8646282f8db33e3eeb5c76 ;;
  Darwin-arm64) platform=darwin_arm64; checksum=8e3da7b807c6b22077892e731e12dd969dada2643d85641d87324900385d9bcd ;;
  Darwin-x86_64) platform=darwin_amd64; checksum=9e2716f9b1a8dc47790d5de2e001def5bf30ee9639e23728ff481bfbdbf98613 ;;
  *) echo 'Unsupported test Collector platform' >&2; exit 1 ;;
esac
# Checksums copied from the official v0.120.0 release manifest:
# https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v0.120.0/opentelemetry-collector-releases_otelcol-contrib_checksums.txt
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
archive="otelcol-contrib_${version}_${platform}.tar.gz"
curl --fail --location --retry 3 --proto '=https' --tlsv1.2 \
  "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${version}/${archive}" \
  --output "$scratch/$archive"
if command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$scratch/$archive" | cut -d ' ' -f 1)
else
  actual=$(shasum -a 256 "$scratch/$archive" | cut -d ' ' -f 1)
fi
[[ "$actual" == "$checksum" ]] || { echo 'Collector archive checksum mismatch' >&2; exit 1; }
mkdir -p "$destination"
tar -xzf "$scratch/$archive" -C "$destination" otelcol-contrib
chmod 755 "$destination/otelcol-contrib"
"$destination/otelcol-contrib" --version
