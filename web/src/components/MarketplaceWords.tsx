import { Button } from "@/components/ui/button";

/** The marketplace's fixed list, as the marketplace names them. */
export const CATEGORIES: [string, string][] = [
  ["climate", "Climate"], ["lighting", "Lighting"], ["cleaning", "Cleaning"], ["energy", "Energy"], ["security", "Security"],
  ["sensors", "Sensors"], ["media", "Media"], ["weather", "Weather"], ["bridges", "Bridges"], ["other", "Other"],
];
export const categoryName = (id: string) => CATEGORIES.find(([key]) => key === id)?.[1] ?? id;

/** A listing's tags. Each one searches the marketplace's website for everything else with it. */
export function TagList({ tags, onTag }: { tags?: string[]; onTag: (tag: string) => void }) {
  if (!tags?.length) return null;
  return (
    <ul aria-label="Tags" className="flex flex-wrap gap-1.5">
      {tags.map((tag) => (
        <li key={tag}>
          <Button variant="outline" size="sm" className="h-auto min-h-11 rounded-full px-2.5 py-0.5 text-xs font-normal sm:min-h-7" aria-label={`Show everything tagged ${tag}`} onClick={() => onTag(tag)}>
            #{tag}
          </Button>
        </li>
      ))}
    </ul>
  );
}
