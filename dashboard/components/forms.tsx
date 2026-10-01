"use client";

import { useActionState, useState } from "react";
import { useFormStatus } from "react-dom";

import {
  createRule,
  deleteRule,
  removeDevice,
  rotateCode,
  signIn,
  testSlack,
  updateOrg,
  updateRule,
  type FormState,
} from "@/app/actions";
import type { AlertRule, MetricDef, Org } from "@/lib/types";

function Submit({ children, className = "btn primary", pendingText }: { children: React.ReactNode; className?: string; pendingText?: string }) {
  const { pending } = useFormStatus();
  return (
    <button className={className} type="submit" disabled={pending}>
      {pending && pendingText ? pendingText : children}
    </button>
  );
}

function Msg({ state }: { state: FormState }) {
  if (state.error) return <p className="msg error" role="alert">{state.error}</p>;
  if (state.ok) return <p className="msg ok" role="status">{state.ok}</p>;
  return null;
}

export function LoginForm() {
  const [state, action] = useActionState(signIn, {});
  return (
    <form action={action} className="stack">
      <label>
        Email
        <input name="email" type="email" autoComplete="username" required autoFocus={!state.email} defaultValue={state.email} />
      </label>
      <label>
        Password
        <input name="password" type="password" autoComplete="current-password" required autoFocus={!!state.email} />
      </label>
      <Msg state={state} />
      <Submit pendingText="Signing in…">Sign in</Submit>
    </form>
  );
}

export function OrgForm({ org }: { org: Org }) {
  const [state, action] = useActionState(updateOrg, {});
  return (
    <form action={action} className="stack">
      <div className="form-grid">
        <label>
          Organization name
          <input name="name" defaultValue={org.name} required maxLength={120} />
        </label>
        <label>
          Reporting interval
          <select name="report_interval_minutes" defaultValue={String(org.report_interval_minutes)}>
            {[15, 30, 60, 120, 240, 720, 1440].map((m) => (
              <option key={m} value={m}>
                {m < 60 ? `Every ${m} minutes` : m === 60 ? "Every hour" : m === 1440 ? "Once a day" : `Every ${m / 60} hours`}
              </option>
            ))}
          </select>
        </label>
      </div>
      <input type="hidden" name="allow_ai_present" value="1" />
      <label className="check">
        <input type="checkbox" name="allow_ai" defaultChecked={org.allow_ai} />
        Allow the AI assistant on enrolled laptops
      </label>
      <div className="row">
        <Submit pendingText="Saving…">Save</Submit>
        <Msg state={state} />
      </div>
    </form>
  );
}

export function SlackForm({ org }: { org: Org }) {
  const [state, action] = useActionState(updateOrg, {});
  const [testState, testAction] = useActionState(testSlack, {});
  return (
    <div className="stack">
      <form action={action} className="stack">
        <label>
          Slack incoming webhook URL
          <input
            name="slack_webhook_url"
            type="url"
            defaultValue={org.slack_webhook_url}
            placeholder="https://hooks.slack.com/services/…"
            pattern="https://hooks\.slack\.com/.*"
            title="Slack webhook URLs start with https://hooks.slack.com/"
          />
        </label>
        <div className="row">
          <Submit pendingText="Saving…">Save webhook</Submit>
          <Msg state={state} />
        </div>
      </form>
      {org.slack_webhook_url && (
        <form action={testAction} className="row">
          <Submit className="btn" pendingText="Sending…">Send test message</Submit>
          <Msg state={testState} />
        </form>
      )}
    </div>
  );
}

export function RotateCodeForm() {
  const [state, action] = useActionState(rotateCode, {});
  return (
    <form
      action={action}
      className="row"
      onSubmit={(e) => {
        if (!confirm("Create a new enrollment code? The current code will stop working for new laptops.")) e.preventDefault();
      }}
    >
      <Submit className="btn" pendingText="Rotating…">Rotate code</Submit>
      <Msg state={state} />
    </form>
  );
}

export function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="btn small"
      onClick={async () => {
        await navigator.clipboard.writeText(text);
        setDone(true);
        setTimeout(() => setDone(false), 1500);
      }}
    >
      {done ? "Copied" : "Copy"}
    </button>
  );
}

