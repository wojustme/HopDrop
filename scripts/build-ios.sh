#!/usr/bin/env bash
# 构建 HopDrop 的 iOS 绑定框架（.xcframework），供参考 App（mobile/ios）依赖。
#
# 前置条件：
#   - macOS + Xcode（含命令行工具）
#   - Go 与本仓库同版本工具链
#   - gomobile:  go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260821190718-4776eadac327
#                gomobile init
#
# 用法:  scripts/build-ios.sh [输出目录，默认 build/ios]
set -euo pipefail

command -v gomobile >/dev/null || {
  echo "gomobile 未安装；先执行 scripts/check-dev-env.sh" >&2
  exit 1
}
xcodebuild -version >/dev/null 2>&1 || {
  echo "未找到完整 Xcode；先执行 scripts/check-dev-env.sh" >&2
  exit 1
}

cd "$(dirname "$0")/.."
OUT_DIR="${1:-build/ios}"
mkdir -p "$OUT_DIR"

echo ">> gomobile bind (ios) → $OUT_DIR/HopDrop.xcframework"
gomobile bind \
  -target=ios \
  -o "$OUT_DIR/HopDrop.xcframework" \
  ./mobile

echo ">> done: $OUT_DIR/HopDrop.xcframework"
echo "   接着在 mobile/ios 运行 xcodegen generate，按根目录 README 的说明用 Xcode 打开。"
