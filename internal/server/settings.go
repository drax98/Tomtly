package server

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"tomtly/internal/store"
)

// A language tag like "en", "pl" or "pt-BR".
var languageTag = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)

func (a *api) getHomeSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	h, err := a.store.HomeSettings(ctx)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("settings can't be read right now"))
		return
	}
	reply(w, http.StatusOK, h)
}

// setHomeSettings replaces them whole. Empty language or timezone is allowed, so a field can be left unchosen.
func (a *api) setHomeSettings(w http.ResponseWriter, r *http.Request) {
	var h store.HomeSettings
	if !readJSON(w, r, &h, 4096) {
		return
	}
	if msg := checkHomeSettings(h); msg != "" {
		reply(w, http.StatusBadRequest, errorBody(msg))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	if err := a.store.SetHomeSettings(ctx, h); err != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("the settings weren't saved. Try again."))
		return
	}
	where := "no location"
	if h.Latitude != nil {
		where = fmt.Sprintf("%.4f,%.4f", *h.Latitude, *h.Longitude)
	}
	a.audit(r, "settings", fmt.Sprintf("language %q, timezone %q, %s", h.Language, h.Timezone, where))
	reply(w, http.StatusOK, h)
}

func checkHomeSettings(h store.HomeSettings) string {
	if h.Language != "" && !languageTag.MatchString(h.Language) {
		return "the language should be a code like en or pl"
	}
	// "Local" and "UTC" load too, but only a named zone means the same thing on every machine.
	if _, err := time.LoadLocation(h.Timezone); h.Timezone != "" && (err != nil || h.Timezone == "Local") {
		return "there is no timezone called " + clip(h.Timezone, 64)
	}
	if (h.Latitude == nil) != (h.Longitude == nil) {
		return "the location needs both a latitude and a longitude, or neither"
	}
	if h.Latitude != nil && (*h.Latitude < -90 || *h.Latitude > 90 || *h.Longitude < -180 || *h.Longitude > 180) {
		return "the latitude should be between -90 and 90, and the longitude between -180 and 180"
	}
	return ""
}
