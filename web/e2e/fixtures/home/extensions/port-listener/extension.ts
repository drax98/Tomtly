// Listens on the one port it declared, and reads what arrives from outside.
defineExtension({
  id: "port-listener",
  name: "Port Listener",
  version: "1.0.0",
  description: "Listens on a port it declared for messages from outside.",
  access: { listen: [41001] },
  background: {
    everySeconds: 0.5,
    run(ctx) {
      socket = socket || ctx.udp.listen(41001);
      socket.read({ timeoutMs: 50 });
    },
  },
});

let socket;
