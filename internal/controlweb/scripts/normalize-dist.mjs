import { readFileSync, writeFileSync } from "node:fs";

const expectedFiles = [
  "dist/index.html",
  "dist/assets/app.css",
  "dist/assets/app.js",
  "dist/assets/react.js",
  "dist/assets/tanstack-query.js"
];

for (const path of expectedFiles) {
  const source = readFileSync(path, "utf8");

  if (source.length === 0 || source.includes("\r") || source.charCodeAt(0) === 0xfeff) {
    throw new Error(`generated asset is not canonical UTF-8 text: ${path}`);
  }

  writeFileSync(path, `${source.replace(/\n+$/u, "")}\n`, "utf8");
}

