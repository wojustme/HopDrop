// Package mobile 是 HopDrop 面向移动端（Android / iOS）的 gomobile 绑定层。
//
// gomobile 对可导出的 API 有严格限制：只能导出方法接收者为具名结构体指针、参数与
// 返回值为基础类型（bool/int/int64/string/[]byte/error）或同样受支持的具名类型，
// 不支持切片-of-struct、map、channel、可变参数、函数类型（回调需用接口）等。
//
// 因此这里把 core/sync.Node 的能力收敛成一个窄接口 Client：
//   - 用 JSON 字符串在 Go 与 Java/Swift 之间传递结构化数据（设备列表、Offer 明细），
//     避免暴露复杂 Go 结构体给 gomobile。
//   - 用 Callback 接口把 peer 变化、收到 Offer、进度、传输结果回调给宿主 App，
//     宿主实现该接口即可（Java 实现 interface、Swift 实现 protocol）。
//   - 接收落地与发送取源由宿主通过 Sink / Source 接口提供，从而对接
//     iOS PhotoKit / Android MediaStore 或应用沙盒目录。
//
// 构建产物：
//
//	Android:  gomobile bind -target=android -o hopdrop.aar        ./mobile
//	iOS:      gomobile bind -target=ios     -o HopDrop.xcframework ./mobile
package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/xurenhe/hopdrop/core/device"
	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
	syncpkg "github.com/xurenhe/hopdrop/core/sync"
)

// Callback 是宿主 App 需要实现的事件回调接口（gomobile 会把它映射成
// Java interface / Swift protocol）。所有回调可能在 Go 的后台 goroutine 中触发，
// 宿主需自行切回主线程再更新 UI。
type Callback interface {
	// OnPeers 在在线设备列表变化时被调用，peersJSON 是 []PeerJSON 的 JSON 编码。
	OnPeers(peersJSON string)
	// OnOffer 在收到一次传输请求时被调用，offerJSON 是 OfferJSON 的 JSON 编码。
	// 宿主应据此弹窗，然后调用 Client.Respond(offerID, accept) 作答。
	OnOffer(offerID string, offerJSON string)
	// OnProgress 在传输进度更新时被调用，progressJSON 是 ProgressJSON 的 JSON 编码。
	OnProgress(progressJSON string)
}

// Sink 是宿主实现的接收落地接口，用于把收到的文件写入相册/沙盒。
// gomobile 会把它映射成 Java interface / Swift protocol。
//
// Open 返回一个宿主侧的可写句柄；HopDrop 会把文件内容分块写入，最后调用 Close。
type Sink interface {
	// OpenWrite 依据文件元数据（JSON 编码的 FileMetaJSON）创建一个可写目标，
	// 返回一个用于后续写入的句柄 ID（宿主自定义，只要能在 Write/Close 里对应回来即可）。
	OpenWrite(metaJSON string) (handle string, err error)
	// Write 向句柄追加一段字节。
	Write(handle string, data []byte) error
	// Close 关闭句柄，完成落地（error 非 nil 表示失败，应清理半份文件）。
	Close(handle string) error
}

// Source 是宿主实现的发送取源接口，用于从相册/沙盒读取要发送的文件。
type Source interface {
	// ListJSON 返回本次要发送文件的 []FileMetaJSON 的 JSON 编码。
	ListJSON() (string, error)
	// OpenRead 打开给定文件 ID 的一个读句柄。
	OpenRead(fileID string) (handle string, err error)
	// Read 从句柄读取最多 len(p) 字节到 p，返回读到的字节数；读到结尾返回 (0, nil)。
	Read(handle string, p []byte) (n int, err error)
	// CloseRead 关闭读句柄。
	CloseRead(handle string) error
}

// Client 是移动端使用 HopDrop 的唯一入口。用 NewClient 创建，Start 启动，Stop 结束。
type Client struct {
	node *syncpkg.Node
	cb   Callback

	mu       sync.Mutex
	offers   map[string]chan protocol.Decision // offerID -> 决策等待通道
	offerSeq int
}

// NewClient 创建一个移动端客户端。
//
//	name       设备展示名（如 "小明的 iPhone"）。
//	platform   "ios" 或 "android"。
//	idDir      一个应用沙盒内可写目录，用于持久化稳定设备 ID。
//	sink       宿主提供的接收落地实现（收到文件时写入）。
//	cb         事件回调。
func NewClient(name, platform, idDir string, sink Sink, cb Callback) (*Client, error) {
	deviceID, err := device.LoadOrCreateID(idDir + "/" + filestore.MetaDir + "_device_id")
	if err != nil {
		return nil, err
	}
	c := &Client{
		cb:     cb,
		offers: make(map[string]chan protocol.Decision),
	}
	c.node = syncpkg.NewNode(name, platformOf(platform), deviceID, &sinkAdapter{sink: sink})
	c.node.OnPeer(func(discovery.PeerEvent) { c.emitPeers() })
	c.node.OnDecision(c.handleOffer)
	c.node.OnProgress(func(p syncpkg.Progress) { c.emitProgress(p) })
	return c, nil
}

// Start 启动监听与发现。port 传 0 表示自动分配。
func (c *Client) Start(port int) error {
	if err := c.node.Start(port); err != nil {
		return err
	}
	c.emitPeers()
	return nil
}

// Stop 停止客户端。
func (c *Client) Stop() { c.node.Stop() }

// SelfJSON 返回本机身份的 JSON（DeviceInfo）。
func (c *Client) SelfJSON() string {
	b, _ := json.Marshal(toPeerDevice(c.node.Self()))
	return string(b)
}

