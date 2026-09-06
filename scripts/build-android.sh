#!/usr/bin/env bash
# 构建 HopDrop 的 Android 绑定库（.aar），供参考 App（mobile/android）依赖。
#
# 前置条件：
#   - Go 与本仓库同版本工具链
#   - Android SDK + NDK（设置 ANDROID_HOME / ANDROID_NDK_HOME）
#   - gomobile:  go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260821190718-4776eadac327
#                gomobile init
#
# 用法:  scripts/build-android.sh [输出目录，默认 build/android]
set -euo pipefail

command -v gomobile >/dev/null || {
  echo "gomobile 未安装；先执行 scripts/check-dev-env.sh" >&2
  exit 1
}
java -version >/dev/null 2>&1 || {
  echo "未找到可用的 JDK 17；先执行 scripts/check-dev-env.sh" >&2
  exit 1
}
: "${ANDROID_HOME:?ANDROID_HOME 未设置；先安装 Android SDK 并执行 scripts/check-dev-env.sh}"
: "${ANDROID_NDK_HOME:?ANDROID_NDK_HOME 未设置；建议使用 NDK 27.0.12077973}"

cd "$(dirname "$0")/.."
OUT_DIR="${1:-build/android}"
mkdir -p "$OUT_DIR"

echo ">> gomobile bind (android) → $OUT_DIR/hopdrop.aar"
gomobile bind \
  -target=android \
  -androidapi 21 \
  -javapkg com.hopdrop \
  -o "$OUT_DIR/hopdrop.aar" \
  ./mobile

mkdir -p mobile/android/app/libs
cp "$OUT_DIR/hopdrop.aar" mobile/android/app/libs/hopdrop.aar

echo ">> done: $OUT_DIR/hopdrop.aar"
echo "   已同步到 mobile/android/app/libs/hopdrop.aar，可直接由 Android Studio 构建 APK。"
