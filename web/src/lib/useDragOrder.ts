import { useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";

/** What one card in a reorderable list needs. */
export interface Drag {
  /** Where the card is, for working out which card the pointer is over. */
  cardRef: (el: HTMLElement | null) => void;
  dragging: boolean;
  onPointerDown: (e: ReactPointerEvent<HTMLElement>) => void;
  /** For the card's handle, a button with data-grip: the arrow keys move it one place. */
  onKey: (e: KeyboardEvent<HTMLButtonElement>) => void;
}

const moved = (list: string[], item: string, to: number) => {
  const next = list.filter((n) => n !== item);
  next.splice(Math.max(0, Math.min(to, next.length)), 0, item);
  return next;
};

/**
 * Reorders cards by dragging or by the arrow keys on a handle. The new order shows at once and stays until save is
 * over; save shows its own error, and the list goes back to what current says.
 */
export function useDragOrder(current: string[], save: (item: string, position: number, list: string[]) => Promise<void>, label: (item: string) => string = (i) => i) {
  const [order, setOrder] = useState<string[] | null>(null); // while dragging, and until save is over
  const [dragged, setDragged] = useState<string | null>(null);
  const [said, setSaid] = useState("");
  const cards = useRef(new Map<string, HTMLElement>());
  const items = order ?? current;

  const place = async (item: string, position: number, list: string[]) => {
    setOrder(list);
    setSaid(`${label(item)} moved to place ${position + 1} of ${list.length}.`);
    try {
      await save(item, position, list);
    } finally {
      setOrder(null);
    }
  };

  const startDrag = (item: string) => (e: ReactPointerEvent<HTMLElement>) => {
    const target = e.target as HTMLElement;
    const grip = target.closest("[data-grip]");
    // A mouse can take a card from anywhere that isn't a control; a finger only by its handle, so the page still scrolls.
    if (e.button !== 0 || (!grip && (e.pointerType !== "mouse" || target.closest("a, button, input, label, select, textarea, [role=dialog], [role=switch], [role=slider], [role=combobox]")))) return;
    e.preventDefault();
    const from = items.indexOf(item);
    let list = items;
    setDragged(item);
    setOrder(list);
    // The card it last swapped with slides under the pointer; swapping back with it would make the two flicker.
    let last: string | null = null;
    const move = (ev: PointerEvent) => {
      for (const [other, el] of cards.current) {
        if (other === item) continue;
        const r = el.getBoundingClientRect();
        if (ev.clientX >= r.left && ev.clientX <= r.right && ev.clientY >= r.top && ev.clientY <= r.bottom) {
          if (other === last) return;
          last = other;
          const next = moved(list, item, list.indexOf(other));
          if (next.join("\u0000") !== list.join("\u0000")) { list = next; setOrder(next); }
          return;
        }
      }
      last = null;
    };
    const end = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", end);
      window.removeEventListener("pointercancel", end);
      setDragged(null);
      const to = list.indexOf(item);
      if (to === from) setOrder(null);
      else void place(item, to, list);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", end);
    window.addEventListener("pointercancel", end);
  };

  const keyMove = (item: string) => (e: KeyboardEvent<HTMLButtonElement>) => {
    const step = e.key === "ArrowUp" || e.key === "ArrowLeft" ? -1 : e.key === "ArrowDown" || e.key === "ArrowRight" ? 1 : 0;
    if (!step) return;
    e.preventDefault();
    const at = items.indexOf(item), to = at + step;
    if (to < 0 || to >= items.length) return;
    void place(item, to, moved(items, item, to));
  };

  const drag = (item: string): Drag => ({
    cardRef: (el) => { if (el) cards.current.set(item, el); else cards.current.delete(item); },
    dragging: dragged === item,
    onPointerDown: startDrag(item),
    onKey: keyMove(item),
  });

  return { items, said, drag };
}
