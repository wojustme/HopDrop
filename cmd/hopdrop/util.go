package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/protocol"
)

// currentPlatform 把 Go 的 GOOS 映射成协议里的 Platform 常量，用于展示。
func currentPlatform() protocol.Platform {
	switch runtime.GOOS {
	case "darwin":
		return protocol.PlatformMacOS
	case "windows":
		return protocol.PlatformWindows
	case "linux":
		return protocol.PlatformLinux
	default:
		return protocol.PlatformCLI
	}
}

func pairingURI(self protocol.DeviceInfo) string {
	addresses := discovery.LocalAddresses()
	if len(addresses) == 0 {
		return ""
	}
	return fmt.Sprintf("hopdrop://%s?id=%s&name=%s&platform=%s&fingerprint=%s",
		net.JoinHostPort(addresses[0], fmt.Sprint(self.SyncPort)), url.QueryEscape(self.ID), url.QueryEscape(self.Name),
		self.Platform, url.QueryEscape(self.Fingerprint))
}

func identityPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "HopDrop", "identity-v1.json")
	}
	return filepath.Join(os.TempDir(), "HopDrop", "identity-v1.json")
}

// durationSeconds 把秒数转成 time.Duration。
func durationSeconds(sec int) time.Duration {
	return time.Duration(sec) * time.Second
}

// humanBytes 把字节数格式化成人类可读的字符串（如 "1.2 MB"）。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
