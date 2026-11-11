import { useRef, useState, type ChangeEvent } from "react";
import { ArrowUpCircle, Ban, ChevronDown, CircleAlert, CircleCheck, CirclePause, KeyRound, Pencil, ShieldCheck, Trash2, TriangleAlert, Upload } from "lucide-react";
import { toast } from "sonner";
import { api, type ExtensionStatus } from "@/api";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { useConfirm } from "@/components/ConfirmDialog";
import { EditSettings, GithubVersion, WebhookAddress } from "@/components/ExtensionEdit";
import { UpdateDialog } from "@/components/UpdateDialog";
import { useAuth } from "@/lib/auth";
import { navigate } from "@/lib/route";
import { cn, messageOf } from "@/lib/utils";

/** An uploaded package's source is a hash, which means nothing to a person. */
export const describeSource = (source: string) => (source.startsWith("upload:") ? "Uploaded file" : source);

const NO_ACCESS = "No access to anything outside itself";

/** A repo's owner can change it whenever they like. Only the marketplace's copies are checked by anyone but you. */
export const GITHUB_RISK = "This comes straight from a GitHub repo and nobody has checked it. Whoever controls that repo decides what's in it, so a tag or branch can change. Tomtly keeps the copy it first downloaded and refuses a different one, but that first copy is on you. Only use it if you trust who wrote it.";

/** What each version between the installed one and the new one says, oldest first. Updating walks through them in order. */
export function UpgradeNotes({ steps }: { steps?: { version: string; note: string; ask?: string[] }[] }) {
  if (!steps?.length) return <p>Nothing changes in its settings.</p>;
  return (
    <ol aria-label="What changes" className="list-inside list-decimal space-y-1">
      {steps.map((step) => (
        <li key={step.version} className="break-words">
          <span className="font-medium">{step.version}:</span> {step.note}
          {(step.ask?.length ?? 0) > 0 && <span className="text-muted-foreground"> It will ask you for {step.ask!.length === 1 ? "1 setting" : `${step.ask!.length} settings`}.</span>}
        </li>
      ))}
    </ol>
  );
}

/** The listing an extension came from, or nothing if it didn't come from the marketplace. */
export const marketListing = (source: string) => /^market:([^@]+)@/.exec(source)?.[1] ?? null;

/** What an extension can reach, each line in a sentence first and the exact grant beneath it. */
export function AccessList({ access }: { access: string[] }) {
  if (access.length === 0) return <p className="text-sm">{NO_ACCESS}</p>;
  return (
    <ul className="list-disc space-y-1 pl-4 text-sm">
      {access.map((a) => <li key={a} className="break-words">{a}</li>)}
    </ul>
  );
}

/** Folded under its title, except where someone is about to agree to it: open says so. */
export function Warnings({ warnings, title = "Check this before you go on", open = false }: { warnings?: string[]; title?: string; open?: boolean }) {
  const [shown, setShown] = useState(open);
  if (!warnings?.length) return null;
  return (
    <Collapsible open={shown} onOpenChange={setShown} asChild>
      <Alert variant="warning" className="py-1.5 sm:py-3">
        {/* The icon sits in the row it labels, so it lines up with the title however tall the row is. */}
        <AlertTitle className="mb-0">
          <CollapsibleTrigger className="flex min-h-11 w-full items-center gap-3 text-left sm:min-h-0">
            <TriangleAlert aria-hidden className="size-4 shrink-0" />
            <span className="flex-1">{title}{warnings.length > 1 && ` (${warnings.length})`}</span>
            <ChevronDown aria-hidden className={cn("size-4 shrink-0 transition-transform", shown && "rotate-180")} />
          </CollapsibleTrigger>
        </AlertTitle>
        <CollapsibleContent asChild>
          <AlertDescription className="pb-1.5 pl-3 pt-1 text-sm sm:pb-0">
            <ul className="list-disc space-y-0.5 pl-4">{warnings.map((w) => <li key={w}>{w}</li>)}</ul>
          </AlertDescription>
        </CollapsibleContent>
      </Alert>
    </Collapsible>
  );
}

