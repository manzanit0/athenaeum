import { type DependencyList, useCallback, useEffect, useRef, useState } from "react";
import { messageOf } from "./format";

/** The state of a value loaded from the server. */
export interface Loaded<T> {
  data: T | undefined;
  error: string;
  loading: boolean;
  reload: () => void;
  set: (value: T) => void;
}

/** Loads a value from the server and reloads it when deps change. */
export function useLoad<T>(load: () => Promise<T>, deps: DependencyList): Loaded<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const latest = useRef(0);
  const run = useCallback(() => {
    const ticket = ++latest.current;
    load()
      .then((value) => {
        if (ticket === latest.current) {
          setData(value);
          setError("");
        }
      })
      .catch((reason) => {
        if (ticket === latest.current) {
          setError(messageOf(reason));
        }
      })
      .finally(() => {
        if (ticket === latest.current) {
          setLoading(false);
        }
      });
  }, deps);
  useEffect(run, [run]);
  return { data, error, loading, reload: run, set: setData };
}

/** Returns value after it has stopped changing for delay milliseconds. */
export function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return settled;
}
