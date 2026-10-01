import type { Metadata } from "next";

import { AppProvider } from "@/components/app-context";
import { Header } from "@/components/header";

import "./globals.css";

export const metadata: Metadata = {
  title: "Tidyfleet Cleaner",
  description: "Find and safely remove old build output and caches.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <AppProvider>
          <Header />
          <main>{children}</main>
        </AppProvider>
      </body>
    </html>
  );
}
