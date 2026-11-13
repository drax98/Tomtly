# Writing automations

Every `.ts` file directly in your `automations/` folder is one script: an ordinary TypeScript program. Top-level code
runs once when the script loads, and variables keep their values between runs, so a script can remember, count and
decide whatever you can write. `new Date()` is in your configured timezone.

Edit scripts on the Automations page, in the editor from VS Code, with autocomplete and type errors for the `home` API.
If you'd rather use your own editor, change the files directly and press **Reload scripts**, or call
`POST /api/automations/reload`.

A save only goes through if the script compiles and loads, and otherwise changes nothing. Saving a live script returns
it to shadow mode unless you choose **Save and keep live**, which is only for someone who may also approve what scripts reach. When anyone else changes a script, its approvals go too and it returns to shadow mode, so new code waits for someone to look at what it asks for. Saving is refused if the file changed since you opened it,
so the page can't overwrite an edit you made elsewhere.

Find device ids and value codes with `curl localhost:8099/api/devices`.

```ts
const fan = home.device("your-fan-id");
const sensor = home.device("your-sensor-id");
let hotSince = 0;

home.reconcile(() => {
  const temperature = sensor.get("temperature");
  if (typeof temperature !== "number") return; // not reported yet

  hotSince = temperature > 26 ? hotSince || Date.now() : 0;
  const hotForTenMinutes = hotSince > 0 && Date.now() - hotSince > 10 * 60_000;
  fan.set("switch", hotForTenMinutes && new Date().getHours() >= 7);
});
```

`examples/automations/heating.ts` is a fuller one: radiators split into rooms, with window sensors and a boiler relay.

The whole API is in [`sdk/home.d.ts`](../sdk/home.d.ts). To get autocomplete and type checks in your own editor, copy
`examples/automations/tsconfig.json` next to your scripts and point its `include` at that file.

## reconcile or daily

- **`home.reconcile(fn)`** runs about once a second. Say what should be true right now, and `set` sends nothing when a device already matches. This is what makes a house recover by itself after a restart or a lost command, but it also means a manual change gets undone, so use it for things that should always hold.
- **`home.daily("HH:MM", fn)`** runs once a day in the configured timezone. Use it when you still want manual control in between. A time missed while the service was down is skipped.

## Unknown values

`get` returns `undefined` until a device has reported that value, which battery sensors can take a while to do after a
restart. Treat it as "don't know", not as off. `set` does nothing while the device is unreachable, because a queued
command would fire stale whenever it came back.

`offlineSeconds()` tells a short gap from a lost device. Don't react to `online()` directly if a wrong reaction is
costly: a gateway reconnecting makes everything offline for a few seconds.

## Shadow mode

A new script changes nothing. It records what it would send on the Automations page, and flags any decision the
devices still disagree with after a minute. Switch it live with the switch on its card once you trust it.

Shadow mode goes through the same approvals as live, so a shadow script can't read what it isn't approved for, and a
write records **Would do** only once it is approved. Everything it was refused is on its card, so you can approve it
before going live. Switching live with something unapproved is allowed, and the page warns you first: those calls stay
refused and the rest of the script runs.

To replace rules from a vendor's app:

1. Add the script and let it run in shadow mode alongside the old rules, ideally through a few real events.
2. Check the activity log shows no **Differs** entries you can't explain.
3. Turn the old rules off in the vendor's app, then switch the script live straight away.

With both running, they can fight over a device whenever they disagree.

## What a script can do

Scripts can only use the `home` object: no network, files, timers or `async`. A script that throws or runs for more than
two seconds is stopped and reported on its card, and the others keep running.

`import` only reaches `.ts` and `.js` files in your `automations/` folder, by a relative path, so a script can't read your
credentials or any data file. `tsconfig.json` is only for your editor: scripts are built without it.

### Approving what a script reaches

Every read and change goes through one check in the service, per script and per thing:

| The script calls | Its card asks you to approve | Until you do |
| --- | --- | --- |
| `get`, `online`, `offlineSeconds` | Read that device | `undefined`, `false`, `0` |
| `set` | Change that device | does nothing |
| a plugin's `data`, `ageSeconds` | Read that plugin's data | `undefined` |
| a plugin's `action("name")` | Run that action on that plugin | does nothing |

