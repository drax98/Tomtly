# Extensions

An extension adds devices, data or actions to Home Control, without changing this repo. It's a folder of TypeScript that
anyone can publish on GitHub, and anyone can add from the page.

- A **plugin** provides data that scripts read, like the weather, or actions they call, like sending a notification.
- An **integration** provides devices that show on the page and that scripts control.
- One extension can be both.

Extensions can use HTTP, raw TCP and UDP, AES and hashes, storage that survives a restart, messages sent to them from
outside (webhooks), and ports they listen on, so they can speak most device protocols directly. They get none of that unless they list it under `access`, and you approve the list. Script authors then get autocomplete for what you provide, with nothing to set up.

This guide covers using and building one. See also the [API reference](extension-api.md) and the
[style guide](extension-style-guide.md).

## Use one

On the **Integrations** or **Plugins** page, choose **Add integration** or **Add plugin**, then where it comes from:

- **From the marketplace** leads to the marketplace's website, which lists extensions whose exact package was checked and approved. **Get** there opens the listing back in Home Control to install. It's the easiest way.
- **From a GitHub repo** asks for a public repo, an optional folder in it, and a version. Nothing has checked it: whoever controls the repo decides what's in it, and the page says so. Use it only if you trust them.
- **From local files** lets you upload a `.zip`, `.tar.gz` or a single `extension.ts`, or use a `./` folder inside your config folder.

You'll see the access it asks for, anything suspicious found in its code,
and the settings it needs. Nothing runs until you allow it. Adding it counts as approving that access, and it comes back
after a restart. Settings you enter are kept in the service's database.

One you added on the page can be changed on the page: **Edit settings** changes its settings and restarts it (a key or
password is never shown, and left blank it stays as it is). **Upload new version**, **Update from GitHub** and the
Marketplace's update replace its code while keeping its settings and saved data. The new version has to have the same
`id`, and if it asks for access you haven't approved it waits for your OK.

Before an update, you see what changed in each version and a form for anything the new version needs, such as a new
access token. Update stays off until the required fields are filled. If it's refused, the old version keeps running as
if nothing happened, and what you typed stays in the form.

One listed in `config.json` is changed in that file. Nothing prompts you there: if a new version needs a setting, its card
names the one that's missing.

If it receives webhooks, its address is shown once, when you add or approve it, as `/hooks/<id>/<secret>`. Copy it then
and give it to the service that should send messages: only a hash is kept, so it can't be shown again. **New webhook
address** makes another and the old one stops working. The secret in the address is all that protects it, and the rest
of Home Control still needs a login, so don't open the whole panel to the internet for it: have your HTTPS proxy pass
only `/hooks/` through.

To keep it in version control instead, add it to `extensions` in `config.json` and restart:

```json
"extensions": [
  { "source": "github:someone/home-control-hue@v1.0.0", "settings": { "host": "192.168.1.13", "apiKey": "${HUE_API_KEY}" } },
  { "source": "github:someone/monorepo/packages/tides@3f2a91c", "settings": {} },
  { "source": "./extensions/my-own", "settings": {} }
]
```

| Source | |
|---|---|
| `github:owner/repo@ref` | A public repo at a tag, branch or commit. A commit is the safest |
| `github:owner/repo/folder@ref` | A folder within one |
| `./folder` | A folder inside your config folder |

Each extension is downloaded once and kept, so it can't change unless you change the `ref`, and it keeps working with no
internet. To update one from `config.json`, change the `ref` there. One added on the page has **Update from GitHub**.

In `config.json`, put secrets in `secrets/env` in your config folder, one `HUE_API_KEY=...` per line, and refer to them
as `${HUE_API_KEY}`. The service won't start if one is unset.

An extension from `config.json` waits for you to choose **Approve access** on the page before it runs.

The pages list each extension, its access, which settings it needs and any problem. A broken extension only affects
itself.

### The access system

Nothing is granted by default. An extension lists what it needs, you approve that list, and Home Control enforces it at
runtime regardless of what the code tries.

