// Package discovery finds HopDrop HTTPS receivers on the local network.
// Automatic discovery uses DNS-SD/Bonjour. Mobile applications use the native
// Bonjour/NSD APIs and feed their results into ManualBackend.
package discovery

import (
	"fmt"
	"net"
	"time"

	"github.com/xurenhe/hopdrop/core/protocol"
)

const (
	defaultInterval = 3 * time.Second
	peerTTL         = 12 * time.Second
)

// Peer is one directly reachable HTTPS receiver.
type Peer struct {
	Device   protocol.DeviceInfo
	Addr     string
	LastSeen time.Time
}

func (p Peer) SyncEndpoint() string {
	return net.JoinHostPort(p.Addr, fmt.Sprintf("%d", p.Device.SyncPort))
}

type PeerEvent struct {
	Online bool
	Peer   Peer
}

// LocalAddresses returns non-loopback IPv4 and IPv6 addresses, preferring
// private/ULA addresses for QR-code pairing.
func LocalAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var private, other []string
	seen := make(map[string]bool)
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			if ip.IsPrivate() {
				private = append(private, ip.String())
			} else {
				other = append(other, ip.String())
			}
		}
	}
	return append(private, other...)
}
