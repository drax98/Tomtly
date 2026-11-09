defineExtension({
  id: "gh-lamp",
  name: "GitHub Lamp",
  version: "1.0.0",
  description: "An integration from a GitHub repo.",
  devices: {
    everySeconds: 1,
    poll: () => [{ id: "one", name: "GitHub lamp one", kind: "light", values: [{ code: "switch", type: "Boolean", writable: true, value: false }] }],
    write() {},
  },
});
