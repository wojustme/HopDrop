// Package sync 实现了 HopDrop 设备之间的定向文件传输会话。
//
// 会话建立在一条 TCP 连接之上，由发送方主动发起（Dial/Send），接收方（Serve）
// 先把对方给出的文件清单交给上层决策（接受/拒绝/部分接受），随后接收方把被接受
// 的文件流式落地。协议细节见 core/protocol。
//
// 典型用法：
//   - 接收端调用 Serve，在 TCP 监听器上等待入站会话，并用 DecisionFunc 决定是否接收。
//   - 发送端调用 Send，主动连到对方的传输端点并推送一批文件（来自一个 filestore.Source）。
package sync

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
)

// Progress 描述一次传输的实时进度，用于回调给 UI。
type Progress struct {
	// Direction 是本端在该进度里的角色："send" 或 "recv"。
	Direction string
	// PeerID / PeerName 是对端设备。
	PeerID   string
	PeerName string
	// Phase 是当前阶段：
	// "handshake" / "offer" / "transfer" / "done" / "rejected" / "error"。
	Phase string
	// CurrentName 是当前正在传输的文件名（transfer 阶段有效）。
	CurrentName string
	// Files / TotalFiles 是已完成/总文件数。
	Files      int
	TotalFiles int
	// Bytes / TotalBytes 是已传输/总字节数。
	Bytes      int64
	TotalBytes int64
	// Err 在 Phase 为 "error" 时给出错误信息。
	Err string
}

// ProgressFunc 是进度回调。可为 nil。
type ProgressFunc func(Progress)

// DecisionFunc 在接收端收到一次 Offer 后被调用，返回是否接收（可部分接收）。
// 若为 nil，Engine 默认接受全部文件。回调可能在后台 goroutine 中被调用，
// 实现方需自行处理线程/UI 调度。
type DecisionFunc func(peer protocol.DeviceInfo, offer protocol.Offer) protocol.Decision

// Engine 承载一台设备的传输能力：它知道本设备身份、接收落地目标（Sink）与
// 收到 Offer 时的决策回调，并能作为发送端主动推送文件。
type Engine struct {
	self   protocol.DeviceInfo
	sink   filestore.Sink
	decide DecisionFunc
}

// NewEngine 构造传输引擎。self 为本设备身份；sink 为接收落地目标（发送-only 场景可为 nil）；
// decide 为收到 Offer 时的决策回调（nil 表示默认全部接受）。
func NewEngine(self protocol.DeviceInfo, sink filestore.Sink, decide DecisionFunc) *Engine {
	return &Engine{self: self, sink: sink, decide: decide}
}

// Serve 在给定监听器上循环接受入站连接，每条连接起一个 goroutine 处理接收会话。
// 阻塞直到 ln 关闭或 ctx 取消。
func (e *Engine) Serve(ctx context.Context, ln net.Listener, onProgress ProgressFunc) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("sync: accept: %w", err)
			}
		}
		go func() {
			defer conn.Close()
			if err := e.runReceive(ctx, conn, onProgress); err != nil {
				report(onProgress, Progress{Direction: "recv", Phase: "error", Err: err.Error()})
			}
		}()
	}
}

// Send 主动连接对端传输端点（host:port），把 src 中的文件推送过去。
func (e *Engine) Send(ctx context.Context, endpoint string, src filestore.Source, onProgress ProgressFunc) error {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return fmt.Errorf("sync: dial %s: %w", endpoint, err)
	}
	defer conn.Close()
	return e.runSend(ctx, conn, src, onProgress)
}

