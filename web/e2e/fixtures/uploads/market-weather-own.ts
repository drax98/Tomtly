// The marketplace's id with other bytes: someone's own copy, which must not count as the listed one.
defineExtension({
  id: "market-weather",
  name: "My Own Weather",
  version: "1.0.0",
  description: "Not the copy the marketplace lists.",
  data: { everySeconds: 60, fetch: () => ({ mine: true }) },
});
