// Package device 提供设备身份（稳定唯一 ID）的生成与持久化。
package device

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewID 生成一个随机的 16 字节设备 ID（十六进制字符串）。
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// LoadOrCreateID 从 path 读取已持久化的设备 ID；不存在则生成并写入。
// 移动端可传入应用沙盒内的一个文件路径，保证重装前 ID 稳定。
func LoadOrCreateID(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}
	id := NewID()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("device: mkdir: %w", err)
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("device: persist id: %w", err)
	}
	return id, nil
}
