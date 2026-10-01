import type { Metadata } from "next";

import { CopyButton, OrgForm, RotateCodeForm, SlackForm } from "@/components/forms";
import { api } from "@/lib/api";
import { dateTime } from "@/lib/format";
import type { Me, Org } from "@/lib/types";

export const metadata: Metadata = { title: "Settings" };

export default async function SettingsPage() {
  const [org, me] = await Promise.all([api<Org>("/api/v1/org"), api<Me>("/api/v1/me")]);
  const isAdmin = me.user.role === "admin";
  const serverURL = process.env.PUBLIC_SERVER_URL || "<server-url>";
  const enrollCmd = `tidyfleet enroll ${serverURL} ${org.enroll_code}`;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Settings</h1>
          <p className="subtle">
            {org.name} · {org.plan} plan · created {dateTime(org.created_at)}
          </p>
        </div>
      </div>

      {!isAdmin && <p className="card subtle">You have view-only access. Ask an admin to change these settings.</p>}

      {isAdmin && (
        <div className="stack">
          <section className="card stack">
            <div>
              <h2>Enroll laptops</h2>
              <p className="subtle">Anyone with this code can add a laptop to {org.name}. Rotate it if it leaks; enrolled laptops keep working.</p>
            </div>
            <div className="code-box">
              <span style={{ flex: 1 }}>{org.enroll_code}</span>
              <CopyButton text={org.enroll_code} />
            </div>
            <div>
              <p className="subtle" style={{ marginBottom: 6 }}>
                On each laptop, with the agent installed:
              </p>
              <div className="code-box" style={{ fontSize: 13, letterSpacing: 0 }}>
                <span style={{ flex: 1 }}>{enrollCmd}</span>
                <CopyButton text={enrollCmd} />
              </div>
            </div>
            <RotateCodeForm />
          </section>

          <section className="card stack">
            <div>
              <h2>Organization and reporting</h2>
              <p className="subtle">These are the only agent settings you control. Laptops pick up changes on their next report.</p>
            </div>
            <OrgForm org={org} />
          </section>

          <section className="card stack">
            <div>
              <h2>Slack alerts</h2>
              <p className="subtle">
                Posts when an alert opens or resolves. Create an{" "}
                <a href="https://api.slack.com/messaging/webhooks" target="_blank" rel="noreferrer">
                  incoming webhook
                </a>{" "}
                for the channel you want.
              </p>
            </div>
            <SlackForm org={org} />
          </section>
        </div>
      )}

      <section className="section card">
        <h2>Privacy</h2>
        <div className="privacy" style={{ marginTop: 10 }}>
          <div>
            <h3>You can see</h3>
            <ul>
              <li>Disk used and free, and one total for reclaimable dev space</li>
              <li>Memory, CPU load and uptime</li>
              <li>Battery health and cycle count</li>
              <li>OS version, pending updates, disk encryption and firewall status</li>
            </ul>
          </div>
          <div>
            <h3>You can never see</h3>
            <ul>
              <li>File or folder names and paths</li>
              <li>The disk usage tree or what is using space</li>
              <li>Cleanup history, or which folders are scanned or cleaned</li>
              <li>Employees&apos; cleaner settings</li>
            </ul>
          </div>
        </div>
      </section>
    </>
  );
}
