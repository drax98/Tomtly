package extension

import (
	"context"

	"crypto/tls"

	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"io"

	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	defaultSocketWait = 3 * time.Second
	maxSocketWait     = 15 * time.Second
	maxConnections    = 16
	maxListenWait     = 2 * time.Second // everything else the extension does waits while it reads
	maxDiscovered     = 256
	maxStorage        = 1 << 20
)

// Binary data crosses into the VM as hex strings, which any protocol code can slice, compare and log.

type socketOptions struct {
	TimeoutMs int `json:"timeoutMs"`
}

// wrapTLS starts TLS when asked. Skipping verification is only for a host on the local network, where devices often
// have self-signed certificates: on the internet it would let anyone in the path read what's sent.
func wrapTLS(ctx context.Context, vm *goja.Runtime, n *network, conn net.Conn, host string, local bool, value goja.Value) (net.Conn, error) {
	var options struct {
		TLS      bool   `json:"tls"`
		Insecure bool   `json:"insecure"`
		Name     string `json:"serverName"`
	}
	if value != nil && !goja.IsUndefined(value) {
		text, err := stringify(vm, value)
		if err == nil {
			err = json.Unmarshal([]byte(text), &options)
		}
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("options: %w", err)
		}
	}
	if !options.TLS {
		return conn, nil
	}
	if options.Insecure && !local {
		conn.Close()
		return nil, errors.New("insecure TLS is only allowed for a host on the local network")
	}
	name, err := n.serverName(host, options.Name, local)
	if err != nil {
		conn.Close()
		return nil, err
	}
	secure := tls.Client(conn, &tls.Config{ServerName: name, InsecureSkipVerify: options.Insecure, MinVersion: tls.VersionTLS12}) // #nosec G402 -- only for a host on the local network, refused above otherwise; devices there use self-signed certificates
	if err := secure.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("TLS: %w", err)
	}
	return secure, nil
}

func socketWait(vm *goja.Runtime, value goja.Value) time.Duration {
	var options socketOptions
	if value != nil && !goja.IsUndefined(value) {
		text, err := stringify(vm, value)
		if err == nil {
			err = json.Unmarshal([]byte(text), &options)
		}
		if err != nil {
			panic(goError(vm, fmt.Errorf("options: %w", err)))
		}
	}
	if options.TimeoutMs <= 0 {
		return defaultSocketWait
	}
	return min(time.Duration(options.TimeoutMs)*time.Millisecond, maxSocketWait)
}

func unhex(vm *goja.Runtime, text string) []byte {
	data, err := hex.DecodeString(text)
	if err != nil {
		panic(goError(vm, fmt.Errorf("expected hex: %w", err)))
	}
	return data
}

func throw(vm *goja.Runtime, err error) {
	if err != nil {
		panic(goError(vm, err))
	}
}

// needSockets is the gate every socket call passes: declared, within the rate limit.
func (n *network) needSockets(vm *goja.Runtime, what string) {
	if !n.sockets {
		throw(vm, n.violate("sockets", what+" needs sockets in access"))
	}
	throw(vm, n.spend())
}

// needListen is the gate every listen passes: the port declared in access.listen, within the rate limit.
func (n *network) needListen(vm *goja.Runtime, what string, port int) {
	switch {
	case port < 1 || port > 65535:
		throw(vm, n.violate("listen", fmt.Sprintf("%s: the port must be from 1 to 65535, not %d", what, port)))
	case !slices.Contains(n.listen, port):
		throw(vm, n.violate("listen", fmt.Sprintf("%s on port %d needs that port in access.listen", what, port)))
	}
	throw(vm, n.spend())
}

// listenWait is how long a read or accept waits: 3 seconds unless asked, and never more than maxListenWait.
func listenWait(vm *goja.Runtime, options goja.Value) time.Duration {
	return min(socketWait(vm, options), maxListenWait)
}

