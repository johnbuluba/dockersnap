import { defineConfig } from "vite";
import preact from "@preact/preset-vite";
import tailwindcss from "@tailwindcss/vite";

// The dashboard is served by the dockersnap daemon under /ui/. Vite's `base`
// matches that so asset URLs in the built bundle resolve correctly when
// embedded behind the daemon's HTTP layer.
export default defineConfig({
  base: "/ui/",
  plugins: [preact(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2022",
  },
  server: {
    // `task ui:dev` runs Vite directly; we proxy /api/* to a running daemon
    // so React Query can hit the real backend during development without CORS.
    port: 5173,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:9847",
        changeOrigin: true,
      },
    },
  },
});
