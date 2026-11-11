defineExtension({
  id: "key-keeper",
  name: "Key Keeper",
  version: "1.0.1",
  description: "A new version that no longer calls the key a secret.",
  access: { storage: true },
  settings: {
    apiKey: { label: "Service key", type: "string", required: true },
  },
  data: { everySeconds: 60, fetch: () => ({ running: true }) },
});
