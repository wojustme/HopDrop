package discovery

// Backend 是"设备发现"的抽象后端：负责广播自身 + 发现他人，并通过事件通道推送变化。
//
// HopDrop 有三种后端，传输引擎（TCP）在其之上完全复用：
//   - Discoverer（multicast.go 里的自研 UDP 组播）：默认，用于单元测试与老路径；
//   - MDNSBackend（mdns.go，基于标准 mDNS/DNS-SD）：桌面端 macOS/Windows/Linux；
//   - ManualBackend（manual.go）：移动端——发现改由各系统原生 Bonjour（iOS NWBrowser /
//     Android NsdManager）完成，原生把发现到的 peer 通过 AddPeer/RemovePeer 喂进来。
//
// 为什么移动端不在 Go 里做 mDNS：iOS 对 App 自开的 UDP 组播需要 multicast 授权
// （需 Apple 审批），而走系统 Bonjour API（NWBrowser）只需本地网络权限即可。
type Backend interface {
	// Start 启动广播与发现。
	Start() error
	// Stop 停止并释放资源。可安全多次调用。
	Stop()
	// Peers 返回当前在线 peer 快照。
	Peers() []Peer
	// Events 返回 peer 上/下线事件通道。Stop 后通道会被关闭。
	Events() <-chan PeerEvent
}

// 确保三种后端都满足 Backend。
var (
	_ Backend = (*Discoverer)(nil)
	_ Backend = (*MDNSBackend)(nil)
	_ Backend = (*ManualBackend)(nil)
)
