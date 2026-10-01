"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";

import { api, errorText, initToken } from "@/lib/api";
import type { AppState } from "@/lib/types";

type Ctx = {
  state: AppState | null;
  refresh: () => Promise<void>;
  startScan: (path?: string) => Promise<void>;
  /** Bumps whenever a scan finishes or items are cleaned, so trees reload. */
  treeVersion: number;
  bumpTree: () => void;
  toast: (msg: string, action?: ToastAction) => void;
};

export type ToastAction = { label: string; run: () => void };

const AppCtx = createContext<Ctx | null>(null);

export function useApp(): Ctx {
  const c = useContext(AppCtx);
  if (!c) throw new Error("useApp outside AppProvider");
  return c;
}

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [token, setToken] = useState<string | null | undefined>(undefined);
  const [state, setState] = useState<AppState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [treeVersion, setTreeVersion] = useState(0);
  const [toastMsg, setToastMsg] = useState<{ text: string; action?: ToastAction } | null>(null);
  const wasScanning = useRef(false);
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => setToken(initToken()), []);

  const refresh = useCallback(async () => {
    try {
      const s = await api<AppState>("/api/state");
      setState(s);
      setError(null);
      if (wasScanning.current && !s.scanning) setTreeVersion((v) => v + 1);
      wasScanning.current = s.scanning;
    } catch (e) {
      setError(errorText(e));
    }
  }, []);

  // Poll quickly while a scan runs, slowly otherwise.
  useEffect(() => {
    if (!token) return;
    refresh();
    const id = setInterval(refresh, state?.scanning ? 700 : 15000);
    return () => clearInterval(id);
  }, [token, refresh, state?.scanning]);

  const toast = useCallback((text: string, action?: ToastAction) => {
    setToastMsg({ text, action });
    clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToastMsg(null), action ? 10000 : 3200);
  }, []);

  const startScan = useCallback(
    async (path?: string) => {
      try {
        await api("/api/scan", path ? { path } : {});
        wasScanning.current = true;
        await refresh();
      } catch (e) {
        toast(errorText(e));
      }
    },
    [refresh, toast],
  );

  if (token === undefined) return null;
  if (!token || (error && !state)) {
    return (
      <main className="gate">
        <div className="card">
          <h2>Open Tidyfleet from the terminal</h2>
          <p className="subtle">
            This page only works from the private link that <code>tidyfleet ui</code> opens. Run it again to get a fresh link.
          </p>
          {error && <p className="msg error">{error}</p>}
        </div>
      </main>
    );
  }

  return (
    <AppCtx.Provider value={{ state, refresh, startScan, treeVersion, bumpTree: () => setTreeVersion((v) => v + 1), toast }}>
      {children}
      {toastMsg && (
        <div className="toast" role="status">
          {toastMsg.text}
          {toastMsg.action && (
            <button
              className="toast-action"
              onClick={() => {
                toastMsg.action!.run();
                setToastMsg(null);
              }}
            >
              {toastMsg.action.label}
            </button>
          )}
        </div>
      )}
    </AppCtx.Provider>
  );
}
