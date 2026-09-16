#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
  echo "This builder requires macOS." >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
VERSION=$(tr -d '\r\n' < "$SCRIPT_DIR/VERSION")
BUILD_DIR="$SCRIPT_DIR/build"
APP_NAME="XIASS 授权助手.app"
APP_DIR="$BUILD_DIR/$APP_NAME"
CONTENTS="$APP_DIR/Contents"
MACOS_DIR="$CONTENTS/MacOS"
RESOURCES_DIR="$CONTENTS/Resources"
DMG_ROOT="$BUILD_DIR/dmg-root"
DMG_PATH="$BUILD_DIR/xiass-adspower-helper-macos-universal.dmg"
ZIP_PATH="$BUILD_DIR/xiass-adspower-helper-macos-universal.zip"

rm -rf "$APP_DIR" "$BUILD_DIR/macos" "$DMG_ROOT" "$DMG_PATH" "$ZIP_PATH"
mkdir -p "$MACOS_DIR" "$RESOURCES_DIR" "$BUILD_DIR/macos/arm64" "$BUILD_DIR/macos/amd64" "$DMG_ROOT"

(
  cd "$SCRIPT_DIR"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "$BUILD_DIR/macos/arm64/xiass-adspower-helper" .
  CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$BUILD_DIR/macos/amd64/xiass-adspower-helper" .
)
lipo -create "$BUILD_DIR/macos/arm64/xiass-adspower-helper" "$BUILD_DIR/macos/amd64/xiass-adspower-helper" -output "$RESOURCES_DIR/xiass-adspower-helper"
chmod 755 "$RESOURCES_DIR/xiass-adspower-helper"

SWIFT_SOURCE="$SCRIPT_DIR/package/macos/XIASSAuthorizationHelper.swift"
SDK_PATH=$(xcrun --sdk macosx --show-sdk-path)
xcrun swiftc -O -whole-module-optimization -sdk "$SDK_PATH" -target arm64-apple-macos12.0 "$SWIFT_SOURCE" -o "$BUILD_DIR/macos/arm64/XIASSAuthorizationHelper"
xcrun swiftc -O -whole-module-optimization -sdk "$SDK_PATH" -target x86_64-apple-macos12.0 "$SWIFT_SOURCE" -o "$BUILD_DIR/macos/amd64/XIASSAuthorizationHelper"
lipo -create "$BUILD_DIR/macos/arm64/XIASSAuthorizationHelper" "$BUILD_DIR/macos/amd64/XIASSAuthorizationHelper" -output "$MACOS_DIR/XIASSAuthorizationHelper"
chmod 755 "$MACOS_DIR/XIASSAuthorizationHelper"

cp "$SCRIPT_DIR/package/macos/Info.plist" "$CONTENTS/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" "$CONTENTS/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $VERSION" "$CONTENTS/Info.plist"
plutil -lint "$CONTENTS/Info.plist" >/dev/null
codesign --force --deep --sign - "$APP_DIR"
ditto -c -k --keepParent "$APP_DIR" "$ZIP_PATH"

ditto "$APP_DIR" "$DMG_ROOT/$APP_NAME"
ln -s /Applications "$DMG_ROOT/Applications"
hdiutil create -volname "XIASS 授权助手" -srcfolder "$DMG_ROOT" -ov -format UDZO "$DMG_PATH" >/dev/null

echo "$APP_DIR"
echo "$DMG_PATH"
echo "$ZIP_PATH"
