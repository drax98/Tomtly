package automation

import (
	"path/filepath"
	"testing"
	"time"

	"home-control/device"
	"home-control/internal/store"
)

// These run the shipped example scripts against what such a house should do.

const (
	controller = "heating-controller"
	relay      = "heater-relay"
	bathroom   = "thermostat-bathroom"
	bedroom    = "thermostat-bedroom"
	paula      = "thermostat-kids"
	arek       = "thermostat-office"
	salon      = "thermostat-living"
	bedWindow  = "window-bedroom"
	paulaWin1  = "window-kids-1"
	paulaWin2  = "window-kids-2"
	arekWindow = "window-office"
	salonWin   = "window-living"
	plug       = "lamp-plug"
)

func thermostat(id string, calling any) device.Device {
	return device.Device{ID: id, Name: id, Online: true, DPs: []device.DP{{DP: "102", Code: "heating_state", Type: device.Boolean, Value: calling}}}
}

func window(id string, open any) device.Device {
	return device.Device{ID: id, Name: id, Online: true, DPs: []device.DP{{DP: "101", Code: "open", Type: device.Boolean, Value: open}}}
}

func channelsOff() device.Device {
	d := device.Device{ID: controller, Name: "5CH", Online: true}
	for i, code := range []string{"switch_1", "switch_2", "switch_3", "switch_4", "switch_5"} {
		d.DPs = append(d.DPs, device.DP{DP: string(rune('1' + i)), Code: code, Type: device.Boolean, Writable: true, Value: false})
	}
	return d
}

func house() *fakeHub {
	return &fakeHub{apply: true, devices: []device.Device{
		channelsOff(),
		{ID: relay, Name: "Relay", Online: true, DPs: []device.DP{{DP: "1", Code: "switch_1", Type: device.Boolean, Writable: true, Value: false}}},
		thermostat(bathroom, false), thermostat(bedroom, false), thermostat(paula, false), thermostat(arek, false), thermostat(salon, false),
		window(bedWindow, false), window(paulaWin1, false), window(paulaWin2, false), window(arekWindow, false), window(salonWin, false),
		{ID: plug, Name: "Plug", Online: true, DPs: []device.DP{{DP: "1", Code: "switch", Type: device.Boolean, Writable: true, Value: false}}},
	}}
}

func (h *fakeHub) value(id, dp string) any {
	for _, d := range h.devices {
		if d.ID == id {
			for _, x := range d.DPs {
				if x.DP == dp {
					return x.Value
				}
			}
		}
	}
	return nil
}

func liveEngine(t *testing.T, hub *fakeHub) (*Engine, *time.Time) {
	t.Helper()
	root := filepath.Join("..", "..")
	e := New(hub, filepath.Join(root, "examples", "automations"), filepath.Join(root, "sdk"), store.NewMemory(), time.UTC)
	clock := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	e.Reload()
	for _, s := range e.Status().Scripts {
		if s.Error != "" {
			t.Fatalf("%s failed to load: %s", s.Name, s.Error)
		}
		if err := e.SetLive(s.Name, true); err != nil {
			t.Fatal(err)
		}
	}
	return e, &clock
}

// settle ticks until a chain of decisions has played out: thermostat, then channel, then relay.
func settle(e *Engine) {
	for range 4 {
		e.Tick()
	}
}

func TestRoomChannelsFollowTheirThermostatAndWindows(t *testing.T) {
	for _, c := range []struct {
		name       string
		thermostat string
		channel    string
		windows    []string
	}{
		{"bathroom", bathroom, "1", nil},
		{"bedroom", bedroom, "2", []string{bedWindow}},
		{"paula", paula, "3", []string{paulaWin1, paulaWin2}},
		{"arek", arek, "4", []string{arekWindow}},
		{"salon", salon, "5", []string{salonWin}},
	} {
		t.Run(c.name, func(t *testing.T) {
			hub := house()
			e, _ := liveEngine(t, hub)
			settle(e)
			if hub.value(controller, c.channel) != false || hub.value(relay, "1") != false {
				t.Fatal("nothing should heat with no call for heat")
			}
			hub.setDP(c.thermostat, "102", true)
			settle(e)
			if hub.value(controller, c.channel) != true || hub.value(relay, "1") != true {
				t.Fatalf("a call for heat must open channel %s and fire the heater", c.channel)
			}
			for _, w := range c.windows {
				hub.setDP(w, "101", true)
				settle(e)
				if hub.value(controller, c.channel) != false {
					t.Fatalf("window %s open must close channel %s", w, c.channel)
				}
				if hub.value(relay, "1") != false {
					t.Fatal("the heater must stop when no channel is open")
				}
				hub.setDP(w, "101", false)
				settle(e)
				if hub.value(controller, c.channel) != true {
					t.Fatalf("closing window %s must reopen channel %s", w, c.channel)
				}
			}
			hub.setDP(c.thermostat, "102", false)
			settle(e)
			if hub.value(controller, c.channel) != false || hub.value(relay, "1") != false {
				t.Fatal("heating must stop when the thermostat stops calling")
			}
		})
	}
}

