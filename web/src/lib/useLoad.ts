import { useCallback, useEffect, useState } from "react";
import { messageOf } from "@/lib/utils";

/** Loads a list once and on demand. Errors stay a message the page can show with a retry. */
export function useLoad<T>(load: () => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const reload = useCallback(async () => {
    try {
      setData(await load());
      setError(null);
    } catch (e) {
      setError(messageOf(e, "Could not load this"));
    }
  }, [load]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { data, error, reload };
}
