#!/bin/sh
# Packs an install tree for the runtime catalog.
# Usage: package-runtime.sh NAME SERIES VERSION DIR
# Writes dist/NAME-VERSION-macos-aarch64.tar.gz and a matching .meta.json
# that the publish job merges into runtimes.json.
set -eu

name="$1" series="$2" version="$3" dir="$4"
mkdir -p dist
archive="$name-$version-macos-aarch64.tar.gz"
tar -czf "dist/$archive" -C "$dir" .
printf '{"name":"%s","series":"%s","version":"%s","file":"%s"}\n' "$name" "$series" "$version" "$archive" > "dist/$name-$version.meta.json"
ls -la "dist/$archive"
