import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    assetsInlineLimit: 0,
    outDir: "dist",
    emptyOutDir: true,
  },
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: {
    // `make console-dev` proxies API calls to a local `factory console`.
    proxy: { "/api": { target: "http://127.0.0.1:7331", changeOrigin: false } },
  },
});
