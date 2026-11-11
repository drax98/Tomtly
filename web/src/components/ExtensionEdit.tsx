import { useState, type FormEvent } from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "sonner";
import { api, hiddenSetting, type ExtensionStatus } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { SettingField } from "@/components/SettingField";
import { messageOf } from "@/lib/utils";
import { ErrorText } from "@/components/PageHeader";

/** The address is only ever shown here, as it's made: the service keeps nothing it could show again. */
export function WebhookAddress({ path, onClose }: { path: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const address = `${window.location.origin}${path}`;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address);
      setCopied(true);
    } catch {
      toast.error("Couldn't copy. Select the address and copy it yourself.");
    }
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Copy the webhook address now</DialogTitle>
          <DialogDescription>
            Give this to the service that should send messages here. Anyone who has it can send messages, so keep it private. This is the only time it is shown: if you lose it, make a new one and the old one stops working.
          </DialogDescription>
        </DialogHeader>
        <code className="break-all rounded-md border bg-card px-3 py-2 font-mono text-xs" data-testid="webhook-address">{address}</code>
        <p className="text-xs text-muted-foreground">
          This starts with the address you are using right now. If the other service reaches Tomtly by a different address, use that one in its place and keep the rest.
        </p>
        <DialogFooter>
          <Button variant="outline" onClick={() => void copy()}>{copied ? <Check aria-hidden /> : <Copy aria-hidden />}{copied ? "Copied" : "Copy"}</Button>
          <Button onClick={onClose}>I've saved it</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Changes the settings of an extension. A secret or a file is never shown: left empty, it keeps the one it has. */
export function EditSettings({ extension, onClose, onSaved }: { extension: ExtensionStatus; onClose: () => void; onSaved: () => void }) {
  const [values, setValues] = useState<Record<string, unknown>>(() =>
    Object.fromEntries(extension.settings.filter((s) => !hiddenSetting(s) && s.value !== undefined).map((s) => [s.key, s.value])),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      // A blank setting is sent as blank so it is cleared; a blank secret or file is left out so it is kept.
      const settings: Record<string, unknown> = {};
      for (const s of extension.settings) {
        const value = values[s.key];
        if (hiddenSetting(s)) {
          if (value !== undefined && value !== "") settings[s.key] = value;
        } else {
          settings[s.key] = value ?? "";
        }
      }
      await api.updateExtensionSettings(extension.source, settings);
      toast.success("Saved. It's restarting.");
      onSaved();
      onClose();
    } catch (err) {
      setError(messageOf(err, "Couldn't save the settings"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form className="space-y-4" onSubmit={save}>
          <DialogHeader>
            <DialogTitle>Settings for {extension.name ?? "this add-on"}</DialogTitle>
            <DialogDescription>It restarts when you save. Keys and passwords are never shown here: leave one blank to keep the one it has.</DialogDescription>
          </DialogHeader>
          {extension.settings.map((setting) => (
            <SettingField
              key={setting.key}
              setting={setting}
              keep={setting.set}
              value={values[setting.key]}
              onChange={(value) => setValues((current) => ({ ...current, [setting.key]: value }))}
            />
          ))}
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={busy}>{busy ? "Saving…" : "Save"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** Asks which GitHub version to move to, and checks it before anything changes. */
export function GithubVersion({ source, onClose, onFound }: { source: string; onClose: () => void; onFound: (found: ExtensionStatus) => void }) {
  const [next, setNext] = useState(source);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const check = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onFound(await api.inspectExtension(next.trim()));
    } catch (err) {
      setError(messageOf(err, "That didn't work. Try again."));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form className="space-y-4" onSubmit={(e) => void check(e)}>
          <DialogHeader>
            <DialogTitle>Update from GitHub</DialogTitle>
            <DialogDescription>Change what comes after the @ to the new tag or commit. Nothing changes until you've seen what the new version needs.</DialogDescription>
          </DialogHeader>
          <div className="space-y-1">
            <label htmlFor="github-version" className="text-sm">New version</label>
            <Input id="github-version" className="font-mono text-xs" value={next} onChange={(e) => setNext(e.target.value)} />
          </div>
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={busy || next.trim() === source}>{busy ? "Checking…" : "Check"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
