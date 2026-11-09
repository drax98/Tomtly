import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ArrowLeft, CircleCheck, ExternalLink, RefreshCw, ShieldAlert, Store, TriangleAlert, WifiOff } from "lucide-react";
import { toast } from "sonner";
import { api, type ExtensionStatus, type Marketplace, type MarketEntry } from "@/api";
import { WebhookAddress } from "@/components/ExtensionEdit";
import { AccessList, UpgradeNotes } from "@/components/ExtensionParts";
import { defaults, ExtensionReview } from "@/components/ExtensionReview";
import { ListingIcon, MarketplaceDetail } from "@/components/MarketplaceDetail";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { categoryName, TagList } from "@/components/MarketplaceWords";
import { UpdateDialog } from "@/components/UpdateDialog";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useAuth } from "@/lib/auth";
import { navigate, type Page } from "@/lib/route";
import { cn, messageOf, shortDate } from "@/lib/utils";
import { useLoad } from "@/lib/useLoad";

const KIND = { integration: "Integration", plugin: "Plugin", both: "Integration and plugin" } as const;
const kindText = (entry: MarketEntry) => KIND[entry.kind] + (entry.provides.includes("widgets") ? ", with widgets for Home" : "");

const approvedOn = (value: string) => shortDate(value, value);

/** Where something that was just installed shows up. */
const whereItAppears = (provides: string[]): { page: Page; title: string } =>
  provides.includes("devices") ? { page: "integrations", title: "Integrations" } : { page: "plugins", title: "Plugins" };

/** What an update will ask for, by name once its download has told us the labels, and before that by count. */
const updateNeeds = (entry: MarketEntry, labels: Record<string, string> = {}) => {
  const keys = [...new Set((entry.upgradeSteps ?? []).flatMap((step) => step.ask ?? []))];
  if (keys.length === 0) return null;
  if (keys.every((key) => labels[key])) return `This update needs: ${keys.map((key) => labels[key]).join(", ")}`;
  return `This update needs ${keys.length === 1 ? "1 setting" : `${keys.length} settings`} from you.`;
};

