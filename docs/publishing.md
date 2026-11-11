# Publishing an extension

Share an integration or plugin with every Tomtly by publishing it on the marketplace. A person reads each version
before it's listed, so here's what they look for and how to make it quick. To build the extension itself, start with
[extensions.md](extensions.md).

## Before you upload

- **It's open source, under an open license.** Name it in `defineExtension({ license: "MIT", ... })` and put the license
  text in a `LICENSE` file beside `extension.ts`. The accepted licenses are in [extension-api.md](extension-api.md).
- **The code is in a public repository.** You give its link when you upload, and it's shown on your listing, so people can
  read, report and fork it. What homes install is still the exact package you upload, never the repository.
- **It asks only for what it needs**, and its labels say why. The reviewer compares what it asks for with what it does.
- **It loads without warnings** in your own Tomtly: add it from a local folder first and check its card.
- **Say what you tested it on**, such as a device model and firmware, in its README.

## Pack it

From the folder that holds your extension's folder:

```bash
tar -czf tides.tar.gz tides
```

A GitHub release's "Source code (tar.gz)" works too, if `extension.ts` is at the top of the repository. Tests,
`node_modules`, `.git` and macOS's `._*` files are left out for you. A package is at most 4 MiB and 1,000 files.

## Upload it

1. Make an account on the marketplace's website and choose **Publish yours**.
2. Upload the `.tar.gz`, give the repository link, and add an icon and up to five screenshots if you like (PNG, JPEG or
   WebP; an icon up to 256 KiB, a screenshot up to 2 MiB).
3. Follow it on **My uploads**. Once a reviewer approves it, it's listed; if not, the page says why.

The first account to upload an id owns it: nobody else can upload versions of `tides` after you. You can have five
versions waiting at a time.

Consider turning on **two-step sign-in** on your account page: your account is what can publish new versions of your
extension to everyone who has it.

## New versions

Upload a higher version number the same way; each is reviewed again, and people are offered the update once it's
approved. A version number is never reused, even for a version that wasn't approved.

If a version changes its settings, ship a migration, so updating asks for what's new instead of breaking. It's a file for
each such version, at `migrations/<version>.json` in the extension's folder:

```json
{
  "version": "1.1.0",
  "note": "The cloud login needs a region now.",
  "ask": ["region"],
  "rename": { "apiKey": "accessKey" },
  "remove": ["oldToken"]
}
```

- `version` must match the file name, and `note`, saying what changed, is required. Other fields are refused.
- `ask` lists the settings to prompt for when someone updates. A value already saved is kept if they leave it blank.
- `rename` moves a saved value from the old key to the new one.
- `remove` forgets a saved value. Also listing it in `ask` forces a new value, as for a token that stopped working.

The setting itself, with its label and help, is still declared in the code. Someone updating past several versions gets
every migration between them, oldest first. A new required setting without a default must be asked for, or renamed into,
by a migration, or the upload is refused.

## What can happen after

If a problem is found, the marketplace can **suspend** a version (no new installs while it's looked into) or **withdraw**
it. Homes that have it are told why, and it keeps running until each owner decides: nothing switches a home off remotely.

## Running your own marketplace

Homes can point at any marketplace that speaks the same protocol. [marketplace-protocol.md](marketplace-protocol.md) is
the contract: what a marketplace serves, how it's signed, and what every home checks.
