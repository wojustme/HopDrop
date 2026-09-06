# HopDrop

HopDrop 是一个面向 Android、iOS、macOS、Windows 和 Linux 的局域网直连文件传输工具。Go 核心负责设备身份、传输协议和文件流；桌面、CLI、Android 与 iOS 只实现各自的界面和系统文件接口。

## 架构

每一笔传输采用一种明确的 C/S 模型：

```text
发送设备（HTTPS Client） ─────────────▶ 接收设备（HTTPS Server）
                         Offer
                    ◀──── Decision
                     PUT file streams
                    ◀──── SHA-256 receipts
                       Complete
                    ◀──── final ACK
```

- 接收方调用 `StartServer`，监听并发布 `_hopdrop._tcp` 服务。
- 纯发送方调用 `StartClient`，只发现并连接接收方，不开放入站端口。
- 两台完整 App 都可以接收，因此都可运行 Server；A 发给 B 时 B 是 Server，B 发给 A 时 A 是 Server。角色按传输反转，不存在第二套反向协议。
- 控制面是 HTTPS JSON，文件内容直接作为 HTTP `PUT` 请求体流式传输，不在内存中整包缓存。

## 发现与配对

只保留两种入口，最终都得到同一种 `host:port + TLS 指纹` 连接信息：

1. Bonjour / mDNS 自动发现。桌面使用 Go DNS-SD，Android 使用 `NsdManager`，iOS 使用 `NWBrowser`。
2. `hopdrop://` 二维码配对。二维码包含端点、设备 ID、名称、平台和 SHA-256 公钥指纹。

旧 UDP 自定义组播和旧二进制帧协议已经移除。

## 协议 v1

API 根路径为 `/api/v1/transfers`：

1. `POST /api/v1/transfers`：发送清单，接收方全部、部分接受或拒绝。
2. `PUT /api/v1/transfers/{session}/files/{file}`：上传一个已接受文件，`Content-Length` 必须与清单完全一致。
3. `POST /api/v1/transfers/{session}/complete`：请求最终确认。
4. `DELETE /api/v1/transfers/{session}`：取消未完成会话。

双方使用持久化 Ed25519 身份和 TLS 1.3 双向证书。发现记录与二维码携带公钥指纹，连接时进行 pin 校验。每个文件都返回长度和 SHA-256 回执；发送端只有验证最终 ACK 后才显示成功。接收文件使用临时文件，长度正确并同步完成后再原子改名。

二维码属于带外配对，能确认扫码时展示的设备指纹；Bonjour/mDNS 属于局域网内的首次发现，只保证实际 TLS 连接与发现记录中的指纹一致，不等价于人工确认对端身份。

## 目录

```text
core/device/       持久化 Ed25519 身份、自签名 TLS 证书
core/discovery/    Bonjour/mDNS 与移动端手动发现后端
core/protocol/     API v1 数据模型、路径和清单校验
core/filestore/    流式 Source/Sink、目录重建和原子落盘
core/sync/         HTTPS Engine 与 Server/Client 生命周期
cmd/hopdrop/       CLI
cmd/hopdrop-gui/   Fyne 桌面 App
mobile/            gomobile 绑定与 Android/iOS 参考 App
```

## CLI 开发与联调

```bash
go test ./...
go build ./cmd/hopdrop

# 终端 A：作为接收 Server
./hopdrop recv --dir ./inbox --name MacA

# 终端 B：Bonjour 发现后发送
./hopdrop send --to MacA ./photo.jpg ./someFolder

# 也可以粘贴接收端打印的完整配对串
./hopdrop send --to 'hopdrop://192.168.1.9:47772?...' ./photo.jpg
```

## 桌面 App

```bash
go run ./cmd/hopdrop-gui
```

桌面端默认接收目录是 `~/Downloads/HopDrop`。选择在线设备后可发送文件或目录；收到 Offer 时会先弹窗确认。手动配对必须粘贴完整 `hopdrop://` 串，裸 IP 不会绕过证书校验。

## Android 真机

需要 Go、Android Studio/SDK、JDK 和 gomobile：

```bash
scripts/check-dev-env.sh
go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260821190718-4776eadac327
gomobile init
scripts/build-android.sh
```

脚本生成 `build/android/hopdrop.aar`。Android Studio 打开 `mobile/android`，连接已开启 USB 调试的真机后运行 `app`。接收文件先以 `MediaStore.IS_PENDING=1` 写入，完成后才对系统下载目录可见。

## iPhone 真机

需要 macOS、Xcode、Apple Development 签名和 gomobile：

```bash
scripts/check-dev-env.sh
go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260821190718-4776eadac327
gomobile init
scripts/build-ios.sh
```

脚本生成 `build/ios/HopDrop.xcframework`。安装 XcodeGen 后在 `mobile/ios` 执行 `xcodegen generate`，用 Xcode 打开工程、选择开发团队与真机运行。应用只需本地网络、Bonjour 和相机权限，不需要 Apple 的 UDP multicast entitlement。首版移动端传输时需保持 App 在前台。

## 发布产物

`.github/workflows/release.yml` 构建 CLI、macOS/Windows 桌面包、Android AAR/APK 和 iOS XCFramework。Android 正式 APK 与 iOS 安装包仍需项目自己的签名材料。
