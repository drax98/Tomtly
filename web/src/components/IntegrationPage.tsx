import { useCallback, useEffect, useState } from "react";
import { ArrowLeft, Radar } from "lucide-react";
import { api, type Discovery, type FoundDevice, type Integration } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useConfirm } from "@/components/ConfirmDialog";
import { PageHeader } from "@/components/PageHeader";
import { useAuth } from "@/lib/auth";
import { usePoll } from "@/lib/useDevices";
import { cn, messageOf } from "@/lib/utils";

const row = "flex items-center justify-between gap-3 py-2";

function Found({ device, onRestore, busy }: { device: FoundDevice; onRestore: () => void; busy: boolean }) {
  const detail = [device.product, device.address, device.state === "removed" && !device.seen ? "not seen on the network just now" : ""].filter(Boolean).join(" · ");
  return (
    <li className={row}>
      <div className="min-w-0">
        <p className="truncate">{device.name}</p>
        {detail && <p className="truncate text-xs text-muted-foreground">{detail}</p>}
        {device.note && <p className="text-xs text-muted-foreground">{device.note}</p>}
      </div>
      {device.state === "removed" && <Button variant="outline" size="sm" className="min-h-11 shrink-0 sm:min-h-9" disabled={busy} onClick={onRestore}>Add back</Button>}
    </li>
  );
}

/** One integration: its devices, and when it's opened, what's on the network that isn't among them. */
export function IntegrationPage({ id, back }: { id: string; back: () => void }) {
  const { can } = useAuth();
  const confirm = useConfirm();
  const manage = can("extensions.manage");
  const [integration, setIntegration] = useState<Integration | null | undefined>(undefined);
  const [discovery, setDiscovery] = useState<Discovery | null>(null);
  const [scanning, setScanning] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    try {
      const list = await api.integrations();
      setIntegration(list.find((i) => i.type === id) ?? null);
    } catch {
      // The header already says when the service is unreachable.
    }
  }, [id]);
  usePoll(refresh);

  const scan = useCallback(async () => {
    setScanning(true);
    try {
      setDiscovery(await api.discoverDevices(id));
      setError("");
    } catch (e) {
      setError(messageOf(e, "The scan failed"));
    } finally {
      setScanning(false);
    }
  }, [id]);
  useEffect(() => {
    if (manage) void scan();
  }, [manage, scan]);

  const change = async (run: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await run();
      setError("");
      await Promise.all([refresh(), scan()]);
    } catch (e) {
      setError(messageOf(e, "That didn't work"));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (deviceId: string, name: string) => {
    const ok = await confirm({
      title: `Remove ${name}?`,
      body: "It leaves the Devices page and takes no more commands, and this integration stops contacting it where it can. Automations that use it will show an error until you add it back. Nothing changes on the device itself, and you can add it back from this page.",
      action: "Remove",
      danger: true,
    });
    if (ok) await change(() => api.removeDevice(deviceId));
  };

  if (integration === undefined) return <Skeleton className="h-48 rounded-xl" />;
  const title = integration?.name ?? id;
  const elsewhere = discovery?.devices.filter((d) => d.state !== "added") ?? [];
  const answered = discovery?.devices.filter((d) => d.state === "added").length ?? 0;

  return (
    <>
      <Button variant="ghost" size="sm" className="min-h-11 w-fit sm:min-h-9" onClick={back}><ArrowLeft aria-hidden />All integrations</Button>
      <PageHeader title={title} description={integration?.description} />
      {integration === null ? <p className="text-sm text-muted-foreground">This integration isn't installed.</p> : (
        <div className="grid items-start gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">Devices</CardTitle>
              <CardDescription>{integration.devices.length === 0 ? "None in the system right now." : `${integration.devices.length} in the system.`}</CardDescription>
            </CardHeader>
            {integration.devices.length > 0 && (
              <CardContent>
                <ul className="divide-y text-sm">
                  {integration.devices.map((device) => (
                    <li key={device.id} className={row}>
                      <span className="min-w-0 truncate">{device.name}</span>
                      <span className="flex shrink-0 items-center gap-3">
                        <span className={cn("text-xs", device.online ? "text-good" : "text-offline")}>{device.online ? "Online" : "Offline"}</span>
                        {manage && <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" disabled={busy} aria-label={`Remove ${device.name}`} onClick={() => void remove(device.id, device.name)}>Remove</Button>}
                      </span>
                    </li>
                  ))}
                </ul>
              </CardContent>
            )}
          </Card>
          {manage ? (
            <Card>
              <CardHeader>
                <div className="flex items-start justify-between gap-2">
                  <CardTitle className="text-base font-medium">Found on your network</CardTitle>
                  <Button variant="outline" size="sm" className="min-h-11 shrink-0 sm:min-h-9" disabled={scanning} onClick={() => void scan()}><Radar aria-hidden />{scanning ? "Scanning…" : "Scan again"}</Button>
                </div>
                <CardDescription>
                  {discovery === null ? "Looking on your network…" : !discovery.supported ? "This integration can't look on the network, so this only lists what you removed." : `${answered} of the devices above answered. A scan changes nothing on any device.`}
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-2">
                {discovery === null && scanning && <Skeleton className="h-16 rounded-lg" />}
                {(error || discovery?.error) && <p role="alert" className="rounded-lg bg-muted px-3 py-2 text-sm">{error || discovery?.error}</p>}
                {discovery !== null && elsewhere.length === 0 && <p className="text-sm text-muted-foreground">Nothing else found, and nothing removed.</p>}
                {elsewhere.length > 0 && (
                  <ul className="divide-y text-sm">
                    {elsewhere.map((device) => <Found key={device.id} device={device} busy={busy} onRestore={() => void change(() => api.restoreDevice(device.id))} />)}
                  </ul>
                )}
              </CardContent>
            </Card>
          ) : <p className="rounded-lg bg-muted px-3 py-2 text-sm text-muted-foreground">Removing devices or looking for new ones needs an administrator.</p>}
        </div>
      )}
    </>
  );
}
