import { useCallback, useState } from "react";
import { Blocks, TriangleAlert } from "lucide-react";
import { api, type ExtensionStatus, type PluginStatus } from "@/api";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { AddExtension } from "@/components/AddExtension";
import { ExtensionDetails, ExtensionState } from "@/components/ExtensionParts";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { useAuth } from "@/lib/auth";
import { usePoll } from "@/lib/useDevices";
import { cn } from "@/lib/utils";

const updatedAt = (time?: string) => (time ? `Updated ${new Date(time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "No data yet");

function PluginCard({ plugin, extension, onChanged }: { plugin?: PluginStatus; extension?: ExtensionStatus; onChanged: () => void }) {
  const attention = extension && (extension.error || extension.needsApproval || extension.violations?.length);
  return (
    <Card className={cn("gap-3", attention && "border-warn/50")}>
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <CardTitle className="text-base font-medium">{extension?.name ?? plugin?.id ?? extension?.source}</CardTitle>
          <ExtensionState extension={extension} />
        </div>
        {extension?.description && <CardDescription>{extension.description}</CardDescription>}
      </CardHeader>
      <CardContent className="space-y-3">
        {plugin && extension?.provides.includes("data") !== false && <p className="text-sm text-muted-foreground">{updatedAt(plugin.updatedAt)}</p>}
        {extension ? (
          <ExtensionDetails extension={extension} onRemoved={onChanged} onApproved={onChanged} onChanged={onChanged} />
        ) : (
          <>
            {plugin?.error && (
              <Alert variant="destructive">
                <TriangleAlert aria-hidden />
                <AlertTitle>The last fetch failed</AlertTitle>
                <AlertDescription className="break-words font-mono text-xs">{plugin.error}</AlertDescription>
              </Alert>
            )}
          </>
        )}
        {plugin && (plugin.data !== undefined || plugin.actions?.length) && (
          <Collapsible>
            <CollapsibleTrigger asChild>
              <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9">Details for scripts</Button>
            </CollapsibleTrigger>
            <CollapsibleContent className="mt-2 space-y-2">
              <p className="font-mono text-xs">home.plugin("{plugin.id}")</p>
              {plugin.actions?.length ? <p className="font-mono text-xs text-muted-foreground">Actions: {plugin.actions.join(", ")}</p> : null}
              {plugin.data !== undefined && <pre className="max-h-80 overflow-auto rounded-md border p-2 font-mono text-xs">{JSON.stringify(plugin.data, null, 2)}</pre>}
            </CollapsibleContent>
          </Collapsible>
        )}
      </CardContent>
    </Card>
  );
}

export function PluginsPage() {
  const { can } = useAuth();
  const [plugins, setPlugins] = useState<PluginStatus[]>([]);
  const [extensions, setExtensions] = useState<ExtensionStatus[]>([]);
  const refresh = useCallback(async () => {
    try {
      const [automations, extensionList] = await Promise.all([api.automations(), api.extensions()]);
      setPlugins(automations.plugins ?? []);
      setExtensions(extensionList);
    } catch {
      // The header already says when the service is unreachable.
    }
  }, []);
  usePoll(refresh);

  // An extension that failed before it could say what it provides is shown here too, so its problem is visible.
  const dataExtensions = extensions.filter((e) => e.provides.includes("data") || e.provides.includes("actions") || e.provides.includes("widgets") || !e.id);
  const standalone = plugins.filter((p) => !dataExtensions.some((e) => e.id === p.id));
  const empty = dataExtensions.length === 0 && standalone.length === 0;

  return (
    <>
      <PageHeader title="Plugins" description="Add-ons that bring in outside information, like the weather, for your automations to use. They only reach what you approve." actions={can("extensions.manage") && <AddExtension what="plugin" onAdded={() => void refresh()} />} />
      {empty && <EmptyState icon={<Blocks />} title="No plugins yet">Add one with the button at the top of this page. You'll see exactly what it wants to reach before anything runs.</EmptyState>}
      <div className="grid items-start gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {dataExtensions.map((extension) => (
          <PluginCard key={extension.source} extension={extension} plugin={plugins.find((p) => p.id === extension.id)} onChanged={() => void refresh()} />
        ))}
        {standalone.map((plugin) => (
          <PluginCard key={plugin.id} plugin={plugin} onChanged={() => void refresh()} />
        ))}
      </div>
      {!can("extensions.manage") && <p className="rounded-lg bg-muted px-3 py-2 text-sm text-muted-foreground">Adding or removing plugins needs an administrator.</p>}
    </>
  );
}
