// Looks for devices by broadcast with the local network it asked for. The test network has no route for a broadcast, so the
// send may fail; the attempt is what's recorded, and that's all the test checks.
defineExtension({
  id: "lan-discover",
  name: "LAN Discover",
  version: "1.0.0",
  description: "Looks for devices on the local network by broadcast.",
  access: { lan: true, sockets: true },
  data: {
    everySeconds: 3600,
    fetch(ctx) {
      try {
        ctx.udp.discover(9999, "68656c6c6f");
      } catch {
        // Caught: the attempt is recorded before the send.
      }
      return { looked: true };
    },
  },
});