// PeersJSON 主动返回当前在线设备列表（[]PeerJSON 的 JSON 编码）。
func (c *Client) PeersJSON() string { return c.peersJSON() }

// Respond 由宿主在收到 OnOffer 并让用户决定后调用，accept 表示是否接收。
func (c *Client) Respond(offerID string, accept bool) {
	c.mu.Lock()
	ch := c.offers[offerID]
	delete(c.offers, offerID)
	c.mu.Unlock()
	if ch != nil {
		ch <- protocol.Decision{Accept: accept}
	}
}

// SendToDevice 把 src 中的文件发送给设备 ID 为 deviceID 的在线 peer。
func (c *Client) SendToDevice(deviceID string, src Source) error {
	var target *discovery.Peer
	for _, p := range c.node.Peers() {
		if p.Device.ID == deviceID {
			pp := p
			target = &pp
			break
		}
	}
	if target == nil {
		return fmt.Errorf("mobile: device %s not online", deviceID)
	}
	return c.node.SendSource(context.Background(), *target, &sourceAdapter{src: src})
}

// SendToEndpoint 直连一个 "host:port" 端点发送（用于扫码/手动输入地址等场景）。
func (c *Client) SendToEndpoint(endpoint string, src Source) error {
	// 复用 Node 的发送路径需要 peer；这里直接借助引擎的端点发送封装。
	return c.node.SendToEndpointSource(context.Background(), endpoint, &sourceAdapter{src: src})
}

// ---- 内部：事件与回调 ----

func (c *Client) handleOffer(peer protocol.DeviceInfo, offer protocol.Offer) protocol.Decision {
	c.mu.Lock()
	c.offerSeq++
	offerID := fmt.Sprintf("offer-%d", c.offerSeq)
	ch := make(chan protocol.Decision, 1)
	c.offers[offerID] = ch
	c.mu.Unlock()

	if c.cb != nil {
		oj := offerJSON{
			Peer:       toPeerDevice(peer),
			TotalBytes: offer.TotalBytes,
		}
		for _, f := range offer.Files {
			oj.Files = append(oj.Files, toFileMeta(f))
		}
		b, _ := json.Marshal(oj)
		c.cb.OnOffer(offerID, string(b))
	} else {
		// 无回调时默认接受，避免卡死。
		return protocol.Decision{Accept: true}
	}
	return <-ch
}

func (c *Client) emitPeers() {
	if c.cb != nil {
		c.cb.OnPeers(c.peersJSON())
	}
}

func (c *Client) peersJSON() string {
	peers := c.node.Peers()
	out := make([]peerJSON, 0, len(peers))
	for _, p := range peers {
		out = append(out, peerJSON{
			Device:   toPeerDevice(p.Device),
			Addr:     p.Addr,
			Endpoint: p.SyncEndpoint(),
		})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (c *Client) emitProgress(p syncpkg.Progress) {
	if c.cb == nil {
		return
	}
	pj := progressJSON{
		Direction:   p.Direction,
		PeerID:      p.PeerID,
		PeerName:    p.PeerName,
		Phase:       p.Phase,
		CurrentName: p.CurrentName,
		Files:       p.Files,
		TotalFiles:  p.TotalFiles,
		Bytes:       p.Bytes,
		TotalBytes:  p.TotalBytes,
		Err:         p.Err,
	}
	b, _ := json.Marshal(pj)
	c.cb.OnProgress(string(b))
}

func platformOf(s string) protocol.Platform {
	switch s {
	case "ios":
		return protocol.PlatformIOS
	case "android":
		return protocol.PlatformAndroid
	default:
		return protocol.PlatformUnknown
	}
}

// ---- 宿主 Sink/Source 到核心接口的适配 ----

// sinkAdapter 把宿主的分块 Sink 适配成 filestore.Sink（流式 io.Reader）。
type sinkAdapter struct{ sink Sink }

func (a *sinkAdapter) Create(meta protocol.FileMeta, content io.Reader) error {
	mb, _ := json.Marshal(toFileMeta(meta))
	handle, err := a.sink.OpenWrite(string(mb))
	if err != nil {
		return err
	}
	buf := make([]byte, 128<<10)
	for {
		n, rerr := content.Read(buf)
		if n > 0 {
			if werr := a.sink.Write(handle, buf[:n]); werr != nil {
				_ = a.sink.Close(handle)
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = a.sink.Close(handle)
			return rerr
		}
	}
	return a.sink.Close(handle)
}

// sourceAdapter 把宿主的分块 Source 适配成 filestore.Source。
type sourceAdapter struct{ src Source }

func (a *sourceAdapter) List() ([]protocol.FileMeta, error) {
	js, err := a.src.ListJSON()
	if err != nil {
		return nil, err
	}
	var metas []fileMetaJSON
	if err := json.Unmarshal([]byte(js), &metas); err != nil {
		return nil, err
	}
	out := make([]protocol.FileMeta, 0, len(metas))
	for _, m := range metas {
		out = append(out, fromFileMeta(m))
	}
	return out, nil
}

func (a *sourceAdapter) Open(fileID string) (io.ReadCloser, error) {
	handle, err := a.src.OpenRead(fileID)
	if err != nil {
		return nil, err
	}
	return &sourceReader{src: a.src, handle: handle}, nil
}

// sourceReader 把宿主的 Read(handle, p) 适配成 io.ReadCloser。
type sourceReader struct {
	src    Source
	handle string
}

func (r *sourceReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(r.handle, p)
	if err != nil {
		return n, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (r *sourceReader) Close() error { return r.src.CloseRead(r.handle) }
