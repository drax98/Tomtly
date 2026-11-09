# Extension style guide

What makes an extension safe to run in someone's home. **Must** means your extension is broken without it.

## Report the truth

- **Must:** report `null` for a value you don't know, never a guess. Scripts treat unknown differently from off, and a
  script deciding whether to run a boiler depends on that.
- **Must:** throw when a fetch or poll fails, including when a service answers 200 with an error inside. Returning empty
  or default data makes a failure look like a real reading.
- **Must:** check the fields you depend on. Prefer failing on a response you don't recognise to passing on `undefined`.
- Set `online: false` on a device that didn't answer when the service you talk to did, and throw when the service itself
  doesn't answer.

## Be a good guest

- **Must:** ask for only the access you use. `access` is your promise to the person installing it, and they'll see it before your code runs.
- **Must:** use `sendTo` on every secret, so a mistake or a compromised dependency can't send it anywhere else.
- **Must:** leave your code free of scan warnings. No `eval`, no generated code, no long encoded strings, and no addresses you don't list.
- Don't ask for more access in a minor version: it stops your extension for everyone until they approve it again. Say why in your changelog.
- **Must:** send settings only to the service they're meant for.
- **Must:** never return or log a secret, whether as is or transformed.
- Ask for `lan` only if you discover devices or look for one that moved (`ctx.udp.scan`, `ctx.tcp.scan`). If you can, take the address as a setting instead. Prove a device's identity before adopting a new address, and wait before scanning again (the shipped extensions wait 60 seconds of failures, then 5 minutes between scans).
- Choose `everySeconds` from how fast the data really changes and what the service's terms allow. Rate limits are
  shared by everyone using your extension from the same address, so err on the slow side.
- Send a short `User-Agent` naming your extension if the service asks for one.

## Keep ids stable

- **Must:** keep your extension `id` and your device `id`s the same across versions and restarts. Scripts refer to them.
- **Must:** build device ids from something that doesn't change, like a serial number, rather than a position in a list
  or a name someone can edit.
- Don't rename a `code` or change what a value means without a new major version. Say so in your changelog.

## Use the shared vocabulary

- Use a `kind` and the codes the page already understands, listed in
  [Kinds and codes](devices.md#kinds-and-codes-the-page-understands), so your device gets a proper card and works with
  scripts written for other brands. For example: `switch`, `temp_current`, `heating_state`, `open`, `battery`.
- Mark values `writable` only if `write` handles them.
- Give limits with `min`, `max` and `range`, because the service checks writes against them before they reach you.
- Report raw units with `unit` and `scale`, rather than converting to something else.
- Use metric units in data, and name them in a comment in `types.d.ts`.

## Writes

- **Must:** make `write` send one command and return. Throw if the device refuses, so the page shows it.
- The same goes for `actions`: do the one thing, and throw if it failed.
- Don't retry in a loop. A retried command can fire at the wrong moment, and scripts already send it again if it didn't
  take effect.

## Settings

- Give each one a `label` a person understands, and a `help` that says where to get the value.
- Use `secret` for anything that grants access, however harmless it seems.
- Give a `default` where one is right for most people, and don't mark those as `required`.
- A file a tool writes, such as a device list with keys, is a `file` setting, not a `string` holding the contents. Name the JSON fields that are secrets in `secretFields`.
- A local address is a `string` setting listed in `access.network` as `"{host}"`, never hard-coded.
- **Must:** ship a [migration](extensions.md#ask-for-new-settings-in-a-new-version) when a version adds a required setting, renames one or changes its type, so people are asked and nothing is lost. Say in its `note` what changed and where to get the new value.
- To make people replace a token that stopped working, list it in both `remove` and `ask`. In `ask` alone, leaving it blank keeps the old one.

## Sockets and sessions

- Close connections you're done with. If you keep one open, handle it having closed: `read` throws, so reconnect.
- Don't wait longer than you must: nothing else in your extension runs during a wait.
- Keep long-lived secrets like a rotating refresh token in `ctx.storage`, and short-lived ones in a top-level variable.

## Webhooks, listeners and background work

- **Must:** check that a webhook really comes from who you think it does, usually by an HMAC of `request.bodyHex` with a
  `secret` setting. The address is private, not a signature: anyone who learns it can send to it.
- **Must:** treat everything in `request` and everything read from a listener as untrusted input. Check it before it
  reaches `ctx.publish` or a device.
- **Must:** answer fast. Your one thread is shared with everything else you do, and a sender gives up after 10 seconds.
  Do slow work in `background` or the next poll.
- Answer with the right status, and nothing about why you refused: `401` or `404`, not a hint about what was wrong.
- Ask for `listen` only for a port a device really connects to, and tell people to publish it in `compose.yaml`.
- Keep `background.run` short, and keep the listener in a top-level variable instead of opening it each time. Handle
  `null` from `read` and `accept`: it means nothing came, which is normal.
- Don't spin: set `everySeconds` from how fast you must react, not the smallest allowed.
- Never put a secret in `ctx.publish`: it is refused, because scripts and the page can read plugin data.

## Errors

- Write errors for the person reading the card: `the bridge refused: unauthorized user`, not `Cannot read property 0`.
- Say which service failed, and include the status.
- Don't catch a refused call and try another way round. Blocked attempts are counted, and 10 of them stop your extension.

## Publishing

- **Must:** say in your README which access you ask for, and why.
- **Must:** tag versions and use [semantic versioning](https://semver.org), and keep `version` in step with the tag.
- **Must:** include a `README.md` with what it supports, how to get each setting, which hosts it contacts and why, and
  an example `config.json` entry.
- **Must:** say what you tested it against, including models and firmware. Saying "untested" is fine, but say it.
- Include a licence.
- Name your repo `home-control-<name>`, so people can find it.
- Keep it small and readable: people should read an extension before giving it their key. Commit anything you import,
  because the service doesn't run `npm install`.
- Provide a `types.d.ts` if your data has fields that are sometimes missing, or whose units need explaining.
