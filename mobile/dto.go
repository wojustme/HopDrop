package mobile

import "github.com/xurenhe/hopdrop/core/protocol"

// 本文件定义在 Go 与移动端宿主之间用 JSON 传递的数据结构（DTO）。
// gomobile 不擅长跨语言传结构体切片/map，因此统一序列化成 JSON 字符串往返。

// PeerDevice 是设备身份的可序列化形式。
type PeerDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	SyncPort int    `json:"sync_port"`
}

// PeerJSON 是一台在线设备（含网络地址）的可序列化形式。
type PeerJSON struct {
	Device   PeerDevice `json:"device"`
	Addr     string     `json:"addr"`
	Endpoint string     `json:"endpoint"`
}

// FileMetaJSON 是文件元数据的可序列化形式。
type FileMetaJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RelPath  string `json:"rel_path"`
	Size     int64  `json:"size"`
	ModUnix  int64  `json:"mod_unix"`
	MimeType string `json:"mime_type,omitempty"`
}

// OfferJSON 是一次传输请求的可序列化形式，供宿主弹窗展示。
type OfferJSON struct {
	Peer       PeerDevice     `json:"peer"`
	Files      []FileMetaJSON `json:"files"`
	TotalBytes int64          `json:"total_bytes"`
}

// ProgressJSON 是传输进度的可序列化形式。
type ProgressJSON struct {
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

func toPeerDevice(d protocol.DeviceInfo) PeerDevice {
	return PeerDevice{
		ID:       d.ID,
		Name:     d.Name,
		Platform: string(d.Platform),
		SyncPort: d.SyncPort,
	}
}

func toFileMeta(f protocol.FileMeta) FileMetaJSON {
	return FileMetaJSON{
		ID:       f.ID,
		Name:     f.Name,
		RelPath:  f.RelPath,
		Size:     f.Size,
		ModUnix:  f.ModUnix,
		MimeType: f.MimeType,
	}
}

func fromFileMeta(m FileMetaJSON) protocol.FileMeta {
	return protocol.FileMeta{
		ID:       m.ID,
		Name:     m.Name,
		RelPath:  m.RelPath,
		Size:     m.Size,
		ModUnix:  m.ModUnix,
		MimeType: m.MimeType,
	}
}
