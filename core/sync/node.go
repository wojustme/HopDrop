package sync

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
)

// Node 把"设备发现 + 接收服务 + 主动发送"编排成一个开箱即用的整体，
// 是 CLI、桌面端（Fyne）与移动端绑定层共同复用的高层入口。
//
// 启动后 Node 会：
//   - 监听一个 TCP 端口，接受入站传输（收到 Offer 时通过 DecisionFunc 决策）；
//   - 通过可插拔的发现后端发现局域网内的其他设备（供上层选择发送目标）。
//
// 发现后端由 DiscoveryKind 选择：
//   - DiscoveryMulticast：自研 UDP 组播（默认，单元测试与老路径）；
//   - DiscoveryMDNS：标准 mDNS/DNS-SD（桌面端，能与 Bonjour/NsdManager 互通）；
//   - DiscoveryManual：不自行发现，peer 由外部（移动端原生 Bonjour）喂入。
//
// 发送由上层主动触发：调用 SendPaths / SendSource 把文件推送给某个 peer。
type Node struct {
	self     protocol.DeviceInfo
	sink     filestore.Sink
	discKind DiscoveryKind

	engine *Engine
	disc   discovery.Backend
	ln     net.Listener

	onProgress ProgressFunc
	onPeer     func(discovery.PeerEvent)
	decide     DecisionFunc

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// DiscoveryKind 选择 Node 使用的发现后端。
type DiscoveryKind int

const (
	// DiscoveryMulticast 使用自研 UDP 组播（默认）。
	DiscoveryMulticast DiscoveryKind = iota
	// DiscoveryMDNS 使用标准 mDNS/DNS-SD。
	DiscoveryMDNS
	// DiscoveryManual 不自行发现，peer 由外部喂入（AddPeer/RemovePeer）。
	DiscoveryManual
)

// NewNode 构造一个 Node。name/platform 描述本设备；sink 为接收落地目标
// （只发不收的场景可传 nil，但那样将拒绝入站文件）。默认使用组播发现，
// 需要其它后端时在 Start 前调用 SetDiscovery。
func NewNode(name string, platform protocol.Platform, deviceID string, sink filestore.Sink) *Node {
	return &Node{
		self: protocol.DeviceInfo{
			ID:       deviceID,
			Name:     name,
			Platform: platform,
		},
		sink:     sink,
		discKind: DiscoveryMulticast,
	}
}

// SetDiscovery 选择发现后端，必须在 Start 之前调用。
func (n *Node) SetDiscovery(kind DiscoveryKind) { n.discKind = kind }

// OnProgress 注册传输进度回调（可选）。
func (n *Node) OnProgress(fn ProgressFunc) { n.onProgress = fn }

// OnPeer 注册 peer 上/下线回调（可选）。
func (n *Node) OnPeer(fn func(discovery.PeerEvent)) { n.onPeer = fn }

// OnDecision 注册收到 Offer 时的决策回调（可选，nil 表示默认全部接受）。
// 必须在 Start 之前设置。
func (n *Node) OnDecision(fn DecisionFunc) { n.decide = fn }

// Self 返回本设备身份（Start 之后 SyncPort 会被填成实际端口）。
func (n *Node) Self() protocol.DeviceInfo { return n.self }

// Start 绑定 TCP 端口、启动接收服务与设备发现。port 传 0 表示由系统分配。
func (n *Node) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("sync: listen tcp: %w", err)
	}
	n.ln = ln
	n.self.SyncPort = ln.Addr().(*net.TCPAddr).Port

	n.engine = NewEngine(n.self, n.sink, n.decide)
	n.disc = n.newBackend()

	ctx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	n.cancel = cancel
	n.mu.Unlock()

	if err := n.disc.Start(); err != nil {
		cancel()
		_ = ln.Close()
		return err
	}

	n.wg.Add(2)
	go func() {
		defer n.wg.Done()
		_ = n.engine.Serve(ctx, ln, n.onProgress)
	}()
	go func() {
		defer n.wg.Done()
		n.discoveryLoop(ctx)
	}()
	return nil
}

// Stop 停止发现、关闭监听并等待后台 goroutine 退出。
func (n *Node) Stop() {
	n.mu.Lock()
	cancel := n.cancel
	n.cancel = nil
	n.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if n.disc != nil {
		n.disc.Stop()
	}
	if n.ln != nil {
		_ = n.ln.Close()
	}
	n.wg.Wait()
}

// Peers 返回当前在线 peer 快照。
func (n *Node) Peers() []discovery.Peer {
	if n.disc == nil {
		return nil
	}
	return n.disc.Peers()
}

// newBackend 依据 discKind 构造对应的发现后端。
func (n *Node) newBackend() discovery.Backend {
	switch n.discKind {
	case DiscoveryMDNS:
		return discovery.NewMDNS(n.self)
	case DiscoveryManual:
		return discovery.NewManual(n.self.ID)
	default:
		return discovery.New(n.self)
	}
}

// ManualDiscovery 返回底层的手动发现后端（仅当 DiscoveryManual 时非 nil），
// 供移动端把系统原生 Bonjour 发现到的 peer 喂进来。
func (n *Node) ManualDiscovery() *discovery.ManualBackend {
	if mb, ok := n.disc.(*discovery.ManualBackend); ok {
		return mb
	}
	return nil
}

// SendSource 把一个 filestore.Source 中的文件发送给指定 peer。
func (n *Node) SendSource(ctx context.Context, p discovery.Peer, src filestore.Source) error {
	return n.engine.Send(ctx, p.SyncEndpoint(), src, n.onProgress)
}

// SendPaths 把一批本地文件/文件夹发送给指定 peer（便捷封装）。
func (n *Node) SendPaths(ctx context.Context, p discovery.Peer, paths []string) error {
	src, err := filestore.NewLocalSource(paths)
	if err != nil {
		return err
	}
	return n.SendSource(ctx, p, src)
}

// SendToEndpoint 允许在没有发现记录时，直接向一个 "host:port" 端点发送。
func (n *Node) SendToEndpoint(ctx context.Context, endpoint string, paths []string) error {
	src, err := filestore.NewLocalSource(paths)
	if err != nil {
		return err
	}
	return n.engine.Send(ctx, endpoint, src, n.onProgress)
}

// SendToEndpointSource 直连一个 "host:port" 端点，把任意 Source 中的文件发送过去。
func (n *Node) SendToEndpointSource(ctx context.Context, endpoint string, src filestore.Source) error {
	return n.engine.Send(ctx, endpoint, src, n.onProgress)
}

func (n *Node) discoveryLoop(ctx context.Context) {
	events := n.disc.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if n.onPeer != nil {
				n.onPeer(ev)
			}
		}
	}
}
