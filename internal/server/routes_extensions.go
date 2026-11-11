package server

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"tomtly/internal/auth"
	"tomtly/internal/extension"
)

// extensionRoutes are integrations, devices an extension offers, extensions themselves and the marketplace: the part of
// the API that adds, changes or removes code running in the home.
func (a *api) extensionRoutes() []route {
	hub, extensions := a.hub, a.extensions
	return []route{
		{"GET /api/integrations", auth.DevicesRead, func(w http.ResponseWriter, _ *http.Request) {
			reply(w, http.StatusOK, hub.Integrations(extensions.Statuses()))
		}},
		{"POST /api/integrations/{id}/discover", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			found, err := extensions.Discover(r.Context(), r.PathValue("id"))
			if err != nil {
				reply(w, http.StatusNotFound, errorBody("not found"))
				return
			}
			reply(w, http.StatusOK, found)
		}},
		{"DELETE /api/devices/{id}", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			a.changeDevice(w, r, "device.remove", extensions.RemoveDevice)
		}},
		{"POST /api/devices/{id}/restore", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			a.changeDevice(w, r, "device.restore", extensions.RestoreDevice)
		}},
		{"GET /api/extensions", auth.ExtensionsRead, func(w http.ResponseWriter, _ *http.Request) { reply(w, http.StatusOK, extensions.Statuses()) }},
		{"POST /api/extensions/inspect", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source string `json:"source"`
			}
			if !readJSON(w, r, &body, maxExtension) {
				return
			}
			status, err := extensions.Inspect(r.Context(), body.Source)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			reply(w, http.StatusOK, status)
		}},
		{"GET /api/marketplace", auth.ExtensionsRead, func(w http.ResponseWriter, r *http.Request) {
			reply(w, http.StatusOK, extensions.Marketplace(r.Context()))
		}},
		{"POST /api/marketplace/inspect", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				ID string `json:"id"`
			}
			if !readJSON(w, r, &body, maxBody) {
				return
			}
			status, err := extensions.MarketInspect(r.Context(), body.ID)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "marketplace.inspect", body.ID+"@"+status.Version)
			reply(w, http.StatusOK, status)
		}},
		{"GET /api/marketplace/{id}/assets/{name}", auth.ExtensionsRead, func(w http.ResponseWriter, r *http.Request) {
			data, kind, err := extensions.MarketAsset(r.Context(), r.PathValue("id"), r.PathValue("name"))
			if err != nil {
				replyMarketError(w, err)
				return
			}
			h := w.Header()
			h.Set("Content-Type", kind)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
			// The page asks with the image's hash in the address, so what's cached can't go stale.
			h.Set("Cache-Control", "private, max-age=86400")
			if _, err := w.Write(data); err != nil { // #nosec G705 -- an image whose type and hash the signed list gives, sent with nosniff and a sandbox policy
				return // the client went away
			}
		}},
		{"GET /api/marketplace/{id}/reviews", auth.ExtensionsRead, func(w http.ResponseWriter, r *http.Request) {
			offset := 0
			if v := r.URL.Query().Get("offset"); v != "" {
				var err error
				if offset, err = strconv.Atoi(v); err != nil || offset < 0 {
					reply(w, http.StatusBadRequest, errorBody("offset must be a number"))
					return
				}
			}
			page, err := extensions.MarketReviews(r.Context(), r.PathValue("id"), offset)
			if err != nil {
				replyMarketError(w, err)
				return
			}
			reply(w, http.StatusOK, page)
		}},
		{"POST /api/marketplace/{id}/reviews", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Rating int    `json:"rating"`
				Text   string `json:"text"`
				Name   string `json:"name"`
			}
			if !readJSON(w, r, &body, maxReviewBody) {
				return
			}
			id := r.PathValue("id")
			if err := extensions.MarketPostReview(r.Context(), id, body.Rating, body.Text, body.Name); err != nil {
				replyMarketError(w, err)
				return
			}
			a.audit(r, "marketplace.review", id+" rated "+strconv.Itoa(body.Rating))
			reply(w, http.StatusOK, map[string]bool{"ok": true})
		}},
		{"POST /api/extensions/upload", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			// Like JSON, a type that isn't a simple form post forces a CORS preflight, so other websites can't send files.
			if contentType(r) != "application/octet-stream" {
				reply(w, http.StatusUnsupportedMediaType, errorBody("send application/octet-stream"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, extension.MaxUpload+1)
			data, err := io.ReadAll(r.Body)
			var tooLarge *http.MaxBytesError
			switch {
			case errors.As(err, &tooLarge) || len(data) > extension.MaxUpload:
				reply(w, http.StatusRequestEntityTooLarge, errorBody("the file is over 16 MiB"))
				return
			case err != nil:
				reply(w, http.StatusBadRequest, errorBody("could not read the file"))
				return
			}
			status, err := extensions.Upload(r.Context(), r.URL.Query().Get("name"), data)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "extension.upload", r.URL.Query().Get("name"))
			reply(w, http.StatusOK, status)
		}},
		{"POST /api/extensions/approve", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source string   `json:"source"`
				Access []string `json:"access"`
			}
			if !readJSON(w, r, &body, maxExtension) {
				return
			}
			webhook, err := extensions.Approve(body.Source, body.Access)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "extension.approve", body.Source)
			reply(w, http.StatusOK, map[string]any{"approved": true, "webhook": webhook})
		}},
		{"POST /api/extensions/stop", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source string `json:"source"`
			}
			if !readJSON(w, r, &body, maxExtension) {
				return
			}
			if err := extensions.StopExtension(body.Source); err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "extension.stop", body.Source)
			reply(w, http.StatusOK, map[string]bool{"stopped": true})
		}},
		{"POST /api/extensions/settings", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source   string         `json:"source"`
				Settings map[string]any `json:"settings"`
			}
			if !readJSON(w, r, &body, maxSettings) {
				return
			}
			status, err := extensions.UpdateSettings(r.Context(), body.Source, body.Settings)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			// Which settings changed, never to what: some are secrets.
			a.audit(r, "extension.settings", body.Source+" "+strings.Join(slices.Sorted(maps.Keys(body.Settings)), ","))
			reply(w, http.StatusOK, status)
		}},
		{"POST /api/extensions/replace", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source    string         `json:"source"`
				NewSource string         `json:"newSource"`
				Settings  map[string]any `json:"settings"`
				Access    []string       `json:"access"`
			}
			if !readJSON(w, r, &body, maxSettings) {
				return
			}
			status, err := extensions.Replace(r.Context(), body.Source, body.NewSource, body.Settings, body.Access)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "extension.replace", body.Source+" -> "+body.NewSource)
			reply(w, http.StatusOK, status)
		}},
		{"POST /api/extensions/webhook-token", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source string `json:"source"`
			}
			if !readJSON(w, r, &body, maxExtension) {
				return
			}
			webhook, err := extensions.NewWebhookToken(r.Context(), body.Source)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "extension.webhook-token", body.Source)
			reply(w, http.StatusOK, map[string]string{"webhook": webhook})
		}},
		{"POST /api/extensions", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Source   string         `json:"source"`
				Settings map[string]any `json:"settings"`
				Access   []string       `json:"access"`
			}
			if !readJSON(w, r, &body, maxSettings) {
				return
			}
			status, err := extensions.Add(r.Context(), body.Source, body.Settings, body.Access)
			if err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "extension.add", body.Source)
			reply(w, http.StatusOK, status)
		}},
		{"DELETE /api/extensions", auth.ExtensionsManage, func(w http.ResponseWriter, r *http.Request) {
			if err := extensions.Remove(r.URL.Query().Get("source")); err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
			a.audit(r, "extension.remove", r.URL.Query().Get("source"))
			reply(w, http.StatusOK, map[string]bool{"removed": true})
		}},
	}
}
