package server

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"tomtly/internal/extension"
)

const hookBodyTime = 15 * time.Second

// hook delivers a message from outside to the extension whose address it came to. The address is the only credential: no
// cookie is read or set here, so nothing a browser holds can be used through it. A wrong token, an unknown extension and
// one that takes no webhooks all get the same answer.
func (a *api) hook(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox") // whatever an extension answers can't run as a page
	deliver, err := a.extensions.Webhook(r.PathValue("id"), r.PathValue("token"), a.clientIP(r))
	if err != nil {
		hookFailed(w, err)
		return
	}
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(hookBodyTime)) // a slow sender can't hold this open
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, extension.MaxWebhookBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			reply(w, http.StatusRequestEntityTooLarge, errorBody("body too large"))
			return
		}
		reply(w, http.StatusBadRequest, errorBody("could not read the body"))
		return
	}
	req := extension.WebhookRequest{Method: r.Method, Path: "/" + r.PathValue("rest"), Query: map[string]string{}, Headers: map[string]string{}, Body: string(body), BodyHex: hex.EncodeToString(body)}
	for key, values := range r.URL.Query() {
		req.Query[key] = values[0]
	}
	for name, values := range r.Header {
		if lower := strings.ToLower(name); lower != "cookie" {
			req.Headers[lower] = strings.Join(values, ", ")
		}
	}
	resp, err := deliver(r.Context(), req)
	if err != nil {
		hookFailed(w, err)
		return
	}
	for name, value := range resp.Headers {
		h.Set(name, value)
	}
	h.Set("Content-Type", resp.ContentType)
	w.WriteHeader(resp.Status)
	if resp.Status != http.StatusNoContent {
		io.WriteString(w, resp.Body)
	}
}

func hookFailed(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, extension.ErrNoWebhook):
		reply(w, http.StatusNotFound, errorBody("not found"))
	case errors.Is(err, extension.ErrWebhookLimit):
		reply(w, http.StatusTooManyRequests, errorBody("too many messages"))
	case errors.Is(err, extension.ErrWebhookBusy):
		reply(w, http.StatusServiceUnavailable, errorBody("busy, try again"))
	case errors.Is(err, extension.ErrWebhookTimeout):
		reply(w, http.StatusGatewayTimeout, errorBody("the extension took too long"))
	default:
		reply(w, http.StatusBadGateway, errorBody("the extension could not handle that")) // its own error is on its card
	}
}
