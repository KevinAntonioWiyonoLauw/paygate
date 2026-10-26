import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // This dashboard is entirely client-fetched behind an auth cookie: there is no
  // server-rendered data worth caching, and `cacheComponents` would force every
  // usePathname/useSearchParams consumer into a Suspense boundary for no gain.
  cacheComponents: false,
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  // Proxy the gateway through the Next server so the session cookie is
  // first-party. Without this the browser would treat it as cross-site and
  // SameSite=Lax would drop it on navigation.
  async rewrites() {
    const target = process.env.PAYGATE_BACKEND ?? "http://127.0.0.1:8787";
    return [{ source: "/api/:path*", destination: `${target}/api/:path*` }];
  },
};

export default nextConfig;
