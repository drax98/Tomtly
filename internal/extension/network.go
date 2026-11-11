package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	requestTimeout = 15 * time.Second
	maxResponse    = 4 << 20
)

// network is the only way out of an extension's VM. It reaches the hosts the extension declared, so what's on its
// card on the page is all it can talk to.
type network struct {
	public []string // host names written in the extension: internet only, HTTPS only
	local  []string // host names the person running it typed into a setting: may be on the LAN
	lan    bool     // the extension asked for the whole local network, for discovery
	// Loopback holds the service's own unauthenticated API, and link-local holds cloud metadata services.
	allowLoopback bool
	publicClient  *http.Client
	localClient   *http.Client
	sockets       bool // the extension declared raw TCP and UDP
	listen        []int
	own           *ownNetwork

	// The instance's accounting. Left nil, as in some tests, nothing is recorded or limited.
	onViolation func(kind, detail string) error
	onSpend     func() error
	onSend      func(host string, parts ...string) error
	onUse       func(kind, destination string)
}

// specialUse isn't public though Go doesn't call it private, and some of it holds cloud metadata services or a VPN.
var specialUse = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96")}

func isSpecialUse(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	return slices.ContainsFunc(specialUse, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// refusal is a rule saying no, which is a violation when an extension runs into it.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) error { return &refusal{fmt.Sprintf(format, args...)} }

func newNetwork(declared accessDecl, settings map[string]any, allowLoopback bool) *network {
	n := &network{allowLoopback: allowLoopback, lan: declared.Lan, sockets: declared.Sockets, listen: declared.Listen}
	for _, host := range declared.Network {
		if m := hostSetting.FindStringSubmatch(host); m != nil {
			if value, ok := hostIn(settings, m[1]); ok {
				n.local = append(n.local, value)
			}
			continue
		}
		n.public = append(n.public, strings.ToLower(host))
	}
	n.publicClient = n.client(false)
	n.localClient = n.client(true)
	return n
}

func (n *network) violate(kind, detail string) error {
	if n.onViolation == nil {
		return errors.New(detail)
	}
	return n.onViolation(kind, detail)
}

func (n *network) spend() error {
	if n.onSpend == nil {
		return nil
	}
	return n.onSpend()
}

// use records traffic that passed the gates, for the owner to see.
func (n *network) use(kind, destination string) {
	if n.onUse != nil {
		n.onUse(kind, destination)
	}
}

func (n *network) send(host string, parts ...string) error {
	if n.onSend == nil {
		return nil
	}
	return n.onSend(host, parts...)
}

// refused turns a rule saying no into a recorded violation, and leaves any other error alone.
func (n *network) refused(err error) error {
	var r *refusal
	if errors.As(err, &r) {
		return n.violate("network", r.msg)
	}
	return err
}

func (n *network) dialer(allowPrivate bool) *net.Dialer {
	return &net.Dialer{Timeout: requestTimeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("unexpected address %s", address)
		}
		// Checked on the address actually dialled, so a public name resolving to a private address gets no further.
		switch {
		case !ip.IsLoopback() && n.own != nil && n.own.contains(ip):
			return refuse("extensions can't reach the service's own network: %s", ip)
		case ip.IsLoopback():
			if !n.allowLoopback {
				return refuse("extensions can't reach this machine's own services")
			}
		case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsMulticast(), ip.IsUnspecified():
			return refuse("extensions can't reach %s", ip)
		case ip.IsPrivate():
			if !allowPrivate {
				return refuse("%s is on the local network; a local host must come from a setting", ip)
			}
		case !allowPrivate && isSpecialUse(ip):
			return refuse("%s isn't a public address", ip)
		}
		return nil
	}}
}

func (n *network) client(allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout:   requestTimeout,
		Transport: &http.Transport{DialContext: n.dialer(allowPrivate).DialContext, Proxy: nil, TLSHandshakeTimeout: requestTimeout},
		// A redirect could lead anywhere, so it goes back to the extension, whose next request gets checked.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// client returns which client may reach a URL, or why none may.
func (n *network) clientFor(u *url.URL) (*http.Client, error) {
	for _, host := range n.local {
		if strings.EqualFold(u.Host, host) || strings.EqualFold(u.Hostname(), host) {
			if u.Scheme != "http" && u.Scheme != "https" {
				return nil, refuse("unsupported scheme %q", u.Scheme)
			}
			return n.localClient, nil
		}
	}
	for _, host := range n.public {
		if strings.EqualFold(u.Host, host) || strings.EqualFold(u.Hostname(), host) {
			if u.Scheme != "https" {
				return nil, refuse("internet hosts must use https: %s", u.Host)
			}
			return n.publicClient, nil
		}
	}
	if ip := net.ParseIP(u.Hostname()); n.lan && ip != nil && ip.IsPrivate() {
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, refuse("unsupported scheme %q", u.Scheme)
		}
		if port, _ := strconv.Atoi(u.Port()); slices.Contains(relayPorts, port) {
			return nil, refuse("port %d on %s can pass data out of your network, so it needs that address in a setting", port, ip)
		}
		return n.localClient, nil
	}
	return nil, refuse("%s isn't in this extension's network list", u.Hostname())
}

// relayPorts are ones where a device on the LAN passes what it's sent to the internet or opens a way in: DNS, multicast
// DNS and port mapping. "lan" is for talking to devices, so these need the address named in a setting.
var relayPorts = []int{53, 853, 1900, 5351, 5353}

// socketRule says whether a raw socket may reach a host and port, and if it may use a private address.
func (n *network) socketRule(host string, port int) (allowPrivate bool, err error) {
	// An entry that names a port allows only that port.
	matches := func(entry string) bool {
		name, entryPort, err := net.SplitHostPort(entry)
		if err != nil {
			return strings.EqualFold(entry, host)
		}
		return strings.EqualFold(name, host) && entryPort == strconv.Itoa(port)
	}
	if slices.ContainsFunc(n.local, matches) {
		return true, nil
	}
	if slices.ContainsFunc(n.public, matches) {
		return false, nil
	}
	if ip := net.ParseIP(host); n.lan && ip != nil && (ip.IsPrivate() || n.allowLoopback && ip.IsLoopback()) {
		if slices.Contains(relayPorts, port) {
			return false, refuse("port %d on %s can pass data out of your network, so it needs that address in a setting", port, host)
		}
		return true, nil
	}
	return false, refuse("%s isn't in this extension's network list", host)
}

var httpMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}

// response is what an extension gets back from ctx.fetch.
type response struct {
	status int
	header http.Header
	body   string
}

func (n *network) do(ctx context.Context, target, method string, headers map[string]string, body string) (*response, error) {
	if err := n.spend(); err != nil {
		return nil, err
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("bad url %q", target)
	}
	client, err := n.clientFor(u)
	if err != nil {
		return nil, n.refused(err)
	}
	// The method goes out as written, so an unchecked one would carry anything past send, upper-cased.
	if !slices.Contains(httpMethods, method) {
		return nil, n.refused(refuse("fetch: the method must be one of %s", strings.Join(httpMethods, ", ")))
	}
	parts := []string{target, method, body}
	for k, v := range headers {
		parts = append(parts, k, v)
	}
	if err := n.send(u.Host, parts...); err != nil {
		return nil, err
	}
	// The first part of the path too: one approved host can hold many accounts, such as a chat service's bot ids.
	first, _, _ := strings.Cut(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	n.use(u.Scheme+" "+method, strings.TrimSuffix(u.Host+"/"+first, "/"))
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, n.refused(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("response is over %d bytes", maxResponse)
	}
	return &response{resp.StatusCode, resp.Header, string(data)}, nil
}
