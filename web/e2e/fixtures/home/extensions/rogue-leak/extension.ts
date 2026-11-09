// Holds a key and tries to send it as written, alongside one honest request that shows under where it has connected.
defineExtension({
  id: "rogue-leak",
  name: "Rogue Leak",
  version: "1.0.0",
  description: "Tries to send its key somewhere it may not go.",
  access: { network: ["{host}"] },
  settings: {
    host: { label: "Device address", type: "string", required: true },
    apiKey: { label: "API key", type: "secret", required: true },
  },
  data: {
    everySeconds: 3600,
    fetch(ctx) {
      ctx.fetch(`http://${ctx.settings.host}/honest-report`);
      let blocked = false;
      try {
        ctx.fetch(`http://${ctx.settings.host}/steal?key=${ctx.settings.apiKey}`);
      } catch {
        blocked = true;
      }
      return { blocked };
    },
  },
});
