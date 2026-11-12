#!/bin/sh
# Builds dist/StackNest.app: the SwiftUI app with the stacknest engine inside.
# Usage: app/scripts/build-app.sh [VERSION]
# Set SIGN_IDENTITY to a "Developer ID Application" identity to sign for distribution;
# without it the app is signed ad hoc, which only runs on this Mac.
set -eu

version="${1:-0.1.0}"
repo="$(cd "$(dirname "$0")/../.." && pwd)"
app_dir="$repo/app"
dist="$repo/dist"
bundle="$dist/StackNest.app"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "Building the engine"
(cd "$repo" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$work/stacknest" ./cmd/stacknest)

echo "Building the app"
(cd "$app_dir" && swift build -c release --arch arm64)
binary="$(cd "$app_dir" && swift build -c release --arch arm64 --show-bin-path)/StackNest"

echo "Drawing the icon"
swift "$app_dir/scripts/make-icon.swift" "$work/StackNest.iconset"
iconutil -c icns "$work/StackNest.iconset" -o "$work/StackNest.icns"

rm -rf "$bundle"
mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Helpers" "$bundle/Contents/Resources"
cp "$binary" "$bundle/Contents/MacOS/StackNest"
cp "$work/stacknest" "$bundle/Contents/Helpers/stacknest"
cp "$work/StackNest.icns" "$bundle/Contents/Resources/StackNest.icns"
cat > "$bundle/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>StackNest</string>
	<key>CFBundleDisplayName</key><string>StackNest</string>
	<key>CFBundleIdentifier</key><string>dev.stacknest.app</string>
	<key>CFBundleExecutable</key><string>StackNest</string>
	<key>CFBundleIconFile</key><string>StackNest</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$version</string>
	<key>CFBundleVersion</key><string>$version</string>
	<key>LSMinimumSystemVersion</key><string>14.0</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSAppleEventsUsageDescription</key><string>O StackNest pede a password de administrador para ligar os domínios .test e confiar no seu certificado HTTPS.</string>
</dict>
</plist>
PLIST

identity="${SIGN_IDENTITY:--}"
codesign --force --options runtime --timestamp=none --sign "$identity" "$bundle/Contents/Helpers/stacknest"
codesign --force --options runtime --timestamp=none --sign "$identity" "$bundle"
codesign --verify --strict "$bundle"
echo "Built $bundle"
