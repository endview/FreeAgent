import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { build } from "vite";

const project = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = await mkdtemp(join(tmpdir(), "freeagent-controlweb-test-"));

try {
  for (const name of ["controlweb", "modules"]) {
    const testOutput = join(output, name);
    await build({
      configFile: false,
      root: project,
      logLevel: "silent",
      ssr: { noExternal: true },
      build: {
        emptyOutDir: true,
        minify: false,
        outDir: testOutput,
        reportCompressedSize: false,
        sourcemap: false,
        ssr: resolve(project, `tests/${name}.test.tsx`),
        target: "node24",
        rollupOptions: {
          output: { entryFileNames: `${name}.test.mjs` }
        }
      }
    });
    await import(
      `${pathToFileURL(join(testOutput, `${name}.test.mjs`)).href}?run=${Date.now()}`
    );
  }
} finally {
  await rm(output, { force: true, recursive: true });
}
