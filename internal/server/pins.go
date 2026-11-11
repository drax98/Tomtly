package server

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"tomtly/device"
)

// maxPins keeps a card a card: more than this and the drawer is the place for them.
const maxPins = 12

func (a *api) getPins(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	pins, err := a.store.DevicePins(ctx)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("pins can't be read right now"))
		return
	}
	reply(w, http.StatusOK, pins)
}

// setPins replaces one device's pins. Only values the device reports can be pinned, so the list can't fill with junk.
func (a *api) setPins(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		DPs []string `json:"dps"`
	}
	if !readJSON(w, r, &body, 4096) {
		return
	}
	devices := a.hub.Devices()
	i := slices.IndexFunc(devices, func(d device.Device) bool { return d.ID == id })
	if i < 0 {
		reply(w, http.StatusNotFound, errorBody("no such device"))
		return
	}
	known := devices[i].DPs
	if len(body.DPs) > maxPins {
		reply(w, http.StatusBadRequest, errorBody("at most 12 values can be pinned to a card"))
		return
	}
	seen := map[string]bool{}
	for _, dp := range body.DPs {
		if seen[dp] || !slices.ContainsFunc(known, func(k device.DP) bool { return k.DP == dp }) {
			reply(w, http.StatusBadRequest, errorBody("the device has no value "+clip(dp, 64)+" to pin, or it's listed twice"))
			return
		}
		seen[dp] = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	if err := a.store.SetDevicePins(ctx, id, body.DPs); err != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("the pins weren't saved. Try again."))
		return
	}
	a.audit(r, "device.pins", id+": "+strings.Join(body.DPs, ","))
	reply(w, http.StatusOK, map[string][]string{"dps": body.DPs})
}
