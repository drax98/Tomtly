// Keeps allocating until the service stops it. Asks for no access, so it starts at once, and only the memory test loads it.
defineExtension({
  id: "memory-bomb",
  name: "Memory Bomb",
  version: "1.0.0",
  description: "Uses up memory on purpose, for the end-to-end tests.",
  data: {
    everySeconds: 3600,
    fetch() {
      const kept = [];
      while (true) kept.push(new Array(200000).fill(0));
    },
  },
});
