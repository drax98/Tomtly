package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"unicode/utf8"

	"tomtly/device"
	"tomtly/internal/store"
)

const (
	maxDeviceName = 80
	maxRoomName   = 60
	maxRooms      = 100
)

// pageDevice is a device as the page sees it: under the owner's name for it, with the integration's kept beside it.
type pageDevice struct {
	device.Device
	OriginalName string `json:"originalName,omitempty"`
}

// loadLocked reads what the owner changed about devices and rooms, once, since the page asks for devices every few seconds.
// The first time there are no rooms saved, config.json's are copied in with their devices; from then on the page manages
// them. A read that fails is tried again next time. The caller holds overridesMu.
func (a *api) loadLocked(ctx context.Context) error {
	if a.overrides != nil && a.roomList != nil {
		return nil
	}
	list, err := a.store.DeviceOverrides(ctx)
	if err != nil {
		return err
	}
	overrides := map[string]store.DeviceOverride{}
	for _, o := range list {
		overrides[o.ID] = o
	}
	names, seeded, err := a.store.Rooms(ctx)
	if err != nil {
		return err
	}
	if !seeded {
		// In the file's order where it's known; anything it missed goes after, by name.
		names = nil
		for _, name := range a.roomOrder {
			if _, ok := a.rooms[name]; ok && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(a.rooms)) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		for _, room := range names {
			for _, id := range a.rooms[room] {
				o := overrides[id]
				if o.Room != nil {
					continue // moved on the page already
				}
				o.ID, o.Room = id, &room
				if err := a.store.SetDeviceOverride(ctx, o); err != nil {
					return err
				}
				overrides[id] = o
			}
		}
		for _, o := range sortedOverrides(overrides) {
			if o.Room != nil && *o.Room != "" && !slices.Contains(names, *o.Room) {
				names = append(names, *o.Room)
			}
		}
		if err := a.store.SetRooms(ctx, names); err != nil {
			return err
		}
	}
	a.overrides, a.roomList = overrides, append([]string{}, names...)
	return nil
}

// overridesNow is the owner's changes to devices. Until they can be read, devices show as their integrations name them.
func (a *api) overridesNow(ctx context.Context) map[string]store.DeviceOverride {
	a.overridesMu.Lock()
	defer a.overridesMu.Unlock()
	if a.loadLocked(ctx) != nil {
		return map[string]store.DeviceOverride{}
	}
	return maps.Clone(a.overrides)
}

func (a *api) getDevices(w http.ResponseWriter, r *http.Request) {
	overrides := a.overridesNow(r.Context())
	out := []pageDevice{}
	for _, d := range a.hub.Devices() {
		p := pageDevice{Device: d}
		if o, ok := overrides[d.ID]; ok && o.Name != "" {
			p.OriginalName, p.Name = d.Name, o.Name
		}
		out = append(out, p)
	}
	reply(w, http.StatusOK, out)
}

type room struct {
	Name    string
	Devices []string
}

// roomsNow is every room in order, each with its devices. Until rooms can be read, it's config.json's.
func (a *api) roomsNow(ctx context.Context) []room {
	a.overridesMu.Lock()
	defer a.overridesMu.Unlock()
	if a.loadLocked(ctx) != nil {
		out := []room{}
		for _, name := range slices.Sorted(maps.Keys(a.rooms)) {
			out = append(out, room{name, a.rooms[name]})
		}
		return out
	}
	out := make([]room, 0, len(a.roomList))
	for _, name := range a.roomList {
		r := room{Name: name, Devices: []string{}}
		for _, o := range sortedOverrides(a.overrides) {
			if o.Room != nil && *o.Room == name {
				r.Devices = append(r.Devices, o.ID)
			}
		}
		out = append(out, r)
	}
	return out
}

// getRooms answers rooms as one object, room name to device ids, written in order: a Go map would sort the names, and
// the page lists rooms in the order the object's keys come in.
func (a *api) getRooms(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, room := range a.roomsNow(r.Context()) {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, _ := json.Marshal(room.Name)
		ids, _ := json.Marshal(room.Devices)
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(ids)
	}
	buf.WriteByte('}')
	w.Header().Set("Content-Type", "application/json")
	w.Write(buf.Bytes()) //nolint:errcheck // the client went away
}

func sortedOverrides(m map[string]store.DeviceOverride) []store.DeviceOverride {
	out := slices.Collect(maps.Values(m))
	slices.SortFunc(out, func(x, y store.DeviceOverride) int { return strings.Compare(x.ID, y.ID) })
	return out
}

func cleanName(s string, limit int) (string, bool) {
	s = strings.TrimSpace(s)
	return s, utf8.RuneCountInString(s) <= limit && !strings.ContainsFunc(s, func(r rune) bool { return r < ' ' || r == 0x7f })
}

// addRoomLocked puts a new room at the end of the list. The caller holds overridesMu and has loaded.
func (a *api) addRoomLocked(ctx context.Context, name string) error {
	if slices.Contains(a.roomList, name) {
		return nil
	}
	if len(a.roomList) >= maxRooms {
		return errTooManyRooms
	}
	next := append(slices.Clone(a.roomList), name)
	if err := a.store.SetRooms(ctx, next); err != nil {
		return err
	}
	a.roomList = next
	return nil
}

var errTooManyRooms = errors.New("there can be up to 100 rooms")

