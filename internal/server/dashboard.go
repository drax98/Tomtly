package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"tomtly/internal/automation"
	"tomtly/internal/extension"
	"tomtly/internal/widget"
)

const maxDashboard = 30

// widgetKey is "<owner>/<key>": owner is builtin (drawn by the page itself), script:<name> or ext:<id>.
var widgetKey = regexp.MustCompile(`^(builtin|script:[A-Za-z0-9][A-Za-z0-9_-]{0,63}|ext:[a-z0-9-]{1,64})/[A-Za-z][A-Za-z0-9_-]{0,39}$`)

// getWidgets answers what Home shows: the widgets placed on it in order, and every widget there is to place, as last
// drawn. A placed widget whose script or extension is gone stays placed, and the page says so.
func (a *api) getWidgets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	layout, err := a.store.Dashboard(ctx)
	if err != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("Home can't be read right now"))
		return
	}
	if layout == nil {
		layout = []string{}
	}
	widgets := append(a.engine.Widgets(), a.extensions.Widgets()...)
	reply(w, http.StatusOK, map[string]any{"layout": layout, "widgets": widgets})
}

// setDashboard replaces which widgets are on Home and their order. The whole home shares one, like pinned values.
func (a *api) setDashboard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Widgets []string `json:"widgets"`
	}
	if !readJSON(w, r, &body, 8192) {
		return
	}
	if len(body.Widgets) > maxDashboard {
		reply(w, http.StatusBadRequest, errorBody("Home holds up to 30 widgets"))
		return
	}
	for i, key := range body.Widgets {
		if !widgetKey.MatchString(key) || slices.Contains(body.Widgets[:i], key) {
			reply(w, http.StatusBadRequest, errorBody("not a widget, or listed twice: "+clip(key, 120)))
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	if err := a.store.SetDashboard(ctx, body.Widgets); err != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("Home wasn't saved. Try again."))
		return
	}
	a.audit(r, "dashboard.set", strings.Join(body.Widgets, ", "))
	reply(w, http.StatusOK, map[string]any{"layout": body.Widgets})
}

// widgetAction runs what a button on a script's or extension's widget does. Only a button the widget shows now can be
// pressed, so this reaches nothing a widget doesn't offer.
func (a *api) widgetAction(w http.ResponseWriter, r *http.Request) {
	owner, key, action := r.PathValue("owner"), r.PathValue("key"), r.PathValue("action")
	var body struct {
		Input json.RawMessage `json:"input"`
	}
	if !readJSON(w, r, &body, widget.MaxInput+512) {
		return
	}
	input, err := widget.Input(body.Input)
	if err != nil {
		reply(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}
	if !widgetKey.MatchString(owner+"/"+key) || !widget.Name.MatchString(action) {
		reply(w, http.StatusNotFound, errorBody(automation.ErrNoWidget.Error()))
		return
	}
	kind, name, _ := strings.Cut(owner, ":")
	answer := map[string]any{}
	switch kind {
	case "script":
		live, err := a.engine.WidgetAction(name, key, action, input)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, automation.ErrNoWidget) {
				status = http.StatusNotFound
			}
			reply(w, status, errorBody(err.Error()))
			return
		}
		answer["live"] = live
	case "ext":
		err := a.extensions.WidgetAction(name, key, action, input)
		if errors.Is(err, extension.ErrNoWidget) {
			reply(w, http.StatusNotFound, errorBody(err.Error()))
			return
		}
		if err != nil {
			reply(w, http.StatusServiceUnavailable, errorBody("the extension didn't take it. Try again."))
			return
		}
		answer["live"] = true
	default:
		reply(w, http.StatusNotFound, errorBody(automation.ErrNoWidget.Error()))
		return
	}
	a.audit(r, "widget.action", owner+"/"+key+" "+action)
	reply(w, http.StatusOK, answer)
}
