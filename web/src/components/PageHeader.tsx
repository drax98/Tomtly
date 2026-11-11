import type { ReactNode } from "react";

/** Every page opens the same way: what it is, in a sentence, and its main action. */
export function PageHeader({ title, description, actions }: { title: string; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
      <div className="min-w-0 space-y-1">
        <h1 className="text-title font-semibold">{title}</h1>
        {description && <p className="max-w-2xl text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function EmptyState({ icon, title, children, action }: { icon: ReactNode; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-2xl border border-dashed px-6 py-12 text-center">
      <span className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground [&_svg]:size-6">{icon}</span>
      <h2 className="text-base font-semibold">{title}</h2>
      {children && <p className="max-w-md text-sm text-muted-foreground">{children}</p>}
      {action}
    </div>
  );
}

/** The reading column: wide enough for a grid, not so wide a 1440px screen looks stretched. */
export function PageBody({ children, full = false }: { children: ReactNode; full?: boolean }) {
  return full
    ? <div className="flex h-full min-h-0 flex-col gap-3 p-4 sm:px-6">{children}</div>
    : <div className="mx-auto w-full max-w-6xl space-y-6 p-4 sm:p-6 lg:p-8">{children}</div>;
}

/** ErrorText is why the last action didn't work, read out by screen readers when it appears. */
export function ErrorText({ children }: { children: ReactNode }) {
  return <p role="alert" className="break-words text-sm text-destructive">{children}</p>;
}
