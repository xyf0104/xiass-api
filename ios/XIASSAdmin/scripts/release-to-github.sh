#!/bin/bash
set -euo pipefail

# Codex/Xcode command-line environments can have Command Line Tools selected
# even when the full Xcode app is installed. Archiving requires full Xcode.
if [ -z "${DEVELOPER_DIR:-}" ] && [ -d "/Applications/Xcode.app/Contents/Developer" ]; then
  export DEVELOPER_DIR="/Applications/Xcode.app/Contents/Developer"
fi

if [ "$#" -ne 3 ]; then
  echo "Usage: $0 <version> <team-id> <bundle-id>"
  echo "Example: $0 1.0.1 ABCDE12345 com.example.xiassadmin"
  exit 64
fi

VERSION="$1"
TEAM_ID="$2"
BUNDLE_ID="$3"
BUILD_NUMBER="${XIASS_BUILD_NUMBER:-${VERSION##*.}}"
TAG="ios-v${VERSION}"
ASSET_BASENAME="XIASSAdmin-${VERSION}"
ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
PROJECT_DIR="$ROOT_DIR/ios/XIASSAdmin"
PROJECT="$PROJECT_DIR/XIASSAdmin.xcodeproj"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/xiass-admin-release.XXXXXX")"
ARCHIVE_PATH="$WORK_DIR/XIASSAdmin.xcarchive"
EXPORT_DIR="$WORK_DIR/export"
MANIFEST_PATH="$EXPORT_DIR/${ASSET_BASENAME}.plist"
IPA_PATH="$EXPORT_DIR/${ASSET_BASENAME}.ipa"
IPA_URL="https://github.com/xyf0104/xiass-api/releases/download/${TAG}/${ASSET_BASENAME}.ipa"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

command -v gh >/dev/null || {
  echo "GitHub CLI is required. Install it with: brew install gh"
  exit 69
}

find_ad_hoc_profile() {
  local profile_dir candidate candidate_plist app_identifier profile_team task_allow first_device
  local profile_dirs=(
    "$HOME/Library/MobileDevice/Provisioning Profiles"
    "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"
  )

  for profile_dir in "${profile_dirs[@]}"; do
    [ -d "$profile_dir" ] || continue
    while IFS= read -r -d '' candidate; do
      candidate_plist="$WORK_DIR/$(basename "$candidate").plist"
      security cms -D -i "$candidate" > "$candidate_plist" 2>/dev/null || continue
      app_identifier="$(plutil -extract Entitlements.application-identifier raw "$candidate_plist" 2>/dev/null || true)"
      profile_team="$(plutil -extract TeamIdentifier.0 raw "$candidate_plist" 2>/dev/null || true)"
      task_allow="$(plutil -extract Entitlements.get-task-allow raw "$candidate_plist" 2>/dev/null || true)"
      first_device="$(plutil -extract ProvisionedDevices.0 raw "$candidate_plist" 2>/dev/null || true)"
      if [ "$app_identifier" = "$TEAM_ID.$BUNDLE_ID" ] && [ "$profile_team" = "$TEAM_ID" ] && [ "$task_allow" = "false" ] && [ -n "$first_device" ]; then
        printf '%s' "$candidate"
        return 0
      fi
    done < <(find "$profile_dir" -maxdepth 1 -type f -name '*.mobileprovision' -print0)
  done

  return 1
}

PROFILE_PATH="${XIASS_ADHOC_PROFILE:-}"
if [ -n "$PROFILE_PATH" ] && [ ! -f "$PROFILE_PATH" ]; then
  echo "XIASS_ADHOC_PROFILE does not point to an installed provisioning profile."
  exit 70
fi
if [ -z "$PROFILE_PATH" ]; then
  PROFILE_PATH="$(find_ad_hoc_profile)" || {
    echo "No installable Ad Hoc profile was found for ${TEAM_ID}.${BUNDLE_ID}."
    exit 70
  }
fi

PROFILE_PLIST="$WORK_DIR/adhoc-profile.plist"
security cms -D -i "$PROFILE_PATH" > "$PROFILE_PLIST"
PROFILE_NAME="$(plutil -extract Name raw "$PROFILE_PLIST")"
plutil -extract Entitlements xml1 -o "$WORK_DIR/adhoc-entitlements.plist" "$PROFILE_PLIST"

