#!/usr/bin/env bash
# Builds the Go daemon and places it where the Tauri bundler expects the
# sidecar: desktop/src-tauri/binaries/orchestratord-<target-triple>[.exe]
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
out_dir="$root/desktop/src-tauri/binaries"
mkdir -p "$out_dir"

triple="$(rustc -vV | sed -n 's/^host: //p')"
suffix=""
case "$triple" in
*-windows*) suffix=".exe" ;;
esac

echo "building orchestratord for $triple"
go build -C "$root" -o "$out_dir/orchestratord-$triple$suffix" ./cmd/orchestratord
echo "sidecar ready: $out_dir/orchestratord-$triple$suffix"
