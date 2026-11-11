import { useState, type FormEvent } from "react";
import { KeyRound, Trash2, TriangleAlert, UserPlus, Users } from "lucide-react";
import { toast } from "sonner";
import { authApi, MIN_PASSWORD, ROLES, type ManagedUser, type Role } from "@/authApi";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useConfirm } from "@/components/ConfirmDialog";
import { EmptyState, ErrorText, PageHeader } from "@/components/PageHeader";
import { ago, ROLE_INFO } from "@/lib/roles";
import { useAuth } from "@/lib/auth";
import { useLoad } from "@/lib/useLoad";
import { Field } from "./Field";
import { RoleSelect } from "./RoleSelect";
import { messageOf } from "@/lib/utils";

function NewUserDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; onCreated: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("operator");
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await authApi.createUser(username.trim(), password, role);
      toast.success(`${username.trim()} can now sign in`);
      setUsername("");
      setPassword("");
      setError(null);
      onOpenChange(false);
      onCreated();
    } catch (err) {
      setError(messageOf(err, "Could not add this person"));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form className="space-y-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Add a person</DialogTitle>
            <DialogDescription>Give them the name and password; they can change the password after signing in.</DialogDescription>
          </DialogHeader>
          <Field label="Name" autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
          <Field label="Temporary password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} help={`At least ${MIN_PASSWORD} characters.`} />
          <div className="space-y-1.5">
            <p className="text-sm font-medium">What can they do?</p>
            <RoleSelect value={role} roles={ROLES} label="Role" onChange={setRole} />
            <p className="text-xs text-muted-foreground">{ROLE_INFO[role].summary}</p>
          </div>
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={!username.trim() || password.length < MIN_PASSWORD}>Add person</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ResetPasswordDialog({ user, own, onClose }: { user: ManagedUser | null; own: boolean; onClose: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!user) return;
    try {
      await authApi.updateUser(user.id, { password });
      toast.success(`New password set for ${user.username}`);
      setPassword("");
      setError(null);
      onClose();
    } catch (err) {
      setError(messageOf(err, "Could not set the password"));
    }
  };
  return (
    <Dialog open={user !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form className="space-y-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New password for {user?.username}</DialogTitle>
            <DialogDescription>{own ? "This is your own account: setting a new password signs you out everywhere, and you'll sign in again with it." : "Tell them the new password in person. They can change it after signing in."}</DialogDescription>
          </DialogHeader>
          <Field label="New password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} help={`At least ${MIN_PASSWORD} characters.`} />
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={password.length < MIN_PASSWORD}>Set password</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function UsersPage() {
  const { data: users, error: loadError, reload } = useLoad(authApi.users);
  const { status } = useAuth();
  const confirm = useConfirm();
  const [adding, setAdding] = useState(false);
  const [resetting, setResetting] = useState<ManagedUser | null>(null);
  const [error, setError] = useState<string | null>(null);

  const act = async (action: () => Promise<unknown>) => {
    setError(null);
    try {
      await action();
      await reload();
    } catch (e) {
      // The server refuses to remove the last administrator and says so; that message is the useful part.
      setError(messageOf(e, "That didn't work"));
    }
  };

  const myId = status?.user?.id;
  // Changing your own account ends your own sessions on the server, so say so before it happens.
  const changeSelf = async (user: ManagedUser, what: string, action: () => Promise<unknown>) => {
    if (user.id === myId) {
      const ok = await confirm({
        title: `${what} on your own account?`,
        body: <p>This is your own account. Changing it signs you out everywhere, including here, and you'll need to sign in again.</p>,
        action: "Continue",
        danger: true,
      });
      if (!ok) return;
    }
    await act(action);
  };

  const remove = async (user: ManagedUser) => {
    const ok = await confirm({
      title: `Remove ${user.username}?`,
      body: <><p>{user.username} won't be able to sign in any more. Their API tokens stop working too. This can't be undone.</p>{user.id === myId && <p className="font-medium">This is your own account: you'll be signed out straight away.</p>}</>,
      action: "Remove",
      danger: true,
    });
    if (ok) await act(() => authApi.deleteUser(user.id));
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="People"
        description="Everyone who can sign in to this home, and what each person is allowed to do."
        actions={<Button onClick={() => setAdding(true)}><UserPlus aria-hidden />Add a person</Button>}
      />
      {error && (
        <Alert variant="destructive"><TriangleAlert aria-hidden /><AlertDescription>{error}</AlertDescription></Alert>
      )}
      {loadError && !users ? (
        <EmptyState icon={<TriangleAlert />} title="Couldn't load the list" action={<Button variant="outline" onClick={() => void reload()}>Try again</Button>}>{loadError}</EmptyState>
      ) : !users ? (
        <div className="space-y-2"><Skeleton className="h-14" /><Skeleton className="h-14" /></div>
      ) : users.length === 0 ? (
        <EmptyState icon={<Users />} title="No one yet" action={<Button onClick={() => setAdding(true)}>Add a person</Button>}>Add the people who should be able to control the home.</EmptyState>
      ) : (
        <div className="overflow-x-auto rounded-2xl border bg-card">
          <Table>
            <TableHeader>
              <TableRow><TableHead>Name</TableHead><TableHead>Can do</TableHead><TableHead>Added</TableHead><TableHead className="text-right">Actions</TableHead></TableRow>
            </TableHeader>
            <TableBody>
              {users.map((user) => {
                const me = user.id === status?.user?.id;
                return (
                  <TableRow key={user.id}>
                    <TableCell className="font-medium">
                      {user.username} {me && <Badge variant="secondary">You</Badge>} {user.disabled && <Badge variant="outline">Paused</Badge>}
                    </TableCell>
                    <TableCell>
                      <RoleSelect value={user.role} roles={ROLES} label={`Role of ${user.username}`} onChange={(role) => void changeSelf(user, "Change the role", () => authApi.updateUser(user.id, { role }))} />
                    </TableCell>
                    <TableCell className="text-muted-foreground">{ago(user.created)}</TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1">
                        <Button variant="ghost" size="sm" onClick={() => setResetting(user)}><KeyRound aria-hidden />Set password</Button>
                        <Button variant="ghost" size="sm" onClick={() => void changeSelf(user, user.disabled ? "Allow sign-in" : "Pause sign-in", () => authApi.updateUser(user.id, { disabled: !user.disabled }))}>
                          {user.disabled ? "Allow sign-in" : "Pause"}
                        </Button>
                        <Button variant="ghost" size="sm" className="text-destructive hover:text-destructive" aria-label={`Remove ${user.username}`} onClick={() => void remove(user)}>
                          <Trash2 aria-hidden />Remove
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
      <NewUserDialog open={adding} onOpenChange={setAdding} onCreated={() => void reload()} />
      <ResetPasswordDialog own={resetting?.id === myId} user={resetting} onClose={() => setResetting(null)} />
    </div>
  );
}
