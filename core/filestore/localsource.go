package filestore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/xurenhe/hopdrop/core/protocol"
)

// localSource 是基于本地文件系统的 Source 实现，供桌面端/CLI 发送文件或文件夹使用。
//
// 构造时给定一批"顶层路径"（可以是文件也可以是目录）：
//   - 文件：作为一个条目，RelPath 就是其文件名。
//   - 目录：递归展开其下所有普通文件，RelPath 形如 "<目录名>/sub/x.jpg"，
//     接收端据此在下载目录里重建整个目录树。
//
// 会话内每个文件分配一个自增数字 ID（"0" "1" …），Open 依据它回到真实磁盘路径。
type localSource struct {
	mu    sync.Mutex
	metas []protocol.FileMeta
	paths map[string]string // fileID -> 绝对磁盘路径
}

// NewLocalSource 基于给定的顶层路径构造一个发送源。paths 中的每一项可为文件或目录。
func NewLocalSource(paths []string) (Source, error) {
	s := &localSource{paths: make(map[string]string)}
	var next int
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("filestore: abs %s: %w", p, err)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("filestore: stat %s: %w", p, err)
		}
		if fi.IsDir() {
			if err := s.addDir(abs, &next); err != nil {
				return nil, err
			}
		} else {
			s.addFile(abs, fi, fi.Name(), &next)
		}
	}
	if len(s.metas) == 0 {
		return nil, fmt.Errorf("filestore: no files to send")
	}
	return s, nil
}

// addDir 递归把目录 root 下的普通文件加入清单，RelPath 以 root 的目录名为前缀。
func (s *localSource) addDir(root string, next *int) error {
	base := filepath.Base(root)
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil // 跳过符号链接/设备文件等
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// RelPath 统一用 "/" 分隔，并带上顶层目录名，接收端从而重建整棵树。
		relPath := filepath.ToSlash(filepath.Join(base, rel))
		s.addFileWithRel(path, info, filepath.Base(path), relPath, next)
		return nil
	})
}

func (s *localSource) addFile(abs string, fi os.FileInfo, name string, next *int) {
	s.addFileWithRel(abs, fi, name, name, next)
}

func (s *localSource) addFileWithRel(abs string, fi os.FileInfo, name, relPath string, next *int) {
	id := strconv.Itoa(*next)
	*next++
	s.metas = append(s.metas, protocol.FileMeta{
		ID:       id,
		Name:     name,
		RelPath:  relPath,
		Size:     fi.Size(),
		ModUnix:  fi.ModTime().Unix(),
		MimeType: mimeForName(name),
	})
	s.paths[id] = abs
}

func (s *localSource) List() ([]protocol.FileMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.FileMeta, len(s.metas))
	copy(out, s.metas)
	return out, nil
}

func (s *localSource) Open(fileID string) (io.ReadCloser, error) {
	s.mu.Lock()
	path, ok := s.paths[fileID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("filestore: file id %q not found", fileID)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("filestore: open %s: %w", path, err)
	}
	return f, nil
}
