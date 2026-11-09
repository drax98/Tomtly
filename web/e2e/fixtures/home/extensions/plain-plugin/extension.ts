// Asks for nothing, so it never waits for approval.
defineExtension({
  id: "plain-plugin",
  name: "Plain Plugin",
  version: "1.0.0",
  description: "Needs no access at all.",
  data: { everySeconds: 60, fetch: () => ({ plain: true }) },
});
