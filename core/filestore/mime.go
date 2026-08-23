package filestore

import (
	"path/filepath"
	"strings"
)

// mimeForName 依据文件扩展名猜测 MIME 类型，未知则返回空串。
// 只覆盖常见类型；纯展示/提示用途，不参与传输正确性。
func mimeForName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".heic":
		return "image/heic"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md", ".log":
		return "text/plain"
	case ".zip":
		return "application/zip"
	default:
		return ""
	}
}