// runSend 执行发送端的一次完整会话：握手 → 给出清单 → 等待决定 → 推送被接受的文件。
func (e *Engine) runSend(ctx context.Context, conn net.Conn, src filestore.Source, onProgress ProgressFunc) error {
	w := protocol.NewWriter(conn)
	r := protocol.NewReader(conn)

	// ---- 1. 握手 ----
	report(onProgress, Progress{Direction: "send", Phase: "handshake"})
	if err := w.WriteControl(protocol.MsgHello, protocol.Hello{
		Version: protocol.ProtocolVersion,
		Device:  e.self,
		Role:    "sender",
	}); err != nil {
		return err
	}
	peer, err := e.readHello(r)
	if err != nil {
		return err
	}

	// ---- 2. 给出文件清单（Offer） ----
	files, err := src.List()
	if err != nil {
		return fmt.Errorf("sync: list source: %w", err)
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	report(onProgress, Progress{
		Direction: "send", Phase: "offer", PeerID: peer.ID, PeerName: peer.Name,
		TotalFiles: len(files), TotalBytes: total,
	})
	if err := w.WriteControl(protocol.MsgOffer, protocol.Offer{Files: files, TotalBytes: total}); err != nil {
		return err
	}

	// ---- 3. 等待接收方决定 ----
	decision, err := e.readDecision(r)
	if err != nil {
		return err
	}
	if !decision.Accept {
		report(onProgress, Progress{
			Direction: "send", Phase: "rejected", PeerID: peer.ID, PeerName: peer.Name,
			Err: decision.Reason,
		})
		_ = w.WriteControl(protocol.MsgBye, nil)
		return nil
	}

	// 计算实际要发送的文件集合（nil AcceptIDs 表示全部接受）。
	accepted := files
	if decision.AcceptIDs != nil {
		want := make(map[string]struct{}, len(decision.AcceptIDs))
		for _, id := range decision.AcceptIDs {
			want[id] = struct{}{}
		}
		accepted = accepted[:0]
		for _, f := range files {
			if _, ok := want[f.ID]; ok {
				accepted = append(accepted, f)
			}
		}
	}
	var acceptBytes int64
	for _, f := range accepted {
		acceptBytes += f.Size
	}

	// ---- 4. 逐个推送被接受的文件 ----
	var sentFiles int
	var sentBytes int64
	for _, f := range accepted {
		rc, err := src.Open(f.ID)
		if err != nil {
			// 本地打不开（可能已被删除/移动）就跳过该文件，不中断整个会话。
			continue
		}
		if err := w.WriteControl(protocol.MsgFileStart, protocol.FileStart{File: f}); err != nil {
			rc.Close()
			return err
		}
		if err := w.WriteBinary(rc, f.Size); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
		sentFiles++
		sentBytes += f.Size
		report(onProgress, Progress{
			Direction: "send", Phase: "transfer", PeerID: peer.ID, PeerName: peer.Name,
			CurrentName: f.Name, Files: sentFiles, TotalFiles: len(accepted),
			Bytes: sentBytes, TotalBytes: acceptBytes,
		})
	}
	if err := w.WriteControl(protocol.MsgTransferDone, nil); err != nil {
		return err
	}
	report(onProgress, Progress{
		Direction: "send", Phase: "done", PeerID: peer.ID, PeerName: peer.Name,
		Files: sentFiles, TotalFiles: len(accepted), Bytes: sentBytes, TotalBytes: acceptBytes,
	})
	_ = w.WriteControl(protocol.MsgBye, nil)
	return nil
}

// runReceive 执行接收端的一次完整会话：握手 → 收清单并决策 → 落地被接受的文件。
func (e *Engine) runReceive(ctx context.Context, conn net.Conn, onProgress ProgressFunc) error {
	w := protocol.NewWriter(conn)
	r := protocol.NewReader(conn)

	// ---- 1. 握手 ----
	report(onProgress, Progress{Direction: "recv", Phase: "handshake"})
	peer, err := e.readHello(r)
	if err != nil {
		return err
	}
	if err := w.WriteControl(protocol.MsgHello, protocol.Hello{
		Version: protocol.ProtocolVersion,
		Device:  e.self,
	}); err != nil {
		return err
	}

	// ---- 2. 读取 Offer 并交给上层决策 ----
	offer, err := e.readOffer(r)
	if err != nil {
		return err
	}
	report(onProgress, Progress{
		Direction: "recv", Phase: "offer", PeerID: peer.ID, PeerName: peer.Name,
		TotalFiles: len(offer.Files), TotalBytes: offer.TotalBytes,
	})

	decision := protocol.Decision{Accept: true}
	if e.decide != nil {
		decision = e.decide(peer, offer)
	}
	if err := w.WriteControl(protocol.MsgDecision, decision); err != nil {
		return err
	}
	if !decision.Accept {
		report(onProgress, Progress{
			Direction: "recv", Phase: "rejected", PeerID: peer.ID, PeerName: peer.Name,
		})
		// 仍需把连接读到 Bye/EOF，避免对方写阻塞；这里直接返回即可，defer 会关闭连接。
		return nil
	}
	if e.sink == nil {
		return fmt.Errorf("sync: no sink configured to receive files")
	}

	// 计算被接受集合的总数/总字节，用于进度展示。
	acceptedTotal, acceptedBytes := offerSubtotal(offer, decision)

	// ---- 3. 持续接收文件直到 transfer_done ----
	var recvFiles int
	var recvBytes int64
	for {
		f, err := r.ReadFrame()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("sync: read frame: %w", err)
		}
		if f.IsBinary {
			return fmt.Errorf("sync: unexpected binary frame")
		}
		switch f.Control.Type {
		case protocol.MsgFileStart:
			var fs protocol.FileStart
			if err := protocol.DecodePayload(f.Control, &fs); err != nil {
				return err
			}
			bf, err := r.ReadFrame()
			if err != nil {
				return err
			}
			if !bf.IsBinary {
				return fmt.Errorf("sync: expected binary frame after file_start")
			}
			if err := e.storeIncoming(r, bf, fs.File); err != nil {
				return err
			}
			recvFiles++
			recvBytes += fs.File.Size
			report(onProgress, Progress{
				Direction: "recv", Phase: "transfer", PeerID: peer.ID, PeerName: peer.Name,
				CurrentName: fs.File.Name, Files: recvFiles, TotalFiles: acceptedTotal,
				Bytes: recvBytes, TotalBytes: acceptedBytes,
			})
		case protocol.MsgTransferDone, protocol.MsgBye:
			report(onProgress, Progress{
				Direction: "recv", Phase: "done", PeerID: peer.ID, PeerName: peer.Name,
				Files: recvFiles, TotalFiles: acceptedTotal, Bytes: recvBytes, TotalBytes: acceptedBytes,
			})
			return nil
		default:
			// 忽略未知控制消息，向前兼容。
		}
	}
	report(onProgress, Progress{
		Direction: "recv", Phase: "done", PeerID: peer.ID, PeerName: peer.Name,
		Files: recvFiles, TotalFiles: acceptedTotal, Bytes: recvBytes, TotalBytes: acceptedBytes,
	})
	return nil
}

