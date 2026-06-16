import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite config. Env vars are read at build/dev time from `.env*` files and the
// shell, and are exposed to client code only when prefixed with `VITE_`
// (see src/config.ts). This keeps with Twelve-Factor: config lives in the env,
// never hardcoded in source.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // The dev server proxies nothing by default; the app talks directly to the
    // Go gateway at VITE_API_BASE / VITE_WS_URL. Add a proxy here if you want to
    // avoid CORS during local dev.
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
});
