import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { katexWoff2Only } from "./scripts/katex-fonts.mjs";

const frontend = fileURLToPath(new URL("./frontend", import.meta.url));
export default defineConfig(({ mode }) => ({
  root: frontend,
  publicDir: resolve(frontend, "../public"),
  resolve: { alias: { "@": frontend } },
  plugins: [
    react(),
    tailwindcss(),
    katexWoff2Only(),
    {
      name: "freeagent-development-marker",
      generateBundle() {
        if (mode === "development")
          this.emitFile({
            type: "asset",
            fileName: "freeagent-development-ui.json",
            source: '{"development_ui":true}\n',
          });
      },
    },
  ],
  build: {
    outDir: resolve(
      frontend,
      mode === "development" ? "../.next-dist" : "../dist",
    ),
    emptyOutDir: true,
    assetsInlineLimit: 0,
    target: ["chrome111", "edge111", "firefox128", "safari16.4"],
    sourcemap: mode === "development",
  },
}));
