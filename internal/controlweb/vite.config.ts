import { defineConfig, type Plugin } from "vite";

const runtimeChunkByPackage = new Map<string, string>([
  ["@tanstack/query-core", "tanstack-query"],
  ["@tanstack/react-query", "tanstack-query"],
  ["react", "react"],
  ["react-dom", "react"],
  ["scheduler", "react"]
]);

function packageNameForModule(id: string): string | undefined {
  const normalized = id.replaceAll("\\", "/");
  const marker = "/node_modules/";
  const markerIndex = normalized.lastIndexOf(marker);
  if (markerIndex < 0) return undefined;
  const parts = normalized.slice(markerIndex + marker.length).split("/");
  if (parts.length === 0 || parts[0] === "") return undefined;
  return parts[0].startsWith("@") && parts.length >= 2
    ? `${parts[0]}/${parts[1]}`
    : parts[0];
}

function runtimeChunkForModule(id: string): string | undefined {
  const packageName = packageNameForModule(id);
  return packageName === undefined
    ? undefined
    : runtimeChunkByPackage.get(packageName);
}

function verifyRuntimeChunkOwnership(): Plugin {
  return {
    name: "freeagent-runtime-chunk-ownership",
    generateBundle(_options, bundle) {
      const observed = new Set<string>();
      for (const output of Object.values(bundle)) {
        if (output.type !== "chunk") continue;
        for (const moduleID of Object.keys(output.modules)) {
          const packageName = packageNameForModule(moduleID);
          if (packageName === undefined) continue;
          const expectedChunk = runtimeChunkByPackage.get(packageName);
          if (expectedChunk === undefined) {
            this.error(`unexpected emitted runtime package: ${packageName}`);
          }
          const expectedFile = `assets/${expectedChunk}.js`;
          if (output.fileName !== expectedFile) {
            this.error(
              `${packageName} emitted in ${output.fileName}; expected ${expectedFile}`
            );
          }
          observed.add(packageName);
        }
      }
      for (const packageName of runtimeChunkByPackage.keys()) {
        if (!observed.has(packageName)) {
          this.error(`runtime package did not emit any module: ${packageName}`);
        }
      }
    }
  };
}

export default defineConfig({
  base: "/control/ui/",
  plugins: [verifyRuntimeChunkOwnership()],
  build: {
    assetsInlineLimit: 0,
    copyPublicDir: false,
    cssCodeSplit: false,
    emptyOutDir: true,
    manifest: false,
    minify: false,
    modulePreload: false,
    outDir: "dist",
    reportCompressedSize: false,
    rollupOptions: {
      output: {
        assetFileNames: (assetInfo) =>
          assetInfo.names.some((name) => name.endsWith(".css"))
            ? "assets/app.css"
            : "assets/[name][extname]",
        chunkFileNames: "assets/[name].js",
        entryFileNames: "assets/app.js",
        manualChunks: runtimeChunkForModule
      }
    },
    sourcemap: false,
    target: "es2022"
  }
});
