import { History, TriangleAlert } from "lucide-react";
import { authApi } from "@/authApi";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { useLoad } from "@/lib/useLoad";

const load = () => authApi.audit(200);

export function AuditPage() {
  const { data, error, reload } = useLoad(load);
  return (
    <div className="space-y-6">
      <PageHeader
        title="Activity log"
        description="Who signed in and what they changed, newest first. Useful when something unexpected happens."
        actions={<Button variant="outline" onClick={() => void reload()}>Refresh</Button>}
      />
      {error && !data ? (
        <EmptyState icon={<TriangleAlert />} title="Couldn't load the log" action={<Button variant="outline" onClick={() => void reload()}>Try again</Button>}>{error}</EmptyState>
      ) : !data ? (
        <div className="space-y-2"><Skeleton className="h-10" /><Skeleton className="h-10" /><Skeleton className="h-10" /></div>
      ) : data.length === 0 ? (
        <EmptyState icon={<History />} title="Nothing recorded yet">Sign-ins and changes will appear here.</EmptyState>
      ) : (
        <div className="overflow-x-auto rounded-2xl border bg-card">
          <Table>
            <TableHeader>
              <TableRow><TableHead>When</TableHead><TableHead>Who</TableHead><TableHead>What</TableHead><TableHead>Details</TableHead><TableHead>From</TableHead></TableRow>
            </TableHeader>
            <TableBody>
              {data.map((entry, i) => (
                <TableRow key={`${entry.time}-${i}`}>
                  <TableCell className="whitespace-nowrap tabular-nums text-muted-foreground">
                    {new Date(entry.time).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}
                  </TableCell>
                  <TableCell className="font-medium">{entry.user || "–"}</TableCell>
                  <TableCell>{entry.action}</TableCell>
                  <TableCell className="max-w-80 break-words text-muted-foreground">{entry.detail}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{entry.ip}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
