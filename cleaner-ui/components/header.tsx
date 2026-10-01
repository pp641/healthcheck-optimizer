"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { useApp } from "@/components/app-context";
import { relTime } from "@/lib/format";

const tabs = [
  { href: "/", label: "Disk" },
  { href: "/log/", label: "Cleanup log" },
  { href: "/settings/", label: "Settings" },
  { href: "/sharing/", label: "Health & sharing" },
];

export function Header() {
  const path = usePathname();
  const { state, startScan } = useApp();
  const norm = path.endsWith("/") ? path : path + "/";
  if (norm.startsWith("/explore/")) return null; // the big view has its own toolbar
  return (
    <header className="top">
      <div className="brand">
        <span className="brand-mark" aria-hidden>
          T
        </span>
        Tidyfleet <span className="tag">Cleaner</span>
      </div>
      <nav className="tabs" aria-label="Sections">
        {tabs.map((t) => (
          <Link key={t.href} href={t.href} className="tab-link" aria-current={norm === t.href ? "page" : undefined}>
            {t.label}
          </Link>
        ))}
      </nav>
      <div className="scan-status" aria-live="polite">
        {state?.scanning ? (
          <>
            <span className="spinner" aria-hidden />
            Scanning… {state.visited.toLocaleString()} folders
          </>
        ) : (
          <>
            {state?.scan_error ? <span className="msg error">Scan failed: {state.scan_error}</span> : <span>Scanned {relTime(state?.scanned_at)}</span>}
            <button className="btn small" onClick={() => startScan()}>
              Rescan
            </button>
          </>
        )}
      </div>
    </header>
  );
}
