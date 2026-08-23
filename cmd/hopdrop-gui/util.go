package main

import (
	"fmt"
	"runtime"

	"github.com/xurenhe/hopdrop/core/protocol"
)

// currentPlatform 把 Go 的 GOOS 映射成协议里的 Platform 常量。
func currentPlatform() protocol.Platform {
	switch runtime.GOOS {
	case "darwin":
		return protocol.PlatformMacOS
	case "windows":
		return protocol.PlatformWindows
	case "linux":
		return protocol.PlatformLinux
	default:
		return protocol.PlatformUnknown
	}
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