/** Downloads and checks one entry, shows what it asks for, and adds it only when the person agrees to that access. */
function InstallDialog({ entry, onClose, onInstalled }: { entry: MarketEntry; onClose: () => void; onInstalled: () => void }) {
  const [found, setFound] = useState<ExtensionStatus | null>(null);
  const [settings, setSettings] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [added, setAdded] = useState<ExtensionStatus | null>(null);
  const [address, setAddress] = useState<string | null>(null);

  useEffect(() => {
    let stale = false;
    api.inspectMarketEntry(entry.id).then(
      (result) => {
        if (stale) return;
        setFound(result);
        setSettings(defaults(result.settings));
      },
      (e) => !stale && setError(messageOf(e, "That didn't work")),
    );
    return () => { stale = true; };
  }, [entry.id]);

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (!found) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.addExtension(found.source, settings, found.access);
      toast.success(`${found.name} added`);
      setAdded(result);
      if (result.webhook) setAddress(result.webhook);
      onInstalled();
    } catch (err) {
      setError(messageOf(err, "That didn't work"));
    } finally {
      setBusy(false);
    }
  };

  const where = whereItAppears(found?.provides ?? entry.provides);
  return (
    <>
      {address && <WebhookAddress path={address} onClose={() => setAddress(null)} />}
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{added ? `${entry.name} is installed` : `Add ${entry.name}`}</DialogTitle>
            <DialogDescription>
              {added ? "It's running with the access you allowed." : "From the marketplace. Nothing runs until you agree to the access below."}
            </DialogDescription>
          </DialogHeader>
          {added ? (
            <div className="space-y-3 text-sm">
              <p className="flex items-center gap-2"><CircleCheck className="size-4 text-good" aria-hidden />It now appears under {where.title}.</p>
              {(found?.provides ?? entry.provides).includes("widgets") && <p>Put its widgets on Home with <strong>Add widgets</strong>.</p>}
              <div className="flex flex-wrap gap-2">
                <Button asChild><a href={`/${where.page}`} onClick={(e) => { e.preventDefault(); onClose(); navigate(where.page); }}>Go to {where.title}</a></Button>
                <Button variant="outline" onClick={onClose}>Stay here</Button>
              </div>
            </div>
          ) : (
            <>
              {!found && !error && <div role="status" aria-label="Downloading and checking"><Skeleton className="h-32 w-full rounded-lg" /><p className="mt-2 text-xs text-muted-foreground">Downloading and checking it…</p></div>}
              {found && <ExtensionReview found={found} settings={settings} onSettings={setSettings} busy={busy} onSubmit={(e) => void add(e)} />}
              {error && (
                <Alert variant="destructive">
                  <TriangleAlert aria-hidden />
                  <AlertTitle>{found ? "That didn't work. Try again." : "It can't be installed."}</AlertTitle>
                  <AlertDescription className="break-words text-xs">{error}</AlertDescription>
                </Alert>
              )}
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

type EntryProps = { entry: MarketEntry; labels?: Record<string, string>; canInstall: boolean; busy: boolean; onInstall: () => void; onUpdate: () => void; onTag: (tag: string) => void };

const pill = "min-h-11 rounded-full px-5 font-semibold sm:min-h-8";

/** The one thing to do with an entry, as a pill like a store's Get button. large is the listing's own page. */
function EntryAction({ entry, canInstall, busy, onInstall, onUpdate, large = false }: EntryProps & { large?: boolean }) {
  const status = cn("inline-flex min-h-8 items-center gap-1 rounded-full bg-muted px-3 text-xs font-medium text-muted-foreground", large && "min-h-9 text-sm");
  if (entry.updateAvailable) {
    return (
      <Button size="sm" variant={large ? "default" : "secondary"} className={cn(pill, !large && "text-primary")} disabled={!canInstall || busy} onClick={onUpdate} aria-label={busy ? undefined : `Update to ${entry.version}`}>
        {busy ? "Checking…" : "Update"}
      </Button>
    );
  }
  if (entry.installed) return <span className={status}><CircleCheck aria-hidden className="size-3.5" />Installed{entry.installedVersion ? ` · ${entry.installedVersion}` : ""}</span>;
  if (entry.differentCopy) {
    // Not a button: nothing to press. Focusable so the keyboard can reach the tooltip too.
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <span tabIndex={0} className={cn(buttonVariants({ size: "sm", variant: "secondary" }), pill, "cursor-not-allowed text-muted-foreground opacity-60 shadow-none")}>
            <span aria-hidden>Install</span><span className="sr-only">A different copy is installed</span>
          </span>
        </TooltipTrigger>
        <TooltipContent>A different copy is installed</TooltipContent>
      </Tooltip>
    );
  }
  if (entry.suspended) return null;
  return (
    <Button size="sm" variant={large ? "default" : "secondary"} className={cn(pill, !large && "text-primary")} disabled={!canInstall} onClick={onInstall} aria-label={`Install ${entry.name}`}>
      Install
    </Button>
  );
}

/** A label over a value, like the strip under a store listing's title. */
function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[8rem_1fr] gap-3 border-t py-3 first:border-t-0 sm:grid-cols-[12rem_1fr]">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

/** What can be checked about an entry before installing it, on its own page. */
function EntryInfo({ entry, labels, onTag }: Pick<EntryProps, "entry" | "labels" | "onTag">) {
  const needs = entry.updateAvailable ? updateNeeds(entry, labels) : null;
  const link = "break-all text-primary underline-offset-4 hover:underline";
  return (
    <>
      {entry.suspended && (
        <Alert variant="destructive">
          <ShieldAlert aria-hidden />
          <AlertTitle>Suspended for now</AlertTitle>
          <AlertDescription className="break-words text-sm">It can't be installed while this is looked into: {entry.suspended}</AlertDescription>
        </Alert>
      )}
      {entry.updateAvailable && (
        <section className="space-y-2" aria-label="What's new">
          <h2 className="text-lg font-semibold">What's new</h2>
          <p className="text-sm text-muted-foreground">Version {entry.version}{entry.installedVersion ? `, you have ${entry.installedVersion}` : ""}</p>
          {needs && <p className="font-medium">{needs}</p>}
          <UpgradeNotes steps={entry.upgradeSteps} />
        </section>
      )}
      <section className="space-y-2" aria-label="Information">
        <h2 className="text-lg font-semibold">Information</h2>
        <dl>
          <Fact label="Provider">{entry.author}</Fact>
          <Fact label="Version">{entry.version}</Fact>
          {entry.license && <Fact label="License">{entry.license}</Fact>}
          <Fact label="Kind">{kindText(entry)}</Fact>
          {(entry.categories?.length ?? 0) > 0 && <Fact label="Category">{entry.categories!.map(categoryName).join(", ")}</Fact>}
          <Fact label="Tested on">{entry.testedOn}</Fact>
          <Fact label="Approved">{approvedOn(entry.approvedOn)}</Fact>
          <Fact label="It needs this access"><AccessList access={entry.access} /></Fact>
          <Fact label="Verified package"><span className="font-mono text-muted-foreground" title={entry.sha256}>{entry.sha256.slice(0, 12)}…</span></Fact>
          {entry.homepage && /^https?:\/\//.test(entry.homepage) && (
            <Fact label="Website"><a className={link} href={entry.homepage} target="_blank" rel="noreferrer noopener">{entry.homepage}</a></Fact>
          )}
          {entry.repository && /^https?:\/\//.test(entry.repository) && (
            <Fact label="Source">
              <span className="block text-xs text-muted-foreground">The author's own link, not what was verified:</span>
              <a className={link} href={entry.repository} target="_blank" rel="noreferrer noopener">{entry.repository}</a>
            </Fact>
          )}
          {(entry.tags?.length ?? 0) > 0 && <Fact label="Tags"><TagList tags={entry.tags} onTag={onTag} /></Fact>}
        </dl>
      </section>
    </>
  );
}

