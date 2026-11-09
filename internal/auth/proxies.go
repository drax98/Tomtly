package auth

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
)

// ParseProxies reads a comma-separated list of addresses or ranges.
func ParseProxies(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(item)
		if addr, addrErr := netip.ParseAddr(item); addrErr == nil {
			prefix, err = addr.Prefix(addr.BitLen())
		}
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or range", item)
		}
		out = append(out, prefix)
	}
	return out, nil
}

// ClientAddress is who is calling: the peer, or if the peer is a trusted proxy, the last X-Forwarded-For entry that isn't one.
// Everything before that entry was written by the client, so it can say anything. ok is false if the peer isn't an address.
func ClientAddress(remote string, forwarded []string, trusted []netip.Prefix) (addr netip.Addr, ok bool) {
	peer, _, err := net.SplitHostPort(remote)
	if err != nil {
		peer = remote
	}
	addr, err = netip.ParseAddr(peer)
	if err != nil {
		return netip.Addr{}, false
	}
	addr = addr.Unmap()
	isTrusted := func(a netip.Addr) bool {
		return slices.ContainsFunc(trusted, func(p netip.Prefix) bool { return p.Contains(a) })
	}
	hops := strings.Split(strings.Join(forwarded, ","), ",")
	for n := len(hops) - 1; n >= 0 && isTrusted(addr); n-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[n]))
		if err != nil {
			break
		}
		addr = hop.Unmap()
	}
	return addr, true
}
