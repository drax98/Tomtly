import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { api, session, type Device, type Rooms, type Value } from "@/api";
import { messageOf } from "@/lib/utils";

const POLL_MS = 2000;
// A write shows at once. If the device hasn't confirmed it by then, the page goes back to the truth.
const PENDING_MS = 12000;

interface Pending {
  value: Value;
  at: number;
}

function usePoll(fn: () => Promise<void>) {
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      // A signed-out page stays up but stops asking, so it doesn't pile up refused requests.
      if (!session.expired) await fn();
      if (!stopped) timer = setTimeout(tick, POLL_MS);
    };
    void tick();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [fn]);
}

export function useDevices() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [rooms, setRooms] = useState<Rooms>({});
  const [reachable, setReachable] = useState(true);
  const [pending, setPending] = useState<Record<string, Pending>>({});
  const pendingRef = useRef(pending);
  pendingRef.current = pending;

  const refresh = useCallback(async () => {
    try {
      const list = await api.devices();
      setDevices(list);
      setReachable(true);
      const now = Date.now();
      const next: Record<string, Pending> = {};
      for (const [key, p] of Object.entries(pendingRef.current)) {
        const [id, dp] = JSON.parse(key) as [string, string];
        const actual = list.find((d) => d.id === id)?.dps?.find((d) => d.dp === dp)?.value;
        if (actual !== p.value && now - p.at < PENDING_MS) next[key] = p;
      }
      setPending(next);
    } catch {
      setReachable(false);
    }
  }, []);

  usePoll(refresh);

  const reloadRooms = useCallback(() => api.rooms().then(setRooms).catch(() => setRooms({})), []);
  useEffect(() => { void reloadRooms(); }, [reloadRooms]);
  /** After a device was renamed or moved: its name comes with the devices, its room with the rooms. */
  const reload = useCallback(() => { void refresh(); void reloadRooms(); }, [refresh, reloadRooms]);

  const write = useCallback(async (id: string, dp: string, value: Value, track = true) => {
    const key = JSON.stringify([id, dp]);
    if (track) setPending((p) => ({ ...p, [key]: { value, at: Date.now() } }));
    try {
      await api.write(id, dp, value);
    } catch (e) {
      setPending(({ [key]: _dropped, ...rest }) => rest);
      const status = (e as { status?: number }).status;
      const reason = messageOf(e, "");
      // A refusal for permission is the answer itself; anything else is usually the device or the network.
      toast.error(status === 403 ? reason : "That didn't go through", {
        description: status === 403 ? undefined : `${reason ? `${reason}. ` : ""}Check the device has power and Wi-Fi, then try again.`,
      });
    }
  }, []);

  const merged = useMemo(() => devices?.map((d) => ({
    ...d,
    dps: d.dps?.map((x) => {
      const p = pending[JSON.stringify([d.id, x.dp])];
      return p ? { ...x, value: p.value } : x;
    }) ?? [],
  })) ?? null, [devices, pending]);

  return { devices: merged, rooms, reachable, write, reload };
}

export { usePoll };