// storeIncoming 把一个二进制帧的内容交给 Sink 落盘（流式，不占内存）。
func (e *Engine) storeIncoming(r *protocol.Reader, bf *protocol.Frame, meta protocol.FileMeta) error {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- e.sink.Create(meta, pr)
	}()
	copyErr := r.ReadBinaryBody(bf, pw)
	pw.CloseWithError(copyErr)
	if createErr := <-done; createErr != nil {
		return createErr
	}
	return copyErr
}

// offerSubtotal 依据决定算出被接受文件的数量与总字节。
func offerSubtotal(offer protocol.Offer, decision protocol.Decision) (int, int64) {
	if decision.AcceptIDs == nil {
		return len(offer.Files), offer.TotalBytes
	}
	want := make(map[string]struct{}, len(decision.AcceptIDs))
	for _, id := range decision.AcceptIDs {
		want[id] = struct{}{}
	}
	var n int
	var b int64
	for _, f := range offer.Files {
		if _, ok := want[f.ID]; ok {
			n++
			b += f.Size
		}
	}
	return n, b
}

func (e *Engine) readHello(r *protocol.Reader) (protocol.DeviceInfo, error) {
	env, err := e.readControl(r, protocol.MsgHello)
	if err != nil {
		return protocol.DeviceInfo{}, err
	}
	var h protocol.Hello
	if err := protocol.DecodePayload(env, &h); err != nil {
		return protocol.DeviceInfo{}, err
	}
	if h.Version != protocol.ProtocolVersion {
		return protocol.DeviceInfo{}, fmt.Errorf("sync: incompatible protocol version %d (want %d)", h.Version, protocol.ProtocolVersion)
	}
	return h.Device, nil
}

func (e *Engine) readOffer(r *protocol.Reader) (protocol.Offer, error) {
	env, err := e.readControl(r, protocol.MsgOffer)
	if err != nil {
		return protocol.Offer{}, err
	}
	var o protocol.Offer
	err = protocol.DecodePayload(env, &o)
	return o, err
}

func (e *Engine) readDecision(r *protocol.Reader) (protocol.Decision, error) {
	env, err := e.readControl(r, protocol.MsgDecision)
	if err != nil {
		return protocol.Decision{}, err
	}
	var d protocol.Decision
	err = protocol.DecodePayload(env, &d)
	return d, err
}

// readControl 读取下一条控制帧并校验其类型符合预期。
func (e *Engine) readControl(r *protocol.Reader, want protocol.MsgType) (protocol.Envelope, error) {
	f, err := r.ReadFrame()
	if err != nil {
		return protocol.Envelope{}, err
	}
	if f.IsBinary {
		return protocol.Envelope{}, fmt.Errorf("sync: expected control %q, got binary frame", want)
	}
	if f.Control.Type != want {
		return protocol.Envelope{}, fmt.Errorf("sync: expected control %q, got %q", want, f.Control.Type)
	}
	return f.Control, nil
}

func report(fn ProgressFunc, p Progress) {
	if fn != nil {
		fn(p)
	}
}
