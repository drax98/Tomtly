import { useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { ChevronDown, Plus, TriangleAlert, Upload } from "lucide-react";
import { toast } from "sonner";
import { api, type ExtensionStatus } from "@/api";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { defaults, ExtensionReview } from "@/components/ExtensionReview";
import { WebhookAddress } from "@/components/ExtensionEdit";
import { navigate } from "@/lib/route";
import { messageOf } from "@/lib/utils";

type What = "integration" | "plugin";
type Mode = "github" | "local";

const CHOICE = "min-h-11 flex-col items-start gap-0";

/** Turns the GitHub fields into a source. A repo's address, pasted from the browser, works too. */
export const githubSource = (repo: string, folder: string, version: string) => {
  const name = repo.trim().replace(/^(https?:\/\/(www\.)?github\.com\/|github:)/, "").replace(/\.git$/, "").replace(/\/+$/, "");
  const inside = folder.trim().replace(/^\/+|\/+$/g, "");
  return `github:${name}${inside && `/${inside}`}@${version.trim()}`;
};

/** Adds an extension in two steps, so you see what access it needs and what it asks for before anything runs. */
function AddDialog({ mode, what, onClose, onAdded }: { mode: Mode; what: What; onClose: () => void; onAdded: (webhook?: string) => void }) {
  const [repo, setRepo] = useState("");
  const [inside, setInside] = useState("");
  const [version, setVersion] = useState("");
  const [folder, setFolder] = useState("");
  const [found, setFound] = useState<ExtensionStatus | null>(null);
  const [settings, setSettings] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState<string | null>(null);
  const [uploaded, setUploaded] = useState<string | null>(null); // the file's name, once its package is stored
  const [error, setError] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);

  const attempt = async (action: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (e) {
      setError(messageOf(e, "That didn't work"));
    } finally {
      setBusy(false);
    }
  };

  const review = (result: ExtensionStatus) => {
    setFound(result);
    setSettings(defaults(result.settings));
  };

  const inspect = (e: FormEvent, source: string) => {
    e.preventDefault();
    void attempt(async () => review(await api.inspectExtension(source)));
  };

  const upload = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // so choosing the same file again still counts as a change
    if (!file) return;
    setUploading(file.name);
    void attempt(async () => {
      try {
        review(await api.uploadExtension(file));
        setUploaded(file.name);
      } finally {
        setUploading(null);
      }
    });
  };

  const add = (e: FormEvent) => {
    e.preventDefault();
    if (!found) return;
    void attempt(async () => {
      const added = await api.addExtension(found.source, settings, found.access);
      toast.success(`${found.name} added`);
      onAdded(added.webhook);
    });
  };

  const named = what === "integration" ? "an integration" : "a plugin";
  const checking = busy && !uploading ? "Checking…" : "Check";
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Add {named} from {mode === "github" ? "a GitHub repo" : "local files"}</DialogTitle>
          <DialogDescription>Nothing runs yet: you'll see what it wants first.</DialogDescription>
        </DialogHeader>

        {found ? (
          <div className="space-y-3">
            <ExtensionReview found={found} uploaded={uploaded} settings={settings} onSettings={setSettings} busy={busy} onSubmit={add} />
            <Button type="button" variant="outline" onClick={() => { setFound(null); setUploaded(null); }}>Back</Button>
          </div>
        ) : mode === "github" ? (
          <form className="space-y-3" onSubmit={(e) => inspect(e, githubSource(repo, inside, version))}>
            <Label className="grid gap-1.5">
              Repository
              <Input placeholder="owner/repo" className="font-mono text-xs" value={repo} onChange={(e) => setRepo(e.target.value)} />
            </Label>
            <Label className="grid gap-1.5">
              Folder (optional)
              <Input placeholder="packages/tides" className="font-mono text-xs" value={inside} onChange={(e) => setInside(e.target.value)} />
            </Label>
            <Label className="grid gap-1.5">
              Version
              <Input placeholder="v1.0.0" className="font-mono text-xs" value={version} onChange={(e) => setVersion(e.target.value)} />
            </Label>
            <p className="text-xs text-muted-foreground">The repo must be public. Use a tag, branch or commit; a commit is the safest. It stays at this version until you change it. Nothing here is verified: it's your choice to trust whoever controls that repo.</p>
            <Button type="submit" variant="outline" disabled={busy || !repo.trim() || !version.trim()}>{checking}</Button>
          </form>
        ) : (
          <div className="space-y-4">
            <div className="space-y-1.5">
              <p className="text-sm font-medium">Upload a file</p>
              <Button type="button" variant="outline" disabled={busy} onClick={() => picker.current?.click()}>
                <Upload aria-hidden />{uploading ? "Uploading…" : "Choose file"}
              </Button>
              <input ref={picker} type="file" hidden tabIndex={-1} accept=".zip,.tar.gz,.tgz,.ts" aria-label={`File for the ${what}`} onChange={upload} />
              <p className="text-xs text-muted-foreground">A .zip, .tar.gz or .ts file.</p>
              {uploading && <p role="status" className="text-xs text-muted-foreground">Uploading {uploading}…</p>}
            </div>
            <form className="space-y-3 border-t pt-4" onSubmit={(e) => inspect(e, folder.trim())}>
              <Label className="grid gap-1.5">
                Or use a folder in your config folder
                <Input placeholder="./extensions/my-folder" className="font-mono text-xs" value={folder} onChange={(e) => setFolder(e.target.value)} />
              </Label>
              <Button type="submit" variant="outline" disabled={busy || !folder.trim()}>{checking}</Button>
            </form>
          </div>
        )}

        {error && (
          <Alert variant="destructive">
            <TriangleAlert aria-hidden />
            <AlertTitle>That didn't work. Check the details and try again.</AlertTitle>
            <AlertDescription className="break-words font-mono text-xs">{error}</AlertDescription>
          </Alert>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** The page's Add button: the same three places to get an extension from, wherever you start. */
export function AddExtension({ what, onAdded }: { what: What; onAdded: () => void }) {
  const [mode, setMode] = useState<Mode | null>(null);
  const [address, setAddress] = useState<string | null>(null); // a new webhook address, shown once

  return (
    <>
      {/* Not modal: a modal menu and the dialog it opens fight over focus and can leave the page unclickable. */}
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button className="min-h-11 sm:min-h-9"><Plus aria-hidden />Add {what}<ChevronDown aria-hidden /></Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-72">
          <DropdownMenuItem asChild className={CHOICE}>
            <a href="/marketplace" onClick={(e) => { e.preventDefault(); navigate("marketplace"); }}>
              <span className="font-medium">From the marketplace</span>
              <span className="text-xs text-muted-foreground">Checked and approved</span>
            </a>
          </DropdownMenuItem>
          <DropdownMenuItem className={CHOICE} onSelect={() => setMode("github")}>
            <span className="font-medium">From a GitHub repo</span>
            <span className="text-xs text-muted-foreground">A public repo at a version you choose</span>
          </DropdownMenuItem>
          <DropdownMenuItem className={CHOICE} onSelect={() => setMode("local")}>
            <span className="font-medium">From local files</span>
            <span className="text-xs text-muted-foreground">Upload a file, or use your config folder</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {mode && (
        <AddDialog
          mode={mode}
          what={what}
          onClose={() => setMode(null)}
          onAdded={(webhook) => {
            setMode(null);
            if (webhook) setAddress(webhook);
            onAdded();
          }}
        />
      )}
      {address && <WebhookAddress path={address} onClose={() => setAddress(null)} />}
    </>
  );
}
