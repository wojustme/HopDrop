package discovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"

	"github.com/xurenhe/hopdrop/core/protocol"
)

// serviceName 是 HopDrop 在 mDNS/DNS-SD 里注册的服务类型。标准 Bonjour 命名，
// iOS(NWBrowser)/Android(NsdManager)/桌面(本包)都用同一个名字，从而互相发现。
const serviceName = "_hopdrop._tcp"

// MDNSBackend 用标准 mDNS/DNS-SD 做设备发现：注册一条 _hopdrop._tcp 服务广播自身，
// 并周期性浏览同类服务，把设备身份和 TLS 指纹放在 TXT 记录里。
//
// 相比自研组播，它是业界标准协议：能与 Apple Bonjour、Android NsdManager 直接互通，
// 过路由也更稳。用于桌面端（macOS/Windows/Linux）。
type MDNSBackend struct {
	self     protocol.DeviceInfo
	interval time.Duration

	mu    sync.Mutex
	peers map[string]Peer // key: device ID
	srv   *mdns.Server

	events chan PeerEvent
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewMDNS 构造一个 mDNS 发现后端。SyncPort 大于零时同时发布服务；为零时只浏览。
func NewMDNS(self protocol.DeviceInfo) *MDNSBackend {
	return &MDNSBackend{
		self:     self,
		interval: defaultInterval,
		peers:    make(map[string]Peer),
		events:   make(chan PeerEvent, 32),
	}
}

// Events 返回 peer 上/下线事件通道。
func (m *MDNSBackend) Events() <-chan PeerEvent { return m.events }

// Start 注册本机服务并启动浏览与过期清理循环。
func (m *MDNSBackend) Start() error {
	// A client-only node browses without advertising a fake port.
	if m.self.SyncPort > 0 {
		svc, err := mdns.NewMDNSService(
			m.self.ID, serviceName, "", "", m.self.SyncPort, nil, m.txtRecords(),
		)
		if err != nil {
			return fmt.Errorf("discovery(mdns): new service: %w", err)
		}
		srv, err := mdns.NewServer(&mdns.Config{Zone: svc})
		if err != nil {
			return fmt.Errorf("discovery(mdns): new server: %w", err)
		}
		m.srv = srv
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	m.wg.Add(2)
	go m.browseLoop(ctx)
	go m.expireLoop(ctx)
	return nil
}

// Stop 注销服务、停止循环并关闭事件通道。
func (m *MDNSBackend) Stop() {
	m.mu.Lock()
	if m.cancel == nil {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	m.cancel = nil
	srv := m.srv
	m.srv = nil
	m.mu.Unlock()

	cancel()
	if srv != nil {
		_ = srv.Shutdown()
	}
	m.wg.Wait()
	close(m.events)
}

// Peers 返回当前在线 peer 快照。
func (m *MDNSBackend) Peers() []Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Peer, 0, len(m.peers))
	for _, p := range m.peers {
		out = append(out, p)
	}
	return out
}

// txtRecords 把本机身份编码成 DNS-SD TXT 记录（key=value）。
func (m *MDNSBackend) txtRecords() []string {
	return []string{
		"id=" + m.self.ID,
		"name=" + m.self.Name,
		"platform=" + string(m.self.Platform),
		"fingerprint=" + m.self.Fingerprint,
	}
}

// browseLoop 周期性浏览 _hopdrop._tcp，把发现到的服务解析成 Peer 并 upsert。
func (m *MDNSBackend) browseLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	m.browseOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.browseOnce(ctx)
		}
	}
}

func (m *MDNSBackend) browseOnce(ctx context.Context) {
	entries := make(chan *mdns.ServiceEntry, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			m.handleEntry(e)
		}
	}()

	params := mdns.DefaultParams(serviceName)
	params.Entries = entries
	params.Timeout = m.interval - 200*time.Millisecond
	if params.Timeout <= 0 {
		params.Timeout = time.Second
	}
	_ = mdns.Query(params)
	close(entries)
	<-done
}

// handleEntry 把一条 mDNS 服务记录转成 Peer；忽略自己与信息不全的记录。
func (m *MDNSBackend) handleEntry(e *mdns.ServiceEntry) {
	if e == nil || e.Port == 0 {
		return
	}
	address := e.AddrV4
	if address == nil || address.IsLinkLocalUnicast() {
		address = e.AddrV6
	}
	// ServiceEntry does not carry an IPv6 interface zone. A link-local IPv6
	// literal without that zone cannot be dialed, so wait for a usable address.
	if address == nil || address.IsLinkLocalUnicast() {
		return
	}
	id, name, platform, fingerprint := parseTXT(e.InfoFields)
	if id == "" {
		// 退回用实例名（我们注册时实例名就是设备 ID）。
		id = trimInstance(e.Name)
	}
	if id == "" || id == m.self.ID {
		return
	}
	if err := protocol.ValidateFingerprint(fingerprint); err != nil {
		return
	}
	dev := protocol.DeviceInfo{
		ID:          id,
		Name:        name,
		Platform:    protocol.Platform(platform),
		SyncPort:    e.Port,
		Fingerprint: fingerprint,
	}
	if dev.Name == "" {
		dev.Name = id
	}
	m.upsert(dev, address.String())
}

func (m *MDNSBackend) upsert(dev protocol.DeviceInfo, addr string) {
	now := time.Now()
	m.mu.Lock()
	previous, existed := m.peers[dev.ID]
	p := Peer{Device: dev, Addr: addr, LastSeen: now}
	m.peers[dev.ID] = p
	m.mu.Unlock()

	if !existed || previous.Device != dev || previous.Addr != addr {
		m.emit(PeerEvent{Online: true, Peer: p})
	}
}

func (m *MDNSBackend) expireLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(peerTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			var expired []Peer
			m.mu.Lock()
			for id, p := range m.peers {
				if now.Sub(p.LastSeen) > peerTTL {
					expired = append(expired, p)
					delete(m.peers, id)
				}
			}
			m.mu.Unlock()
			for _, p := range expired {
				m.emit(PeerEvent{Online: false, Peer: p})
			}
		}
	}
}

func (m *MDNSBackend) emit(ev PeerEvent) {
	select {
	case m.events <- ev:
	default:
	}
}

func parseTXT(fields []string) (id, name, platform, fingerprint string) {
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "id":
			id = v
		case "name":
			name = v
		case "platform":
			platform = v
		case "fingerprint":
			fingerprint = v
		}
	}
	return
}

// trimInstance 从完整实例名（"<id>._hopdrop._tcp.local."）里截出实例前缀。
func trimInstance(fqdn string) string {
	i := strings.Index(fqdn, "."+serviceName)
	if i <= 0 {
		return ""
	}
	return fqdn[:i]
}
