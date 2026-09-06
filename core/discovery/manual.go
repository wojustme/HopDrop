package discovery

import (
	"sync"
	"time"

	"github.com/xurenhe/hopdrop/core/protocol"
)

// ManualBackend 是"被动喂入"的发现后端：自己不做任何组播/mDNS，peer 列表完全由外部
// （移动端各系统的原生 Bonjour：iOS NWBrowser / Android NsdManager）通过 AddPeer /
// RemovePeer 灌入。
//
// 这样移动端就把"发现"交给系统服务完成——iOS 因此无需 multicast 授权、只用本地网络
// 权限即可自动发现设备；而 TCP 传输引擎仍复用同一份 Go 核心。
type ManualBackend struct {
	selfID string

	mu    sync.Mutex
	peers map[string]Peer

	events  chan PeerEvent
	stopped bool
}

// NewManual 构造一个手动后端。selfID 用于过滤掉自己（原生偶尔会发现到本机服务）。
func NewManual(selfID string) *ManualBackend {
	return &ManualBackend{
		selfID: selfID,
		peers:  make(map[string]Peer),
		events: make(chan PeerEvent, 32),
	}
}

// Events 返回 peer 上/下线事件通道。
func (b *ManualBackend) Events() <-chan PeerEvent { return b.events }

// Start 无需启动任何后台循环。
func (b *ManualBackend) Start() error { return nil }

// Stop marks the backend inactive. The event channel stays immutable so native
// discovery callbacks racing with application shutdown cannot send on a
// closed channel.
func (b *ManualBackend) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
}

// Peers 返回当前在线 peer 快照。
func (b *ManualBackend) Peers() []Peer {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Peer, 0, len(b.peers))
	for _, p := range b.peers {
		out = append(out, p)
	}
	return out
}

// AddPeer 由原生发现层调用：新增/更新一个已解析出地址的 peer。
// addr 为对端 IPv4/IPv6 字符串，port 为其 TCP 同步端口。
func (b *ManualBackend) AddPeer(id, name, platform, fingerprint, addr string, port int) {
	if id == "" || id == b.selfID || protocol.ValidateFingerprint(fingerprint) != nil {
		return
	}
	dev := protocol.DeviceInfo{
		ID:          id,
		Name:        name,
		Platform:    protocol.Platform(platform),
		SyncPort:    port,
		Fingerprint: fingerprint,
	}
	if dev.Name == "" {
		dev.Name = id
	}
	p := Peer{Device: dev, Addr: addr, LastSeen: time.Now()}

	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	previous, existed := b.peers[id]
	b.peers[id] = p
	b.mu.Unlock()

	if !existed || previous.Device != dev || previous.Addr != addr {
		b.emit(PeerEvent{Online: true, Peer: p})
	}
}

// RemovePeer 由原生发现层调用：某个 peer 离线（服务消失）。
func (b *ManualBackend) RemovePeer(id string) {
	b.mu.Lock()
	p, existed := b.peers[id]
	if existed {
		delete(b.peers, id)
	}
	b.mu.Unlock()
	if existed {
		b.emit(PeerEvent{Online: false, Peer: p})
	}
}

// Clear 清空全部 peer（如网络切换、原生浏览器重启时）。
func (b *ManualBackend) Clear() {
	b.mu.Lock()
	old := b.peers
	b.peers = make(map[string]Peer)
	b.mu.Unlock()
	for _, p := range old {
		b.emit(PeerEvent{Online: false, Peer: p})
	}
}

func (b *ManualBackend) emit(ev PeerEvent) {
	b.mu.Lock()
	ch, stopped := b.events, b.stopped
	b.mu.Unlock()
	if stopped {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}
