#!/bin/bash
set -euo pipefail

macos_dir=$(cd "$(dirname "$0")" && pwd)
output_dir="${1:-$macos_dir/../dist}"
bundle="$output_dir/AudioService.app"
mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources"

for arch in arm64 x86_64; do
    swift build --package-path "$macos_dir" -c release --triple "$arch-apple-macosx14.0"
done
arm_dir=$(swift build --package-path "$macos_dir" -c release --triple arm64-apple-macosx14.0 --show-bin-path)
intel_dir=$(swift build --package-path "$macos_dir" -c release --triple x86_64-apple-macosx14.0 --show-bin-path)

lipo -create "$arm_dir/AudioService" "$intel_dir/AudioService" -output "$bundle/Contents/MacOS/AudioService"
chmod +x "$bundle/Contents/MacOS/AudioService"
cp "$macos_dir/Info.plist" "$bundle/Contents/Info.plist"
cp "$macos_dir/Resources/AppIcon.icns" "$bundle/Contents/Resources/AppIcon.icns"
codesign --force --sign - "$bundle"
codesign --verify --strict "$bundle"
printf '%s\n' "$bundle"