- **No access without a declaration.** Network, raw sockets, the local network, saved data, webhooks and listening
  ports each have to be listed. An
  extension that didn't declare network access can't use the network.
- **A new version can't quietly ask for more.** If a version asks for anything you haven't approved, it stops until you
  approve it. That includes a new address for a host in a setting: the page shows the address itself, and approving
  covers that address in that setting. Typing a new one under **Edit settings** is your approval, so it doesn't ask
  again. An address changed any other way asks once: in `config.json`, moved by a migration's `rename`, or typed into
  an update's form, whose wording is the author's.
- **Blocked attempts are recorded,** and shown on its card with what it tried. Catching the error doesn't hide it,
  because they're counted outside the extension. After 10 it's stopped and needs approving again.
- **Its code is scanned** when you add it. Attempts to run generated code, hidden strings and addresses missing from
  its access list are shown as warnings for you to judge.
- **It can't generate code.** `eval`, `Function` and their equivalents are blocked, so what you can read is what runs.
- **Secrets can be tied to a host.** A secret with `sendTo` is refused on its way to any other host, whether plain, URL
  encoded, Base64, hex or escaped as JSON. A broadcast never carries a secret. [What is and isn't recognised](extension-api.md#what-the-secret-checks-catch).
- **Some places are always off limits,** whatever it declares: this machine, the service's own API and database, other
  containers beside it, and cloud metadata services.

Where the limits are:

- An extension can send a setting to a host it's allowed to reach, so **read the list, and the code, before you give it
  a key**. A secret that's hashed, split or otherwise transformed first can't be detected, and the checks on a file
  stop mistakes and the obvious ways out, not an author set on getting one field out of it.
- It controls the devices it provides, and scripts may act on what it reports, so only install what you'd trust with
  them.
- Memory can't be limited per extension. Instead the service watches its own memory (512 MB by default, set with
  `EXTENSION_HEAP_LIMIT_MB` in `secrets/env`), and past that it stops the extension whose current call the heap grew most during and records a
  `memory` violation against each. A single built-in that grabs gigabytes in one step, such as
  `"x".repeat(2**30)`, can't be interrupted and could still bring the service down first.
- An extension that listens on a port or takes webhooks can be reached by whoever can reach that port or address.
  Anything it receives is untrusted input.

## Build one

You need one file, `extension.ts`, at the top of a folder. Here is a complete plugin with an API key:

