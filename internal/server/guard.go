package server

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// hostName strips the port and brackets from a Host header.
func hostName(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}

// listed reports whether the host is one of the names in allowedHosts, which unlike localhost and IPs is a deliberate choice.
func listed(host string, allowed []string) bool {
	name := hostName(host)
	return slices.ContainsFunc(allowed, func(a string) bool { return strings.EqualFold(a, name) })
}

// guardOrigin refuses requests that come from another website in your browser. A login cookie alone would let a
// page you visit point its own domain at this machine (DNS rebinding) and use the API as you.
// A rebinding attack always arrives with its domain as the Host, so IP addresses are safe to allow.
func guardOrigin(next http.Handler, allowed []string) http.Handler {
	hostAllowed := func(host string) bool {
		return strings.EqualFold(hostName(host), "localhost") || net.ParseIP(hostName(host)) != nil || listed(host, allowed)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !hostAllowed(r.Host) {
				reply(w, http.StatusForbidden, errorBody(`this host name isn't allowed; add it to "allowedHosts" in config.json`))
				return
			}
			if origin := r.Header.Get("Origin"); r.Method != http.MethodGet && origin != "" {
				if u, err := url.Parse(origin); err != nil || !strings.EqualFold(u.Host, r.Host) {
					reply(w, http.StatusForbidden, errorBody("requests from other websites aren't allowed"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
