import type { Metadata } from "next";

import { LoginForm } from "@/components/forms";

export const metadata: Metadata = { title: "Sign in" };

export default function LoginPage() {
  return (
    <main className="login">
      <div className="card stack">
        <div className="brand" style={{ padding: 0 }}>
          <span className="brand-mark" aria-hidden>
            T
          </span>
          Tidyfleet
        </div>
        <div>
          <h1>Sign in</h1>
          <p className="subtle">Your team&apos;s laptop health dashboard.</p>
        </div>
        <LoginForm />
      </div>
    </main>
  );
}