```ts
defineExtension({
  id: "tides",
  name: "Tides",
  version: "1.0.0",
  access: { network: ["api.example.com"] },
  settings: {
    apiKey: { label: "API key", type: "secret", required: true, sendTo: ["api.example.com"], help: "Create one at example.com/keys." },
    station: { label: "Station", type: "string", default: "gdansk" },
  },
  data: {
    everySeconds: 900,
    fetch(ctx) {
      const response = ctx.fetch(`https://api.example.com/tides/${ctx.settings.station}`, {
        headers: { Authorization: `Bearer ${ctx.settings.apiKey}` },
      });
      if (!response.ok) throw new Error(`the API answered ${response.status}`);
      return response.json();
    },
  },
});
```

A script then reads it with `home.plugin("tides").data()`.

An integration returns devices instead. Here's the core of `extensions/hue`:

```ts
devices: {
  poll(ctx) {
    return [{ id: "1", name: "Desk", kind: "light", values: [{ code: "switch", type: "Boolean", writable: true, value: true }] }];
  },
  write(ctx, deviceId, code, value) {
    ctx.fetch(`http://${ctx.settings.host}/lights/${deviceId}`, { method: "PUT", body: JSON.stringify({ [code]: value }) });
  },
},
```

Scripts see that device as `home.device("hue:1")`.

### Declare your access

```ts
access: {
  network: ["api.example.com", "{host}"], // named hosts, and the host in your setting called host, which can't have a default
  lan: true,       // any private address, for discovering devices and ctx.udp.scan / ctx.tcp.scan
  sockets: true,   // ctx.tcp and ctx.udp
  storage: true,   // ctx.storage
  webhooks: true,  // an address outside callers can send messages to: see webhooks below
  listen: [5683],  // ports for ctx.udp.listen and ctx.tcp.listen
},
```

`sockets` with `lan` also gives `ctx.udp.scan` and `ctx.tcp.scan`, which probe up to 254 private addresses in parallel, for
finding a device whose address changed: `extensions/tuya`, `extensions/xiaomi` and `extensions/gree` do it.

Leave out what you don't need, which is the point: people read this list before they let your code run. Asking for more
in a later version stops your extension until they approve it.

### Settings that are files

For credentials a tool writes to a file, such as the Tuya device list, declare a setting of type `file`:

```ts
settings: {
  devices: { label: "Devices file", type: "file", required: true, secretFields: ["key"], help: "Inside your config folder, like secrets/tuya.json." },
},
```

The owner gives a path inside the config folder (`"devices": "secrets/tuya.json"`), and `ctx.settings.devices` is the
file's text, up to 1 MiB. Its whole text, and any long stretch of it, is refused in any request, socket payload, output
or log, and the page shows the path with "Reads the file you choose for: Devices file". It can't have a default. For a
JSON file, `secretFields` makes the named fields, such as `key`, secrets of their own at any depth; list every one,
because a value you don't list, like a device's name, can be published as you'd expect. The file is read when the
extension starts, so restart after changing it. [What exactly is checked](extension-api.md#what-the-secret-checks-catch).

### Receive messages and hold a connection

Some services call you rather than waiting to be asked, and some devices push their state down a socket.

```ts
let sock: UdpListener | undefined;
defineExtension({
  id: "pusher", name: "Pusher", version: "1.0.0",
  access: { webhooks: true, listen: [5683] },
  settings: { expected: { label: "The shared secret the sender uses", type: "secret", required: true } },
  webhooks: {
    receive(ctx, request) {
      if (request.headers["x-signature"] !== ctx.settings.expected) return { status: 401 };
      ctx.publish(JSON.parse(request.body)); // scripts see it straight away
    },
  },
  background: {
    everySeconds: 0.1,
    run(ctx) {
      sock ??= ctx.udp.listen(5683);
      const packet = sock.read({ timeoutMs: 500 });
      if (packet) ctx.publish({ from: packet.from, data: packet.data });
    },
  },
});
```

`data` is optional when you have `webhooks` or `background`: the extension is still a plugin, and `ctx.publish` is how it
sets its data. For devices, `ctx.refreshDevices()` runs `devices.poll` straight after the current call. The details are
in the [API reference](extension-api.md#webhooks).

**Docker:** a port you listen on is inside the container until you publish it, so add it under `ports:` in
`compose.yaml`, such as `"5683:5683/udp"`. Webhooks arrive on the service's ordinary port.

### Ask for new settings in a new version

People update without reading your changelog, so tell Home Control what a new version needs in a migration: a file in
your folder called `migrations/<version>.json`, for the version that makes the change. Everyone who passes that version
sees its note and is asked for what it lists, however many versions they skip.

You need one when a version adds a required setting, renames a setting, or needs a value someone already gave you to be
replaced. Adding an optional setting, or one with a `default`, needs none. A setting you stop declaring is forgotten
without one. Migrations only work with [semantic versions](https://semver.org) like `1.2.0`.

| Field | |
|---|---|
| `version` | The same version as the file's name. Required |
| `note` | What changed and what to do, for the person updating. Required |
| `ask` | Settings to ask for. A value that's already there is kept if the field is left blank |
| `rename` | `{ "old": "new" }`, so a saved value moves to its new name |
| `remove` | Saved values to forget |

The setting itself, with its label and help, is declared in `extension.ts` as usual. A migration only ever touches
settings, never saved data. A file that's wrong refuses the update and says what's wrong with it.

Adding a token, in `migrations/1.3.0.json`, with `pat` declared in that version's `settings`:

```json
{
  "version": "1.3.0",
  "note": "The service now needs a personal access token. Make one on your account page.",
  "ask": ["pat"]
}
```

Renaming a setting, so nobody has to type it again:

```json
{ "version": "1.4.0", "note": "The address setting is now called host.", "rename": { "address": "host" } }
```

If that setting is a `{...}` entry in `access.network`, the person approves the address once more, since the same address
under another name can change where a `sendTo` secret goes. A setting like that can't have a `default` in any version.

Forcing a new token when the old ones stopped working. Listing it in both `remove` and `ask` means it can't be left blank:

```json
{
  "version": "2.0.0",
  "note": "Tokens made before March stopped working. Make a new one on your account page.",
  "remove": ["token"],
  "ask": ["token"]
}
```

Do the same when a setting changes type, since the saved value won't fit any more. A secret that becomes a plain setting
is always asked for again, so the page never shows it.

A required setting no migration mentions is still asked for, without an explanation, so write the note.

A person only ever types what `ask` lists, and what they type goes in your settings and nowhere else. `config.json` users
aren't prompted: list what changed in your README for them.

### Develop it

1. Put your folder in `extensions/` in your config folder, and add `{ "source": "./extensions/your-id" }` to `config.json`.
2. Copy [`sdk/extension.d.ts`](../sdk/extension.d.ts) and `extensions/weather/tsconfig.json` next to it for
   autocomplete and type checks. The service doesn't check types when it loads your code, so run `tsc` or use an editor.
3. Restart the service after each change with `docker compose restart`.
4. Problems show on your extension's card on the Integrations or Plugins page, and `ctx.log` writes to `docker compose logs`.
   To see your data, open the plugin's **Show data**, or `curl localhost:8099/api/devices`.

Start from `extensions/weather` for a plugin, or `extensions/hue` for an integration. For a LAN protocol over TCP or UDP, `extensions/tuya`, `extensions/xiaomi` and `extensions/gree` are complete examples.

### Give scripts types

You don't have to do anything: the script editor works out each device's codes and allowed values, and the shape of your
data from what you last returned, and suggests them as people type. Your action names come along too.

For better types, like comments on each field or a value that's sometimes missing, add a `types.d.ts` next to
`extension.ts`, and list your data in `PluginMap`:

```ts
interface Tides {
  /** Metres above chart datum. */
  height: number;
}

