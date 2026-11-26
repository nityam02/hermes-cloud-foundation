import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Relative base so the build works from any path, and so `hermes ui` can serve it from "/".
export default defineConfig({
  base: "./",
  plugins: [react()],
  server: {
    // `npm run dev` proxies the read-only API of a running `hermes ui` (default port).
    proxy: { "/api": "http://127.0.0.1:8787" },
  },
  build: { outDir: "dist", sourcemap: false },
  test: { environment: "node" },
});