A refused call doesn't throw, so one unapproved sensor doesn't stop the rest of a script, and it looks like a value that
isn't known yet, which a script has to handle anyway. It is recorded by the service, so a `try`/`catch` can't hide it: it
shows on the card with how many times it was refused, and once in the activity log as **Refused**. `home.device(id)` itself
never needs approval, and can't be used to find out which ids exist. `home.log`, `reconcile` and `daily` need none either,
and logging is limited to 120 lines a minute.

Approving takes the Administrator role and is recorded in the audit log. Approvals are kept per script, so editing a script
keeps them, and anything new it asks for is refused until approved. Approving reloads the script, which restarts its
variables. A new script that reuses a deleted one's name through the page starts with nothing.

The service finds out what a script needs in three ways. It runs the script once in a throwaway copy each time it loads,
including its `reconcile` and `daily` functions, with nothing sent. It watches every real run. And a script can list things
that a rare branch needs, so you can approve them before the day it runs:

```ts
home.needs({ read: ["frost-sensor"], write: ["boiler"], data: ["weather"], actions: { phone: ["notify"] } });
```

Without that, something a branch only needs in January is first refused in January. So for anything costly, like heating,
list it or watch a script through its branches in shadow mode before relying on it. `home.needs` grants nothing, and the
check when a call runs is what counts: an id worked out at run time is asked for like any other.

An approved action receives whatever arguments the script gives it, so a script approved to read something and to send a
notification can send what it read. Read a script from someone else before you approve it.

## Outside data

Plugins bring in data such as the weather, and scripts read it like this. See [plugins.md](plugins.md).

```ts
const weather = home.plugin<Weather>("weather");

home.reconcile(() => {
  const now = weather.data();
  if (now && now.temperature < 0) home.log("freezing outside");
});
```

## Widgets on Home

A script can offer widgets, which anyone with the Family role or higher places on Home with **Add widgets**. A widget
is data, not markup: `render` returns blocks and the page draws them. Buttons call the widget's `actions` with what its
inputs hold, plus the button's `args`.

```ts
const heaters = ["tuya:living", "tuya:bedroom"].map((id) => home.device(id));

home.widget("heating", {
  title: "All heaters",
  render: () => [
    ...heaters.map((h) => ({ type: "stat" as const, label: h.name, value: h.get("temp_current"), unit: "°C" })),
    { type: "number", name: "temp", label: "Target", value: 21, min: 5, max: 30, step: 0.5, unit: "°C" },
    { type: "button", label: "Set every heater", action: "setAll" },
  ],
  actions: {
    setAll: (input) => heaters.forEach((h) => h.set("temp_set", Math.round(input.temp * 10))),
  },
});
```

The blocks are `text`, `stat`, `rows`, `device` (a device's value with its control; `min` and `max` narrow its slider to
the range that matters, in the units shown), the inputs `number`, `toggle`, `select` and `time` (`"HH:MM"`, in `step`
minutes), `button`, and `dialog`: a button that opens a form of its own `blocks` (inputs, text, rows and groups), whose
**Save** calls its `action` with the form's values and its `args`, like a button; `columns: 2` pairs its inputs side
by side. A dialog keeps a widget short when
there's more to change than fits, such as a week's heating program. `group` starts a titled section: the blocks after it, up to the next group, are drawn together
under its `label`, so a widget about several things says which control is which. `sdk/home.d.ts` lists their fields.
A script has up to 10 widgets of up to 40 blocks each, a group counting as one.

- `render` runs every 5 seconds and after a press. A render that fails shows its error on the widget, not in the log.
- A press runs like the rest of the script. A practising script only logs what it would do, and the page says so.
  Reads and changes need approving: what the actions use is asked for when the script is saved, not first when
  someone presses.
- A press shows in the activity log. Like any `set`, the same value isn't sent again within 30 seconds.
- Viewers see the widget without its inputs and buttons. The service refuses a press from them, and any press of a
  button the widget doesn't show right now.
- A reconcile that sets the same value decides it again a second later, so a button and a reconcile shouldn't fight
  over one setting.

## Sharing code between scripts

Put shared code in a subfolder of `automations/` and import it with a relative path. The page only edits top-level
scripts, and its editor can't see these files, so it underlines the import as an error even though the script runs.
