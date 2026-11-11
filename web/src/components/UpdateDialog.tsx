import { useState, type FormEvent } from "react";
import { api, hiddenSetting, type ExtensionSetting, type ExtensionStatus } from "@/api";
import { AccessList, Warnings } from "@/components/ExtensionParts";
import { SettingField } from "@/components/SettingField";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { messageOf } from "@/lib/utils";
import { ErrorText } from "@/components/PageHeader";

const blank = (value: unknown) => value === undefined || value === null || (typeof value === "string" && value.trim() === "");

/** What a field starts with. A secret or a file always starts empty, and a required switch has only two answers, so off is one. */
const startingValue = (s: ExtensionSetting) => {
  if (hiddenSetting(s)) return undefined;
  return s.value ?? s.default ?? (s.type === "boolean" && s.required ? false : undefined);
};

/** Shows what moving to a new version means and asks for what it needs. A refusal stays here with what was typed. */
export function UpdateDialog({ title, action, source, found, currentAccess, onClose, onDone }: {
  title: string;
  action: string;
  /** What runs now, which this replaces. */
  source: string;
  found: ExtensionStatus;
  currentAccess: string[];
  onClose: () => void;
  onDone: (waitsForApproval: boolean) => void;
}) {
  const steps = found.upgrade?.steps ?? [];
  const ask = found.upgrade?.ask ?? [];
  const more = found.access.filter((a) => !currentAccess.includes(a));
  const [values, setValues] = useState<Record<string, unknown>>(() => Object.fromEntries(ask.map((s) => [s.key, startingValue(s)])));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const missing = ask.some((s) => s.required && !s.set && s.default === undefined && blank(values[s.key]));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (missing) return;
    setBusy(true);
    setError(null);
    try {
      const settings = Object.fromEntries(Object.entries(values).filter(([, value]) => !blank(value)));
      await api.replaceExtension(source, found.source, ask.length ? settings : undefined, found.access);
      onDone(more.length > 0);
      onClose();
    } catch (err) {
      setError(messageOf(err, "That didn't work. Try again."));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto">
        <form className="space-y-4" onSubmit={(e) => void submit(e)}>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{ask.length ? "Its saved data stays. It restarts with the new code." : "Its settings and saved data stay. It restarts with the new code."}</DialogDescription>
          </DialogHeader>
          {steps.length > 0 && (
            <div className="space-y-1 text-sm">
              <p className="font-medium">What changed</p>
              <ul className="space-y-1">
                {steps.map((step) => <li key={step.version} className="break-words"><span className="font-medium">Version {step.version}:</span> {step.note}</li>)}
              </ul>
            </div>
          )}
          {ask.length > 0 && (
            <div className="space-y-3">
              <p className="text-sm font-medium">This update needs</p>
              {ask.map((setting) => (
                <SettingField
                  key={setting.key}
                  setting={setting}
                  keep={setting.set}
                  value={values[setting.key]}
                  onChange={(value) => setValues((current) => ({ ...current, [setting.key]: value }))}
                />
              ))}
            </div>
          )}
          <div className="space-y-2 text-sm">
            {more.length > 0 ? (
              <>
                <p>This version asks for access the old one didn't have, so it will wait for your OK before it runs again:</p>
                <AccessList access={more} />
              </>
            ) : <p>It asks for the same access as before.</p>}
          </div>
          <Warnings warnings={found.risks} title="The risk in approving this" open />
          <Warnings warnings={found.warnings} open />
          {error && <ErrorText>{error}</ErrorText>}
          {missing && <p className="text-xs text-muted-foreground">Fill in what this update needs to continue.</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={busy || missing}>{busy ? "Working…" : action}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
