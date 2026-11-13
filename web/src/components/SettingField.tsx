import { useRef, useState } from "react";
import { FileUp } from "lucide-react";
import type { ExtensionSetting } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";

/** A file setting is sent as the file's text, at most 1 MiB, and kept by the service like a key. */
const MAX_FILE = 1 << 20;

/** One setting as a form field. keep marks a secret or file that is already set: left empty, it stays as it is. */
export function SettingField({ setting, value, onChange, keep }: { setting: ExtensionSetting; value: unknown; onChange: (value: unknown) => void; keep?: boolean }) {
  const id = `setting-${setting.key}`;
  const label = `${setting.label}${setting.required ? "" : " (optional)"}`;
  return (
    <div className="space-y-1">
      {setting.type === "boolean" ? (
        <div className="flex items-center gap-2">
          <Switch id={id} aria-label={setting.label} checked={value === true} onCheckedChange={onChange} />
          <label htmlFor={id} className="text-sm">{label}</label>
        </div>
      ) : setting.type === "file" ? (
        <>
          <label htmlFor={id} className="text-sm">{label}</label>
          <FileText id={id} required={setting.required && !keep} keep={keep} onChange={onChange} />
        </>
      ) : (
        <>
          <label htmlFor={id} className="text-sm">{label}</label>
          <Input
            id={id}
            type={setting.type === "secret" ? "password" : setting.type === "number" ? "number" : "text"}
            step="any"
            autoComplete="off"
            placeholder={keep && setting.type === "secret" ? "Leave blank to keep it" : undefined}
            required={setting.required && !keep}
            value={(value as string | number | undefined) ?? ""}
            onChange={(e) => onChange(setting.type === "number" && e.target.value !== "" ? Number(e.target.value) : e.target.value)}
          />
        </>
      )}
      {setting.help && <p className="text-xs text-muted-foreground">{setting.help}</p>}
    </div>
  );
}

/**
 * Reads the chosen file as text in the browser. Only its name is shown; what it holds goes straight to the service.
 * A button rather than the browser's own picker, which the text-box styling misaligns. The real input stays in the form,
 * visually hidden, so its label and "required" still work.
 */
function FileText({ id, required, keep, onChange }: { id: string; required: boolean; keep?: boolean; onChange: (value: unknown) => void }) {
  const picker = useRef<HTMLInputElement>(null);
  const [name, setName] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(keep ? "Set. Choose a file only to replace it." : null);
  const choose = async (file: File | undefined) => {
    setName(file?.name ?? null);
    if (!file) return onChange(undefined);
    if (file.size > MAX_FILE) {
      setNote(`${file.name} is over 1 MiB.`);
      return onChange(undefined);
    }
    setNote(`${file.name} is read and will be kept encrypted.`);
    onChange(await file.text());
  };
  return (
    <>
      <div className="flex min-w-0 items-center gap-3">
        <Button type="button" variant="outline" size="sm" className="min-h-11 shrink-0 sm:min-h-9" onClick={() => picker.current?.click()}>
          <FileUp aria-hidden />{name || keep ? "Replace file" : "Choose file"}
        </Button>
        <span className="min-w-0 truncate text-sm text-muted-foreground">{name ?? (keep ? "Set" : "No file chosen")}</span>
      </div>
      <input ref={picker} id={id} type="file" required={required} className="sr-only" tabIndex={-1} onChange={(e) => void choose(e.target.files?.[0])} />
      {note && <p className="text-xs text-muted-foreground" role="status">{note}</p>}
    </>
  );
}
