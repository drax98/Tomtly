import { useCallback, useState } from "react";
import { FlaskConical, Pencil, Play, Plus, RefreshCw, ScrollText, ShieldAlert, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { api, type AutomationStatus, type ScriptStatus } from "@/api";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Input } from "@/components/ui/input";
import { useConfirm } from "@/components/ConfirmDialog";
import { EmptyState, PageBody, PageHeader } from "@/components/PageHeader";
import { ScriptEditor } from "@/components/ScriptEditor";
import { useAuth } from "@/lib/auth";
import { capitalize } from "@/lib/device";
import type { Page } from "@/lib/route";
import { usePoll } from "@/lib/useDevices";
import { cn, messageOf } from "@/lib/utils";

const KIND_STYLES: Record<string, string> = {
  set: "bg-good/15 text-good",
  matched: "bg-good/15 text-good",
  mismatch: "bg-heat/15 text-heat",
  error: "bg-destructive/15 text-destructive",
  refused: "bg-heat/15 text-heat",
};
const KIND_LABELS: Record<string, string> = { set: "Did", shadow: "Would do", mismatch: "Differs", matched: "Agrees", refused: "Refused" };

const VALID_NAME = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;

function ScriptAccessList({ script, canApprove, onAccess }: {
  script: ScriptStatus; canApprove: boolean; onAccess: (keys: string[], approved: boolean) => void;
}) {
  const waiting = (script.access ?? []).filter((a) => !a.approved);
  const approved = (script.access ?? []).filter((a) => a.approved);
  return (
    <>
      {waiting.length > 0 && (
        <Alert className="border-heat/40">
          <ShieldAlert aria-hidden />
          <AlertTitle>{script.live ? "Running, but these calls are refused" : "Needs your approval before it goes live"}</AlertTitle>
          <AlertDescription className="space-y-2">
            <ul className="w-full space-y-1.5">
              {waiting.map((a) => (
                <li key={a.key} className="flex flex-wrap items-center justify-between gap-2">
                  <span className="min-w-0 break-words">
                    {a.label}
                    {!!a.refused && <span className="text-muted-foreground"> · refused {a.refused} {a.refused === 1 ? "time" : "times"}</span>}
                  </span>
                  {canApprove && <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" aria-label={`Approve: ${a.label}`} onClick={() => onAccess([a.key], true)}>Approve</Button>}
                </li>
              ))}
            </ul>
            {canApprove && waiting.length > 1 && (
              <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" onClick={() => onAccess(waiting.map((a) => a.key), true)}>Approve all {waiting.length}</Button>
            )}
            {!canApprove && <p>An administrator has to approve these.</p>}
          </AlertDescription>
        </Alert>
      )}
      {approved.length > 0 && (
        <details className="text-sm">
          <summary className="cursor-pointer text-muted-foreground">Approved access ({approved.length})</summary>
          <ul className="mt-2 space-y-1.5">
            {approved.map((a) => (
              <li key={a.key} className="flex flex-wrap items-center justify-between gap-2">
                <span className="min-w-0 break-words">{a.label}</span>
                {canApprove && <Button variant="ghost" size="sm" className="min-h-11 sm:min-h-9" aria-label={`Withdraw: ${a.label}`} onClick={() => onAccess([a.key], false)}>Withdraw</Button>}
              </li>
            ))}
          </ul>
        </details>
      )}
    </>
  );
}

function ScriptCard({ script, canEdit, canLive, canApprove, onEdit, onLive, onAccess }: {
  script: ScriptStatus; canEdit: boolean; canLive: boolean; canApprove: boolean; onEdit: () => void; onLive: (live: boolean) => void; onAccess: (keys: string[], approved: boolean) => void;
}) {
  return (
    <Card className={cn("gap-3 p-4", script.live && "border-primary/35 bg-primary/[0.06]")}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-1.5">
          <h3 className="truncate text-base font-medium">{capitalize(script.name)}</h3>
          <Badge variant="secondary" className={cn(script.live ? "bg-primary/15 text-primary" : "text-muted-foreground")}>
            {script.live ? <Play className="size-3" aria-hidden /> : <FlaskConical className="size-3" aria-hidden />}
            {script.live ? "Running" : "Practising"}
          </Badge>
        </div>
        <Switch aria-label={`Run ${script.name} live`} checked={script.live} disabled={!canLive} onCheckedChange={onLive} />
      </div>
      <p className="text-sm text-muted-foreground">
        {script.live ? "It is controlling your devices right now." : "Shows what it would do, changes nothing. Switch it on when you're happy with that."}
      </p>
      {script.error && (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>This automation has a problem</AlertTitle>
          <AlertDescription className="break-words font-mono text-xs">{script.error}</AlertDescription>
        </Alert>
      )}
      <ScriptAccessList script={script} canApprove={canApprove} onAccess={onAccess} />
      {canEdit && (
        <div><Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" aria-label={`Edit ${script.name}`} onClick={onEdit}><Pencil aria-hidden />Edit</Button></div>
      )}
    </Card>
  );
}

export function AutomationsPage({ routeName, go }: { routeName: string; go: (page: Page, sub?: string) => void }) {
  const { can } = useAuth();
  const confirm = useConfirm();
  const [status, setStatus] = useState<AutomationStatus | null>(null);
  const [creating, setCreating] = useState<string | null>(null);
  const [newName, setNewName] = useState("");

  const refresh = useCallback(async () => {
    try {
      setStatus(await api.automations());
    } catch {
      // The header already says when the service is unreachable.
    }
  }, []);
  usePoll(refresh);

  const run = async (action: () => Promise<AutomationStatus>) => {
    try {
      setStatus(await action());
    } catch (e) {
      toast.error("That didn't work", { description: messageOf(e, "Try again in a moment.") });
    }
  };

  const setLive = async (script: ScriptStatus, live: boolean) => {
    if (live) {
      const waiting = (script.access ?? []).filter((a) => !a.approved);
      const ok = await confirm({
        title: `Let "${capitalize(script.name)}" control your home?`,
        body: (
          <>
            <p>From now on this automation will switch devices on and off by itself, following its rules.</p>
            <p>Before you do: open Activity below and check "Would do" matches what you'd want. And turn off any rule in the vendor's app that does the same job, or the two will fight.</p>
            {waiting.length > 0 && (
              <>
                <p>It isn't approved for these, so they will be refused and it won't work fully until they are:</p>
                <ul className="list-disc pl-5">{waiting.map((a) => <li key={a.key}>{a.label}</li>)}</ul>
              </>
            )}
          </>
        ),
        action: "Yes, switch it on",
      });
      if (!ok) return;
    }
    await run(() => api.setLive(script.name, live));
    if (!live) {
      toast(`${capitalize(script.name)} is back to practising`, { action: { label: "Undo", onClick: () => void run(() => api.setLive(script.name, true)) } });
    }
  };

  const editing = VALID_NAME.test(routeName) ? { name: routeName, isNew: false } : creating ? { name: creating, isNew: true } : null;

  if (editing && status) {
    return (
      <PageBody full>
        <ScriptEditor
          key={editing.name}
          name={editing.name}
          isNew={editing.isNew}
          live={status.scripts.find((s) => s.name === editing.name)?.live ?? false}
          // New code keeping old approvals is an approval decision, so without that permission a save always goes to shadow.
          canLive={can("automations.live") && can("automations.approve")}
          onClose={() => {
            setCreating(null);
            go("automations");
            void refresh();
          }}
        />
      </PageBody>
    );
  }

  const nameTaken = status?.scripts.some((s) => s.name === newName) ?? false;
  const canCreate = VALID_NAME.test(newName) && !nameTaken;
  const canEdit = can("automations.edit");

  return (
    <PageBody>
      <PageHeader
        title="Automations"
        description="Small programs that look after your home for you, like turning the heating down at night. New ones practise first: they show what they would do without doing it."
        actions={<Button variant="outline" onClick={() => run(api.reload)}><RefreshCw aria-hidden />Reload from disk</Button>}
      />
      {!canEdit && <p className="rounded-lg bg-muted px-3 py-2 text-sm text-muted-foreground">You can see automations here. Editing needs the Editor role{can("automations.live") ? "" : ", and switching them on needs it too"}.</p>}

      {!status ? (
        <div className="grid gap-4 sm:grid-cols-2"><Skeleton className="h-36 rounded-xl" /><Skeleton className="h-36 rounded-xl" /></div>
      ) : status.scripts.length === 0 ? (
        <EmptyState icon={<ScrollText />} title="No automations yet" action={canEdit ? <Button onClick={() => document.getElementById("new-script")?.focus()}><Plus aria-hidden />Write your first one</Button> : undefined}>
          {canEdit ? "Give it a name below and you'll get an editor with an example to start from." : "Ask an editor or administrator to add one."}
        </EmptyState>
      ) : (
        <div className="grid items-start gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {status.scripts.map((script) => (
            <ScriptCard
              key={script.name}
              script={script}
              canEdit={canEdit}
              canLive={can("automations.live")}
              canApprove={can("automations.approve")}
              onEdit={() => go("automations", script.name)}
              onLive={(live) => void setLive(script, live)}
              onAccess={(keys, approved) => void run(() => api.setAccess(script.name, keys, approved))}
            />
          ))}
        </div>
      )}

      {canEdit && (
        <form
          className="flex flex-wrap items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (canCreate) {
              setCreating(newName);
              setNewName("");
            }
          }}
        >
          <Input id="new-script" aria-label="New script name" placeholder="new-script-name" className="w-60 font-mono text-sm" value={newName} onChange={(e) => setNewName(e.target.value)} />
          <Button type="submit" variant="outline" disabled={!canCreate}><Plus aria-hidden />New script</Button>
          {newName && !canCreate && (
            <span className="text-sm text-muted-foreground">{nameTaken ? "That name is taken." : "Use letters, digits, - and _."}</span>
          )}
        </form>
      )}

      {status && (
        <section className="space-y-3" aria-labelledby="activity">
          <h2 id="activity" className="text-lg font-semibold">Activity</h2>
          {status.events.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nothing yet. Each decision an automation makes will appear here, so you can see it working.</p>
          ) : (
            <Card className="py-1">
              <ul className="divide-y">
                {status.events.map((event, i) => (
                  <li key={`${event.time}-${i}`} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-sm">
                    <time className="w-20 shrink-0 text-xs tabular-nums text-muted-foreground" dateTime={event.time}>
                      {new Date(event.time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
                    </time>
                    <Badge variant="secondary" className={cn("shrink-0", KIND_STYLES[event.kind])}>{KIND_LABELS[event.kind] ?? capitalize(event.kind)}</Badge>
                    <span className="text-xs text-muted-foreground">{event.script}</span>
                    <span className="min-w-0 break-words">{event.message.replace(/^would set /, "")}</span>
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </section>
      )}
    </PageBody>
  );
}