/** One glance: is this running, waiting for you, or broken. */
export function ExtensionState({ extension }: { extension?: ExtensionStatus }) {
  if (!extension) return <Badge variant="secondary">Built in</Badge>;
  const [Icon, text, tone] = extension.error ? [CircleAlert, "Problem", "bg-destructive/15 text-destructive"]
    : extension.needsApproval ? [ShieldCheck, "Needs your OK", "bg-warn/15 text-warn"]
      : extension.running ? [CircleCheck, "Running", "bg-good/15 text-good"]
        : [CirclePause, "Not running", "text-muted-foreground"];
  return <Badge variant="secondary" className={cn("shrink-0", tone)}><Icon className="size-3.5" aria-hidden />{text}</Badge>;
}

/** Where an extension came from, what it can reach and how its setup stands. It never knows a setting's value. */
/** Plain text rather than a badge per setting, which looked like buttons. An empty optional setting isn't worth a line. */
function SettingsSummary({ settings }: { settings: ExtensionStatus["settings"] }) {
  const names = (list: typeof settings) => list.map((s) => s.label).join(", ");
  const configured = settings.filter((s) => s.set);
  const missing = settings.filter((s) => !s.set && s.required);
  return (
    <>
      {configured.length > 0 && <p className="text-muted-foreground">Configured: {names(configured)}</p>}
      {missing.length > 0 && <p className="font-medium text-destructive">Missing: {names(missing)}</p>}
    </>
  );
}