// editDevice renames a device or moves it to a room. Leaving a field out keeps it; an empty name goes back to the
// integration's, and an empty room is no room. A room that doesn't exist yet is added.
func (a *api) editDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name *string         `json:"name"`
		Room json.RawMessage `json:"room"`
	}
	if !readJSON(w, r, &body, 2048) {
		return
	}
	if !slices.ContainsFunc(a.hub.Devices(), func(d device.Device) bool { return d.ID == id }) {
		reply(w, http.StatusNotFound, errorBody("no such device"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	a.overridesMu.Lock()
	defer a.overridesMu.Unlock()
	if a.loadLocked(ctx) != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("devices can't be changed right now. Try again."))
		return
	}
	next := a.overrides[id]
	next.ID = id
	if body.Name != nil {
		name, ok := cleanName(*body.Name, maxDeviceName)
		if !ok {
			reply(w, http.StatusBadRequest, errorBody("a name is up to 80 characters on one line"))
			return
		}
		next.Name = name
	}
	if raw := strings.TrimSpace(string(body.Room)); raw != "" {
		var name string
		if json.Unmarshal(body.Room, &name) != nil {
			reply(w, http.StatusBadRequest, errorBody("room is a name, or empty for none"))
			return
		}
		name, ok := cleanName(name, maxRoomName)
		if !ok {
			reply(w, http.StatusBadRequest, errorBody("a room's name is up to 60 characters on one line"))
			return
		}
		if name != "" {
			if err := a.addRoomLocked(ctx, name); err != nil {
				reply(w, http.StatusBadRequest, errorBody(err.Error()))
				return
			}
		}
		next.Room = &name
	}
	if err := a.store.SetDeviceOverride(ctx, next); err != nil {
		reply(w, http.StatusServiceUnavailable, errorBody("the change wasn't saved. Try again."))
		return
	}
	a.overrides[id] = next
	room := "none"
	if next.Room != nil && *next.Room != "" {
		room = *next.Room
	}
	a.audit(r, "device.edit", id+": name "+next.Name+", room "+room)
	reply(w, http.StatusOK, next)
}

// changeRooms runs one change to the room list with it loaded and locked, and answers with the rooms as they are after it.
func (a *api) changeRooms(w http.ResponseWriter, r *http.Request, action, detail string, change func(ctx context.Context) (int, string)) {
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	a.overridesMu.Lock()
	if a.loadLocked(ctx) != nil {
		a.overridesMu.Unlock()
		reply(w, http.StatusServiceUnavailable, errorBody("rooms can't be changed right now. Try again."))
		return
	}
	code, problem := change(ctx)
	a.overridesMu.Unlock()
	if problem != "" {
		reply(w, code, errorBody(problem))
		return
	}
	a.audit(r, action, detail)
	a.getRooms(w, r)
}

func roomName(raw string) (string, string) {
	name, ok := cleanName(raw, maxRoomName)
	if !ok || name == "" {
		return "", "a room's name is 1 to 60 characters on one line"
	}
	return name, ""
}

func (a *api) addRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body, 1024) {
		return
	}
	name, problem := roomName(body.Name)
	if problem != "" {
		reply(w, http.StatusBadRequest, errorBody(problem))
		return
	}
	a.changeRooms(w, r, "room.add", name, func(ctx context.Context) (int, string) {
		if slices.Contains(a.roomList, name) {
			return http.StatusConflict, "there's already a room called " + name
		}
		if err := a.addRoomLocked(ctx, name); err != nil {
			return http.StatusBadRequest, err.Error()
		}
		return 0, ""
	})
}

// editRoom renames a room, its devices moving with it, or moves it to another place in the order.
func (a *api) editRoom(w http.ResponseWriter, r *http.Request) {
	from := r.PathValue("name")
	var body struct {
		Name     *string `json:"name"`
		Position *int    `json:"position"`
	}
	if !readJSON(w, r, &body, 1024) {
		return
	}
	to := from
	if body.Name != nil {
		name, problem := roomName(*body.Name)
		if problem != "" {
			reply(w, http.StatusBadRequest, errorBody(problem))
			return
		}
		to = name
	}
	a.changeRooms(w, r, "room.edit", from+" to "+to, func(ctx context.Context) (int, string) {
		i := slices.Index(a.roomList, from)
		if i < 0 {
			return http.StatusNotFound, "no such room"
		}
		if to != from {
			if slices.Contains(a.roomList, to) {
				return http.StatusConflict, "there's already a room called " + to
			}
			if a.store.RenameRoom(ctx, from, to) != nil {
				return http.StatusServiceUnavailable, "the room wasn't renamed. Try again."
			}
			a.roomList[i] = to
			for id, o := range a.overrides {
				if o.Room != nil && *o.Room == from {
					o.Room = &to
					a.overrides[id] = o
				}
			}
		}
		if body.Position != nil {
			next := slices.Delete(slices.Clone(a.roomList), i, i+1)
			at := min(max(*body.Position, 0), len(next))
			next = slices.Insert(next, at, to)
			if a.store.SetRooms(ctx, next) != nil {
				return http.StatusServiceUnavailable, "the order wasn't saved. Try again."
			}
			a.roomList = next
		}
		return 0, ""
	})
}

// deleteRoom removes a room. Its devices stay, in no room.
func (a *api) deleteRoom(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	a.changeRooms(w, r, "room.delete", name, func(ctx context.Context) (int, string) {
		i := slices.Index(a.roomList, name)
		if i < 0 {
			return http.StatusNotFound, "no such room"
		}
		if a.store.DeleteRoom(ctx, name) != nil {
			return http.StatusServiceUnavailable, "the room wasn't deleted. Try again."
		}
		a.roomList = slices.Delete(a.roomList, i, i+1)
		none := ""
		for id, o := range a.overrides {
			if o.Room != nil && *o.Room == name {
				o.Room = &none
				a.overrides[id] = o
			}
		}
		return 0, ""
	})
}
