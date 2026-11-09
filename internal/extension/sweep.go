package extension

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	maxSweepHosts   = 254
	maxSweepWait    = 2 * time.Second
	sweepHostsPerOp = 8 // a sweep costs its share of the operations budget, so it can't be looped to flood the network
)

// sweepCheck is the gate a scan passes: sockets and lan declared, private addresses only, within the rate limit.
func (n *network) sweepCheck(vm *goja.Runtime, what string, hosts []string, port int) {
	n.needSockets(vm, what)
	if !n.lan {
		throw(vm, n.violate("lan", what+` needs "lan" in access`))
	}
	if port < 1 || port > 65535 {
		panic(goError(vm, fmt.Errorf("%s: the port must be from 1 to 65535, not %d", what, port)))
	}
	if slices.Contains(relayPorts, port) {
		throw(vm, n.violate("lan", fmt.Sprintf("%s: port %d can pass data out of your network", what, port)))
	}
	if len(hosts) > maxSweepHosts {
		panic(goError(vm, fmt.Errorf("%s: at most %d addresses at once", what, maxSweepHosts)))
	}
	for _, host := range hosts {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is4() || !(ip.IsPrivate() || n.allowLoopback && ip.IsLoopback()) {
			throw(vm, n.violate("lan", what+": "+host+" isn't a private IPv4 address"))
		}
	}
	for range len(hosts) / sweepHostsPerOp {
		throw(vm, n.spend())
	}
}

// sweptRange names what a scan covered, for the owner to read. A scan carrying a payload is traffic like any other.
func sweptRange(hosts []string, port int) string {
	nets := []string{}
	for _, host := range hosts {
		if at := strings.LastIndexByte(host, '.'); at > 0 && !slices.Contains(nets, host[:at]+".0/24") {
			nets = append(nets, host[:at]+".0/24")
		}
	}
	return strings.Join(nets, ", ") + " port " + strconv.Itoa(port)
}

// sweep runs probe on each host in parallel, and returns what answered in the order given.
func sweep[T any](ctx context.Context, hosts []string, wait time.Duration, probe func(ctx context.Context, host string) (T, bool)) []T {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	results, found := make([]T, len(hosts)), make([]bool, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		// All at once: a silent address waits until the deadline, so taking turns would leave most of them unasked.
		wg.Go(func() { results[i], found[i] = probe(ctx, host) })
	}
	wg.Wait()
	out := []T{}
	for i, ok := range found {
		if ok {
			out = append(out, results[i])
		}
	}
	return out
}

func sweepWait(vm *goja.Runtime, options goja.Value) time.Duration {
	return min(socketWait(vm, options), maxSweepWait)
}

// udpScan sends one datagram to each host and returns the first reply from each.
func (n *network) udpScan(ctx context.Context, vm *goja.Runtime, hosts []string, port int, data string, options goja.Value) []map[string]string {
	n.sweepCheck(vm, "udp.scan", hosts, port)
	payload := unhex(vm, data)
	for _, host := range hosts {
		throw(vm, n.send(net.JoinHostPort(host, strconv.Itoa(port)), string(payload)))
	}
	n.use("udp scan", sweptRange(hosts, port))
	dialer := n.dialer(true)
	return sweep(ctx, hosts, sweepWait(vm, options), func(ctx context.Context, host string) (map[string]string, bool) {
		conn, err := dialer.DialContext(ctx, "udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return nil, false
		}
		defer conn.Close()
		defer context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })()
		if _, err := conn.Write(payload); err != nil {
			return nil, false
		}
		reply := make([]byte, 65535)
		size, err := conn.Read(reply)
		if err != nil {
			return nil, false
		}
		return map[string]string{"from": host, "data": hex.EncodeToString(reply[:size])}, true
	})
}

// tcpScan returns the hosts that accept a connection on the port. It sends nothing, and closes each connection at once.
func (n *network) tcpScan(ctx context.Context, vm *goja.Runtime, hosts []string, port int, options goja.Value) []string {
	n.sweepCheck(vm, "tcp.scan", hosts, port)
	n.use("tcp scan", sweptRange(hosts, port))
	dialer := n.dialer(true)
	return sweep(ctx, hosts, sweepWait(vm, options), func(ctx context.Context, host string) (string, bool) {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return "", false
		}
		conn.Close()
		return host, true
	})
}
