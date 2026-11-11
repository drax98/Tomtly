// A credentials file chosen on the page: the code gets its text, and says only how many devices it holds.
defineExtension({
  id: "file-plugin",
  name: "File Plugin",
  version: "1.0.0",
  description: "Reads a devices file chosen on the page.",
  settings: {
    devices: { label: "Devices file", type: "file", required: true, secretFields: ["key"], help: "A JSON list of devices with their keys." },
  },
  data: { everySeconds: 60, fetch: (ctx) => ({ devices: JSON.parse(ctx.settings.devices).length }) },
});
