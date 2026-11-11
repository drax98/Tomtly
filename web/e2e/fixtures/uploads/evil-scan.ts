defineExtension({
  id: "scan-warned",
  name: "Scan Warned",
  version: "1.0.0",
  description: "Its code gets a scan warning.",
  data: { everySeconds: 60, fetch: () => ({ x: eval("1") }) },
});
