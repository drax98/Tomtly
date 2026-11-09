import { useEffect, useState, type FormEvent } from "react";
import { LocateFixed, Save, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { api, type HomeSettings } from "@/api";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { Field } from "@/components/auth/Field";
import { useLoad } from "@/lib/useLoad";
import { messageOf } from "@/lib/utils";

const LANGUAGES = ["en", "pl", "de", "fr", "es", "it", "nl", "pt", "cs", "uk", "sv"];
// Each language in its own words, so someone can find theirs whatever the page is in.
const languageName = (code: string) => new Intl.DisplayNames([code], { type: "language" }).of(code) ?? code;
const TIMEZONES = Intl.supportedValuesOf("timeZone");
const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone;

/** The home's language, timezone and location, which everyone in it shares. */
export function SettingsPage() {
  const { data, error: loadError, reload } = useLoad(api.settings);
  const [language, setLanguage] = useState("");
  const [timezone, setTimezone] = useState("");
  const [lat, setLat] = useState("");
  const [lon, setLon] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!data) return;
    setLanguage(data.language || "en");
    setTimezone(data.timezone || browserZone);
    setLat(data.latitude?.toString() ?? "");
    setLon(data.longitude?.toString() ?? "");
  }, [data]);

  const locate = () => {
    if (!navigator.geolocation) return setError("This browser can't tell where it is. Type the location in instead.");
    navigator.geolocation.getCurrentPosition(
      ({ coords }) => { setLat(coords.latitude.toFixed(5)); setLon(coords.longitude.toFixed(5)); setError(null); },
      () => setError("The browser didn't share its location. Type it in instead."),
    );
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const settings: HomeSettings = { language, timezone: timezone.trim(), latitude: lat.trim() ? Number(lat) : null, longitude: lon.trim() ? Number(lon) : null };
    setSaving(true);
    try {
      await api.setSettings(settings);
      toast("Settings saved");
    } catch (err) {
      setError(messageOf(err, "The settings weren't saved"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title="Settings" description="How your home is set up. Everyone in it shares these." />
      {loadError && !data ? (
        <EmptyState icon={<TriangleAlert />} title="Couldn't load the settings" action={<Button variant="outline" onClick={() => void reload()}>Try again</Button>}>{loadError}</EmptyState>
      ) : !data ? (
        <Skeleton className="h-64" />
      ) : (
        <form className="max-w-xl space-y-5 rounded-2xl border bg-card p-4 sm:p-6" onSubmit={save}>
          <div className="space-y-1.5">
            <Label id="language-label">Language</Label>
            <Select value={language} onValueChange={setLanguage}>
              <SelectTrigger className="h-11 w-full" aria-labelledby="language-label"><SelectValue /></SelectTrigger>
              <SelectContent>
                {LANGUAGES.map((code) => <SelectItem key={code} value={code}>{languageName(code)}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <Field label="Timezone" list="timezones" required autoComplete="off" value={timezone} onChange={(e) => setTimezone(e.target.value)} help="Start typing a city, like Europe/Warsaw." />
          <datalist id="timezones">{TIMEZONES.map((z) => <option key={z} value={z} />)}</datalist>
          <fieldset className="space-y-3">
            <legend className="text-sm font-medium">Location (optional)</legend>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Latitude" type="number" step="any" min={-90} max={90} required={!!lon.trim()} placeholder="52.2297" value={lat} onChange={(e) => setLat(e.target.value)} />
              <Field label="Longitude" type="number" step="any" min={-180} max={180} required={!!lat.trim()} placeholder="21.0122" value={lon} onChange={(e) => setLon(e.target.value)} />
            </div>
            <Button type="button" variant="outline" onClick={locate}><LocateFixed aria-hidden />Use this device's location</Button>
          </fieldset>
          {error && <Alert variant="destructive"><TriangleAlert aria-hidden /><AlertDescription>{error}</AlertDescription></Alert>}
          <Button type="submit" disabled={saving}><Save aria-hidden />{saving ? "Saving…" : "Save"}</Button>
        </form>
      )}
    </div>
  );
}
