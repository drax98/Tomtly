import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { authApi, MIN_PASSWORD } from "@/authApi";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field } from "./Field";
import { messageOf } from "@/lib/utils";
import { ErrorText } from "@/components/PageHeader";

export function ChangePasswordDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [endTokens, setEndTokens] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const ready = current !== "" && next.length >= MIN_PASSWORD && next === again;

  const close = (value: boolean) => {
    if (!value) {
      setCurrent("");
      setNext("");
      setAgain("");
      setEndTokens(true);
      setError(null);
    }
    onOpenChange(value);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await authApi.changePassword(current, next, !endTokens);
      toast.success("Password changed");
      close(false);
    } catch (err) {
      // The server answers 403 for a wrong current password; that is not a permissions problem.
      setError((err as { status?: number }).status === 403 ? "Current password is wrong" : messageOf(err, "Could not change the password"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <form className="space-y-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Change your password</DialogTitle>
            <DialogDescription>Use at least {MIN_PASSWORD} characters. There is no email recovery, so keep it somewhere safe.</DialogDescription>
          </DialogHeader>
          <Field label="Current password" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          <Field label="New password" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} help={next && next.length < MIN_PASSWORD ? `${MIN_PASSWORD - next.length} more characters needed.` : undefined} />
          <Field label="New password again" type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} help={again && again !== next ? "These two don't match yet." : undefined} />
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" className="mt-0.5 size-4" checked={endTokens} onChange={(e) => setEndTokens(e.target.checked)} />
            <span>Also delete my API tokens. Leave this on if a token might be why you're changing your password. Anything using one will stop working until you make a new one.</span>
          </label>
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => close(false)}>Cancel</Button>
            <Button type="submit" disabled={!ready || busy}>{busy ? "Saving…" : "Change password"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
