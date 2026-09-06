package mobile

import "github.com/xurenhe/hopdrop/core/protocol"

// 本文件定义在 Go 与移动端宿主之间用 JSON 传递的数据结构（DTO）。
// gomobile 不擅长跨语言传结构体切片/map，因此统一序列化成 JSON 字符串往返。
//
// 注意：这些类型刻意用小写（未导出）命名 —— 它们只在 Go 内部用于 json 编解码，
// 不应被 gomobile 绑定成 Java/Swift 类；未导出可避免 gomobile 试图绑定含
// []struct 字段（如 offerJSON.Files）时报“unsupported type”。字段仍为导出，
// 以便 encoding/json 正常工作。

// peerDevice 是设备身份的可序列化形式。
type peerDevice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Platform    string `json:"platform"`
	SyncPort    int    `json:"sync_port"`
	Fingerprint string `json:"fingerprint"`
}

// peerJSON 是一台在线设备（含网络地址）的可序列化形式。
type peerJSON struct {
	Device   peerDevice `json:"device"`
	Addr     string     `json:"addr"`
	Endpoint string     `json:"endpoint"`
}

// fileMetaJSON 是文件元数据的可序列化形式。
type fileMetaJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RelPath  string `json:"rel_path"`
	Size     int64  `json:"size"`
	ModUnix  int64  `json:"mod_unix"`
	MimeType string `json:"mime_type,omitempty"`
}

// offerJSON 是一次传输请求的可序列化形式，供宿主弹窗展示。
type offerJSON struct {
	Peer       peerDevice     `json:"peer"`
	Files      []fileMetaJSON `json:"files"`
	TotalBytes int64          `json:"total_bytes"`
}

// progressJSON 是传输进度的可序列化形式。
type progressJSON struct {
	Direction   string `json:"direction"`
	PeerID      string `json:"peer_id"`
	PeerName    string `json:"peer_name"`
	Phase       string `json:"phase"`
	CurrentName string `json:"current_name"`
	Files       int    `json:"files"`
	TotalFiles  int    `json:"total_files"`
	Bytes       int64  `json:"bytes"`
	TotalBytes  int64  `json:"total_bytes"`
	Err         string `json:"err,omitempty"`
}

func toPeerDevice(d protocol.DeviceInfo) peerDevice {
	return peerDevice{
		ID:          d.ID,
		Name:        d.Name,
		Platform:    string(d.Platform),
		SyncPort:    d.SyncPort,
		Fingerprint: d.Fingerprint,
	}
}

func toFileMeta(f protocol.FileMeta) fileMetaJSON {
	return fileMetaJSON{
		ID:       f.ID,
		Name:     f.Name,
		RelPath:  f.RelPath,
		Size:     f.Size,
		ModUnix:  f.ModUnix,
		MimeType: f.MimeType,
	}
}

func fromFileMeta(m fileMetaJSON) protocol.FileMeta {
	return protocol.FileMeta{
		ID:       m.ID,
		Name:     m.Name,
		RelPath:  m.RelPath,
		Size:     m.Size,
		ModUnix:  m.ModUnix,
		MimeType: m.MimeType,
	}
}
