# HopDrop

跨设备（**Android / iOS / macOS / Windows / Linux**）局域网文件传输工具，类似 AirDrop：
发送方在局域网内发现设备、挑选文件/文件夹主动推送，接收方确认后落地。

核心是一份纯标准库的 Go 库，被桌面端（Fyne GUI）、命令行（CLI）与移动端（gomobile
绑定 + 原生 App）共同复用，做到「一份核心、多端共用」。

## 目录结构

```
core/
  protocol/     线路协议：帧编解码 + 控制消息（Hello/Offer/Decision/FileStart/…）
  discovery/    UDP 组播设备发现（announce + browse，类 mDNS，纯标准库）
  filestore/    发送取源（Source）与接收落地（Sink）抽象 + 本地文件系统实现
  device/       稳定设备 ID 的生成与持久化
  sync/         传输引擎（Engine）+ 高层编排（Node）
cmd/
  hopdrop/      命令行客户端（recv / send / peers）
  hopdrop-gui/  Fyne 跨平台桌面 GUI（macOS / Windows / Linux）
mobile/         gomobile 绑定层（导出窄接口给 Java/Swift）
  android/      Android 参考实现（Kotlin：Controller + MediaStore Sink）
  ios/          iOS 参考实现（Swift：Controller + DTO）
scripts/        gomobile 构建脚本（.aar / .xcframework）
.github/workflows/release.yml   五端安装包的发布流水线
```

## 传输模型

定向、按需（AirDrop 式），由发送方发起：

```
Sender ──Hello──▶ Receiver      交换设备身份
Sender ◀─Hello── Receiver
Sender ──Offer──▶ Receiver      给出本次文件清单（名字/大小/相对路径）
Sender ◀Decision─ Receiver      接收方接受（可部分接受）或拒绝
Sender ──FileStart+Binary─▶     逐个流式推送被接受的文件
Sender ──TransferDone──▶
Sender ──Bye──────────▶
```

- 文件以「长度前缀帧」流式传输，天然支持大文件不占内存。
- 发送文件夹时保留子目录结构（`FileMeta.RelPath`），接收端做**路径穿越防护**与原子落盘。
- 发现使用组播 `239.192.71.71:47771`，magic 前缀 `PSYNC1`，协议版本 `2`。

## 快速开始（桌面 / CLI）

```bash
# 构建
go build ./cmd/hopdrop        # CLI
go build ./cmd/hopdrop-gui    # 桌面 GUI（需要 CGO/OpenGL 环境）

# 单机联调：一个终端收，一个终端发
./hopdrop recv --dir ./inbox --name MacA
./hopdrop send --to MacA ./photo.jpg ./someFolder

# 只看局域网里有哪些设备
./hopdrop peers
```

## 桌面 GUI

```bash
go run ./cmd/hopdrop-gui
```

左侧是在线设备列表，点选后「发送文件/发送文件夹」；收到传输会弹窗询问是否接收，
接受后落到下载目录（默认 `~/Downloads/HopDrop`，可在界面里更改）。

打安装包（本地）：

```bash
go install fyne.io/tools/cmd/fyne@latest
fyne package --os darwin  --name HopDrop --app-id com.hopdrop.desktop --src ./cmd/hopdrop-gui
fyne package --os windows --name HopDrop --app-id com.hopdrop.desktop --src ./cmd/hopdrop-gui
```

## 移动端（Android / iOS）

先用 gomobile 产出绑定库，再由参考 App 集成：

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

scripts/build-android.sh    # → build/android/hopdrop.aar
scripts/build-ios.sh        # → build/ios/HopDrop.xcframework
```

- Android：把 `hopdrop.aar` 作模块依赖，参考 `mobile/android/`。需声明
  `CHANGE_WIFI_MULTICAST_STATE` 并获取 `MulticastLock`（Controller 已封装）。
  参考 App 支持双向传输：点选设备后用系统文件选择器（SAF）挑文件推送，收到
  传输时弹窗确认；接收落地走 `MediaStore.Downloads`，故 `minSdk = 29`（Android 10+），
  无需运行时存储权限。
- iOS：把 `HopDrop.xcframework` 拖入 Xcode，参考 `mobile/ios/`。需在 Info.plist
  声明 `NSLocalNetworkUsageDescription` 与 `NSBonjourServices`。参考 App 同样支持
  双向传输：点选设备后用 `.fileImporter` 挑文件（`FileSource` 流式读出）推送，收到
  传输时弹窗确认；接收落地到 App `Documents/HopDrop`（`DocumentSink`，按 `rel_path`
  重建子目录并做路径穿越防护）。

## 发布安装包

推送形如 `v1.2.3` 的 tag（或在 Actions 页手动触发）即可跑
`.github/workflows/release.yml`，产出并挂到 GitHub Release：

| 端 | 产物 |
|----|------|
| CLI | `hopdrop-cli-<os>-<arch>.{tar.gz,zip}`（darwin/windows/linux × amd64/arm64） |
| macOS 桌面 | `HopDrop-macos-<arch>.zip`（内含 `HopDrop.app`） |
| Windows 桌面 | `HopDrop-windows-amd64.zip`（内含 `HopDrop.exe`） |
| Android | `hopdrop.aar`（+ 若含 Gradle 工程则出 `.apk`） |
| iOS | `HopDrop.xcframework`（+ 若含 Xcode 工程则出未签名 `.ipa`） |

> iOS 上架 / Android 正式签名需自备证书与描述文件；流水线默认产出未签名归档，便于内测分发。
