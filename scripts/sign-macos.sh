#!/bin/bash
# Builds the darwin/arm64 release binary into ./gopro-yank and signs it with the
# IndeSpark Developer ID certificate. Pass --notarize to additionally submit it
# to Apple for notarization, using the "highlights-notary" notarytool keychain
# profile (override with NOTARY_PROFILE). Pass --package to zip the binary with
# scripts/share-README.txt and LICENSE into ./build for sharing.
#
# A bare Mach-O binary cannot be stapled, so Gatekeeper confirms notarization
# online the first time it runs the binary.
set -euo pipefail

NOTARIZE=false
PACKAGE=false
for arg in "$@"; do
    case "$arg" in
        --notarize) NOTARIZE=true ;;
        --package) PACKAGE=true ;;
        *) echo "error: unknown argument '$arg'" >&2; exit 1 ;;
    esac
done

[ "$(uname -s)" = Darwin ] || { echo "error: signing requires macOS" >&2; exit 1; }

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="$ROOT_DIR/gopro-yank"
IDENTITY="Developer ID Application: IndeSpark, LLC (DASAX5TMR3)"
BUNDLE_ID="com.azohra.gopro-yank"
NOTARY_PROFILE="${NOTARY_PROFILE:-highlights-notary}"
VERSION="$(git -C "$ROOT_DIR" describe --tags --always --dirty)"
VERSION="${VERSION#v}"

# Same flags as .goreleaser.yaml, so the signed binary matches a release build.
echo "==> Building gopro-yank $VERSION (darwin/arm64, release)…"
( cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$BINARY" ./cmd/gopro-yank )

# The Go linker leaves an ad-hoc signature; replace it with Developer ID plus the
# hardened runtime and a secure timestamp, both of which notarization requires.
echo "==> Signing with ${IDENTITY}…"
codesign --force --options runtime --timestamp \
    --identifier "$BUNDLE_ID" --sign "$IDENTITY" "$BINARY"
codesign --verify --strict --verbose=2 "$BINARY"

if [ "$NOTARIZE" = true ]; then
    echo "==> Notarizing…"
    WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gopro-yank-notarize.XXXXXX")"
    trap 'rm -rf "$WORK_DIR"' EXIT
    ZIP_PATH="$WORK_DIR/gopro-yank.zip"
    ditto -c -k --keepParent "$BINARY" "$ZIP_PATH"

    # notarytool exits 0 even when Apple rejects the upload, so check the status.
    RESULT="$WORK_DIR/result.json"
    xcrun notarytool submit "$ZIP_PATH" --keychain-profile "$NOTARY_PROFILE" \
        --wait --output-format json > "$RESULT"
    STATUS="$(plutil -extract status raw "$RESULT")"
    if [ "$STATUS" != Accepted ]; then
        echo "error: notarization status '$STATUS'" >&2
        xcrun notarytool log "$(plutil -extract id raw "$RESULT")" \
            --keychain-profile "$NOTARY_PROFILE" >&2 || true
        exit 1
    fi
    echo "Notarization accepted."
    spctl --assess --type open --context context:primary-signature --verbose=2 "$BINARY"
fi

if [ "$PACKAGE" = true ]; then
    echo "==> Packaging…"
    [ "$NOTARIZE" = true ] || echo "warning: not notarized; Gatekeeper will block it on other Macs" >&2
    BUILD_DIR="$ROOT_DIR/build"
    NAME="gopro-yank-$VERSION-macos-arm64"
    STAGE_DIR="$BUILD_DIR/$NAME"
    rm -rf "$STAGE_DIR" "$BUILD_DIR/$NAME.zip"
    mkdir -p "$STAGE_DIR"
    cp "$BINARY" "$STAGE_DIR/gopro-yank"
    cp "$ROOT_DIR/scripts/share-README.txt" "$STAGE_DIR/README.txt"
    cp "$ROOT_DIR/LICENSE" "$STAGE_DIR/LICENSE"
    ditto -c -k --keepParent "$STAGE_DIR" "$BUILD_DIR/$NAME.zip"
    rm -rf "$STAGE_DIR"
fi

echo "==> Done."
echo "Binary: $BINARY"
[ "$PACKAGE" = false ] || echo "Package: $BUILD_DIR/$NAME.zip"