SIGNING_IDENTITY="$(security find-identity -v -p codesigning | awk -F'"' '
  /Apple Distribution:/ { print $2; found = 1; exit }
  /iPhone Distribution:/ && fallback == "" { fallback = $2 }
  END { if (!found && fallback != "") print fallback }
')"
if [ -z "$SIGNING_IDENTITY" ]; then
  echo "No Apple Distribution signing identity is installed."
  exit 70
fi

xcodebuild \
  -allowProvisioningUpdates \
  -project "$PROJECT" \
  -scheme XIASSAdmin \
  -configuration Release \
  -archivePath "$ARCHIVE_PATH" \
  DEVELOPMENT_TEAM="$TEAM_ID" \
  PRODUCT_BUNDLE_IDENTIFIER="$BUNDLE_ID" \
  MARKETING_VERSION="$VERSION" \
  CURRENT_PROJECT_VERSION="$BUILD_NUMBER" \
  CODE_SIGN_STYLE=Automatic \
  archive

ARCHIVED_APP="$(find "$ARCHIVE_PATH/Products/Applications" -maxdepth 1 -type d -name '*.app' -print -quit)"
if [ -z "$ARCHIVED_APP" ]; then
  echo "Xcode did not produce an application bundle in the archive."
  exit 70
fi

# Xcode 26 can archive correctly but fail to export an Xcode-managed Ad Hoc
# profile. Re-signing the archive with the matching installed profile produces
# the same OTA-ready package while retaining an explicit, verifiable profile.
mkdir -p "$EXPORT_DIR/Payload"
SIGNED_APP="$EXPORT_DIR/Payload/$(basename "$ARCHIVED_APP")"
cp -R "$ARCHIVED_APP" "$SIGNED_APP"
cp "$PROFILE_PATH" "$SIGNED_APP/embedded.mobileprovision"
codesign --force --sign "$SIGNING_IDENTITY" --entitlements "$WORK_DIR/adhoc-entitlements.plist" "$SIGNED_APP"
codesign --verify --deep --strict --verbose=2 "$SIGNED_APP"
ditto -c -k --sequesterRsrc --keepParent "$EXPORT_DIR/Payload" "$IPA_PATH"

unzip -p "$IPA_PATH" 'Payload/*.app/embedded.mobileprovision' > "$WORK_DIR/embedded.mobileprovision"
security cms -D -i "$WORK_DIR/embedded.mobileprovision" > "$WORK_DIR/embedded.mobileprovision.plist"
PROFILE_TASK_ALLOW="$(plutil -extract Entitlements.get-task-allow raw "$WORK_DIR/embedded.mobileprovision.plist")"
PROFILE_DEVICE="$(plutil -extract ProvisionedDevices.0 raw "$WORK_DIR/embedded.mobileprovision.plist")"
if [ "$PROFILE_TASK_ALLOW" != "false" ] || [ -z "$PROFILE_DEVICE" ]; then
  echo "Exported IPA is not signed with an installable Ad Hoc profile."
  exit 71
fi
echo "Using Ad Hoc profile: ${PROFILE_NAME}"

sed \
  -e "s|__IPA_URL__|${IPA_URL}|g" \
  -e "s|__BUNDLE_ID__|${BUNDLE_ID}|g" \
  -e "s|__VERSION__|${VERSION}|g" \
  "$PROJECT_DIR/Release/OTA-manifest.template.plist" > "$MANIFEST_PATH"
plutil -lint "$MANIFEST_PATH"

if ! gh release view "$TAG" --repo xyf0104/xiass-api >/dev/null 2>&1; then
  # The service release must remain GitHub's Latest because XIASS API's web
  # updater reads that channel. iOS packages use their own independent tag.
  gh release create "$TAG" --repo xyf0104/xiass-api --title "XIASS 管理端 ${VERSION}" --notes "iOS 管理端发布包。" --latest=false
fi

gh release upload "$TAG" --repo xyf0104/xiass-api \
  "${IPA_PATH}#${ASSET_BASENAME}.ipa"

gh release upload "$TAG" --repo xyf0104/xiass-api \
  "${MANIFEST_PATH}#${ASSET_BASENAME}.plist"

echo "Release published: https://github.com/xyf0104/xiass-api/releases/tag/${TAG}"
