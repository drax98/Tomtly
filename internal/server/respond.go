package server

import (
	"encoding/json"
	"errors"
	"home-control/internal/automation"
	"home-control/internal/extension"
	"net/http"
	"strings"
	"time"
)

func replyScriptError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, automation.ErrScriptNotFound):
		code = http.StatusNotFound
	case errors.Is(err, automation.ErrConflict):
		code = http.StatusConflict
	}
	reply(w, code, errorBody(err.Error()))
}

// replyMarketError says why something from the marketplace isn't available. The page keeps working either way.
func replyMarketError(w http.ResponseWriter, err error) {
	var refused *extension.ReviewRefused
	switch {
	case errors.As(err, &refused):
		reply(w, refused.Status, errorBody(refused.Message))
	case errors.Is(err, extension.ErrMarketNotListed):
		reply(w, http.StatusNotFound, errorBody(err.Error()))
	default:
		reply(w, http.StatusBadGateway, errorBody(err.Error()))
	}
}

// storeTimeout is how long a request waits for the store before it gives up and says so.
const storeTimeout = 5 * time.Second

func errorBody(message string) map[string]string { return map[string]string{"error": message} }

func reply(w http.ResponseWriter, code int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(data); err != nil {
		return // the client went away
	}
}

func contentType(r *http.Request) string {
	return strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
}

// readJSON enforces the rules on every state change. Requiring JSON forces a CORS preflight, so other websites in
// your browser cannot send commands.
func readJSON(w http.ResponseWriter, r *http.Request, out any, limit int64) bool {
	if contentType(r) != "application/json" {
		reply(w, http.StatusUnsupportedMediaType, errorBody("send application/json"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			reply(w, http.StatusRequestEntityTooLarge, errorBody("body too large"))
			return false
		}
		reply(w, http.StatusBadRequest, errorBody("bad JSON"))
		return false
	}
	return true
}
