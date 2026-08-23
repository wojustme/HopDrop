#!/usr/bin/env bash
# 构建 HopDrop 的 Android 绑定库（.aar），供参考 App（mobile/android）依赖。
#
# 前置条件：
#   - Go 与本仓库同版本工具链
#   - Android SDK + NDK（设置 ANDROID_HOME / ANDROID_NDK_HOME）
#   - gomobile:  go install golang.org/x/mobile/cmd/gomobile@latest
#                gomobile init
#
# 用法:  scripts/build-android.sh [输出目录，默认 build/android]
set -euo pipefail

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

echo ">> done: $OUT_DIR/hopdrop.aar"
echo "   把该 .aar 作为模块依赖加入 Android 工程（见 mobile/android/README.md）。"
