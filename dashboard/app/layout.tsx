import type { Metadata } from "next";

import "./globals.css";

export const metadata: Metadata = {
  title: { default: "Tidyfleet", template: "%s · Tidyfleet" },
  description: "Laptop health for small teams, without the monitoring.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
