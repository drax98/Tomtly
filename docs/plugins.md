# Plugins

A plugin brings outside data, like the weather or energy prices, into your scripts. Devices are things you control;
plugins are things you only read.

A plugin is an [extension](extensions.md) that provides `data`, such as [weather](https://github.com/drax98/tomtly-extensions/tree/main/weather). Add it on the **Plugins**
page, or list it in `config.json`, and restart the service to pick up changes:

```json
"extensions": [
  { "source": "github:drax98/tomtly-extensions/weather@weather-v1.1.0", "settings": { "latitude": 52.23, "longitude": 21.01 } }
]
```

A plugin's name for scripts is its extension's `id`, so `weather`. Each one refreshes on the
`everySeconds` its extension sets, and the page shows the latest data and any failure.

## Using one in a script

```ts
const weather = home.plugin<Weather>("weather");
const ac = home.device("your-ac-id");

home.reconcile(() => {
  const now = weather.data();
  const age = weather.ageSeconds();
  if (!now || age === undefined || age > 3600) return; // no fresh data: leave things as they are

  const next = now.hourly.filter((hour) => hour.time > Date.now()).slice(0, 6);
  const gettingColder = next.some((hour) => hour.temperature < now.temperature - 3);
  ac.set("power", now.temperature < 5 || gettingColder);
});
```

`data()` is `undefined` until the first successful fetch, which is always the case just after a restart. If fetching
later fails, `data()` keeps returning the last good result and `ageSeconds()` keeps growing, so decide how old is too
old for your rule. Each call parses the data again, so read it once per run.

A plugin can also offer actions, such as `home.plugin("telegram").action("send", { text: "Window left open" })`.

## Write one

Write an [extension](extensions.md) with `data`, and `actions` if scripts should be able to call it.

## Access for scripts

Each script has to be approved separately before it can read a plugin's data (`data`, `ageSeconds`) or run one of its
actions. Until then `data()` returns `undefined` and an action does nothing, without throwing, and the refusal shows on
the script's card. Approving takes the Administrator role. See [Approving what a script reaches](automations.md#approving-what-a-script-reaches).
