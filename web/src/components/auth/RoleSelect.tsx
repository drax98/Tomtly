import type { Role } from "@/authApi";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ROLE_INFO } from "@/lib/roles";

export function RoleSelect({ value, roles, label, onChange, disabled }: { value: Role; roles: readonly Role[]; label: string; onChange: (role: Role) => void; disabled?: boolean }) {
  return (
    <Select value={value} onValueChange={(v) => onChange(v as Role)} disabled={disabled}>
      <SelectTrigger className="h-11 w-44" aria-label={label}><SelectValue>{ROLE_INFO[value].name}</SelectValue></SelectTrigger>
      <SelectContent>
        {roles.map((role) => (
          <SelectItem key={role} value={role}>
            <span className="block">{ROLE_INFO[role].name}</span>
            <span className="block max-w-56 whitespace-normal text-xs text-muted-foreground">{ROLE_INFO[role].summary}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