export function RemoveDeviceForm({ id, hostname }: { id: string; hostname: string }) {
  const [state, action] = useActionState(removeDevice, {});
  return (
    <form
      action={action}
      className="row"
      onSubmit={(e) => {
        if (!confirm(`Remove ${hostname}? Its history is deleted and the laptop stops reporting until it is enrolled again.`)) e.preventDefault();
      }}
    >
      <input type="hidden" name="id" value={id} />
      <Submit className="btn danger" pendingText="Removing…">Remove device</Submit>
      <Msg state={state} />
    </form>
  );
}

function opLabel(ops: Record<string, string>, op: string) {
  return ops[op] ?? op;
}

export function RuleRow({ rule, metrics, ops, canEdit }: { rule: AlertRule; metrics: MetricDef[]; ops: Record<string, string>; canEdit: boolean }) {
  const [state, action] = useActionState(updateRule, {});
  const [delState, delAction] = useActionState(deleteRule, {});
  const def = metrics.find((m) => m.key === rule.metric);
  const condition = def?.bool
    ? `${def.label.replace(/ on$/, "")} is ${rule.threshold === 1 ? "on" : "off"}`
    : `${def?.label ?? rule.metric} ${opLabel(ops, rule.op)}`;
  return (
    <tr>
      <td>
        <div className="host">{rule.name}</div>
        <Msg state={state.error ? state : delState} />
      </td>
      <td>{condition}</td>
      <td className="num">
        {def?.bool ? (
          "—"
        ) : canEdit ? (
          <form action={action} className="row" style={{ justifyContent: "flex-end" }}>
            <input type="hidden" name="id" value={rule.id} />
            <input
              name="threshold"
              type="number"
              step="any"
              defaultValue={rule.threshold}
              aria-label={`Threshold for ${rule.name}`}
              style={{ width: 90, textAlign: "right" }}
            />
            <span className="subtle">{def?.unit}</span>
            <Submit className="btn small" pendingText="…">Save</Submit>
          </form>
        ) : (
          `${rule.threshold}${def?.unit ?? ""}`
        )}
      </td>
      <td>
        {canEdit ? (
          <form action={action}>
            <input type="hidden" name="id" value={rule.id} />
            <input type="hidden" name="enabled" value={rule.enabled ? "false" : "true"} />
            <button className="btn link" type="submit" aria-label={`${rule.enabled ? "Turn off" : "Turn on"} ${rule.name}`}>
              {rule.enabled ? "On" : "Off"}
            </button>
          </form>
        ) : rule.enabled ? (
          "On"
        ) : (
          "Off"
        )}
      </td>
      {canEdit && (
        <td className="num">
          <form
            action={delAction}
            onSubmit={(e) => {
              if (!confirm(`Delete the rule "${rule.name}" and its alerts?`)) e.preventDefault();
            }}
          >
            <input type="hidden" name="id" value={rule.id} />
            <Submit className="btn link danger">Delete</Submit>
          </form>
        </td>
      )}
    </tr>
  );
}

export function NewRuleForm({ metrics, ops }: { metrics: MetricDef[]; ops: Record<string, string> }) {
  const [state, action] = useActionState(createRule, {});
  const [metric, setMetric] = useState(metrics[0]?.key ?? "");
  const def = metrics.find((m) => m.key === metric);
  return (
    <form action={action} className="stack">
      <div className="form-grid">
        <label>
          Name
          <input name="name" required placeholder="e.g. Low free space" maxLength={120} />
        </label>
        <label>
          Metric
          <select name="metric" value={metric} onChange={(e) => setMetric(e.target.value)}>
            {metrics.map((m) => (
              <option key={m.key} value={m.key}>
                {m.label}
                {m.unit ? ` (${m.unit})` : ""}
              </option>
            ))}
          </select>
        </label>
        {def?.bool ? (
          <>
            <input type="hidden" name="op" value="eq" />
            <label>
              Alert when
              <select name="threshold" defaultValue="0">
                <option value="0">it is off</option>
                <option value="1">it is on</option>
              </select>
            </label>
          </>
        ) : (
          <>
            <label>
              Condition
              <select name="op" defaultValue="gt">
                {Object.entries(ops).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Threshold{def?.unit ? ` (${def.unit})` : ""}
              <input name="threshold" type="number" step="any" required />
            </label>
          </>
        )}
      </div>
      <div className="row">
        <Submit pendingText="Adding…">Add rule</Submit>
        <Msg state={state} />
      </div>
    </form>
  );
}
