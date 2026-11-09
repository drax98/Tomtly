import { useEffect } from "react";
import { WifiOff } from "lucide-react";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { AppShell } from "@/components/AppShell";
import { AutomationsPage } from "@/components/AutomationsPage";
import { ConfirmProvider } from "@/components/ConfirmDialog";
import { DevicesPage } from "@/components/DevicesPage";
import { RoomsPage } from "@/components/RoomsPage";
import { HomePage } from "@/components/HomePage";
import { IntegrationsPage } from "@/components/IntegrationsPage";
import { MarketplacePage } from "@/components/MarketplacePage";
import { PageBody } from "@/components/PageHeader";
import { PluginsPage } from "@/components/PluginsPage";
import { SettingsPage } from "@/components/SettingsPage";
import { Welcome } from "@/components/Welcome";
import { AuditPage } from "@/components/auth/AuditPage";
import { AuthLayout } from "@/components/auth/AuthLayout";
import { LoginScreen } from "@/components/auth/LoginScreen";
import { SetupScreen } from "@/components/auth/SetupScreen";
import { TokensPage } from "@/components/auth/TokensPage";
import { UsersPage } from "@/components/auth/UsersPage";
import { AuthProvider, useAuth } from "@/lib/auth";
import { useRoute } from "@/lib/route";
import { useDevices } from "@/lib/useDevices";

function Connection({ online, total, reachable }: { online: number; total: number | null; reachable: boolean }) {
  const dot = (colour: string) => <span aria-hidden className={`mr-2 inline-block size-2 rounded-full ${colour}`} />;
  return (
    <p role="status" className="flex items-center whitespace-nowrap text-muted-foreground">
      {!reachable ? <>{dot("bg-warn")}<span className="font-medium text-warn">Can't reach the home</span></>
        : total === null ? <>{dot("bg-offline")}Connecting…</>
          : <>{dot("bg-good")}<span><span className="hidden sm:inline">Connected · </span><span className="tabular-nums">{online} of {total}</span> online</span></>}
    </p>
  );
}

function Home() {
  const { devices, rooms, reachable, write, reload } = useDevices();
  const { can, welcome, setWelcome } = useAuth();
  const [route, go] = useRoute();
  const online = devices?.filter((d) => d.online).length ?? 0;

  // The page can't be reached without a permission its API needs; send people to Devices instead of a wall of errors.
  useEffect(() => {
    if ((route.page === "users" && !can("users.manage")) || (route.page === "audit" && !can("audit.read")) || (route.page === "settings" && !can("settings.manage"))) go("home");
  }, [route.page, can, go]);

  return (
    <AppShell page={route.page} go={go} connection={<Connection online={online} total={devices?.length ?? null} reachable={reachable} />}>
      {route.page === "home" && <PageBody><HomePage devices={devices} rooms={rooms} write={write} /></PageBody>}
      {route.page === "devices" && <PageBody><DevicesPage devices={devices} rooms={rooms} reachable={reachable} write={write} onEdited={reload} /></PageBody>}
      {route.page === "rooms" && <PageBody><RoomsPage devices={devices} rooms={rooms} onChanged={reload} /></PageBody>}
      {route.page === "automations" && <AutomationsPage routeName={route.sub} go={go} />}
      {route.page === "integrations" && <PageBody><IntegrationsPage open={route.sub} go={go} /></PageBody>}
      {route.page === "plugins" && <PageBody><PluginsPage /></PageBody>}
      {route.page === "marketplace" && <PageBody><MarketplacePage open={route.sub} /></PageBody>}
      {route.page === "users" && can("users.manage") && <PageBody><UsersPage /></PageBody>}
      {route.page === "audit" && can("audit.read") && <PageBody><AuditPage /></PageBody>}
      {route.page === "tokens" && <PageBody><TokensPage /></PageBody>}
      {route.page === "settings" && can("settings.manage") && <PageBody><SettingsPage /></PageBody>}
      <Welcome open={welcome} onClose={() => setWelcome(false)} go={go} />
    </AppShell>
  );
}

function Gate() {
  const { status, expired, unreachable, refresh } = useAuth();
  if (!status) {
    return unreachable ? (
      <AuthLayout title="Can't reach Home Control" intro="The service doesn't answer. Check the computer running it is on, then try again.">
        <Button className="w-full" onClick={() => void refresh()}><WifiOff aria-hidden />Try again</Button>
      </AuthLayout>
    ) : (
      <div className="flex min-h-svh items-center justify-center" aria-busy="true" aria-label="Loading"><Skeleton className="h-40 w-full max-w-md rounded-2xl" /></div>
    );
  }
  if (status.setupRequired) return <SetupScreen />;
  if (!status.user) return <LoginScreen />;
  return (
    <>
      {/* After a lost session the page stays mounted (so an open script isn't lost) but can't be used until signing in. */}
      <div key={status.user.id} inert={expired} aria-hidden={expired}><Home /></div>
      {expired && <LoginScreen overlay />}
    </>
  );
}

export function App() {
  return (
    <AuthProvider>
      <ConfirmProvider>
        <TooltipProvider delayDuration={200}>
          <Gate />
          <Toaster />
        </TooltipProvider>
      </ConfirmProvider>
    </AuthProvider>
  );
}
