// Package filestore 提供 HopDrop 文件传输两端的落地/取源抽象。
//
// 传输是定向的（发送方推送、接收方落地），因此这里有两个角色：
//
//   - Source（发送源）：把本地的文件或整个文件夹枚举成一批 protocol.FileMeta，
//     并能按会话内的文件 ID 打开对应内容以供流式发送。桌面端/CLI 直接用
//     NewLocalSource；移动端可由绑定层实现 Source 接口（如从 PhotoKit/MediaStore
//     取图）。
//
//   - Sink（接收落地）：把对端推送来的文件写入一个下载目录，按 FileMeta.RelPath
//     重建子目录结构，并做路径穿越防护与原子落盘。桌面端/CLI 直接用
//     NewDirSink；移动端可由绑定层实现 Sink 接口（写入相册/沙盒）。
//
// 同步引擎（core/sync）只依赖 Source / Sink 两个接口，不关心文件究竟来自哪里、
// 最终落到哪儿，从而做到"一份核心、多端共用"。
package filestore

import (
	"io"

	"github.com/xurenhe/hopdrop/core/protocol"
)

// MetaDir 是相册/工作目录下存放 HopDrop 元数据（设备 ID 等）的隐藏目录名。
// 导出以便 CLI / 桌面端复用同一约定，避免各处硬编码字符串。
const MetaDir = ".hopdrop"

// Source 表示一次发送的文件来源。实现需保证方法可被多个 goroutine 并发调用。
type Source interface {
	// List 返回本次要发送的全部文件元数据（不含内容）。每个 FileMeta.ID
	// 在本 Source 内唯一，Open 依据它定位内容。
	List() ([]protocol.FileMeta, error)

	// Open 打开给定 ID 的文件内容以供读取。调用方负责 Close。
	Open(fileID string) (io.ReadCloser, error)
}

// Sink 表示接收端的落地目标。实现需保证方法可被多个 goroutine 并发调用。
type Sink interface {
	// Create 把一个从对端拉取到的文件写入本地。
	// 实现应先落临时文件、成功后再原子改名，避免中断产生半份文件；
	// 并按 meta.RelPath 重建子目录、做路径穿越防护。
	Create(meta protocol.FileMeta, content io.Reader) error
}
