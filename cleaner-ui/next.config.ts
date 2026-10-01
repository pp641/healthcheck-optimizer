import type { NextConfig } from "next";

// Exported as static files and embedded in the Go agent binary, which serves
// them on 127.0.0.1 together with the local API. No Node at runtime.
const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
  poweredByHeader: false,
};

export default nextConfig;