export function ExtensionDetails({ extension, onRemoved, onApproved, onChanged }: { extension: ExtensionStatus; onRemoved: () => void; onApproved?: () => void; onChanged?: () => void }) {
  const { can } = useAuth();
  const confirm = useConfirm();
  const manage = can("extensions.manage");
  const [editing, setEditing] = useState(false);
  const [address, setAddress] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [choosing, setChoosing] = useState(false); // which GitHub version to move to
  const [candidate, setCandidate] = useState<ExtensionStatus | null>(null);
  const [fromMarket, setFromMarket] = useState(false); // the candidate is the marketplace's newer version, not an upload
  const picker = useRef<HTMLInputElement>(null);
  const listing = marketListing(extension.source);
  const refresh = () => (onChanged ?? onApproved)?.();
  const fail = (title: string) => (e: unknown) => toast.error(title, { description: messageOf(e, "Try again.") });

  const approve = async () => {
    const ok = await confirm({
      title: `Let ${extension.name ?? "this add-on"} start?`,
      body: (
        <>
          <p>You're agreeing that it may:</p>
          <AccessList access={extension.access} />
          <Warnings warnings={extension.risks} title="The risk in approving this" open />
          <p>It starts running straight away and keeps this access until you remove it. If it ever asks for more, it stops and waits for you again.</p>
        </>
      ),
      action: "Approve and start",
    });
    if (!ok) return;
    try {
      const approved = await api.approveExtension(extension.source, extension.access);
      toast.success("Approved. It's starting.");
      if (approved.webhook) setAddress(approved.webhook);
      onApproved?.();
    } catch (e) {
      toast.error("Couldn't approve it", { description: messageOf(e, "Try again.") });
    }
  };
  const remove = async () => {
    const ok = await confirm({
      title: `Remove ${extension.name ?? extension.source}?`,
      body: <p>It stops running and its saved settings (such as keys) are deleted. Automations that use it will stop working until you add it again.</p>,
      action: "Remove",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.removeExtension(extension.source);
      onRemoved();
    } catch (e) {
      toast.error("Couldn't remove it", { description: messageOf(e, "Try again.") });
    }
  };

  const stop = async () => {
    const ok = await confirm({
      title: `Stop ${extension.name ?? "this add-on"}?`,
      body: <p>It stops now and stays stopped until you approve it again. Anything it runs, such as heating, won't be managed until then.</p>,
      action: "Stop it",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.stopExtension(extension.source);
      toast.success("Stopped.");
      refresh();
    } catch (e) {
      fail("Couldn't stop it")(e);
    }
  };

  const newAddress = async () => {
    const ok = await confirm({
      title: "Make a new webhook address?",
      body: <p>The old address stops working straight away. Whatever sends messages to it will need the new one.</p>,
      action: "Make a new address",
      danger: true,
    });
    if (!ok) return;
    try {
      setAddress((await api.newWebhookAddress(extension.source)).webhook);
    } catch (e) {
      fail("Couldn't make a new address")(e);
    }
  };

  const marketUpdate = async () => {
    if (!listing) return;
    setBusy(true);
    try {
      const found = await api.inspectMarketEntry(listing);
      setFromMarket(true);
      setCandidate(found);
    } catch (err) {
      fail("Couldn't get the update")(err);
    } finally {
      setBusy(false);
    }
  };

  const newVersion = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // so choosing the same file again still counts as a change
    if (!file) return;
    setBusy(true);
    try {
      setFromMarket(false);
      setCandidate(await api.uploadExtension(file));
    } catch (err) {
      fail("Couldn't replace it")(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-3 text-sm">
      {editing && <EditSettings extension={extension} onClose={() => setEditing(false)} onSaved={refresh} />}
      {address && <WebhookAddress path={address} onClose={() => setAddress(null)} />}
      {choosing && <GithubVersion source={extension.source} onClose={() => setChoosing(false)} onFound={(found) => { setChoosing(false); setCandidate(found); }} />}
      {candidate && (
        <UpdateDialog
          title={fromMarket
            ? `Update ${extension.name ?? "it"} to ${candidate.version ?? "?"}?`
            : `Replace ${extension.name ?? "it"} ${extension.version ?? ""} with version ${candidate.version ?? "?"}?`.replace("  ", " ")}
          action={fromMarket ? "Update" : "Replace"}
          source={extension.source}
          found={candidate}
          currentAccess={extension.access}
          onClose={() => setCandidate(null)}
          onDone={(waits) => {
            const done = fromMarket ? "Updated" : "Replaced";
            toast.success(waits ? `${done}. It's waiting for your OK.` : `${done}. It's restarting.`);
            refresh();
          }}
        />
      )}
      {extension.revoked && (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>The marketplace {extension.revoked.state === "withdrawn" ? "withdrew" : "suspended"} this version</AlertTitle>
          <AlertDescription className="space-y-2">
            <p className="break-words">Its reason: {extension.revoked.reason}</p>
            <p>It's still running, because stopping it could leave something like your heating unmanaged, and nothing outside your home should be able to switch your home off. It's your decision: you can stop it or remove it.</p>
            {manage && !extension.needsApproval && <Button size="sm" variant="outline" className="min-h-11 sm:min-h-9" onClick={() => void stop()}>Stop it</Button>}
          </AlertDescription>
        </Alert>
      )}

      {extension.marketUpdate && (
        <Alert>
          <ArrowUpCircle aria-hidden />
          <AlertTitle>Version {extension.marketUpdate.version} is available</AlertTitle>
          <AlertDescription className="space-y-2">
            <UpgradeNotes steps={extension.marketUpdate.steps} />
            {manage && (
              <Button size="sm" className="min-h-11 sm:min-h-9" disabled={busy} onClick={() => void marketUpdate()}>
                {busy ? "Checking…" : `Update to ${extension.marketUpdate.version}`}
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )}

      {extension.source.startsWith("github:") && <Warnings warnings={[GITHUB_RISK]} title="Not verified" />}

      {extension.violations?.length ? (
        <Alert variant="destructive">
          <Ban aria-hidden />
          <AlertTitle>Blocked {extension.violations.length} {extension.violations.length === 1 ? "attempt" : "attempts"}</AlertTitle>
          <AlertDescription className="space-y-1">
            <p>It tried to do something it wasn't allowed to. Nothing got through. If you don't recognise this, remove it.</p>
            <ul className="space-y-1 text-xs">
              {extension.violations.slice(0, 3).map((v) => (
                <li key={`${v.time}${v.kind}${v.detail}`} className="break-words">
                  <span className="tabular-nums">{new Date(v.time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span> {v.kind}: <span className="font-mono">{v.detail}</span>
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      ) : null}

      {extension.needsApproval && (
        <Alert variant="warning">
          <ShieldCheck aria-hidden />
          <AlertTitle>Waiting for your OK</AlertTitle>
          <AlertDescription className="space-y-2">
            <p>It isn't running. It's new, or it changed what it asks for, and nothing starts until you agree to the access below.</p>
            {manage
              ? <Button size="sm" className="min-h-11 sm:min-h-9" onClick={() => void approve()}>Approve access</Button>
              : <p className="font-medium">Only an administrator can approve it.</p>}
          </AlertDescription>
        </Alert>
      )}

      {/* Open while it waits for an OK, which is when they matter; folded once it's running. */}
      <Warnings warnings={extension.risks} title="The risk in approving this" open={extension.needsApproval} />
      <Warnings warnings={extension.warnings} open={extension.needsApproval} />

      <div className="space-y-1.5">
        <p className="font-medium">What it can reach</p>
        <AccessList access={extension.access} />
      </div>

      {extension.activity?.length ? (
        <details>
          <summary className="cursor-pointer font-medium">Where it has connected ({extension.activity.length})</summary>
          <ul className="mt-1.5 space-y-1 text-xs text-muted-foreground">
            {extension.activity.map((a) => (
              <li key={`${a.kind}${a.destination}`} className="break-words">
                <span className="font-mono">{a.kind} {a.destination}</span> · <span className="tabular-nums">{a.count}</span> {a.count === 1 ? "time" : "times"}, last {new Date(a.last).toLocaleString([], { dateStyle: "short", timeStyle: "short" })}
              </li>
            ))}
          </ul>
        </details>
      ) : null}

      <SettingsSummary settings={extension.settings} />

      {extension.error && (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>This has a problem</AlertTitle>
          <AlertDescription className="break-words font-mono text-xs">{extension.error}</AlertDescription>
        </Alert>
      )}

      <p className="break-all font-mono text-xs text-muted-foreground">{describeSource(extension.source)}{extension.version && ` · ${extension.version}`}</p>

      {listing && (
        <p>
          <a
            className="inline-flex min-h-11 items-center underline sm:min-h-0"
            href={`/marketplace/${encodeURIComponent(listing)}`}
            onClick={(e) => { e.preventDefault(); navigate("marketplace", listing); }}
          >
            See in the marketplace
          </a>
        </p>
      )}

      {manage && (
        <div className="flex flex-wrap gap-2">
          {extension.settings.length > 0 && (
            <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" onClick={() => setEditing(true)}>
              <Pencil aria-hidden />Edit settings
            </Button>
          )}
          <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" disabled={busy} onClick={() => picker.current?.click()}>
            <Upload aria-hidden />{busy ? "Working…" : "Upload new version"}
          </Button>
          {extension.source.startsWith("github:") && (
            <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" onClick={() => setChoosing(true)}>
              <ArrowUpCircle aria-hidden />Update from GitHub
            </Button>
          )}
          <input ref={picker} type="file" hidden tabIndex={-1} accept=".zip,.tar.gz,.tgz,.ts" aria-label="File with the new version" onChange={(e) => void newVersion(e)} />
          {extension.provides.includes("webhooks") && !extension.needsApproval && (
            <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" onClick={() => void newAddress()}>
              <KeyRound aria-hidden />New webhook address
            </Button>
          )}
        </div>
      )}
      {manage && (
        <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" onClick={() => void remove()}>
          <Trash2 aria-hidden />Remove
        </Button>
      )}
    </div>
  );
}
