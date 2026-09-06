#!/usr/bin/env bash
# Read-only preflight for the macOS + iPhone + Android development setup.
set -u

status=0

check_command() {
  local command_name="$1"
  local install_hint="$2"
  if command -v "$command_name" >/dev/null 2>&1; then
    echo "[ok] $command_name: $(command -v "$command_name")"
  else
    echo "[missing] $command_name — $install_hint"
    status=1
  fi
}

check_command go "install Go 1.26 or newer"
check_command gomobile "go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260821190718-4776eadac327"
check_command adb "install Android SDK Platform Tools"
check_command xcodegen "brew install xcodegen"

if java -version >/dev/null 2>&1; then
  echo "[ok] Java runtime"
else
  echo "[missing] Java runtime — install a JDK 17 distribution"
  status=1
fi

if xcodebuild -version >/dev/null 2>&1; then
  echo "[ok] full Xcode"
else
  echo "[missing] full Xcode — install Xcode and select it with xcode-select"
  status=1
fi

android_sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
if [ -d "$android_sdk" ]; then
  echo "[ok] Android SDK: $android_sdk"
else
  echo "[missing] Android SDK: expected $android_sdk"
  status=1
fi

if command -v adb >/dev/null 2>&1; then
  echo "Android devices:"
  adb devices -l
fi

if command -v xcrun >/dev/null 2>&1 && xcrun xctrace help >/dev/null 2>&1; then
  echo "Apple devices:"
  xcrun xctrace list devices 2>/dev/null
fi

exit "$status"
