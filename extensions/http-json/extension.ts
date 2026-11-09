// Data for scripts from any service that answers a GET with JSON, written in any language.
const settings = {
  host: { label: "Address", type: "string", required: true, help: "The service's host and port, such as prices.local:9000." },
  path: { label: "Path", type: "string", default: "/", help: "What to ask for, such as /today." },
  https: { label: "Use HTTPS", type: "boolean", default: false, help: "Hosts on the internet need it." },
  token: { label: "Token", type: "secret", sendTo: ["{host}"], help: "Optional. Sent as a bearer token, only to this address." },
} as const;

defineExtension({
  id: "http-json",
  name: "JSON service",
  version: "1.0.1",
  license: "MIT",
  description: "Data for scripts from any service that answers a GET with JSON.",
  categories: ["other"],
  tags: ["HTTP", "JSON", "REST", "API"],
  access: { network: ["{host}"] },
  settings,
  data: {
    everySeconds: 300,
    fetch(ctx) {
      const scheme = ctx.settings.https ? "https" : "http";
      const headers: Record<string, string> = ctx.settings.token ? { Authorization: `Bearer ${ctx.settings.token}` } : {};
      const response = ctx.fetch(`${scheme}://${ctx.settings.host}${ctx.settings.path}`, { headers });
      if (response.status !== 200) throw new Error(`the service answered ${response.status}`);
      return response.json();
    },
  },
});
