// Package discovery 负责在局域网内发现同类设备（peer）。
//
// 实现方式为 UDP 组播（multicast）：每台设备周期性地把自己的身份信息广播到
// 一个约定的组播地址，同时监听该地址收集其他设备的广播。这类似 mDNS/Bonjour
// 的"announce + browse"模型，但只依赖 Go 标准库，便于随 gomobile 一起打包进
// iOS / Android，无需额外的 mDNS 库或系统服务。
//
// 注意：iOS 14+ 访问本地网络（含组播）需要在 Info.plist 声明
// NSLocalNetworkUsageDescription 与 NSBonjourServices，并会弹窗请求用户授权；
// Android 使用组播需持有 CHANGE_WIFI_MULTICAST_STATE 并获取 MulticastLock。
// 这些属于平台集成层的职责，本包只负责协议与收发。
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/xurenhe/hopdrop/core/protocol"
)

const (
	// multicastGroup 是本应用约定的 IPv4 组播地址（239.x 为管理性局域网组播段）。
	multicastGroup = "239.192.71.71"
	// multicastPort 是发现用的 UDP 端口。
	multicastPort = 47771
	// defaultInterval 是两次广播之间的间隔。
	defaultInterval = 3 * time.Second
	// peerTTL 是一个 peer 在多久未再收到广播后被视为离线。
	peerTTL = 12 * time.Second
	// magic 用于快速过滤非本应用的 UDP 包。
	magic = "PSYNC1"
)

// announcement 是广播到组播地址的 UDP 负载。
type announcement struct {
	Magic  string              `json:"m"`
	Device protocol.DeviceInfo `json:"d"`
}

// Peer 是发现到的一台远端设备。
type Peer struct {
	Device protocol.DeviceInfo
	// Addr 是对方的 IP 地址（来自 UDP 源地址），与 Device.SyncPort 组合即为其同步端点。
	Addr string
	// LastSeen 是最近一次收到其广播的时间。
	LastSeen time.Time
}

// SyncEndpoint 返回可直接用于 TCP 拨号的 "host:port"。
func (p Peer) SyncEndpoint() string {
	return net.JoinHostPort(p.Addr, fmt.Sprintf("%d", p.Device.SyncPort))
}

// PeerEvent 描述一次 peer 变化事件，供上层（UI / gomobile 回调）消费。
type PeerEvent struct {
	// Online 为 true 表示该 peer 上线或信息更新，false 表示离线（超时）。
	Online bool
	Peer   Peer
}

// Discoverer 在局域网内持续广播自身并发现其他设备。
//
// 生命周期由 Start/Stop 控制。发现到的 peer 变化通过 Events 通道推送；
// 也可随时调用 Peers 获取当前在线快照。
type Discoverer struct {
	self     protocol.DeviceInfo
	interval time.Duration

	mu    sync.Mutex
	peers map[string]Peer // key: device ID
	conn  *net.UDPConn

	events chan PeerEvent
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New 构造一个 Discoverer。self 为本设备身份（其中 SyncPort 必须为
// 实际监听的 TCP 同步端口，供对方回连）。
func New(self protocol.DeviceInfo) *Discoverer {
	return &Discoverer{
		self:     self,
		interval: defaultInterval,
		peers:    make(map[string]Peer),
		events:   make(chan PeerEvent, 32),
	}
}

// Events 返回 peer 上/下线事件通道。Stop 后通道会被关闭。
func (d *Discoverer) Events() <-chan PeerEvent { return d.events }

// Start 打开组播套接字并启动收发与过期清理循环。
func (d *Discoverer) Start() error {
	gaddr := &net.UDPAddr{IP: net.ParseIP(multicastGroup), Port: multicastPort}

	// 监听组播地址；ListenMulticastUDP 会自动加入组播组并允许地址复用，
	// 从而支持同一台机器上跑多个虚拟设备（CLI 联调）。
	conn, err := net.ListenMulticastUDP("udp4", nil, gaddr)
	if err != nil {
		return fmt.Errorf("discovery: listen multicast: %w", err)
	}
	_ = conn.SetReadBuffer(1 << 20)
	d.conn = conn

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	d.wg.Add(3)
	go d.announceLoop(ctx, gaddr)
	go d.receiveLoop(ctx)
	go d.expireLoop(ctx)
	return nil
}

// Stop 停止所有循环、关闭套接字与事件通道。可安全多次调用。
func (d *Discoverer) Stop() {
	d.mu.Lock()
	if d.cancel == nil {
		d.mu.Unlock()
		return
	}
	cancel := d.cancel
	d.cancel = nil
	conn := d.conn
	d.mu.Unlock()

	cancel()
	if conn != nil {
		_ = conn.Close()
	}
	d.wg.Wait()
	close(d.events)
}

// Peers 返回当前在线 peer 的快照。
func (d *Discoverer) Peers() []Peer {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Peer, 0, len(d.peers))
	for _, p := range d.peers {
		out = append(out, p)
	}
	return out
}

func (d *Discoverer) announceLoop(ctx context.Context, gaddr *net.UDPAddr) {
	defer d.wg.Done()
	// 单独开一条 UDP 连接用于发送，避免与接收套接字相互干扰。
	out, err := net.DialUDP("udp4", nil, gaddr)
	if err != nil {
		return
	}
	defer out.Close()

	msg, _ := json.Marshal(announcement{Magic: magic, Device: d.self})
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	// 立即广播一次，让新加入的设备尽快被发现。
	_, _ = out.Write(msg)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = out.Write(msg)
		}
	}
}

func (d *Discoverer) receiveLoop(ctx context.Context) {
	defer d.wg.Done()
	buf := make([]byte, 64<<10)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = d.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, src, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		var a announcement
		if err := json.Unmarshal(buf[:n], &a); err != nil || a.Magic != magic {
			continue
		}
		// 忽略自己发出的广播。
		if a.Device.ID == d.self.ID {
			continue
		}
		d.upsertPeer(a.Device, src.IP.String())
	}
}

func (d *Discoverer) upsertPeer(dev protocol.DeviceInfo, addr string) {
	now := time.Now()
	d.mu.Lock()
	_, existed := d.peers[dev.ID]
	p := Peer{Device: dev, Addr: addr, LastSeen: now}
	d.peers[dev.ID] = p
	d.mu.Unlock()

	if !existed {
		d.emit(PeerEvent{Online: true, Peer: p})
	}
}

func (d *Discoverer) expireLoop(ctx context.Context) {
	defer d.wg.Done()
	ticker := time.NewTicker(peerTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			var expired []Peer
			d.mu.Lock()
			for id, p := range d.peers {
				if now.Sub(p.LastSeen) > peerTTL {
					expired = append(expired, p)
					delete(d.peers, id)
				}
			}
			d.mu.Unlock()
			for _, p := range expired {
				d.emit(PeerEvent{Online: false, Peer: p})
			}
		}
	}
}

// emit 非阻塞地投递事件；通道满时丢弃最旧策略改为直接丢弃，避免阻塞收发循环。
func (d *Discoverer) emit(ev PeerEvent) {
	select {
	case d.events <- ev:
	default:
	}
}
