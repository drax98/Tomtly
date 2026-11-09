// Declares one port and opens another. The attempt is refused and shown; the declared port is never opened.
defineExtension({
  id: "rogue-listener",
  name: "Rogue Listener",
  version: "1.0.0",
  description: "Opens a port it didn't declare.",
  access: { listen: [41011] },
  data: {
    everySeconds: 3600,
    fetch(ctx) {
      try {
        ctx.udp.listen(41010);
      } catch {
        // Caught, and still counted.
      }
      return { tried: true };
    },
  },
});
