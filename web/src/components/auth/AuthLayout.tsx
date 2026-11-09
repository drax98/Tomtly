import type { ReactNode } from "react";
import { House } from "lucide-react";
import { cn } from "@/lib/utils";

/** The frame around sign-in and first-run setup. As an overlay it covers a page that's still mounted underneath. */
export function AuthLayout({ title, intro, children, overlay = false }: { title: string; intro?: ReactNode; children: ReactNode; overlay?: boolean }) {
  return (
    <div className={cn("flex min-h-svh items-center justify-center bg-background px-4 py-10", overlay && "fixed inset-0 z-50 overflow-auto")}>
      <main className="w-full max-w-md space-y-6">
        <div className="flex items-center gap-3">
          <span className="flex size-10 items-center justify-center rounded-xl bg-primary text-primary-foreground"><House className="size-5" aria-hidden /></span>
          <span className="text-lg font-semibold tracking-tight">Home Control</span>
        </div>
        <div className="space-y-1.5">
          <h1 className="text-title font-semibold">{title}</h1>
          {intro && <div className="text-sm text-muted-foreground">{intro}</div>}
        </div>
        <div className="rounded-2xl border bg-card p-5 shadow-xs sm:p-6">{children}</div>
      </main>
    </div>
  );
}
