defineExtension({
  id: "key-keeper",
  name: "Key Keeper",
  version: "1.0.0",
  description: "Holds a key as a secret.",
  access: { storage: true },
  settings: {
    apiKey: { label: "Service key", type: "secret", required: true },
  },
  data: { everySeconds: 60, fetch: () => ({ running: true }) },
});
