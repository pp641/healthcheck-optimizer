"use client";

import { useEffect, useState } from "react";

import { useApp } from "@/components/app-context";
import { api, errorText } from "@/lib/api";
import { bytes, dateTime, tilde } from "@/lib/format";
import type { LogEntry } from "@/lib/types";

export default function LogPage() {
  const { state, treeVersion } = useApp();
  const [entries, setEntries] = useState<LogEntry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api<{ entries: LogEntry[] }>("/api/log")
      .then((r) => setEntries(r.entries))
      .catch((e) => setErr(errorText(e)));
  }, [treeVersion]);

  const home = state?.home ?? null;
  const where = (e: LogEntry) => {
    if (!e.trashed_to) return e.method === "permanent" ? "Deleted" : e.method === "command" ? "Tool command" : "";
    return e.trashed_to === "Trash" || e.trashed_to === "Recycle Bin" ? e.trashed_to : `Trash: ${tilde(e.trashed_to, home)}`;
  };

  return (
    <>
      <div className="section-head">
        <h2>Cleanup log</h2>
        <p className="subtle">Everything Tidyfleet has removed, newest first. Items moved to the Trash can be restored from there.</p>
      </div>
      <div className="card table-wrap">
        {err && <p className="empty msg error">{err}</p>}
        {!err && entries?.length === 0 && <p className="empty">Nothing cleaned yet.</p>}
        {entries && entries.length > 0 && (
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>Result</th>
                <th>Item</th>
                <th>Went to</th>
                <th className="num">Size</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e, i) => (
                <tr key={i}>
                  <td style={{ whiteSpace: "nowrap" }}>{dateTime(e.time)}</td>
                  <td>{e.ok ? "Cleaned" : <span className="msg error">Failed: {e.error}</span>}</td>
                  <td className="path">{e.path ? tilde(e.path, home) : e.rule_id}</td>
                  <td className="path subtle">{where(e)}</td>
                  <td className="num">{bytes(e.bytes)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
