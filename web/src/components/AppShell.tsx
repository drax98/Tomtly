import { useState, type ReactNode } from "react";
import { ChevronsUpDown, Cpu, DoorOpen, History, House, KeyRound, LayoutGrid, Lock, LogOut, Plug, ScrollText, Settings, Store, Users } from "lucide-react";
import { toast } from "sonner";
import { authApi, type Permission } from "@/authApi";
import { Avatar } from "@/components/Avatar";
import { ChangePasswordDialog } from "@/components/auth/ChangePasswordDialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import {
  Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel, SidebarHeader, SidebarInset, SidebarMenu,
  SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarTrigger, useSidebar,
} from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import { useAuth, useUser } from "@/lib/auth";
import { ROLE_INFO } from "@/lib/roles";
import type { Page } from "@/lib/route";

const NAV: { page: Page; title: string; icon: typeof House }[] = [
  { page: "home", title: "Home", icon: House },
  { page: "devices", title: "Devices", icon: LayoutGrid },
  { page: "rooms", title: "Rooms", icon: DoorOpen },
  { page: "automations", title: "Automations", icon: ScrollText },
  { page: "integrations", title: "Integrations", icon: Plug },
  { page: "plugins", title: "Plugins", icon: Cpu },
  { page: "marketplace", title: "Marketplace", icon: Store },
];
const ADMIN: typeof NAV = [
  { page: "users", title: "People", icon: Users },
  { page: "audit", title: "Activity log", icon: History },
  { page: "settings", title: "Settings", icon: Settings },
];

const ADMIN_PERMISSION: Partial<Record<Page, Permission>> = { users: "users.manage", audit: "audit.read", settings: "settings.manage" };

export const PAGE_TITLES: Record<Page, string> = {
  home: "Home", devices: "Devices", rooms: "Rooms", automations: "Automations", integrations: "Integrations", plugins: "Plugins", marketplace: "Marketplace", users: "People", audit: "Activity log", tokens: "API tokens", settings: "Settings",
};

function UserMenu({ page, go }: { page: Page; go: (p: Page) => void }) {
  const user = useUser();
  const { signedOut } = useAuth();
  const { isMobile } = useSidebar();
  const [changing, setChanging] = useState(false);

  const logout = async () => {
    try {
      await authApi.logout();
    } catch {
      // The session may already be gone; either way this screen should be signed out.
    }
    signedOut();
    toast("You're signed out");
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <SidebarMenuButton size="lg" aria-label={`Account menu for ${user.username}`} className="min-h-12 data-[state=open]:bg-sidebar-accent">
            <Avatar name={user.username} />
            <span className="grid min-w-0 flex-1 text-left leading-tight">
              <span className="truncate text-sm font-medium">{user.username}</span>
              <span className="truncate text-xs text-muted-foreground">{ROLE_INFO[user.role].name}</span>
            </span>
            <ChevronsUpDown className="ml-auto size-4 text-muted-foreground" aria-hidden />
          </SidebarMenuButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent side={isMobile ? "top" : "right"} align="end" className="w-64">
          <DropdownMenuLabel className="space-y-0.5 font-normal">
            <span className="block text-sm font-medium">{user.username}</span>
            <span className="block text-xs text-muted-foreground">{ROLE_INFO[user.role].summary}</span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem className="min-h-11" onSelect={() => setChanging(true)}><Lock aria-hidden />Change password</DropdownMenuItem>
          <DropdownMenuItem className="min-h-11" onSelect={() => go("tokens")} data-active={page === "tokens"}><KeyRound aria-hidden />API tokens</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem className="min-h-11" onSelect={() => void logout()}><LogOut aria-hidden />Sign out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ChangePasswordDialog open={changing} onOpenChange={setChanging} />
    </>
  );
}

function NavList({ items, page, go, label }: { items: typeof NAV; page: Page; go: (p: Page) => void; label?: string }) {
  const { isMobile, setOpenMobile } = useSidebar();
  return (
    <SidebarGroup>
      {label && <SidebarGroupLabel>{label}</SidebarGroupLabel>}
      <SidebarGroupContent>
        <SidebarMenu>
          {items.map(({ page: target, title, icon: Icon }) => (
            <SidebarMenuItem key={target}>
              <SidebarMenuButton
                asChild
                isActive={page === target}
                className="min-h-11 text-[0.95rem] data-[active=true]:font-semibold"
              >
                <a
                  href={`/${target}`}
                  aria-current={page === target ? "page" : undefined}
                  onClick={(e) => {
                    e.preventDefault();
                    go(target);
                    if (isMobile) setOpenMobile(false);
                  }}
                >
                  <Icon aria-hidden />{title}
                </a>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}

/** Where the page is, who is signed in, and whether the home answers. */
export function AppShell({ page, go, connection, children }: { page: Page; go: (p: Page) => void; connection: ReactNode; children: ReactNode }) {
  const { can } = useAuth();
  const admin = ADMIN.filter((i) => can(ADMIN_PERMISSION[i.page]!));
  return (
    <SidebarProvider>
      <Sidebar>
        <SidebarHeader>
          <div className="flex items-center gap-2.5 px-2 py-2">
            <span className="flex size-9 items-center justify-center rounded-xl bg-sidebar-primary text-sidebar-primary-foreground"><House className="size-5" aria-hidden /></span>
            <span className="text-base font-semibold tracking-tight">Tomtly</span>
          </div>
        </SidebarHeader>
        <SidebarContent>
          <nav aria-label="Main">
            <NavList items={NAV} page={page} go={go} />
            {admin.length > 0 && <NavList items={admin} page={page} go={go} label="Administration" />}
          </nav>
        </SidebarContent>
        <SidebarFooter>
          <SidebarMenu><SidebarMenuItem><UserMenu page={page} go={go} /></SidebarMenuItem></SidebarMenu>
        </SidebarFooter>
      </Sidebar>
      <SidebarInset className="h-svh min-w-0 overflow-hidden">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b bg-background/90 px-4 backdrop-blur">
          <SidebarTrigger className="-ml-1 size-10" aria-label="Open the menu" />
          <Separator orientation="vertical" className="h-5" />
          <p className="truncate text-sm font-medium" aria-hidden>{PAGE_TITLES[page]}</p>
          <div className="ml-auto text-sm">{connection}</div>
        </header>
        <main id="main" className="min-h-0 flex-1 overflow-auto">{children}</main>
      </SidebarInset>
    </SidebarProvider>
  );
}