func isTimeout(err error) bool {
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

// connSet is the connections and listeners an extension has open, which the service closes when it stops.
type connSet struct {
	mu    sync.Mutex
	conns map[io.Closer]bool
}

func (c *connSet) add(conn io.Closer)    { c.mu.Lock(); c.conns[conn] = true; c.mu.Unlock() }
func (c *connSet) remove(conn io.Closer) { c.mu.Lock(); delete(c.conns, conn); c.mu.Unlock() }
func (c *connSet) size() int             { c.mu.Lock(); defer c.mu.Unlock(); return len(c.conns) }
func (c *connSet) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for conn := range c.conns {
		conn.Close() // shortcut: racing a connect at the moment of stopping could leave one open until the service ends, which is rare and harmless
	}
}

// udpObject is ctx.udp: one request and reply, asking the local network who's there, or listening on a declared port.
func (n *network) udpObject(ctx context.Context, vm *goja.Runtime, open *connSet) map[string]any {
	return map[string]any{
		"request": func(host string, port int, data string, options goja.Value) string {
			n.needSockets(vm, "udp.request")
			allowPrivate, err := n.socketRule(host, port)
			throw(vm, n.refused(err))
			payload, wait := unhex(vm, data), socketWait(vm, options)
			throw(vm, n.send(net.JoinHostPort(host, strconv.Itoa(port)), string(payload)))
			n.use("udp", net.JoinHostPort(host, strconv.Itoa(port)))
			ctx, cancel := context.WithTimeout(ctx, wait)
			defer cancel()
			conn, err := n.dialer(allowPrivate).DialContext(ctx, "udp", net.JoinHostPort(host, strconv.Itoa(port)))
			throw(vm, n.refused(err))
			defer conn.Close()
			throw(vm, conn.SetDeadline(time.Now().Add(wait)))
			_, err = conn.Write(payload)
			throw(vm, err)
			reply := make([]byte, 65535)
			size, err := conn.Read(reply)
			throw(vm, err)
			return hex.EncodeToString(reply[:size])
		},
		"scan": func(hosts []string, port int, data string, options goja.Value) goja.Value {
			return plain(vm, n.udpScan(ctx, vm, hosts, port, data, options))
		},
		// Datagrams sent to a port the extension declared. Keep the listener in a top-level variable between calls.
		"listen": func(port int) map[string]any {
			n.needListen(vm, "udp.listen", port)
			if open.size() >= maxConnections {
				panic(goError(vm, fmt.Errorf("over %d connections are open; close some", maxConnections)))
			}
			n.use("udp listen", "port "+strconv.Itoa(port))
			conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
			throw(vm, err)
			open.add(conn)
			return map[string]any{
				// Returns the next datagram as { from, data }, or null if none arrived in time.
				"read": func(options goja.Value) goja.Value {
					throw(vm, conn.SetReadDeadline(time.Now().Add(listenWait(vm, options))))
					buf := make([]byte, 65535)
					size, from, err := conn.ReadFrom(buf)
					if isTimeout(err) {
						return goja.Null()
					}
					throw(vm, err)
					address, _, _ := net.SplitHostPort(from.String())
					n.use("udp from outside", address)
					return plain(vm, map[string]string{"from": address, "data": hex.EncodeToString(buf[:size])})
				},
				"close": func() {
					open.remove(conn)
					conn.Close()
				},
			}
		},
		"discover": func(port int, data string, options goja.Value) goja.Value {
			n.needSockets(vm, "udp.discover")
			if !n.lan {
				throw(vm, n.violate("lan", `discovery needs "lan" in access`))
			}
			payload, wait := unhex(vm, data), socketWait(vm, options)
			throw(vm, n.send(broadcast, string(payload)))
			n.use("udp broadcast", "port "+strconv.Itoa(port))
			conn, err := net.ListenPacket("udp4", ":0")
			throw(vm, err)
			defer conn.Close()
			throw(vm, conn.SetDeadline(time.Now().Add(wait)))
			_, err = conn.WriteTo(payload, &net.UDPAddr{IP: net.IPv4bcast, Port: port})
			throw(vm, err)
			found := []map[string]string{}
			reply := make([]byte, 65535)
			for len(found) < maxDiscovered {
				size, from, err := conn.ReadFrom(reply)
				if err != nil {
					break // the wait is over
				}
				address, _, _ := net.SplitHostPort(from.String())
				found = append(found, map[string]string{"from": address, "data": hex.EncodeToString(reply[:size])})
			}
			return plain(vm, found)
		},
	}
}

