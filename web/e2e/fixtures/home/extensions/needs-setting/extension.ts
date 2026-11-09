// config.json doesn't set what this requires, so its card has to say so.
defineExtension({
  id: "needs-setting",
  name: "Needs Setting",
  version: "1.0.0",
  description: "A required setting is missing.",
  settings: {
    city: { label: "City name", type: "string", required: true },
    units: { label: "Units", type: "string", default: "metric" },
  },
  data: { everySeconds: 60, fetch: (ctx) => ({ city: ctx.settings.city }) },
});
