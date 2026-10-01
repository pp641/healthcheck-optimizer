"use client";

import { useCallback, useEffect, useState } from "react";

import { useApp } from "@/components/app-context";
import { changeFolders, FolderPicker, type FolderListName } from "@/components/folder-picker";
import { api, errorText } from "@/lib/api";
import { tilde } from "@/lib/format";
import type { Config, Rule } from "@/lib/types";

type ConfigResp = {
  config: Config;
  config_dir: string;
  managed?: { org_name: string; allow_ai: boolean; report_interval_minutes: number };
};

function PathList({
  list,
  label,
  hint,
  paths,
  home,
  onChanged,
}: {
  list: FolderListName;
  label: string;
  hint: string;
  paths: string[];
  home: string | null;
  onChanged: () => void;
}) {
  const [picking, setPicking] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  return (
    <div className="settings-card card">
      <div className="section-head" style={{ justifyContent: "space-between", marginBottom: 0 }}>
        <div>
          <h3>{label}</h3>
          <p className="subtle">{hint}</p>
        </div>
        <button className="btn" onClick={() => setPicking(true)}>
          Add folder…
        </button>
      </div>
      {paths.length > 0 ? (
        <div className="path-list">
          {paths.map((p) => (
            <div className="path-item" key={p}>
              <span title={p}>{tilde(p, home)}</span>
              <button
                className="btn link"
                aria-label={`Remove ${p}`}
                onClick={async () => {
                  setErr(null);
                  try {
                    await changeFolders(list, "remove", p);
                    onChanged();
                  } catch (e) {
                    setErr(errorText(e));
                  }
                }}
              >
                Remove
              </button>
            </div>
          ))}
        </div>
      ) : (
        <p className="subtle">None yet.</p>
      )}
      {err && <p className="msg error">{err}</p>}
      {picking && (
        <FolderPicker
          list={list}
          title={label}
          onClose={(changed) => {
            setPicking(false);
            if (changed) onChanged();
          }}
        />
      )}
    </div>
  );
}

export default function SettingsPage() {
  const { state, startScan } = useApp();
  const [data, setData] = useState<ConfigResp | null>(null);
  const [rules, setRules] = useState<Rule[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);

  const load = useCallback(async () => {
    try {
      const [c, r] = await Promise.all([api<ConfigResp>("/api/config"), api<{ rules: Rule[] }>("/api/rules")]);
      setData(c);
      setRules(r.rules);
    } catch (e) {
      setErr(errorText(e));
    }
  }, []);
  useEffect(() => {
    load();
  }, [load]);

  async function set(key: string, value: string) {
    setErr(null);
    try {
      await api("/api/config", { key, value });
      setDirty(true);
      await load();
    } catch (e) {
      setErr(errorText(e));
    }
  }

  if (!data) return err ? <p className="msg error">{err}</p> : null;
  const c = data.config;
  const home = state?.home ?? null;
  const aiLocked = data.managed && !data.managed.allow_ai;
  const disabled = new Set(c.disabled_rules ?? []);

  return (
    <>
      {dirty && (
        <div className="banner" role="status">
          <span>Saved. Rescan to see the effect.</span>
          <button
            className="btn small primary"
            onClick={() => {
              setDirty(false);
              startScan();
            }}
          >
            Rescan now
          </button>
        </div>
      )}
      {err && (
        <p className="notice msg error" role="alert">
          {err}
        </p>
      )}
      <div className="settings-grid">
        <PathList
          list="scan_roots"
          label="Scan locations"
          hint="Folders where you keep code. Only these are measured and searched for build output."
          paths={c.scan_roots ?? []}
          home={home}
          onChanged={() => {
            setDirty(true);
            load();
          }}
        />
        <PathList
          list="exclude"
          label="Never touch"
          hint="Excluded folders are never read or cleaned. They show as locked in the tree."
          paths={c.exclude ?? []}
          home={home}
          onChanged={() => {
            setDirty(true);
            load();
          }}
        />

        <div className="settings-card card">
          <h3>Cleanup behavior</h3>
          <label className="field" style={{ maxWidth: 360 }}>
            Only clean projects idle for at least
            <select value={String(c.stale_days)} onChange={(e) => set("stale_days", e.target.value)}>
              {[0, 30, 60, 90, 180].map((d) => (
                <option key={d} value={d}>
                  {d === 0 ? "No minimum (not recommended)" : `${d} days`}
                </option>
              ))}
            </select>
          </label>
          <fieldset style={{ border: 0, padding: 0, margin: 0, display: "flex", flexDirection: "column", gap: 6 }}>
            <legend className="subtle" style={{ fontSize: 13, marginBottom: 4 }}>
              When cleaning
            </legend>
            <label className="check">
              <input type="radio" name="delete_mode" checked={c.delete_mode === "trash"} onChange={() => set("delete_mode", "trash")} />
              Move to Trash (recommended, can be restored)
            </label>
            <label className="check">
              <input type="radio" name="delete_mode" checked={c.delete_mode === "permanent"} onChange={() => set("delete_mode", "permanent")} />
              Delete permanently
            </label>
          </fieldset>
          <label className="check">
            <input type="checkbox" checked={c.clean_dirty_repos} onChange={(e) => set("clean_dirty_repos", String(e.target.checked))} />
            Also clean build output in repos with uncommitted changes (still only gitignored, regenerable folders)
          </label>
          <label className="field" style={{ maxWidth: 360 }}>
            Reminders
            <select value={c.schedule} onChange={(e) => set("schedule", e.target.value)}>
              <option value="manual">Off: I&apos;ll clean when I want</option>
              <option value="weekly">Weekly &quot;you can reclaim X GB&quot; notification</option>
            </select>
          </label>
          <label className="field" style={{ maxWidth: 360 }}>
            AI assistant
            <select value={aiLocked ? "off" : c.ai_mode} disabled={aiLocked} onChange={(e) => set("ai_mode", e.target.value)}>
              <option value="off">Off</option>
              <option value="local">Local model only</option>
              <option value="cloud">Cloud</option>
            </select>
            {aiLocked && <span className="managed">Managed by {data.managed!.org_name}</span>}
          </label>
        </div>

        <div className="settings-card card">
          <div>
            <h3>Cleanup rules</h3>
            <p className="subtle">
              Custom rules: add YAML files to <code>{tilde(data.config_dir, home)}/rules</code>.
            </p>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>On</th>
                  <th>Rule</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {rules.map((r) => (
                  <tr key={r.id}>
                    <td>
                      <input
                        type="checkbox"
                        checked={!disabled.has(r.id)}
                        aria-label={`Use rule ${r.name}`}
                        onChange={(e) => {
                          const next = new Set(disabled);
                          if (e.target.checked) next.delete(r.id);
                          else next.add(r.id);
                          set("disabled_rules", [...next].join(","));
                        }}
                      />
                    </td>
                    <td>
                      <strong>{r.name}</strong>
                      <div className="subtle">{r.description}</div>
                    </td>
                    <td className="subtle" style={{ whiteSpace: "nowrap" }}>
                      {r.status}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </>
  );
}
