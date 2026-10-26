"use client";

import * as React from "react";

type State<T> = {
  data: T | null;
  error: string | null;
  loading: boolean;
};

type Settled<T> = {
  data: T | null;
  error: string | null;
  /** Identity of the request that produced this state; null before the first. */
  settled: string | null;
};

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : "Tidak bisa memuat data";
}

/**
 * Fetch-on-mount hook.
 *
 * `loader` may be redefined on every render — it is read through a ref, so only
 * `key` (plus an explicit `reload()`) re-issues the request. `loading` is
 * derived from the identity of the in-flight request instead of being set
 * inside the effect, which keeps the render free of cascading state updates.
 */
export function useResource<T>(
  loader: () => Promise<T>,
  key: React.DependencyList = [],
): State<T> & {
  reload: () => void;
  setData: (next: T | ((prev: T | null) => T | null)) => void;
} {
  const [tick, setTick] = React.useState(0);
  const identity = JSON.stringify([tick, ...key]);

  const [state, setState] = React.useState<Settled<T>>({
    data: null,
    error: null,
    settled: null,
  });

  const loaderRef = React.useRef(loader);
  React.useEffect(() => {
    loaderRef.current = loader;
  });

  React.useEffect(() => {
    let cancelled = false;

    loaderRef
      .current()
      .then((data) => {
        if (!cancelled) setState({ data, error: null, settled: identity });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setState({ data: null, error: messageOf(err), settled: identity });
      });

    return () => {
      cancelled = true;
    };
  }, [identity]);

  const reload = React.useCallback(() => setTick((n) => n + 1), []);

  const setData = React.useCallback(
    (next: T | ((prev: T | null) => T | null)) => {
      setState((prev) => {
        const value =
          typeof next === "function"
            ? (next as (p: T | null) => T | null)(prev.data)
            : next;
        return { data: value, error: null, settled: identity };
      });
    },
    [identity],
  );

  return {
    data: state.data,
    error: state.error,
    loading: state.settled !== identity,
    reload,
    setData,
  };
}

/** Ticking clock for countdowns; one interval per component. */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs]);
  return now;
}