/** The marketplace answered, but something stops it being browsed. null when there is a list to show. */
function unavailable(data: Marketplace | null, error: string | null, retry: ReactNode): ReactNode | null {
  if (error) return <EmptyState icon={<WifiOff />} title="Can't reach Home Control" action={retry}>Check that the computer running it is on, then try again.</EmptyState>;
  if (!data) return <div className="grid gap-x-8 gap-y-4 md:grid-cols-2 xl:grid-cols-3">{[1, 2, 3].map((n) => <Skeleton key={n} className="h-24 rounded-xl" />)}</div>;
  if (!data.configured) return <EmptyState icon={<Store />} title="The marketplace isn't set up">To browse it, set MARKETPLACE_URL, MARKETPLACE_PUBLIC_KEY and MARKETPLACE_APPROVAL_PUBLIC_KEYS for Home Control and restart it. You can still add things yourself on the Integrations and Plugins pages.</EmptyState>;
  if (!data.reachable) return <EmptyState icon={<WifiOff />} title="Can't reach the marketplace" action={retry}>It may be down, or this computer may be offline. What you've already installed keeps working. Try again in a little while.</EmptyState>;
  if (!data.verified) {
    return (
      <EmptyState icon={<ShieldAlert />} title="Can't verify the marketplace" action={retry}>
        {data.error && <span className="mb-2 block break-words first-letter:uppercase">{data.error}.</span>}
        Its list didn't pass the safety check, so nothing is offered and nothing was downloaded. What you've already installed isn't affected. If this keeps happening, check that the marketplace keys are the right ones.
      </EmptyState>
    );
  }
  return null;
}

/** Installing and updating, the same from the list and from a listing's own page. */
function useEntryActions(reload: () => void, onTag: (tag: string) => void) {
  const { can } = useAuth();
  const canInstall = can("extensions.manage");
  const [installing, setInstalling] = useState<MarketEntry | null>(null);
  const [updating, setUpdating] = useState<string | null>(null);
  const [candidate, setCandidate] = useState<{ entry: MarketEntry; found: ExtensionStatus; currentAccess: string[] } | null>(null);
  const [labels, setLabels] = useState<Record<string, Record<string, string>>>({});

  const update = async (entry: MarketEntry) => {
    if (!entry.installedSource) return;
    setUpdating(entry.id);
    try {
      const found = await api.inspectMarketEntry(entry.id);
      const current = (await api.extensions()).find((x) => x.source === entry.installedSource);
      setLabels((known) => ({ ...known, [entry.id]: Object.fromEntries((found.upgrade?.ask ?? []).map((s) => [s.key, s.label])) }));
      setCandidate({ entry, found, currentAccess: current?.access ?? [] });
    } catch (e) {
      toast.error("Couldn't update it", { description: messageOf(e, "Try again.") });
    } finally {
      setUpdating(null);
    }
  };

  const props = (entry: MarketEntry): EntryProps => ({ entry, labels: labels[entry.id], canInstall, busy: updating === entry.id, onInstall: () => setInstalling(entry), onUpdate: () => void update(entry), onTag });
  const dialogs = (
    <>
      {candidate?.entry.installedSource && (
        <UpdateDialog
          title={`Update ${candidate.entry.name} to ${candidate.found.version ?? candidate.entry.version}?`}
          action="Update"
          source={candidate.entry.installedSource}
          found={candidate.found}
          currentAccess={candidate.currentAccess}
          onClose={() => setCandidate(null)}
          onDone={(waits) => {
            toast.success(waits ? "Updated. It's waiting for your OK." : "Updated. It's restarting.");
            reload();
          }}
        />
      )}
      {installing && <InstallDialog entry={installing} onClose={() => setInstalling(null)} onInstalled={reload} />}
    </>
  );
  return { canInstall, props, dialogs };
}

