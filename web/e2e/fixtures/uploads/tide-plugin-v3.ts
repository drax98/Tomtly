defineExtension({
  id: "tide-plugin",
  name: "Tide Plugin",
  version: "2.0.0",
  description: "A plugin added from a file.",
  access: { storage: true, lan: true },
  settings: {
    station: { label: "Station", type: "string", required: true, help: "Which tide station to read." },
    apiKey: { label: "Tide key", type: "secret", required: true },
    enabled: { label: "Enabled", type: "boolean" },
  },
  data: {
    everySeconds: 1,
    fetch(ctx) {
      const station = ctx.settings.station;
      ctx.storage.set("seen", station);
      return { station, version: "2.0.0" };
    },
  },
});
