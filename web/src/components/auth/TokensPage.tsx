import { useState, type FormEvent } from "react";
import { Check, Copy, KeyRound, Trash2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { authApi, type Role } from "@/authApi";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useConfirm } from "@/components/ConfirmDialog";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { useAuth, useUser } from "@/lib/auth";
import { ago, ROLE_INFO, rolesUpTo } from "@/lib/roles";
import { useLoad } from "@/lib/useLoad";
import { Field } from "./Field";
import { RoleSelect } from "./RoleSelect";
import { messageOf } from "@/lib/utils";

/** The secret lives only in this component's state: it is shown once and never stored or put in a URL. */
function NewSecret({ name, secret, onDone }: { name: string; secret: string; onDone: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
    } catch {
      toast.error("Couldn't copy. Select the text and copy it yourself.");
    }
  };
  return (
    <Alert variant="warning" className="space-y-3">
      <TriangleAlert aria-hidden />
      <div className="space-y-3">
        <AlertTitle>Copy the token for "{name}" now</AlertTitle>
        <AlertDescription className="space-y-3">
          <p>This is the only time it is shown. If you lose it, delete it and make a new one.</p>
          <div className="flex flex-wrap items-center gap-2">
            <code className="min-w-0 flex-1 break-all rounded-md border bg-card px-3 py-2 font-mono text-xs text-foreground" data-testid="token-secret">{secret}</code>
            <Button variant="outline" onClick={() => void copy()}>{copied ? <Check aria-hidden /> : <Copy aria-hidden />}{copied ? "Copied" : "Copy"}</Button>
          </div>
          <Button onClick={onDone}>I've saved it</Button>
        </AlertDescription>
      </div>
    </Alert>
  );
}

export function TokensPage() {
  const user = useUser();
  const { can } = useAuth();
  const confirm = useConfirm();
  const { data: tokens, error: loadError, reload } = useLoad(authApi.tokens);
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [created, setCreated] = useState<{ name: string; secret: string } | null>(null);
  const [error, setError] = useState<string | null>(null);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      const token = await authApi.createToken(name.trim(), role);
      setCreated({ name: token.name, secret: token.token });
      setName("");
      await reload();
    } catch (err) {
      setError(messageOf(err, "Could not create the token"));
    }
  };

  const remove = async (id: string, label: string) => {
    const ok = await confirm({
      title: `Delete "${label}"?`,
      body: <p>Anything using this token (a script, another app) will stop working straight away.</p>,
      action: "Delete token",
      danger: true,
    });
    if (!ok) return;
    try {
      await authApi.deleteToken(id);
      await reload();
    } catch (err) {
      setError(messageOf(err, "Could not delete the token"));
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title="API tokens" description="A token lets another program, like a script or a dashboard, talk to your home without a password. It can do no more than the role you give it." />
      {created && <NewSecret {...created} onDone={() => setCreated(null)} />}
      <form className="flex flex-wrap items-end gap-3 rounded-2xl border bg-card p-4" onSubmit={create}>
        <div className="min-w-48 flex-1"><Field label="What is it for?" placeholder="Kitchen dashboard" value={name} onChange={(e) => setName(e.target.value)} /></div>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">Allowed to</p>
          <RoleSelect value={role} roles={rolesUpTo(user.role)} label="Token role" onChange={setRole} />
        </div>
        <Button type="submit" disabled={!name.trim()}><KeyRound aria-hidden />Create token</Button>
        <p className="basis-full text-xs text-muted-foreground">{ROLE_INFO[role].summary}</p>
      </form>
      {error && <Alert variant="destructive"><TriangleAlert aria-hidden /><AlertDescription>{error}</AlertDescription></Alert>}
      {loadError && !tokens ? (
        <EmptyState icon={<TriangleAlert />} title="Couldn't load your tokens" action={<Button variant="outline" onClick={() => void reload()}>Try again</Button>}>{loadError}</EmptyState>
      ) : !tokens ? (
        <Skeleton className="h-24" />
      ) : tokens.length === 0 ? (
        <EmptyState icon={<KeyRound />} title="No tokens yet">You only need one if another program should control your home. Most people never do.</EmptyState>
      ) : (
        <div className="overflow-x-auto rounded-2xl border bg-card">
          <Table>
            <TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Allowed to</TableHead><TableHead>Last used</TableHead>{can("users.manage") && <TableHead>Owner</TableHead>}<TableHead className="text-right">Actions</TableHead></TableRow></TableHeader>
            <TableBody>
              {tokens.map((token) => (
                <TableRow key={token.id}>
                  <TableCell className="font-medium">{token.name}</TableCell>
                  <TableCell>{ROLE_INFO[token.role].name}</TableCell>
                  <TableCell className="text-muted-foreground">{ago(token.lastUsed)}</TableCell>
                  {can("users.manage") && <TableCell className="text-muted-foreground">{token.owner}</TableCell>}
                  <TableCell className="text-right">
                    <Button variant="ghost" size="sm" className="text-destructive hover:text-destructive" aria-label={`Delete ${token.name}`} onClick={() => void remove(token.id, token.name)}><Trash2 aria-hidden />Delete</Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
