import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { ArrowLeft, Save, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { api, type ScriptAccess } from "@/api";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ConfirmDialog";
import { messageOf } from "@/lib/utils";

// Monaco is several megabytes, so only people who open a script load it.
const MonacoEditor = lazy(() => import("./MonacoEditor"));

export const NEW_SCRIPT = `// A script is ordinary TypeScript: variables, functions, classes and anything else you need.
// Find device ids and value codes with: curl localhost:8099/api/devices

// const fan = home.device("your-fan-id");
// const sensor = home.device("your-sensor-id");

// Runs about once a second: say what should be true right now.
// home.reconcile(() => {
//   const temperature = sensor.get("temperature");
//   if (typeof temperature !== "number") return; // not reported yet
//   const night = new Date().getHours() < 7;
//   fan.set("switch", !night && temperature > 26);
// });

// Runs once a day, leaving manual control free in between.
// home.daily("08:30", () => fan.set("switch", true));
`;

interface Props {
  name: string;
  /** Whether this is a script that doesn't exist yet. */
  isNew?: boolean;
  live: boolean;
  /** Whether this person may keep a script running after saving. */
  canLive?: boolean;
  onClose: () => void;
}

export function ScriptEditor({ name, isNew = false, live, canLive = true, onClose }: Props) {
  const confirm = useConfirm();
  const [source, setSource] = useState<string | null>(isNew ? NEW_SCRIPT : null);
  const [saved, setSaved] = useState("");
  const [version, setVersion] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [saving, setSaving] = useState(false);
  const [stillLive, setStillLive] = useState(live);
  const [loads, setLoads] = useState(0); // a new text from the server rebuilds the editor
  const [waiting, setWaiting] = useState<ScriptAccess[]>([]);

  const load = useCallback(async () => {
    try {
      const script = await api.source(name);
      setSource(script.source);
      setSaved(script.source);
      setLoads((n) => n + 1);
      setVersion(script.version);
      setError(null);
      setConflict(false);
    } catch (e) {
      setError(messageOf(e, "Could not load the script"));
    }
  }, [name]);

  useEffect(() => {
    if (!isNew) void load();
  }, [isNew, load]);

  const dirty = source !== null && source !== saved;

  // A browser can't be stopped from closing a tab without this.
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const save = useCallback(async (keepLive: boolean) => {
    if (source === null || saving) return;
    setSaving(true);
    try {
      const result = await api.saveSource(name, source, version, keepLive);
      setSaved(result.source);
      setVersion(result.version);
      setWaiting((result.access ?? []).filter((a) => !a.approved));
      setError(null);
      setConflict(false);
      const toShadow = stillLive && !keepLive;
      if (toShadow) setStillLive(false);
      toast.success(toShadow ? "Saved. The script is back in shadow mode." : "Saved");
    } catch (e) {
      const status = (e as { status?: number }).status;
      setConflict(status === 409);
      setError(messageOf(e, "Could not save the script"));
    } finally {
      setSaving(false);
    }
  }, [name, source, version, saving, stillLive]);

  // Ctrl+S saves the safe way, so a reflex can't change what runs on live devices.
  const saveRef = useRef(save);
  saveRef.current = save;
  const onSaveKey = useCallback(() => void saveRef.current(false), []);

  const close = async () => {
    if (!dirty || await confirm({ title: "Leave without saving?", body: <p>Your changes to {name} will be lost.</p>, action: "Discard changes", danger: true })) onClose();
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Button variant="ghost" size="sm" onClick={() => void close()}><ArrowLeft aria-hidden />Back</Button>
          <h2 className="truncate font-mono text-sm font-medium">{name}.ts</h2>
          <Badge variant="secondary" className={stillLive ? "bg-primary/15 text-primary" : "text-muted-foreground"}>{stillLive ? "Live" : "Shadow"}</Badge>
          {dirty && <span className="text-xs text-muted-foreground">Unsaved changes</span>}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {stillLive && canLive && (
            <Button variant="outline" size="sm" disabled={!dirty || saving} onClick={() => void save(true)}>
              Save and keep live
            </Button>
          )}
          <Button size="sm" disabled={!dirty || saving} onClick={() => void save(false)}>
            <Save aria-hidden />{stillLive ? "Save to shadow" : "Save"}
          </Button>
        </div>
      </div>

      {stillLive && (
        <p className="text-xs text-muted-foreground">
          This automation is controlling real devices. Saving returns it to practice (shadow) mode so a typo can't run on your heating{canLive ? ", unless you choose to keep it live" : ""}.
        </p>
      )}

      {error && (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>{conflict ? "Someone else changed this script" : "Nothing was saved"}</AlertTitle>
          <AlertDescription className="space-y-2">
            <p className="break-words font-mono text-xs">{error}</p>
            {conflict && (
              <Button
                variant="outline"
                size="sm"
                onClick={async () => {
                  if (await confirm({ title: "Load the latest version?", body: <p>Your changes here will be lost.</p>, action: "Load it", danger: true })) void load();
                }}
              >
                Load the latest version
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )}

      {waiting.length > 0 && (
        <Alert>
          <TriangleAlert aria-hidden />
          <AlertTitle>Saved, but it isn't approved for everything it asks for</AlertTitle>
          <AlertDescription>
            <p>An administrator can approve these on the Automations page. Until then these calls are refused and the rest of the script keeps running.</p>
            <ul className="list-disc pl-5">{waiting.map((a) => <li key={a.key}>{a.label}</li>)}</ul>
          </AlertDescription>
        </Alert>
      )}

      {source === null ? (
        !error && <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <Suspense fallback={<div className="min-h-72 flex-1 animate-pulse rounded-xl bg-muted" aria-label="Loading the editor" />}>
          <MonacoEditor key={loads} value={source} onChange={setSource} onSave={onSaveKey} />
        </Suspense>
      )}
    </div>
  );
}
