package discovery

import (
	"net"
	"strings"
	"testing"

	"github.com/hashicorp/mdns"

	"github.com/xurenhe/hopdrop/core/protocol"
)

func TestManualBackendRequiresPinnedReachablePeer(t *testing.T) {
	backend := NewManual("self")
	if err := backend.Start(); err != nil {
		t.Fatal(err)
	}

	backend.AddPeer("bad", "Bad", "ios", "", "192.168.1.2", 47772)
	if got := backend.Peers(); len(got) != 0 {
		t.Fatal("peer without fingerprint was accepted")
	}

	fingerprint := strings.Repeat("a", 64)
	backend.AddPeer("peer", "Phone", "ios", fingerprint, "fd00::2", 47772)
	peers := backend.Peers()
	if len(peers) != 1 || peers[0].SyncEndpoint() != "[fd00::2]:47772" {
		t.Fatalf("unexpected peers: %#v", peers)
	}

	backend.Stop()
	backend.AddPeer("late", "Late", "android", fingerprint, "192.168.1.3", 47772)
	if got := backend.Peers(); len(got) != 1 {
		t.Fatal("stopped backend accepted a native discovery callback")
	}
}

func TestMDNSParsesIdentityAndSkipsUndialableIPv6(t *testing.T) {
	fingerprint := strings.Repeat("b", 64)
	backend := NewMDNS(protocol.DeviceInfo{ID: "self"})
	backend.handleEntry(&mdns.ServiceEntry{
		Name:       "peer._hopdrop._tcp.local.",
		AddrV4:     net.ParseIP("192.168.1.9"),
		Port:       47772,
		InfoFields: []string{"id=peer", "name=Mac", "platform=macos", "fingerprint=" + fingerprint},
	})
	peers := backend.Peers()
	if len(peers) != 1 || peers[0].Device.Fingerprint != fingerprint || peers[0].Addr != "192.168.1.9" {
		t.Fatalf("unexpected mDNS peer: %#v", peers)
	}

	linkLocalOnly := NewMDNS(protocol.DeviceInfo{ID: "self"})
	linkLocalOnly.handleEntry(&mdns.ServiceEntry{
		Name:       "peer._hopdrop._tcp.local.",
		AddrV6:     net.ParseIP("fe80::1"),
		Port:       47772,
		InfoFields: []string{"id=peer", "fingerprint=" + fingerprint},
	})
	if got := linkLocalOnly.Peers(); len(got) != 0 {
		t.Fatal("accepted link-local IPv6 address without an interface zone")
	}
}
