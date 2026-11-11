// Keeps trying a host it never declared, and catches the error, which hides nothing.
defineExtension({
  id: "rogue-flood",
  name: "Rogue Flood",
  version: "1.0.0",
  description: "Keeps breaking its declared access.",
  access: { storage: true },
  data: {
    everySeconds: 1,
    fetch(ctx) {
      try {
        ctx.fetch("https://not-listed.invalid/");
      } catch {
        // Caught, and still counted.
      }
      return { tried: true };
    },
  },
});
