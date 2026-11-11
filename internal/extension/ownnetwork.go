package extension

import (
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// A person can type any address into a host setting, and "lan" reaches every private address. Neither should lead to
// the service's own database, its own API, or the machine running its container: those aren't devices.
type ownNetwork struct {
	subnets []*net.IPNet
	names   []string // resolved when checked, since a container's address can change

	mu       sync.Mutex
	resolved []net.IP
	at       time.Time
}

const ownNamesFor = time.Minute

// findOwnNetwork looks at this process's surroundings.
func findOwnNetwork(databaseURL string) *ownNetwork {
	o := &ownNetwork{names: []string{"host.docker.internal", "gateway.docker.internal"}}
	if u, err := url.Parse(databaseURL); err == nil && u.Hostname() != "" {
		o.names = append(o.names, u.Hostname())
	}
	// From a container this machine's real addresses can't be seen, and the service's published port is on them.
	for _, text := range strings.Split(os.Getenv("HOST_IPS"), ",") {
		if ip := net.ParseIP(strings.TrimSpace(text)); ip != nil {
			bits := 8 * len(ip)
			o.subnets = append(o.subnets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return o
	}
	_, inContainer := os.Stat("/.dockerenv")
	for _, address := range addresses {
		ipNet, ok := address.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		if inContainer == nil {
			// shortcut: in a container its whole network is other containers, which is wrong with network_mode: host, where it's the real LAN; detect that if someone needs it.
			o.subnets = append(o.subnets, ipNet)
			continue
		}
		bits := 8 * len(ipNet.IP)
		o.subnets = append(o.subnets, &net.IPNet{IP: ipNet.IP, Mask: net.CIDRMask(bits, bits)}) // on a real LAN, only this machine
	}
	return o
}

func (o *ownNetwork) contains(ip net.IP) bool {
	for _, subnet := range o.subnets {
		if subnet.Contains(ip) {
			return true
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if time.Since(o.at) > ownNamesFor {
		o.resolved, o.at = nil, time.Now()
		for _, name := range o.names {
			if ips, err := net.LookupIP(name); err == nil {
				o.resolved = append(o.resolved, ips...)
			}
		}
	}
	for _, own := range o.resolved {
		if own.Equal(ip) {
			return true
		}
	}
	return false
}