// tcpObject is ctx.tcp. A connection can stay open between calls, for protocols that need a session.
func (i *instance) tcpObject(ctx context.Context, vm *goja.Runtime, n *network, open *connSet) map[string]any {
	return map[string]any{
		"scan": func(hosts []string, port int, options goja.Value) goja.Value {
			return plain(vm, n.tcpScan(ctx, vm, hosts, port, options))
		},
		"connect": func(host string, port int, options goja.Value) map[string]any {
			n.needSockets(vm, "tcp.connect")
			if open.size() >= maxConnections {
				panic(goError(vm, fmt.Errorf("over %d connections are open; close some", maxConnections)))
			}
			allowPrivate, err := n.socketRule(host, port)
			throw(vm, n.refused(err))
			wait := socketWait(vm, options)
			n.use("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
			dialing, cancel := context.WithTimeout(ctx, wait)
			defer cancel()
			conn, err := n.dialer(allowPrivate).DialContext(dialing, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
			throw(vm, n.refused(err))
			if conn, err = wrapTLS(dialing, vm, n, conn, host, allowPrivate, options); err != nil {
				throw(vm, err)
			}
			open.add(conn)
			return connObject(vm, n, open, conn, net.JoinHostPort(host, strconv.Itoa(port)), socketWait, n.needSockets)
		},
		// Accepts connections on a port the extension declared. Keep the listener in a top-level variable between calls.
		"listen": func(port int) map[string]any {
			n.needListen(vm, "tcp.listen", port)
			if open.size() >= maxConnections {
				panic(goError(vm, fmt.Errorf("over %d connections are open; close some", maxConnections)))
			}
			n.use("tcp listen", "port "+strconv.Itoa(port))
			listener, err := net.Listen("tcp", ":"+strconv.Itoa(port)) // #nosec G102 -- a port the extension declared and its owner approved
			throw(vm, err)
			open.add(listener)
			return map[string]any{
				// Returns the next connection, or null if none arrived in time.
				"accept": func(options goja.Value) goja.Value {
					throw(vm, listener.(*net.TCPListener).SetDeadline(time.Now().Add(listenWait(vm, options))))
					conn, err := listener.Accept()
					if isTimeout(err) {
						return goja.Null()
					}
					throw(vm, err)
					if open.size() >= maxConnections {
						conn.Close()
						panic(goError(vm, fmt.Errorf("over %d connections are open; close some", maxConnections)))
					}
					open.add(conn)
					address, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
					n.use("tcp from outside", address)
					return vm.ToValue(connObject(vm, n, open, conn, conn.RemoteAddr().String(), listenWait, nil))
				},
				"close": func() {
					open.remove(listener)
					listener.Close()
				},
			}
		},
	}
}

// connObject is an open TCP connection as the extension sees it. gate runs before each use of one it dialled; one a
// declared listener accepted needs none, since its port was the declaration.
func connObject(vm *goja.Runtime, n *network, open *connSet, conn net.Conn, label string, wait func(*goja.Runtime, goja.Value) time.Duration, gate func(*goja.Runtime, string)) map[string]any {
	check := func(what string) {
		if gate != nil {
			gate(vm, what)
		}
	}
	return map[string]any{
		"write": func(data string) {
			check("tcp.write")
			payload := unhex(vm, data)
			throw(vm, n.send(label, string(payload)))
			throw(vm, conn.SetWriteDeadline(time.Now().Add(maxSocketWait)))
			_, err := conn.Write(payload)
			throw(vm, err)
		},
		// Returns what has arrived, or "" if nothing did in time. A closed connection throws.
		"read": func(options goja.Value) string {
			check("tcp.read")
			throw(vm, conn.SetReadDeadline(time.Now().Add(wait(vm, options))))
			reply := make([]byte, 65535)
			size, err := conn.Read(reply)
			if isTimeout(err) {
				return ""
			}
			throw(vm, err)
			return hex.EncodeToString(reply[:size])
		},
		"close": func() {
			open.remove(conn)
			conn.Close()
		},
	}
}

func parseJSON(vm *goja.Runtime, text string) goja.Value {
	parse, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	if !ok {
		panic(goError(vm, errors.New("JSON.parse was replaced")))
	}
	value, err := parse(goja.Undefined(), vm.ToValue(text))
	if err != nil {
		panic(err)
	}
	return value
}