interface PluginMap {
  tides: Tides;
}
```

### Share it

Push the folder to a public GitHub repo with `extension.ts` at its top, or in a folder for a monorepo, and tag a
version, or send someone the file to upload. Say what you've tested it with, as the [style guide](extension-style-guide.md) describes.

## Choose how to build it

| You need | Use |
|---|---|
| A cloud API, a LAN device, a custom protocol over TCP or UDP | An extension |
| Something scripts call, like sending a message | An extension with `actions` |
| Messages sent to you by an outside service (webhooks) | An extension with `webhooks` |
| A device that pushes its state over UDP or TCP, or a protocol like MQTT where you hold a connection | An extension with `background` and `access.listen` for a device that connects to you. A protocol where you connect out and stay connected works with `ctx.tcp.connect` and `background` |
| An existing library in another language, a USB or Bluetooth device, native code | [A service in any language](devices.md#a-service-in-any-language) |
| Anything an extension's sandbox rules out: [below](#what-an-extension-still-cant-do) | [A service in any language](devices.md#a-service-in-any-language) |

A service in any language can do anything its machine can, and reaches Home Control through `extensions/service-bridge` and a small HTTP API.

### What an extension still can't do

These are limits of the sandbox, not of the extension API, and the bridge to a service covers each of them (see the
`service-bridge` example): the extension asks a small service you run, which does the work and answers over HTTP.

- **No threads or timers.** An extension has one thread that does one thing at a time. A slow call delays its other
  calls, though never another extension's. A read waits at most 2 seconds, so hold a connection by returning and
  reading again from `background`.
- **Only the handshakes `ctx.crypto` can do.** `ctx.tcp` speaks TLS, but a protocol needing other ciphers, like DTLS, needs the bridge.
- **No hardware.** Bluetooth, USB, serial and GPIO need native code.
- **No files, no child processes, no libraries with native parts or that need `async`.**
- **Nothing is reachable from outside unless Docker publishes the port.**
- **Memory is watched, not capped:** see the access system above.
