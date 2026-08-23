// Package protocol 定义了 HopDrop 跨设备文件传输使用的线路协议（wire protocol）。
//
// 设计目标：
//   - 纯标准库实现，可被 gomobile 绑定到 iOS / Android，也可被 Go 桌面端（Fyne）
//     与 CLI 直接使用，做到"一份协议、多端共用"。
//   - 与具体的文件来源/落地方式解耦：发送端可以来自本地文件夹、iOS 的
//     PhotoKit、Android 的 MediaStore；接收端只需把字节写进一个"下载目录"。
//   - 传输是"定向、按需"的（类似 AirDrop）：由发送方发起，接收方显式接受或拒绝，
//     而不是两端自动取并集。
//
// 一次传输会话（session）大致流程：
//
//	Sender ──Hello──▶ Receiver      交换设备身份
//	Sender ◀─Hello── Receiver
//	Sender ──Offer──▶ Receiver       列出本次要发送的文件清单（名字/大小/相对路径）
//	Sender ◀Decision─ Receiver       接收方接受（可只接受其中一部分）或整体拒绝
//	Sender ──FileStart─▶ Receiver    accept 的每个文件：头 + 紧随其后的原始字节帧
//	Sender ──Binary────▶ Receiver
//	Sender ──TransferDone─▶ Receiver 全部发送完毕
//	Sender ──Bye───────▶ Receiver
//
// 与旧版"相册双向同步"不同，本协议是单向的：发起方推送、接收方落地。
// 双向传输只需各自再发起一次会话即可。
package protocol

// ProtocolVersion 是当前线路协议版本号。握手时若不一致则拒绝会话，
// 便于后续演进时做兼容性判断。
const ProtocolVersion = 2

// MsgType 是控制消息的类型标识。
type MsgType string

const (
	// MsgHello 握手消息，携带设备身份与协议版本，连接建立后双方各发一次。
	MsgHello MsgType = "hello"
	// MsgOffer 发送方给出本次要传输的文件清单，等待接收方决定。
	MsgOffer MsgType = "offer"
	// MsgDecision 接收方的决定：接受哪些文件（或整体拒绝）。
	MsgDecision MsgType = "decision"
	// MsgFileStart 单个文件传输的头，紧随其后的是一个二进制数据帧（原始字节）。
	MsgFileStart MsgType = "file_start"
	// MsgTransferDone 表示发送方已把接收方接受的文件全部推送完毕。
	MsgTransferDone MsgType = "transfer_done"
	// MsgBye 优雅关闭会话。
	MsgBye MsgType = "bye"
)

// Platform 标识设备平台，用于展示与统计，不影响协议本身。
type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformMacOS   Platform = "macos"
	PlatformWindows Platform = "windows"
	PlatformLinux   Platform = "linux"
	PlatformCLI     Platform = "cli"
	PlatformUnknown Platform = "unknown"
)

// DeviceInfo 描述一台参与传输的设备身份。
type DeviceInfo struct {
	// ID 是设备的稳定唯一标识（首次启动时随机生成并持久化）。
	ID string `json:"id"`
	// Name 是用户可读的设备名，如 "小明的 iPhone"。
	Name string `json:"name"`
	// Platform 是设备平台。
	Platform Platform `json:"platform"`
	// SyncPort 是本设备用于接收 TCP 传输连接的端口。
	SyncPort int `json:"sync_port"`
}

// FileMeta 描述一份待传输文件的元数据（不含内容）。
//
// ID 由发送方生成，仅需在"单次会话内"唯一，用于把 Offer 中的条目、Decision 中
// 被接受的条目、以及后续 FileStart 一一对应起来。它不要求跨设备稳定，因此发送
// 任意文件/文件夹都无需先做内容哈希，发起传输很轻量。
type FileMeta struct {
	// ID 是本次会话内的文件序号/标识（如 "0" "1" …）。
	ID string `json:"id"`
	// Name 是文件名（不含目录部分），用于展示与落盘。
	Name string `json:"name"`
	// RelPath 是相对于本次传输根的相对路径（含子目录，使用 "/" 分隔）。
	// 发送单个文件时等于 Name；发送文件夹时形如 "sub/dir/photo.jpg"，
	// 接收端据此在下载目录中重建目录结构。
	RelPath string `json:"rel_path"`
	// Size 是文件字节数。
	Size int64 `json:"size"`
	// ModUnix 是文件修改时间（Unix 秒），用于展示与可选的保留时间戳。
	ModUnix int64 `json:"mod_unix"`
	// MimeType 是 MIME 类型，如 "image/jpeg"，可为空。
	MimeType string `json:"mime_type,omitempty"`
}

// Hello 是握手消息的负载。
type Hello struct {
	Version int        `json:"version"`
	Device  DeviceInfo `json:"device"`
	// Role 标识发起方角色："sender" 表示我方要向你推送文件。
	// 当前协议只有 sender 主动发起，保留字段以便未来支持"请求拉取"。
	Role string `json:"role,omitempty"`
}

// Offer 是发送方给出的传输清单。
type Offer struct {
	// Files 是本次要发送的全部文件。
	Files []FileMeta `json:"files"`
	// TotalBytes 是全部文件的总字节数，便于接收端提示与决策。
	TotalBytes int64 `json:"total_bytes"`
}

// Decision 是接收方对一次 Offer 的答复。
type Decision struct {
	// Accept 为 true 表示接受（可能只接受一部分，见 AcceptIDs）。
	Accept bool `json:"accept"`
	// AcceptIDs 为被接受文件的 ID 列表。为 nil 且 Accept 为 true 时表示"全部接受"。
	AcceptIDs []string `json:"accept_ids,omitempty"`
	// Reason 在拒绝时可携带原因（展示用）。
	Reason string `json:"reason,omitempty"`
}

// FileStart 是单个文件传输的头，描述紧随其后的二进制数据帧对应哪个文件。
type FileStart struct {
	File FileMeta `json:"file"`
}

// Envelope 是所有控制消息的统一信封。Type 决定 Payload 的具体结构，
// 编解码时按 Type 反序列化到对应结构体。
type Envelope struct {
	Type    MsgType `json:"type"`
	Payload []byte  `json:"payload,omitempty"`
}
