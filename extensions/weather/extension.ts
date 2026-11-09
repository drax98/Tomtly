// A plugin with no credentials: current conditions and an hourly forecast for scripts to read, and a widget for Home.

let latest: Weather | undefined; // what the widget shows, from the last good fetch

const round = (n: number) => Math.round(n * 10) / 10;

defineExtension({
  id: "weather",
  name: "Weather",
  version: "1.1.0",
  license: "MIT",
  description: "Current conditions and a two-day hourly forecast from Open-Meteo (CC BY 4.0).",
  categories: ["weather"],
  tags: ["Open-Meteo", "forecast", "temperature", "rain"],
  homepage: "https://open-meteo.com",
  access: { network: ["api.open-meteo.com"] },
  settings: {
    latitude: { label: "Latitude", type: "number", required: true, help: "Sent to Open-Meteo, so round it if you prefer." },
    longitude: { label: "Longitude", type: "number", required: true },
  },
  data: {
    everySeconds: 300,
    fetch(ctx) {
      const query = [
        `latitude=${ctx.settings.latitude}`,
        `longitude=${ctx.settings.longitude}`,
        "current=temperature_2m,apparent_temperature,relative_humidity_2m,precipitation,cloud_cover,wind_speed_10m,weather_code,is_day",
        "hourly=temperature_2m,precipitation_probability",
        "forecast_days=2",
        "timeformat=unixtime",
      ].join("&");
      const response = ctx.fetch(`https://api.open-meteo.com/v1/forecast?${query}`);
      if (!response.ok) throw new Error(`Open-Meteo answered ${response.status}`);

      const { current, hourly } = response.json();
      // Rather a visible failure than a script heating the house on a temperature that was never reported.
      if (typeof current?.temperature_2m !== "number") throw new Error("Open-Meteo sent no current temperature");

      const hours: WeatherHour[] = [];
      hourly.time.forEach((time: number, i: number) => {
        if (hourly.temperature_2m[i] !== null) {
          hours.push({ time: time * 1000, temperature: hourly.temperature_2m[i], precipitationProbability: hourly.precipitation_probability[i] });
        }
      });
      const weather: Weather = {
        temperature: current.temperature_2m,
        feelsLike: current.apparent_temperature,
        humidity: current.relative_humidity_2m,
        precipitation: current.precipitation,
        cloudCover: current.cloud_cover,
        windSpeed: current.wind_speed_10m,
        weatherCode: current.weather_code,
        isDay: current.is_day === 1,
        hourly: hours,
      };
      latest = weather;
      return weather;
    },
  },
  widgets: {
    now: {
      title: "Weather now",
      render() {
        if (!latest) return [{ type: "text", text: "Waiting for the first forecast." }];
        const now = Date.now();
        const next = latest.hourly.filter((h) => h.time > now).slice(0, 3);
        return [
          { type: "stat", label: "Outside", value: round(latest.temperature), unit: "°C" },
          {
            type: "rows",
            rows: [
              { label: "Feels like", value: `${round(latest.feelsLike)} °C` },
              { label: "Wind", value: `${round(latest.windSpeed)} km/h` },
              ...next.map((h) => ({
                label: `In ${Math.max(1, Math.round((h.time - now) / 3_600_000))} h`,
                value: `${round(h.temperature)} °C${h.precipitationProbability ? `, rain ${h.precipitationProbability}%` : ""}`,
                tone: (h.precipitationProbability ?? 0) >= 60 ? ("warn" as const) : undefined,
              })),
            ],
          },
        ];
      },
    },
  },
});
