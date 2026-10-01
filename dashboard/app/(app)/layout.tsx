import { signOut } from "@/app/actions";
import { Nav } from "@/components/nav";
import { api } from "@/lib/api";
import type { FleetSummary, Me } from "@/lib/types";

export default async function AppLayout({ children }: { children: React.ReactNode }) {
  const [me, fleet] = await Promise.all([api<Me>("/api/v1/me"), api<{ summary: FleetSummary }>("/api/v1/fleet")]);
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark" aria-hidden>
            T
          </span>
          Tidyfleet
        </div>
        <div className="org-name hide-sm">{me.org_name}</div>
        <Nav openAlerts={fleet.summary.open_alerts} />
        <div className="sidebar-foot">
          <span className="hide-sm" title={me.user.role}>
            {me.user.email}
          </span>
          <form action={signOut}>
            <button className="btn link" type="submit">
              Sign out
            </button>
          </form>
        </div>
      </aside>
      <main className="main">{children}</main>
    </div>
  );
}
