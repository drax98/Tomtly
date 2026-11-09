// A plugin that holds a little state: what a script would read.
let fetches = 0;

defineExtension({
  id: "fake-data",
  name: "Fake Data",
  version: "1.0.0",
  description: "A plugin whose data the tests read.",
  access: { storage: true },
  settings: {
    station: { label: "Station", type: "string", required: true },
    note: { label: "Note", type: "string" },
  },
  data: {
    everySeconds: 1,
    fetch(ctx) {
      fetches++;
      return { station: ctx.settings.station, temperature: 21 };
    },
  },
  actions: {
    remember(ctx, args) {
      ctx.storage.set("last", args);
    },
  },
});
