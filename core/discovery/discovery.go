// Package discovery 负责在局域网内发现同类设备（peer）。
//
// 实现方式为 UDP 组播（multicast）：每台设备周期性地把自己的身份信息广播到
// 一个约定的组播地址，同时监听该地址收集其他设备的广播。这类似 mDNS/Bonjour
// 的“announce + browse”模型，但只依赖 Go 标准库 + golang.org/x/net/ipv4，
// 便于随 gomobile 一起打包进 iOS / Android，无需额外的 mDNS 库或系统服务。
//
// 关于多网卡（这是跨设备发现的关键）：
//
//	手机等设备通常同时有 Wi-Fi、蜂窝、VPN 等多个网络接口。若只用系统默认接口
//	收发组播（例如 net.DialUDP / ListenMulticastUDP 传 nil 接口），实际可能走到
//	蜂窝或 VPN，导致同一 Wi-Fi 下的设备互相发现不到。因此这里显式枚举所有“已启用、
//	支持组播、非回环”的接口，在每个接口上都加入组播组（收）并逐个发出广播（发），
//	确保覆盖到 Wi-Fi 局域网。
//
// 注意：iOS 14+ 收发组播需要 com.apple.developer.networking.multicast 授权
// （需 Apple 人工审批）并在 Info.plist 声明 NSLocalNetworkUsageDescription /
// NSBonjourServices；Android 使用组播需持有 CHANGE_WIFI_MULTICAST_STATE 并
// 获取 MulticastLock。这些属于平台集成层的职责，本包只负责协议与收发。
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/net/ipv4"

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
	gaddr    *net.UDPAddr

	mu    sync.Mutex
	peers map[string]Peer // key: device ID
	conn  *net.UDPConn
	pconn *ipv4.PacketConn

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
	d.gaddr = &net.UDPAddr{IP: net.ParseIP(multicastGroup), Port: multicastPort}

	// 监听组播端口；ListenMulticastUDP 会设置 SO_REUSEADDR 并允许地址复用，
	// 从而支持同一台机器上跑多个虚拟设备（CLI 联调）。传 nil 接口时它只在系统
	// 默认接口上加入组播组，下面再用 ipv4.PacketConn 显式补齐所有接口。
	conn, err := net.ListenMulticastUDP("udp4", nil, d.gaddr)
	if err != nil {
		return fmt.Errorf("discovery: listen multicast: %w", err)
	}
	_ = conn.SetReadBuffer(1 << 20)
	d.conn = conn

	// 用 ipv4.PacketConn 精细控制组播：在所有合格接口上加入组播组（保证多网卡设备
	// 无论组播从哪个 NIC 到达都能收到），并开启回环（同机多进程联调需要）。
	// 注意：接收仍走下面的 conn.ReadFromUDP —— 它能可靠响应 SetReadDeadline，
	// 便于 Stop 时干净退出；ipv4.PacketConn 仅用于加入组播组与按接口发送。
	p := ipv4.NewPacketConn(conn)
	_ = p.SetMulticastLoopback(true)
	for _, ifi := range multicastInterfaces() {
		// 部分接口可能已由 ListenMulticastUDP 加入过，重复加入的报错可忽略。
		_ = p.JoinGroup(&ifi, &net.UDPAddr{IP: d.gaddr.IP})
	}
	d.pconn = p

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	d.wg.Add(3)
	go d.announceLoop(ctx)
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

// announceLoop 周期性地向所有合格接口发出自身广播。
func (d *Discoverer) announceLoop(ctx context.Context) {
	defer d.wg.Done()
	msg, _ := json.Marshal(announcement{Magic: magic, Device: d.self})
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	// 立即广播一次，让新加入的设备尽快被发现。
	d.broadcast(msg)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.broadcast(msg)
		}
	}
}

// broadcast 逐个接口把 msg 发到组播组。每次都重新枚举接口，以适应 Wi-Fi 重连、
// 热点开关等网络变化；同时兜底再发一次“默认接口”（cm 为 nil）。
func (d *Discoverer) broadcast(msg []byte) {
	ifaces := multicastInterfaces()
	sent := false
	for i := range ifaces {
		ifi := ifaces[i]
		if err := d.pconn.SetMulticastInterface(&ifi); err != nil {
			continue
		}
		if _, err := d.pconn.WriteTo(msg, nil, d.gaddr); err == nil {
			sent = true
		}
	}
	if !sent {
		// 没有可用接口时，退回系统默认路由再试一次。
		_, _ = d.pconn.WriteTo(msg, nil, d.gaddr)
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
		// 用底层 UDPConn 收包：它能可靠响应 SetReadDeadline，便于 Stop 干净退出。
		// 组播组已通过 ipv4.PacketConn 在各接口加入，收包不受影响。
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
		addr := ""
		if src != nil {
			addr = src.IP.String()
		}
		d.upsertPeer(a.Device, addr)
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

// emit 非阻塞地投递事件；通道满时直接丢弃，避免阻塞收发循环。
func (d *Discoverer) emit(ev PeerEvent) {
	select {
	case d.events <- ev:
	default:
	}
}

// multicastInterfaces 返回所有“已启用、支持组播、非回环、且带 IPv4 地址”的网络接口。
// 这是保证多网卡（Wi-Fi/蜂窝/VPN 并存）设备能在正确的局域网上收发组播的关键。
func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 ||
			ifi.Flags&net.FlagMulticast == 0 ||
			ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if !hasIPv4(ifi) {
			continue
		}
		out = append(out, ifi)
	}
	return out
}

// hasIPv4 判断接口是否配置了至少一个 IPv4 地址。
func hasIPv4(ifi net.Interface) bool {
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil && ip.To4() != nil {
			return true
		}
	}
	return false
}

// LocalIPv4s 返回本机所有“已启用、非回环”接口上的 IPv4 地址（点分十进制），
// 供上层做手动配对（展示 IP / 生成二维码）。私网地址（192.168/10/172.16-31）
// 会排在最前面，因为它们通常才是同一 Wi-Fi 局域网里对端可达的地址。
func LocalIPv4s() []string {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var priv, other []string
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
				continue
			}
			if v4.IsPrivate() {
				priv = append(priv, v4.String())
			} else {
				other = append(other, v4.String())
			}
		}
	}
	return append(priv, other...)
}

