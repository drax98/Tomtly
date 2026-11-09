import { describe, expect, it } from "vitest";
import { loadExtension, makeHost } from "./harness";

const load = () => loadExtension(() => import("../weather/extension.ts"));
const settings = { latitude: 51.5, longitude: -0.12 };
const URL =
  "https://api.open-meteo.com/v1/forecast?latitude=51.5&longitude=-0.12" +
  "&current=temperature_2m,apparent_temperature,relative_humidity_2m,precipitation,cloud_cover,wind_speed_10m,weather_code,is_day" +
  "&hourly=temperature_2m,precipitation_probability&forecast_days=2&timeformat=unixtime";

const sample = () => ({
  current: {
    temperature_2m: 12.5,
    apparent_temperature: 10.1,
    relative_humidity_2m: 80,
    precipitation: 0.2,
    cloud_cover: 60,
    wind_speed_10m: 14,
    weather_code: 3,
    is_day: 1,
  },
  hourly: {
    time: [1700000000, 1700003600, 1700007200],
    temperature_2m: [11, null, 9],
    precipitation_probability: [10, 20, null],
  },
});

describe("weather", () => {
  it("asks Open-Meteo for the exact current and hourly fields", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: sample() }) });
    def.data.fetch(host.ctx);
    expect(host.fetches).toEqual([{ url: URL, options: {} }]);
  });

  it("returns the current conditions, and drops hours with no temperature", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: sample() }) });
    expect(def.data.fetch(host.ctx)).toEqual({
      temperature: 12.5,
      feelsLike: 10.1,
      humidity: 80,
      precipitation: 0.2,
      cloudCover: 60,
      windSpeed: 14,
      weatherCode: 3,
      isDay: true,
      hourly: [
        { time: 1700000000000, temperature: 11, precipitationProbability: 10 },
        { time: 1700007200000, temperature: 9, precipitationProbability: null },
      ],
    });
  });

  it("reports night when is_day is 0", async () => {
    const def = await load();
    const body = sample();
    body.current.is_day = 0;
    const host = makeHost({ settings, fetch: () => ({ body }) });
    expect(def.data.fetch(host.ctx).isDay).toBe(false);
  });

  it("throws the status when Open-Meteo doesn't answer 200", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ status: 429 }) });
    expect(() => def.data.fetch(host.ctx)).toThrow("Open-Meteo answered 429");
  });

  it("refuses a reply with no current temperature rather than report a made-up one", async () => {
    const def = await load();
    const body = sample();
    body.current.temperature_2m = "12" as unknown as number;
    const host = makeHost({ settings, fetch: () => ({ body }) });
    expect(() => def.data.fetch(host.ctx)).toThrow("Open-Meteo sent no current temperature");
  });

  it("refuses a reply with no current block at all", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: { hourly: { time: [], temperature_2m: [], precipitation_probability: [] } } }) });
    expect(() => def.data.fetch(host.ctx)).toThrow("Open-Meteo sent no current temperature");
  });
});
