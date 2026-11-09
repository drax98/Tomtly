/** Initials in a quiet circle; there are no profile pictures to load, so nothing leaves the house. */
export function Avatar({ name }: { name: string }) {
  return (
    <span aria-hidden className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary/15 text-sm font-semibold uppercase text-primary">
      {name.slice(0, 2)}
    </span>
  );
}
