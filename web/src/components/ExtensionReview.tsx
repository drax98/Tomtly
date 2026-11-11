import type { FormEvent } from "react";
import { Plus } from "lucide-react";
import type { ExtensionSetting, ExtensionStatus } from "@/api";
import { Button } from "@/components/ui/button";
import { AccessList, GITHUB_RISK, Warnings } from "@/components/ExtensionParts";
import { SettingField } from "@/components/SettingField";

export const defaults = (settings: ExtensionSetting[]) => Object.fromEntries(settings.filter((s) => s.default !== undefined).map((s) => [s.key, s.default]));

/** The second step of adding: what it needs, what it asks for, and the button that agrees to the access. */
export function ExtensionReview({ found, uploaded, settings, onSettings, busy, onSubmit }: {
  found: ExtensionStatus;
  uploaded?: string | null;
  settings: Record<string, unknown>;
  onSettings: (settings: Record<string, unknown>) => void;
  busy: boolean;
  onSubmit: (e: FormEvent) => void;
}) {
  return (
    <form className="space-y-3" onSubmit={onSubmit}>
      <div className="space-y-1 text-sm">
        <p className="font-medium">{found.name} <span className="text-xs font-normal text-muted-foreground">{found.version}</span></p>
        {found.description && <p className="text-xs text-muted-foreground">{found.description}</p>}
        {uploaded && <p className="break-all text-xs text-muted-foreground">Uploaded file: {uploaded}</p>}
        <p className="pt-1 text-sm font-medium">It needs this access:</p>
        <AccessList access={found.access} />
        <p className="text-xs text-muted-foreground">
          Provides {found.provides.join(" and ")}. It can send the settings below to whatever it can reach, so only continue if you trust where it came from.
        </p>
      </div>
      {found.settings.map((setting) => (
        <SettingField
          key={setting.key}
          setting={setting}
          value={settings[setting.key]}
          onChange={(value) => onSettings({ ...settings, [setting.key]: value })}
        />
      ))}
      {found.source.startsWith("github:") && <Warnings warnings={[GITHUB_RISK]} title="Not verified" open />}
      <Warnings warnings={found.risks} title="The risk in approving this" open />
          <Warnings warnings={found.warnings} open />
      <Button type="submit" disabled={busy}><Plus aria-hidden />{busy ? "Adding…" : `Allow this access and add ${found.name}`}</Button>
    </form>
  );
}
