import type { ExtensionSetting } from "@/api";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";

/** One setting as a form field. keep marks a secret that is already set: left blank, it stays as it is. */
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
