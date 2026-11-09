// Served to the script editor, so home.plugin("weather") gets autocomplete with these comments.

/** One hour of the forecast. */
interface WeatherHour {
  /** Milliseconds, so new Date(hour.time) works. */
  time: number;
  /** °C */
  temperature: number;
  /** %, null where the forecast doesn't say. */
  precipitationProbability: number | null;
}

interface Weather {
  /** °C */
  temperature: number;
  /** °C */
  feelsLike: number;
  /** % */
  humidity: number;
  /** mm */
  precipitation: number;
  /** % */
  cloudCover: number;
  /** km/h */
  windSpeed: number;
  /** WMO weather code. */
  weatherCode: number;
  isDay: boolean;
  /** Each hour of today and tomorrow, including hours already past. */
  hourly: WeatherHour[];
}

interface PluginMap {
  weather: Weather;
}
