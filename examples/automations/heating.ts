// Radiators split into rooms: each valve follows its thermostat unless a window is open, and the heater runs while any
// valve is open. Replace these ids with your own, listed by GET /api/devices.
const controller = home.device("heating-controller");
const heater = home.device("heater-relay");

interface Room {
  channel: string;
  thermostat: Device;
  windows: Device[];
}

const rooms: Room[] = [
  { channel: "switch_1", thermostat: home.device("thermostat-bathroom"), windows: [] },
  { channel: "switch_2", thermostat: home.device("thermostat-bedroom"), windows: [home.device("window-bedroom")] },
  { channel: "switch_3", thermostat: home.device("thermostat-kids"), windows: [home.device("window-kids-1"), home.device("window-kids-2")] },
  { channel: "switch_4", thermostat: home.device("thermostat-office"), windows: [home.device("window-office")] },
  { channel: "switch_5", thermostat: home.device("thermostat-living"), windows: [home.device("window-living")] },
];

// A silent thermostat stops counting as a call for heat, so a lost device can't keep the heater running.
// Long enough to ride out a gateway reconnect or a restart, which must not cycle the heater.
const lostAfterSeconds = 300;

home.reconcile(() => {
  for (const room of rooms) {
    const lost = room.thermostat.offlineSeconds() > lostAfterSeconds;
    const calling = lost ? false : room.thermostat.get("heating_state");
    const windows = room.windows.map((window) => window.get("open"));

    if (calling === false || windows.includes(true)) {
      controller.set(room.channel, false);
    } else if (calling === true && !windows.includes(undefined)) {
      controller.set(room.channel, true);
    }
    // Otherwise something isn't known yet, typically a sensor that hasn't reported since a restart: change nothing.
  }

  const channels = rooms.map((room) => controller.get(room.channel));
  if (channels.includes(true)) {
    heater.set("switch_1", true);
  } else if (!channels.includes(undefined)) {
    heater.set("switch_1", false);
  }
});
