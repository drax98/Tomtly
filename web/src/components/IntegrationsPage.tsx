import { useCallback, useState } from "react";
import { Plug } from "lucide-react";
import { api, type Integration } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { AddExtension } from "@/components/AddExtension";
import { ExtensionState, UpdateBadge } from "@/components/ExtensionParts";
import { IntegrationPage } from "@/components/IntegrationPage";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { useAuth } from "@/lib/auth";
import { usePoll } from "@/lib/useDevices";
import type { Page } from "@/lib/route";
import { cn } from "@/lib/utils";

/** A summary: how it stands and whether an update waits. Its settings, versions and buttons are on its own page. */
function IntegrationCard({ integration, onOpen }: { integration: Integration; onOpen: () => void }) {
  const { name, description, devices, extension } = integration;
  const online = devices.filter((d) => d.online).length;
  const attention = extension && (extension.error || extension.needsApproval || extension.violations?.length);
  const openable = Boolean(extension?.id);
  return (
    // The whole card opens its page; the Open button is there for the keyboard, and its click reaches the card.
    <Card className={cn("gap-3", attention && "border-warn/50", openable && "cursor-pointer transition-colors hover:border-primary/40")} onClick={openable ? onOpen : undefined}>
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <CardTitle className="text-base font-medium">{name}</CardTitle>
          <div className="flex flex-wrap justify-end gap-1.5">
            <UpdateBadge extension={extension} />
            <ExtensionState extension={extension} />
          </div>
        </div>
        {description && <CardDescription>{description}</CardDescription>}
        {extension?.version && <p className="text-xs text-muted-foreground tabular-nums">Version {extension.version}</p>}
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm">
          {devices.length === 0 ? "No devices from this yet" : (
            <><span className={cn("mr-2 inline-block size-2 rounded-full", online === devices.length ? "bg-good" : "bg-warn")} aria-hidden /><span className="tabular-nums">{online} of {devices.length}</span> online</>
          )}
        </p>
        <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" disabled={!openable}>Open</Button>
      </CardContent>
    </Card>
  );
}

/** The list of integrations, or the one named in the address. */
export function IntegrationsPage({ open = "", go }: { open?: string; go: (page: Page, sub?: string) => void }) {
  return open ? <IntegrationPage id={open} back={() => go("integrations")} /> : <IntegrationList go={go} />;
}

function IntegrationList({ go }: { go: (page: Page, sub?: string) => void }) {
  const { can } = useAuth();
  const [integrations, setIntegrations] = useState<Integration[] | null>(null);
  const refresh = useCallback(async () => {
    try {
      setIntegrations(await api.integrations());
    } catch {
      // The header already says when the service is unreachable.
    }
  }, []);
  usePoll(refresh);

  return (
    <>
      <PageHeader title="Integrations" description="The brands and apps your devices come from, like Smart Life or Xiaomi Home. Each one shows whether it's healthy and what it's allowed to reach." actions={can("extensions.manage") && <AddExtension what="integration" onAdded={() => void refresh()} />} />
      {integrations === null ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"><Skeleton className="h-48 rounded-xl" /><Skeleton className="h-48 rounded-xl" /></div>
      ) : integrations.length === 0 ? (
        <EmptyState icon={<Plug />} title="No integrations yet">Add one with the button at the top of this page.</EmptyState>
      ) : (
        <div className="grid items-start gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {integrations.map((integration) => (
            <IntegrationCard key={integration.type} integration={integration} onOpen={() => go("integrations", integration.type)} />
          ))}
        </div>
      )}
      {!can("extensions.manage") && <p className="rounded-lg bg-muted px-3 py-2 text-sm text-muted-foreground">Adding or removing integrations needs an administrator.</p>}
    </>
  );
}
