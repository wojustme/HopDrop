package sync

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/xurenhe/hopdrop/core/device"
	"github.com/xurenhe/hopdrop/core/discovery"
	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
)

// Node is a LAN transfer endpoint. For each transfer the receiver is an HTTPS
// server and the sender is its client. Reversing the transfer reverses those
// roles; there is no second wire protocol.
type Node struct {
	self            protocol.DeviceInfo
	identity        *device.Identity
	sink            filestore.Sink
	manualDiscovery bool

	engine *Engine
	disc   discovery.Backend
	ln     net.Listener

	onProgress ProgressFunc
	onPeer     func(discovery.PeerEvent)
	decide     DecisionFunc

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewNode creates a node around a stable cryptographic identity. Pass nil only
// for an intentionally ephemeral identity, such as an isolated test.
func NewNode(name string, platform protocol.Platform, identity *device.Identity, sink filestore.Sink) *Node {
	return &Node{
		self:     protocol.DeviceInfo{Name: name, Platform: platform},
		identity: identity,
		sink:     sink,
	}
}

// UseManualDiscovery delegates Bonjour/NSD discovery to the native mobile UI.
// Desktop nodes use mDNS automatically.
func (n *Node) UseManualDiscovery() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.manualDiscovery = true
}

func (n *Node) OnProgress(fn ProgressFunc) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onProgress = fn
}

func (n *Node) OnPeer(fn func(discovery.PeerEvent)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onPeer = fn
}

func (n *Node) OnDecision(fn DecisionFunc) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.decide = fn
}

func (n *Node) Self() protocol.DeviceInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.self
}

func (n *Node) Identity() *device.Identity {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.identity
}

// StartServer opens the HTTPS receive server and advertises it. A port of zero
// asks the OS for an available port.
func (n *Node) StartServer(port int) error {
	return n.start(true, port)
}

// StartClient starts only discovery and the HTTPS client. It does not open or
// advertise an inbound port, which is useful for a send-only process.
func (n *Node) StartClient() error {
	return n.start(false, 0)
}

func (n *Node) start(serve bool, port int) error {
	n.mu.Lock()
	if n.cancel != nil {
		n.mu.Unlock()
		return errors.New("sync: node is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.ctx = ctx
	n.cancel = cancel
	identity := n.identity
	self := n.self
	n.mu.Unlock()

	fail := func(err error) error {
		cancel()
		n.mu.Lock()
		if n.ctx == ctx {
			n.ctx, n.cancel, n.disc, n.ln, n.engine = nil, nil, nil, nil, nil
			n.self.SyncPort = 0
		}
		n.mu.Unlock()
		return err
	}

	if identity == nil {
		var err error
		identity, err = device.NewIdentity("")
		if err != nil {
			return fail(err)
		}
		n.mu.Lock()
		n.identity = identity
		n.mu.Unlock()
	}
	cert, err := identity.TLSCertificate(self.Name)
	if err != nil {
		return fail(err)
	}
	var ln net.Listener
	if serve {
		ln, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			return fail(fmt.Errorf("sync: listen: %w", err))
		}
	}

	n.mu.Lock()
	self.ID = identity.ID
	self.Fingerprint = identity.Fingerprint()
	self.SyncPort = 0
	if serve {
		self.SyncPort = ln.Addr().(*net.TCPAddr).Port
	}
	n.self = self
	n.ln = ln
	n.engine = NewEngine(self, n.sink, n.decide, cert)
	if n.manualDiscovery {
		n.disc = discovery.NewManual(self.ID)
	} else {
		n.disc = discovery.NewMDNS(self)
	}
	engine, backend := n.engine, n.disc
	onProgress, onPeer := n.onProgress, n.onPeer
	n.mu.Unlock()

	if err := backend.Start(); err != nil {
		if ln != nil {
			_ = ln.Close()
		}
		return fail(fmt.Errorf("sync: start discovery: %w", err))
	}

	n.wg.Add(1)
	if serve {
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			if err := engine.Serve(ctx, ln, onProgress); err != nil {
				report(onProgress, Progress{Direction: "recv", Phase: "error", Err: err.Error()})
			}
		}()
	}
	go func() {
		defer n.wg.Done()
		n.discoveryLoop(ctx, backend, onPeer)
	}()
	return nil
}

func (n *Node) Stop() {
	n.mu.Lock()
	cancel, backend, ln := n.cancel, n.disc, n.ln
	n.ctx, n.cancel, n.disc, n.ln, n.engine = nil, nil, nil, nil, nil
	n.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if backend != nil {
		backend.Stop()
	}
	if ln != nil {
		_ = ln.Close()
	}
	n.wg.Wait()
}

func (n *Node) Peers() []discovery.Peer {
	n.mu.Lock()
	backend := n.disc
	n.mu.Unlock()
	if backend == nil {
		return nil
	}
	return backend.Peers()
}

func (n *Node) ManualDiscovery() *discovery.ManualBackend {
	n.mu.Lock()
	defer n.mu.Unlock()
	backend, _ := n.disc.(*discovery.ManualBackend)
	return backend
}

func (n *Node) SendSource(ctx context.Context, peer discovery.Peer, source filestore.Source) error {
	return n.send(ctx, peer.SyncEndpoint(), peer.Device, source)
}

func (n *Node) SendPaths(ctx context.Context, peer discovery.Peer, paths []string) error {
	source, err := filestore.NewLocalSource(paths)
	if err != nil {
		return err
	}
	return n.SendSource(ctx, peer, source)
}

// SendToEndpoint connects to a QR/manual endpoint. fingerprint is mandatory;
// accepting an unauthenticated endpoint would make QR pairing misleading.
func (n *Node) SendToEndpoint(ctx context.Context, endpoint, fingerprint string, paths []string) error {
	source, err := filestore.NewLocalSource(paths)
	if err != nil {
		return err
	}
	return n.SendToEndpointSource(ctx, endpoint, fingerprint, source)
}

func (n *Node) SendToEndpointSource(ctx context.Context, endpoint, fingerprint string, source filestore.Source) error {
	if fingerprint == "" {
		return errors.New("sync: peer fingerprint is required for direct pairing")
	}
	return n.send(ctx, endpoint, protocol.DeviceInfo{Fingerprint: fingerprint}, source)
}

func (n *Node) send(ctx context.Context, endpoint string, peer protocol.DeviceInfo, source filestore.Source) error {
	n.mu.Lock()
	engine := n.engine
	nodeContext := n.ctx
	onProgress := n.onProgress
	n.mu.Unlock()
	if engine == nil || nodeContext == nil {
		return errors.New("sync: node is not running")
	}
	transferContext, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(nodeContext, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()
	return engine.Send(transferContext, endpoint, peer, source, onProgress)
}

func (n *Node) discoveryLoop(ctx context.Context, backend discovery.Backend, onPeer func(discovery.PeerEvent)) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-backend.Events():
			if !ok {
				return
			}
			if onPeer != nil {
				onPeer(event)
			}
		}
	}
}