func TestHeaterRunsWhileAnyChannelIsOn(t *testing.T) {
	hub := house()
	e, _ := liveEngine(t, hub)
	hub.setDP(salon, "102", true)
	hub.setDP(arek, "102", true)
	settle(e)
	hub.setDP(salon, "102", false)
	settle(e)
	if hub.value(relay, "1") != true {
		t.Fatal("the heater must keep running for the remaining room")
	}
	hub.setDP(arek, "102", false)
	settle(e)
	if hub.value(relay, "1") != false {
		t.Fatal("the heater must stop with the last room")
	}
}

func TestUnknownWindowChangesNothingButOpenWindowWins(t *testing.T) {
	hub := house()
	e, _ := liveEngine(t, hub)
	hub.setDP(paula, "102", true)
	hub.setDP(paulaWin1, "101", nil) // not reported since a restart
	settle(e)
	if hub.value(controller, "3") != false {
		t.Fatal("must not start heating past a window it can't see")
	}
	hub.setDP(paulaWin2, "101", true)
	settle(e)
	if hub.value(controller, "3") != false {
		t.Fatal("the known open window must still keep the channel closed")
	}
}

func TestUnknownThermostatLeavesChannelAlone(t *testing.T) {
	hub := house()
	e, _ := liveEngine(t, hub)
	hub.setDP(salon, "102", true)
	settle(e)
	hub.setDP(salon, "102", nil)
	settle(e)
	if hub.value(controller, "5") != true {
		t.Fatal("an unknown value is not a reason to change the channel")
	}
}

func (h *fakeHub) setOnline(id string, online bool) {
	for i := range h.devices {
		if h.devices[i].ID == id {
			h.devices[i].Online = online
		}
	}
}

func TestLostThermostatStopsHeatingOnlyAfterAWhile(t *testing.T) {
	hub := house()
	e, clock := liveEngine(t, hub)
	hub.setDP(salon, "102", true)
	settle(e)
	hub.setOnline(salon, false)
	*clock = clock.Add(2 * time.Minute)
	settle(e)
	if hub.value(controller, "5") != true || hub.value(relay, "1") != true {
		t.Fatal("a short gap, like a gateway reconnect, must not cycle the gas heater")
	}
	*clock = clock.Add(4 * time.Minute)
	settle(e)
	if hub.value(controller, "5") != false || hub.value(relay, "1") != false {
		t.Fatal("a thermostat silent this long must not keep the gas burning")
	}
}

func TestReconnectDoesNotCycleHeater(t *testing.T) {
	hub := house()
	e, _ := liveEngine(t, hub)
	hub.setDP(salon, "102", true)
	settle(e)
	writes := len(hub.writes)
	for _, d := range hub.devices {
		hub.setOnline(d.ID, false)
	}
	settle(e)
	hub.setOnline(controller, true) // the controller reports before the thermostat does
	settle(e)
	hub.setOnline(salon, true)
	settle(e)
	if len(hub.writes) != writes {
		t.Fatalf("a reconnect sent commands: %v", hub.writes[writes:])
	}
}

func TestRestartWithUnknownValuesSendsNothing(t *testing.T) {
	hub := house()
	for i := range hub.devices {
		hub.devices[i].Online = false
		if hub.devices[i].ID != controller && hub.devices[i].ID != relay {
			for j := range hub.devices[i].DPs {
				hub.devices[i].DPs[j].Value = nil
			}
		}
	}
	hub.setDP(controller, "5", true) // heating was running when the service restarted
	hub.setDP(relay, "1", true)
	hub.setOnline(controller, true)
	hub.setOnline(relay, true)
	e, _ := liveEngine(t, hub)
	settle(e)
	if len(hub.writes) != 0 {
		t.Fatalf("a restart sent commands before it knew anything: %v", hub.writes)
	}
}

func TestLightFollowsItsSchedule(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	hub := house()
	e, clock := liveEngine(t, hub)
	e.location = warsaw
	*clock = time.Date(2026, 11, 2, 8, 30, 0, 0, warsaw)
	e.Tick()
	if hub.value(plug, "1") != true {
		t.Fatal("the light must turn on at 08:30")
	}
	hub.setDP(plug, "1", false) // switched off by hand
	*clock = clock.Add(time.Hour)
	e.Tick()
	if hub.value(plug, "1") != false {
		t.Fatal("a manual change must stick between schedules")
	}
	hub.setDP(plug, "1", true)
	*clock = time.Date(2026, 11, 3, 0, 30, 0, 0, warsaw)
	e.Tick()
	if hub.value(plug, "1") != false {
		t.Fatal("the light must turn off at 00:30")
	}
}
