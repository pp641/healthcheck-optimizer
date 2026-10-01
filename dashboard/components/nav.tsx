"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const links = [
  { href: "/", label: "Fleet" },
  { href: "/alerts", label: "Alerts" },
  { href: "/settings", label: "Settings" },
];

export function Nav({ openAlerts }: { openAlerts: number }) {
  const path = usePathname();
  return (
    <nav className="nav" aria-label="Main">
      {links.map((l) => {
        const active = l.href === "/" ? path === "/" || path.startsWith("/devices") : path.startsWith(l.href);
        return (
          <Link key={l.href} href={l.href} aria-current={active ? "page" : undefined}>
            {l.label}
            {l.href === "/alerts" && openAlerts > 0 && <span className="pill critical">{openAlerts}</span>}
          </Link>
        );
      })}
    </nav>
  );
}
