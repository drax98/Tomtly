# Extension API reference

The types are in [`sdk/extension.d.ts`](../sdk/extension.d.ts), and that file is the source of truth. This is API
version 1 of Tomtly, which hasn't reached a stable release, so expect it to change until it does.

## Lifecycle

1. The service gets your folder, compiles `extension.ts` with its imports, and runs it once.
2. Your code calls `defineExtension` exactly once. It has 2 seconds, and no network yet.
3. The service checks the definition and the settings. If something's wrong, the problem goes on your card and it tries
   again every 5 minutes.
4. If you ask for access the owner hasn't approved, it waits for them. None of your functions run until they do.
5. It calls `data.fetch` and `devices.poll` straight away and then every `everySeconds`, `devices.write` for each
   command, `webhooks.receive` for each message from outside, and `background.run` over and over. These never overlap,
   since each extension has one thread. A slow request delays your next call but no other extension's.

Top-level variables keep their values between calls, for as long as the service runs. Use them for things like an access
token. Nothing is saved across a restart.

## `defineExtension(definition)`

| Field | Type | |
|---|---|---|
| `id` | string | Required. Lowercase letters, digits and `-`. Scripts use it, so treat it as permanent |
| `name` | string | Required |
| `version` | string | Required. Semantic version |
| `description`, `homepage` | string | Shown on the page |
| `license` | string | The SPDX id of its open license, with the text in a `LICENSE` file beside it. Required to be listed on the marketplace. One of `MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `MPL-2.0`, `LGPL-2.1-only`, `LGPL-2.1-or-later`, `LGPL-3.0-only`, `LGPL-3.0-or-later`, `GPL-2.0-only`, `GPL-2.0-or-later`, `GPL-3.0-only`, `GPL-3.0-or-later`, `AGPL-3.0-only`, `AGPL-3.0-or-later`, `EUPL-1.2`, `0BSD`, `Unlicense` |
| `categories` | string[] | Where the marketplace files it, from a fixed list: `climate`, `lighting`, `cleaning`, `energy`, `security`, `sensors`, `media`, `weather`, `bridges`, `other`. Anything else is refused |
| `tags` | string[] | Up to 20 tags of up to 40 characters, such as the brands, models and devices it works with. They show on the listing, people can search for them, and pressing one shows everything with it |
| `access` | object | What you need outside your own code: see [Access](#access) |
| `settings` | object | What you need from whoever runs this: see [Settings](#settings) |
| `data` | object | Makes this a plugin |
| `devices` | object | Makes this an integration |
| `actions` | object | Functions scripts can call |
| `webhooks` | object | Messages from outside: see [`webhooks`](#webhooks). Makes this a plugin |
| `background` | object | Work between polls: see [`background`](#background) |

At least one of `data`, `devices`, `actions`, `webhooks` and `background` is required. An extension with `webhooks`, or
`background` and no `devices`, is a plugin even without `data`, and sets its data with `ctx.publish`.

### `data`

| Field | |
|---|---|
| `everySeconds` | How often to fetch, at least 1, 300 by default |
| `fetch(ctx)` | Returns anything JSON can hold, up to 1 MiB. Throw on failure |

A throw keeps the last good data for scripts, while `home.plugin(id).ageSeconds()` keeps growing, and shows the error on
the page.

### `devices`

| Field | |
|---|---|
| `everySeconds` | How often to poll, at least 1, 30 by default |
| `poll(ctx)` | Returns every device, up to 1 MiB. Throw on failure |
| `write(ctx, deviceId, code, value)` | Optional. Sends one command, which the service has checked against the value's type and limits |

A throw from `poll` marks all your devices offline and keeps their last values. After a successful `write`, the service
polls straight away, so the page shows the result. If `write` throws, the error shows on the page.

A device:

| Field | |
|---|---|
| `id` | Required and stable across restarts. Scripts see `<extension id>:<id>` |
| `name` | Required |
| `kind` | `climate`, `sensor`, `light`, `plug`, `cover`, `vacuum`, `air`, `fan`, `safety`, `infrastructure`, or your own for a generic card. `other` by default. See [the codes each card uses](devices.md#kinds-and-codes-the-page-understands) |
| `product` | Model name |
| `online` | `true` by default |
| `values` | Its values |

A value:

| Field | |
|---|---|
| `code` | Required, and unique within the device |
| `type` | `Boolean`, `Enum`, `Integer`, `Action`, or read-only `String` and `Number` |
| `writable` | `false` by default |
| `value` | `null` while unknown. An `Action` stays `null` |
| `range` | Choices for an `Enum` or `Action` |
| `min`, `max`, `step`, `unit` | Limits for an `Integer` |
| `scale` | How to display an `Integer`: `205` with scale `1` reads as `20.5` |

### `actions`

An object of functions, each called as `name(ctx, args)`. A script calls one with `home.plugin(id).action("name", args)`,
and `args` is anything JSON can hold.

```ts
actions: {
  send(ctx, args) {
    ctx.fetch("https://api.example.com/messages", { method: "POST", body: JSON.stringify({ text: args.text }) });
  },
},
```

Calls are queued and run in order, up to 16 waiting. Scripts don't wait or see the result, so an error shows on your
card. In shadow mode a script only logs what it would call, and an identical call within 30 seconds is dropped.

### `webhooks`

Needs `access.webhooks: true`. The owner is shown a private address, `/hooks/<id>/<secret>`, when they add or approve the
extension. `POST`, `GET` and `PUT` to it, and to anything after the secret, call `receive(ctx, request)`:

| `request` | |
|---|---|
| `method` | `"GET"`, `"POST"` or `"PUT"` |
| `path` | What follows the secret, starting with `/`, or `/` if nothing does |
| `query` | The first value of each query parameter |
| `headers` | Lower-case names to values. Never a cookie |
| `body` | Up to 1 MiB, as text |
| `bodyHex` | The same bytes as hex, for checking a signature |

It returns nothing, for a `204`, or `{ status?, body?, headers? }`. `status` is 200 to 599, `body` is text as it is or
anything JSON can hold, and only `Content-Type`, `Cache-Control`, `ETag`, `Retry-After` and headers starting `X-` are
sent. A secret setting in the answer is replaced with `***`. Throwing, or taking over 10 seconds, gives the sender a
`502` or `504` with nothing about why, and the error shows on your card.

```ts
webhooks: {
  receive(ctx, request) {
    const sent = request.headers["x-hub-signature-256"];
    if (sent !== "sha256=" + ctx.crypto.hmac("sha256", ctx.crypto.hex(ctx.settings.secret), request.bodyHex)) return { status: 401 };
    ctx.publish(JSON.parse(request.body));
    return { status: 202 };
  },
},
```

The address is the only thing that protects it. It is 256 random bits, kept only as a hash, shown once, and replaced from
the page with **New webhook address**. A wrong secret, an unknown extension and one that takes no webhooks all get the same
`404`. Each extension takes 300 messages a minute (then `429`), and a message waits behind whatever else the extension is
doing. No login or cookie is read or set on these addresses, so they can't be used from a browser's session. Define
`webhooks` without declaring `access.webhooks` and no address is made, and the attempt is recorded.

### `background`

| Field | |
|---|---|
| `everySeconds` | How long to wait after each run, at least 0.05, 1 by default |
| `run(ctx)` | Called again each time it returns. A run that throws is retried no sooner than a second later |

For holding a socket: keep a listener or connection in a top-level variable, open it if it isn't yet, read for a moment,
and return. Everything else the extension does waits while `run` does, so keep each run short; a read on a listener waits
at most 2 seconds. Call `ctx.publish` with what arrived.

## Settings

Declare what you need, and the person running your extension fills it in when they add it on the page. Your code reads
`ctx.settings.<key>`, typed from your declaration.

| Field | |
|---|---|
| `label` | Required. Shown on the page and in the "required" error |
| `type` | Required: `string`, `number`, `boolean`, `secret` or `file` |
| `required` | `false` by default |
| `default` | Used when it isn't set. Not allowed on a `secret`, a `file`, or a setting named by `{...}` in `access.network` or `sendTo`: the person running it gives an address, not you |
| `help` | Where to find the value |
| `secretFields` | For a `file` holding JSON: names of fields, such as `["key"]`, whose string values (8 characters or more) are secrets of their own, at any depth. Nothing else in the file is recognised on its own |
| `sendTo` | For a `secret`: the `access.network` entries it may go to. The owner approves each one. Refused to anywhere else, and recorded. Without it, the secret can't leave at all |

A `file` setting is a file the owner chooses on the page, such as the device list a vendor tool wrote. Your code receives
its text (up to 1 MiB), which is kept encrypted with the other settings and treated as a secret, as described below. The
page never shows it again, and its access list says "Holds the file you give it for: <label>".

A setting that isn't declared, or has the wrong type, is an error. A `secret` gets these protections:

- The API and page only ever say whether it's set.
- It's replaced by `***` in errors and logs.
- If `data.fetch`, `devices.poll` or `ctx.publish` is given it, the result is refused. Don't transform a secret into your output.

### What the secret checks catch

They look at `data.fetch`, `devices.poll` and `ctx.publish` results, at webhook answers, logs, errors and recorded
violations, and at what leaves: a request's URL, method, headers and body, `ctx.udp` and `ctx.tcp` payloads, and a TLS `serverName`.

**Recognised:**

- A `secret`, a `secretFields` value and a file's whole text, as written, URL-encoded, Base64, hex or escaped as JSON once or twice.
- The same inside any string of a text that is JSON, up to 4 levels of JSON inside JSON, such as `JSON.stringify({ k: secret })`.
- A stretch of a file: always from 512 bytes, sometimes from 256. That holds as written, escaped as JSON, or re-serialised with
  `JSON.stringify(JSON.parse(text))`.

**Not recognised, so read the code:**

- A value hashed, encrypted, reversed, split or otherwise changed first, or escaped three times inside text that isn't JSON.
- A file sent in pieces shorter than that, or a stretch of one encoded some other way.
- A field not in `secretFields`, alone or joined with others: `JSON.parse(text).map((d) => d.key).join()` gets out if `key` isn't
  listed. Whoever writes the extension writes the list. Every value can't be blocked, because a device's name and id come from the same file and have to be published.

These stop mistakes and the obvious ways out, not an author determined to leak. The network list is what limits those, so the page warns the owner whenever an extension holding a secret can also send data elsewhere.

### What the owner sees

- Before it runs: each host, each listening port, and each secret with where it may go. Any change to those needs approving again, and so does a setting approved as a secret that the code stops declaring as one.
- An update can't change where an extension comes from, such as another GitHub repo, even with the same id.
- What's granted is what the person was shown. Your definition runs again each time, so one that answers differently, say by
  branching on `Date.now()`, gets refused when it's added, approved or updated. Declare the same `access` every time.
- From the marketplace, what the code declares must also match what the signed entry lists, and the version must be on the
  verified list at that moment and not suspended. An update can't go to an older version.
- A copy from the marketplace that gets withdrawn or suspended keeps running. Its card says why and offers to stop or remove it.
- From GitHub, nothing has checked the code, and the page says so. The copy first downloaded is kept. If the stored copy is
  lost and a new download of that ref has different files, it's refused, since the tag or branch has moved.
- A plain warning, on the card and when approving, when an extension holds a secret and can also reach an internet host, a webhook address, a listening port or the whole local network.
- After it runs: every destination it used, with how many times and when, whether it's a request, socket, scan, broadcast, listener or webhook. A destination is saved before the first thing is sent to it, unless the database is down, when the traffic carries on and it's saved once it can be. Over 200 different destinations is recorded as a violation, since it could hide where data went.

## The `ctx` object

Passed to each of your functions.

| | |
|---|---|
| `ctx.settings` | Your settings |
| `ctx.fetch(url, options?)` | Makes an HTTP request and waits for the answer |
| `ctx.udp` | UDP requests and discovery |
| `ctx.tcp` | TCP connections, which can stay open between calls, and listening for them |
| `ctx.publish(data)` | Sets the plugin's data now. Needs `data`, `webhooks` or `background` |
| `ctx.refreshDevices()` | Runs `devices.poll` straight after this call. Needs `devices` |
| `ctx.removedDevices()` | The ids of the devices the owner removed. The service hides them whatever you do, so this only saves work: don't spend reads or connections on them. Needs `devices` |
| `ctx.crypto` | Hashes, HMAC, AES and encodings |
| `ctx.storage` | Values that survive a restart |
| `ctx.log(message)` | Writes to the service's log, with secrets removed |

There's also standard JavaScript up to ES2017, and newer syntax gets compiled down. There's no `setTimeout`, `console`,
`require` or file access, and calls wait for their answer rather than using promises.

All binary data is a hex string.

### `ctx.fetch`

`options` takes `method`, `headers`, and either `body` as a string or `bodyHex`. It returns:

| | |
|---|---|
| `status` | The status code |
| `ok` | `true` for 2xx |
| `text` | The body |
| `hex()` | The body as hex |
| `json()` | The body, parsed. Throws if it isn't JSON |
| `header(name)` | A response header |

`method` is `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE` or `OPTIONS`; anything else is refused and recorded.
It throws if the host isn't allowed or can't be reached. A request has 15 seconds, and a response can be up to 4 MiB.
Redirects aren't followed: you get the 3xx, and a request to the new location has to pass the same checks.

### `ctx.udp`

| | |
|---|---|
| `request(host, port, data, options?)` | Sends one datagram and returns the first reply. Throws if none arrives |
| `scan(hosts, port, data, options?)` | Sends the datagram to each address at once and returns the first `{ from, data }` from each, within 2 seconds. Up to 254 private IPv4 addresses; needs `"sockets"` and `"lan"`. Every 8 addresses count as one operation |
| `discover(port, data, options?)` | Broadcasts, and returns every `{ from, data }` reply in time. Needs `"lan"`. A payload holding a secret is refused, whatever its `sendTo` says, since a broadcast has no one host. Doesn't work from Docker Desktop, so also offer a setting for the address |
| `listen(port)` | Receives datagrams on a port in `access.listen`. Returns `{ read(options?), close() }`, where `read` gives `{ from, data }` or `null` if none came in time |

`options` takes `timeoutMs`, 3 seconds by default and at most 15.

### `ctx.tcp`

`connect(host, port, options?)` returns a connection. `options` also takes `tls`, `serverName` (a host name, checked like anything else you send when it isn't `host`), and `insecure` for a self-signed device on the local network, which is refused for anything else. The connection has:

| | |
|---|---|
| `write(data)` | Sends bytes |
| `read(options?)` | Returns what has arrived, or `""` if nothing did in time. Throws if the connection closed |
| `close()` | Closes it |

Keep one in a top-level variable to use it across calls. Up to 16 can be open, and all close when the extension stops.

`scan(hosts, port, options?)` tries every address at once, within 2 seconds, and returns the ones that accept a connection. It closes each connection at once and sends nothing. Up to 254 private IPv4 addresses; needs `sockets` and `lan`, and every 8 addresses count as one operation.

`listen(port)` accepts connections on a port in `access.listen`, and returns `{ accept(options?), close() }`. `accept` gives
a connection like the one above, or `null` if none came in time. A listener, and each connection it accepts, counts
among the 16.

A port you listen on is inside the container until Docker publishes it, so add it to `ports:` in `compose.yaml`
(`"5683:5683/udp"`), and open it on your network only if the device needs to reach it. Anyone who can reach the port can
send to it, so treat everything read as untrusted. Listening needs `access.listen` but not `sockets`, and reads and
accepts don't count toward the 600 operations a minute, though listening does.

### `ctx.crypto`

| | |
|---|---|
| `hash(algorithm, data)` | `md5`, `sha1`, `sha256` or `sha512` |
| `hmac(algorithm, key, data)` | The same algorithms |
| `encrypt(options)`, `decrypt(options)` | AES: `{ mode, key, iv?, aad?, data, padding? }`, with `mode` of `ecb`, `cbc` or `gcm` |
| `random(size)` | Secure random bytes |
| `hex(text)`, `text(data)` | Between text and hex |
| `fromBase64(text)`, `toBase64(data)` | Between Base64 and hex |

### `ctx.storage`

`get(key)`, `set(key, value)` and `delete(key)`, holding anything JSON can, up to 1 MiB in total. Kept across restarts,
readable only by the service and by your extension, and deleted when your extension is removed on the page. A `set` or
`delete` the database can't take throws a plain message, and the reason goes to the service's log.

An error you catch from any `ctx` call holds a message and nothing else.

## Access

Anything you don't list is refused, and the attempt is recorded. At 10 recorded attempts your extension is stopped and
its approval removed.

| Field | Grants |
|---|---|
| `network` | Hosts for `ctx.fetch`, and for sockets if you also list `sockets`. See below |
| `sockets` | `ctx.tcp` and `ctx.udp` |
| `lan` | Any private address by IP, `ctx.udp.discover`, and with `sockets` `ctx.udp.scan` and `ctx.tcp.scan` |
| `storage` | `ctx.storage` |
| `webhooks` | A private address that outside callers can send messages to: shown to the owner as "Receive messages from outside" |
| `listen` | A list of ports for `ctx.udp.listen` and `ctx.tcp.listen`: shown as "Accept connections from outside on port N" |

`ctx.settings`, `ctx.crypto` and `ctx.log` need no access. An unknown key in `access` fails your load.

`network` entries:

| Entry | Means | May reach |
|---|---|---|
| `"api.example.com"` | An internet host you chose | Public addresses only, even if the name resolves to a private one. `ctx.fetch` is HTTPS only, and a socket's TLS `serverName` must be the host itself |
| `"{host}"` | The value of your `string` setting called `host`, as `host` or `host:port`. It can't have a `default`. The owner approves the address itself, and approves again if it changes other than by editing the setting on the page | HTTP or HTTPS, including the local network |

No extension can reach this machine, the service's own network including its database, or link-local addresses such as
cloud metadata services. There are no wildcards. In a container the service can't see this machine's real address, so set
`HOST_IPS` to it, comma separated, when it isn't on loopback: without that an extension with `lan` can reach the published port.

`lan` doesn't cover ports where a device passes data on or opens a way in: 53, 853, 1900, 5351 and 5353. Those need the address in a setting. An entry with a port, such as a host setting of `10.0.0.5:9000`, allows sockets only on that port.

Also recorded and refused: listening on a port you didn't list, `ctx.publish` without being a plugin, `eval`, `Function` and similar ways of building code at run time, a `sendTo` secret going to
the wrong host, and over 600 network operations in a minute. Using too much memory is recorded too, and stops the extension
at once whatever its count: see below.

## Scan warnings

When your code is checked, the page warns about `eval(`, `Function(`, `constructor.constructor`, `__proto__`, long
`String.fromCharCode` sequences, hex or Base64 strings over 500 characters, and addresses in your code that aren't in
`access.network`. Warnings don't block anything, but people will read them, so keep your code free of them.

## Limits

| | |
|---|---|
| Loading | 2 seconds |
| One call | 60 seconds, then it's stopped and counts as a failure |
| One request | 15 seconds and 4 MiB |
| What `fetch` or `poll` returns | 1 MiB |
| One socket wait | 15 seconds, and 2 for a listener's `read` or `accept` |
| Open connections, including listeners | 16 |
| `background.everySeconds` | 0.05 at least |
| A webhook | 1 MiB in, 1 MiB out, 10 seconds, 300 a minute with the right token. Wrong tokens have their own limit per address, so they can't use that up |
| Turning one value into JSON | 4 MiB |
| Memory | The service's heap, 512 MB by default. `EXTENSION_HEAP_LIMIT_MB` changes it |
| Storage | 1 MiB |
| Queued actions and writes | 16 each |
| GitHub download | 64 MiB |
| Marketplace package | 4 MiB and 1000 files |

### Memory

The VM can't cap its own memory, so the service samples its whole heap about every 200 ms. Over the limit, it first collects
garbage, then interrupts the call the heap grew most during, records a `memory` violation against it, stops it at once and
takes its approval back, then frees the memory. A well-behaved extension that started after memory was already high isn't blamed. If memory stays high for 5 seconds with no long call to blame, every running call is stopped, so a
well-behaved one could be caught then; it just needs approving again. A single built-in that
allocates gigabytes in one step, like `"x".repeat(2**30)`, can't be interrupted and can pass the limit between samples, so a hostile extension can jump past several gigabytes before it's
stopped. Docker's memory limit is the last line of defence: if it's reached, the whole service restarts, and your automations
stop until it's back. A true per-extension limit would need each extension in its own process. It's also possible for a
well-behaved extension to be stopped during such an attack, and it then needs approving again.
Run the service with a container memory limit as well.

## Widgets

`widgets` in the definition offers widgets people can place on Home, by a key of letters, digits, `-` and `_`. Up to
10, and an extension may offer only widgets.

| Field | |
|---|---|
| `title` | Up to 60 characters |
| `render(ctx)` | Returns up to 40 blocks, the same ones as a script's widget (see [automations.md](automations.md#widgets-on-home)). Drawn on the extension's thread every 15 seconds and after any action. Refused, with the reason on the widget, if it holds a secret or is over 32 KiB |

A `button` block names one of your `actions`, which gets every input's value by name and the button's `args`. Only a
button the widget shows right now can be pressed, by someone with the Family role or higher, so a widget can't be used to
reach your other actions. The press waits in line like a script's call. Keep what `render` shows in a top-level variable
that `data.fetch` or `background.run` updates, rather than fetching in `render`. The Weather extension does this.

## HTTP API

These need signing in, or an API token, with permission for extensions: see the [README](../README.md#login-and-roles). Requests with a body need `Content-Type: application/json`, except uploads.

| | |
|---|---|
| `GET /api/integrations` | Every service with its devices, built in or an extension |
| `GET /api/extensions` | Each extension's source, identity, network list, settings, what it provides, whether it's running and its error. Never a secret, though a setting that isn't one is shown, so it can be edited |
| `POST /api/extensions/inspect` | Fetches `{"source": ...}` and says what it is and needs, without running anything but its definition |
| `POST /api/extensions/upload?name=<file>` | Stores a `.zip`, `.tar.gz`, `.tgz` or `.ts` of up to 16 MiB, sent as `application/octet-stream`, and answers like inspect, with a source to add it by |
| `POST /api/extensions` | Adds `{"source": ..., "settings": {...}}`, approves its access and starts it |
| `POST /api/extensions/approve` | Approves what `{"source": ...}` asks for now, clears its blocked attempts and starts it. If that made its first webhook address, `webhook` in the answer holds it, once |
| `POST /api/extensions/settings` | Sets `{"source": ..., "settings": {...}}` on one added on the page and restarts it. A secret left out or blank keeps its value, a blank setting is unset, and one left out stays. The audit log names the settings, never the values |
| `POST /api/extensions/replace` | Swaps `{"source": ..., "newSource": ...}` for a new version of the same extension, keeping its settings and saved data. A different `id` is refused. What it asks for beyond what was approved waits for approval |
| `POST /api/extensions/webhook-token` | Makes a new webhook address for `{"source": ...}` and returns it as `webhook`, once. The old one stops working |
| `POST`, `GET`, `PUT /hooks/<id>/<secret>[/...]` | Not under `/api` and needs no login: the address is the credential. Sends a message to an extension's `webhooks.receive` |
| `DELETE /api/extensions?source=...` | Stops one and deletes its approvals and storage. and its settings |
| `GET /api/automations/sdk` | The types the script editor uses, including those generated for this home |

## Removing devices and finding them again

An owner can remove any device from its integration's page. The service then hides it from pages and scripts and refuses commands to it, and remembers that across restarts, so your extension needs no code for this. `devices.poll` can keep returning it, and `ctx.removedDevices()` says which to stop contacting.

When the page is opened, the service calls `devices.discover(ctx)` if you have one, at most every 30 seconds. Return what you found on the network as `{ id, name?, product?, address?, note? }`, using the same `id` as `devices.poll`. The result passes the same secret check as a poll. A device you can't use yet gets a `note` saying what the owner has to do. Use `ctx.udp.scan` or `ctx.tcp.scan`, which need `lan` and `sockets`, and only send what changes nothing on a device. Removed devices are offered back whether or not you found them.
