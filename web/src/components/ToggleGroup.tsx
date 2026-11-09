import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

/** A small either/or choice, built on tabs so arrow keys and focus work without extra code. */
export function ToggleGroup<T extends string>({ label, value, onChange, options }: { label: string; value: T; onChange: (v: T) => void; options: [T, string][] }) {
  return (
    <Tabs value={value} onValueChange={(v) => onChange(v as T)}>
      <TabsList aria-label={label} className="h-11">
        {options.map(([v, text]) => <TabsTrigger key={v} value={v} className="min-h-9">{text}</TabsTrigger>)}
      </TabsList>
    </Tabs>
  );
}
