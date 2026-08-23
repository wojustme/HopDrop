#!/usr/bin/env bash
# 构建 HopDrop 的 iOS 绑定框架（.xcframework），供参考 App（mobile/ios）依赖。
#
# 前置条件：
#   - macOS + Xcode（含命令行工具）
#   - Go 与本仓库同版本工具链
#   - gomobile:  go install golang.org/x/mobile/cmd/gomobile@latest
#                gomobile init
#
# 用法:  scripts/build-ios.sh [输出目录，默认 build/ios]
set -euo pipefail

cd "$(dirname "$0")/.."
OUT_DIR="${1:-build/ios}"
mkdir -p "$OUT_DIR"

echo ">> gomobile bind (ios) → $OUT_DIR/HopDrop.xcframework"
gomobile bind \
  -target=ios \
  -o "$OUT_DIR/HopDrop.xcframework" \
  ./mobile

echo ">> done: $OUT_DIR/HopDrop.xcframework"
echo "   把该 .xcframework 拖入 Xcode 工程（见 mobile/ios/README.md）。"
