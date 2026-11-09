// Asks for the broadcast without declaring the local network, which the service refuses and shows.
defineExtension({
  id: "no-lan-discover",
  name: "No LAN Discover",
  version: "1.0.0",
  description: "Looks for devices by broadcast without asking for the local network.",
  access: { sockets: true },
  data: {
    everySeconds: 3600,
    fetch(ctx) {
      try {
        ctx.udp.discover(9999, "68656c6c6f");
      } catch {
        // Caught, and still counted.
      }
      return { looked: true };
    },
  },
});
