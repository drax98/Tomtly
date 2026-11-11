import { LayoutGrid, Users } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import type { Page } from "@/lib/route";

/** Shown once after the first account is made, pointing at what to do next. */
export function Welcome({ open, onClose, go }: { open: boolean; onClose: () => void; go: (p: Page) => void }) {
  const to = (page: Page) => {
    onClose();
    go(page);
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>You're all set</DialogTitle>
          <DialogDescription>Your account is ready and you're signed in. Here's a good order to go in:</DialogDescription>
        </DialogHeader>
        <ol className="list-decimal space-y-2 pl-5 text-sm">
          <li><strong>Look at your devices.</strong> Everything your integrations found is on the first page. Tap a switch to try one.</li>
          <li><strong>Add the people who live here.</strong> Give each person their own name and a role, so a guest can look but not change.</li>
          <li><strong>Try an automation later.</strong> They start in practice mode and change nothing until you say so.</li>
        </ol>
        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={() => to("users")}><Users aria-hidden />Add people</Button>
          <Button onClick={() => to("devices")}><LayoutGrid aria-hidden />See my devices</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
