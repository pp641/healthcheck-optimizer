export type Level = "good" | "warning" | "critical" | "unknown";

const color: Record<Level, string> = {
  good: "var(--good)",
  warning: "var(--warning)",
  critical: "var(--critical)",
  unknown: "var(--muted)",
};

/** Status is always an icon plus a label, so it never relies on color alone. */
export function StatusIcon({ level, title }: { level: Level; title?: string }) {
  return (
    <svg className="status-icon" viewBox="0 0 16 16" aria-hidden={title ? undefined : true} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      {level === "good" && (
        <>
          <circle cx="8" cy="8" r="7" fill={color.good} />
          <path d="M4.8 8.2l2.1 2.1 4.3-4.4" fill="none" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </>
      )}
      {level === "warning" && (
        <>
          <path d="M8 1.5l6.8 12H1.2z" fill={color.warning} strokeLinejoin="round" />
          <path d="M8 6v3.5" stroke="#0b0b0b" strokeWidth="1.6" strokeLinecap="round" />
          <circle cx="8" cy="11.6" r="0.9" fill="#0b0b0b" />
        </>
      )}
      {level === "critical" && (
        <>
          <circle cx="8" cy="8" r="7" fill={color.critical} />
          <path d="M8 4.5v4.2" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" />
          <circle cx="8" cy="11.3" r="1" fill="#fff" />
        </>
      )}
      {level === "unknown" && <circle cx="8" cy="8" r="6.2" fill="none" stroke={color.unknown} strokeWidth="1.6" strokeDasharray="2.5 2" />}
    </svg>
  );
}

export function Status({ level, children }: { level: Level; children: React.ReactNode }) {
  return (
    <span className="status">
      <StatusIcon level={level} />
      <span>{children}</span>
    </span>
  );
}
