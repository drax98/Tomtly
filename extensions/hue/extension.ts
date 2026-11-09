// An integration with a local host and a secret: Philips Hue lights, through the bridge's REST API.
defineExtension({
  id: "hue",
  name: "Philips Hue",
  version: "1.0.1",
  license: "MIT",
  description: "Lights on a Philips Hue bridge on your network.",
  categories: ["lighting"],
  tags: ["Philips", "Hue", "bulb", "lamp", "light strip"],
  access: { network: ["{host}"] },
  settings: {
    host: { label: "Bridge address", type: "string", required: true, help: "Its IP address, shown in the Hue app. Reserve it on your router." },
    apiKey: { label: "Bridge API key", type: "secret", required: true, sendTo: ["{host}"], help: "Press the bridge's button, then POST {\"devicetype\":\"home-control\"} to http://<bridge>/api." },
  },
  devices: {
    everySeconds: 10,
    poll(ctx) {
      const response = ctx.fetch(`http://${ctx.settings.host}/api/${ctx.settings.apiKey}/lights`);
      if (!response.ok) throw new Error(`the bridge answered ${response.status}`);
      const lights = response.json();
      if (Array.isArray(lights)) throw new Error(`the bridge refused: ${lights[0]?.error?.description ?? "no reason given"}`); // errors come back as a 200

      return Object.keys(lights).map((id) => {
        const { name, productname, state } = lights[id];
        const values: DeviceValue[] = [{ code: "switch", type: "Boolean", writable: true, value: state.on }];
        if (typeof state.bri === "number") {
          values.push({ code: "brightness", type: "Integer", writable: true, value: state.bri, min: 1, max: 254 });
        }
        return { id, name, kind: "light", product: productname, online: state.reachable, values };
      });
    },
    write(ctx, deviceId, code, value) {
      const state = code === "switch" ? { on: value } : { bri: value };
      const url = `http://${ctx.settings.host}/api/${ctx.settings.apiKey}/lights/${deviceId}/state`;
      const response = ctx.fetch(url, { method: "PUT", body: JSON.stringify(state) });
      if (!response.ok) throw new Error(`the bridge answered ${response.status}`);
    },
  },
});