function retryButton(reload: () => void) {
  return <Button variant="outline" className="min-h-11" onClick={reload}><RefreshCw aria-hidden />Try again</Button>;
}

/** One listing on its own page, from the address. */
function ListingPage({ id }: { id: string }) {
  const { data, error, reload } = useLoad(api.marketplace);
  // A tag leads back to the list, on the tab the listing is on.
  const { canInstall, props, dialogs } = useEntryActions(() => void reload(), (tag) => openSite(data?.site, `?q=${encodeURIComponent(tag)}`));
  // The list can be scrolled far down when one of its entries is opened.
  const top = useRef<HTMLParagraphElement>(null);
  useEffect(() => { top.current?.scrollIntoView?.({ block: "start" }); }, [id]);
  const entry = data?.entries.find((e) => e.id === id);
  return (
    <>
      <p ref={top}>
        <a className="inline-flex min-h-11 items-center gap-1 text-sm underline sm:min-h-0" href="/marketplace" onClick={(e) => { e.preventDefault(); navigate("marketplace"); }}>
          <ArrowLeft aria-hidden className="size-4" />Marketplace
        </a>
      </p>
      {unavailable(data, error, retryButton(() => void reload())) ?? (entry
        ? <MarketplaceDetail key={entry.id} entry={entry} canReview={canInstall} onPosted={() => void reload()} action={<EntryAction {...props(entry)} large />}><EntryInfo {...props(entry)} /></MarketplaceDetail>
        : <EmptyState icon={<Store />} title="Not in the marketplace any more">It may have been withdrawn. Anything you've already installed keeps working.</EmptyState>)}
      {dialogs}
    </>
  );
}

/** The marketplace's website, where people browse; Get there opens a listing here. */
const openSite = (site: string | undefined, search = "") => {
  if (site) window.open(`${site}/${search}`, "_blank", "noopener,noreferrer");
};

/** Landing is what Marketplace shows without a listing: the way to the website, and updates for what's installed. */
function Landing() {
  const { data, error, reload } = useLoad(api.marketplace);
  const updates = data?.entries.filter((e) => e.updateAvailable) ?? [];
  return unavailable(data, error, retryButton(() => void reload())) ?? (data ? (
    <>
      <section className="space-y-3 rounded-xl border bg-card p-5">
        <h2 className="text-lg font-semibold">Find integrations and plugins</h2>
        <p className="text-sm text-muted-foreground">
          Browse the marketplace's website, read each one's code, and choose <strong className="text-foreground">Get</strong>. It comes back
          here, where you see what it can reach and install it.
        </p>
        {data.site && <Button className="min-h-11" onClick={() => openSite(data.site)}><ExternalLink aria-hidden />Open the marketplace</Button>}
      </section>
      {updates.length > 0 && (
        <section className="space-y-2" aria-label="Updates">
          <h2 className="text-lg font-semibold">Updates for what you have</h2>
          <ul className="divide-y rounded-xl border">
            {updates.map((e) => (
              <li key={e.id} className="flex items-center gap-3 px-4 py-3">
                <ListingIcon entry={e} className="size-10 rounded-[22%] ring-1 ring-border" />
                <a className="min-w-0 flex-1 truncate font-medium underline-offset-4 hover:underline" href={`/marketplace/${encodeURIComponent(e.id)}`} onClick={(ev) => { ev.preventDefault(); navigate("marketplace", e.id); }}>{e.name}</a>
                <span className="text-sm text-muted-foreground">{e.installedVersion} → {e.version}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  ) : <Skeleton className="h-32 rounded-xl" />);
}

/** open is the listing shown on its own, from the address. */
export function MarketplacePage({ open = "" }: { open?: string }) {
  const { can } = useAuth();
  return (
    <>
      <PageHeader title="Marketplace" description="Integrations and plugins whose exact package someone checked and approved. Each one still asks you before it can reach anything." />
      {!can("extensions.manage") && <p className="rounded-lg bg-muted px-3 py-2 text-sm text-muted-foreground">Installing needs an administrator. You can look around.</p>}
      {open ? <ListingPage id={open} /> : <Landing />}
    </>
  );
}
