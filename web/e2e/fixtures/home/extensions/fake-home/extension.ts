// A whole home's worth of fake devices, all held by the fake device on the second network, reached like a real integration reaches a LAN device.
defineExtension({
  id: "fake-home",
  name: "Fake Home",
  version: "1.0.0",
  description: "Fake devices for the end-to-end tests.",
  access: { network: ["{host}"], lan: true, sockets: true },
  settings: {
    host: { label: "Device address", type: "string", required: true, help: "host:port of the fake device" },
  },
  devices: {
    everySeconds: 1,
    poll(ctx) {
      const answer = ctx.fetch(`http://${ctx.settings.host}/devices`);
      if (!answer.ok) throw new Error(`the device answered ${answer.status}`);
      return answer.json();
    },
    write(ctx, deviceId, code, value) {
      const answer = ctx.fetch(`http://${ctx.settings.host}/command`, { method: "POST", body: JSON.stringify({ id: deviceId, code, value }) });
      if (!answer.ok) throw new Error(answer.text.trim());
    },
    discover(ctx) {
      const [address, port] = ctx.settings.host.split(":");
      if (ctx.tcp.scan([address], Number(port)).length === 0) return [];
      return ctx.fetch(`http://${ctx.settings.host}/found`).json();
    },
  },
});
