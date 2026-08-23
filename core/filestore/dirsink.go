package filestore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xurenhe/hopdrop/core/protocol"
)

// dirSink 是把接收到的文件写入某个下载目录的 Sink 实现。
//
// 关键行为：
//   - 按 FileMeta.RelPath 在下载目录内重建子目录结构。
//   - 做路径穿越（path traversal）防护：清洗后的相对路径若逃出下载目录则拒绝，
//     避免恶意发送方用 "../../etc/passwd" 之类的相对路径覆写任意文件。
//   - 先写 "<meta>.part" 临时文件，成功后原子改名，避免中断留下半份文件。
//   - 文件名冲突时自动追加数字后缀，绝不覆盖已有文件。
type dirSink struct {
	root string
}

// NewDirSink 打开（或创建）一个以 root 为下载目录的 Sink。
func NewDirSink(root string) (Sink, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("filestore: abs %s: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("filestore: create download dir: %w", err)
	}
	return &dirSink{root: abs}, nil
}

func (d *dirSink) Create(meta protocol.FileMeta, content io.Reader) error {
	target, err := d.resolve(meta)
	if err != nil {
		// 路径非法时丢弃内容并报错，让上层记录但不至于卡死后续文件。
		_, _ = io.Copy(io.Discard, content)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("filestore: mkdir for %s: %w", meta.RelPath, err)
	}
	target = uniquePath(target)

	tmp := target + ".part"
	tf, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("filestore: create temp: %w", err)
	}
	if _, err := io.Copy(tf, content); err != nil {
		tf.Close()
		os.Remove(tmp)
		return fmt.Errorf("filestore: write temp: %w", err)
	}
	if err := tf.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("filestore: close temp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("filestore: finalize %s: %w", meta.RelPath, err)
	}
	// 尽力保留修改时间（失败无所谓）。
	if meta.ModUnix > 0 {
		mt := time.Unix(meta.ModUnix, 0)
		_ = os.Chtimes(target, mt, mt)
	}
	return nil
}

// resolve 把 meta 里的相对路径清洗成下载目录内的安全绝对路径。
func (d *dirSink) resolve(meta protocol.FileMeta) (string, error) {
	rel := meta.RelPath
	if rel == "" {
		rel = meta.Name
	}
	if rel == "" {
		return "", fmt.Errorf("filestore: empty file name")
	}
	// 统一成本地分隔符并清洗 "./" ".." 等。
	rel = filepath.FromSlash(rel)
	clean := filepath.Clean(string(filepath.Separator) + rel) // 前置分隔符把它锚定到根
	clean = strings.TrimPrefix(clean, string(filepath.Separator))
	target := filepath.Join(d.root, clean)
	// 双保险：确保最终路径仍在下载目录内。
	relCheck, err := filepath.Rel(d.root, target)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("filestore: illegal path %q", meta.RelPath)
	}
	return target, nil
}

// uniquePath 若目标已存在，则在扩展名前追加 " (1)" " (2)" … 直到不冲突。
func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}
