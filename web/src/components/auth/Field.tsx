import { useId, type ComponentProps, type ReactNode } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/** A labelled input with optional help, wired so screen readers read the help too. */
export function Field({ label, help, ...props }: { label: string; help?: ReactNode } & ComponentProps<typeof Input>) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} aria-describedby={help ? `${id}-help` : undefined} {...props} />
      {help && <p id={`${id}-help`} className="text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}
