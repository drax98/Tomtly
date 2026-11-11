package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"tomtly/internal/auth"
	"tomtly/internal/store"
)

const (
	cookieName    = "hc_session"
	idleLimit     = 7 * 24 * time.Hour
	absoluteLimit = 30 * 24 * time.Hour
	tokenPrefix   = "hct_"
)

var allPermissions = auth.Permissions("admin")

// principal is who is calling and what they may do right now.
type principal struct {
	user    store.User
	perms   []string
	session string // the session's hash; empty when an API token was used
}

type principalKey struct{}

func principalOf(r *http.Request) *principal {
	p, _ := r.Context().Value(principalKey{}).(*principal)
	return p
}

var errUnauthorized = errors.New("not logged in")

// authenticate finds who sent the request, by API token if it has an Authorization header and otherwise by cookie.
// The answer is read from the store every time, so a disabled user or a demoted role takes effect on the next request.
// It returns errUnauthorized for no or bad credentials, and the store's own error when the store can't say.
func (a *api) authenticate(r *http.Request) (*principal, error) {
	if header := r.Header.Get("Authorization"); header != "" {
		scheme, secret, ok := strings.Cut(header, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || secret == "" {
			return nil, errUnauthorized
		}
		token, owner, err := a.store.TokenUser(r.Context(), auth.HashSecret(strings.TrimSpace(secret)), a.now())
		if errors.Is(err, store.ErrNotFound) || err == nil && owner.Disabled {
			return nil, errUnauthorized
		}
		if err != nil {
			return nil, err
		}
		// A token never does more than its owner can do now.
		limit := auth.Permissions(owner.Role)
		perms := slices.DeleteFunc(auth.Permissions(token.Role), func(p string) bool { return !slices.Contains(limit, p) })
		return &principal{user: owner, perms: perms}, nil
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		return nil, errUnauthorized
	}
	hash := auth.HashSecret(cookie.Value)
	user, err := a.store.SessionUser(r.Context(), hash, a.now(), idleLimit, absoluteLimit)
	if errors.Is(err, store.ErrNotFound) || err == nil && user.Disabled {
		return nil, errUnauthorized
	}
	if err != nil {
		return nil, err
	}
	return &principal{user: user, perms: auth.Permissions(user.Role), session: hash}, nil
}

// unavailable answers when the store can't be reached. Access is refused, and the rest of the service carries on.
func unavailable(w http.ResponseWriter, err error) {
	slog.Warn("authentication store", "err", err)
	reply(w, http.StatusServiceUnavailable, errorBody("sign-in is unavailable right now"))
}

// authorize applies the route table to every /api/ request before the handler runs. Anything the table doesn't list,
// including a wrong method, is refused to anyone who isn't logged in and is not found for those who are.
func (a *api) authorize(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		perm, known := a.perms[pattern]
		if known && perm == public {
			mux.ServeHTTP(w, r)
			return
		}
		p, err := a.authenticate(r)
		switch {
		case errors.Is(err, errUnauthorized):
			reply(w, http.StatusUnauthorized, errorBody("log in first"))
			return
		case err != nil:
			unavailable(w, err)
			return
		case !known:
			reply(w, http.StatusNotFound, errorBody("not found"))
			return
		case perm == session && p.session == "":
			reply(w, http.StatusForbidden, errorBody("this needs a login, not an API token"))
			return
		case perm != session && !slices.Contains(p.perms, perm):
			reply(w, http.StatusForbidden, errorBody("you need the "+perm+" permission"))
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// clientIP is the caller's address, for the login limit and the audit log: the peer, or the last X-Forwarded-For entry past
// the proxies in TRUSTED_PROXIES.
func (a *api) clientIP(r *http.Request) string {
	addr, ok := auth.ClientAddress(r.RemoteAddr, r.Header.Values("X-Forwarded-For"), a.trustedProxies)
	if !ok {
		return r.RemoteAddr
	}
	return addr.String()
}

// secure reports whether the browser reached us over TLS, directly or through a proxy for a host in allowedHosts.
func (a *api) secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") && listed(r.Host, a.allowedHosts)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// audit records what the caller did. A failure to record is logged and never stops the action.
func (a *api) audit(r *http.Request, action, detail string) {
	user := ""
	if p := principalOf(r); p != nil {
		user = p.user.Username
	}
	a.auditAs(r, user, action, detail)
}

func (a *api) auditAs(r *http.Request, user, action, detail string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), storeTimeout)
	defer cancel()
	entry := store.AuditEntry{Time: a.now(), User: clip(user, 64), Action: action, Detail: clip(detail, 512), IP: a.clientIP(r)}
	if err := a.store.AppendAudit(ctx, entry); err != nil {
		slog.Warn("audit write failed", "action", action, "err", err)
	}
}
