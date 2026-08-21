import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";

const fail = (message) => {
  throw new Error(`control-web license manifest: ${message}`);
};
const exactKeys = (value, expected, label) => {
  const actual = Object.keys(value).sort();
  const ordered = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(ordered)) {
    fail(`${label} has unexpected properties`);
  }
};
const sha256 = (payload) => createHash("sha256").update(payload).digest("hex");
const noticeID = (key) => `npm-${sha256(Buffer.from(key)).slice(0, 24)}`;

if (process.argv.length !== 4) fail("expected package-lock and manifest paths");
const lockBytes = readFileSync(process.argv[2]);
const manifestBytes = readFileSync(process.argv[3]);
const lock = JSON.parse(lockBytes.toString("utf8"));
const manifest = JSON.parse(manifestBytes.toString("utf8"));

exactKeys(manifest, [
  "schema_version",
  "kind",
  "package_lock",
  "build_environment",
  "packages",
  "distributed_chunks"
], "manifest");
if (manifest.schema_version !== 1 || manifest.kind !== "freeagent-npm-dependency-licenses") {
  fail("header is invalid");
}
exactKeys(manifest.package_lock, ["path", "sha256", "lockfile_version"], "package_lock");
if (
  manifest.package_lock.path !== "internal/controlweb/package-lock.json" ||
  manifest.package_lock.lockfile_version !== 3 ||
  manifest.package_lock.sha256 !== sha256(lockBytes) ||
  lock.lockfileVersion !== 3
) {
  fail("package-lock binding is invalid");
}
exactKeys(manifest.build_environment, [
  "node_version",
  "npm_version",
  "registry",
  "install_scripts"
], "build_environment");
if (
  manifest.build_environment.node_version !== "24.19.0" ||
  manifest.build_environment.npm_version !== "12.0.2" ||
  manifest.build_environment.registry !== "https://registry.npmjs.org/" ||
  manifest.build_environment.install_scripts !== "disabled"
) {
  fail("build environment is invalid");
}

const expectedPackages = new Map();
for (const [location, entry] of Object.entries(lock.packages)) {
  if (location === "") continue;
  const marker = "node_modules/";
  const name = location.slice(location.lastIndexOf(marker) + marker.length);
  const key = `${name}@${entry.version}`;
  if (expectedPackages.has(key)) fail(`duplicate lock identity ${key}`);
  expectedPackages.set(key, {
    name,
    version: entry.version,
    dependency_kind: entry.dev === true ? "build" : "runtime",
    optional: entry.optional === true,
    resolved: entry.resolved,
    integrity: entry.integrity,
    declared_license_expression: entry.license,
    source_url: entry.resolved,
    notice_id: noticeID(key)
  });
}
if (!Array.isArray(manifest.packages) || manifest.packages.length !== expectedPackages.size) {
  fail("package set size differs from package-lock");
}
let previousKey = "";
const reportedPackages = new Map();
for (const entry of manifest.packages) {
  exactKeys(entry, [
    "name",
    "version",
    "dependency_kind",
    "optional",
    "resolved",
    "integrity",
    "declared_license_expression",
    "source_url",
    "notice_id",
    "required_files"
  ], "package");
  const key = `${entry.name}@${entry.version}`;
  if (key <= previousKey) fail(`packages are not strictly sorted at ${key}`);
  previousKey = key;
  const expected = expectedPackages.get(key);
  if (expected === undefined) fail(`package is absent from package-lock: ${key}`);
  for (const property of Object.keys(expected)) {
    if (entry[property] !== expected[property]) fail(`${key} differs at ${property}`);
  }
  if (!Array.isArray(entry.required_files) || entry.required_files.length !== 1) {
    fail(`${key} must bind one exact legal file`);
  }
  const legal = entry.required_files[0];
  exactKeys(legal, ["path", "role", "size", "sha256"], `${key} legal file`);
  if (
    typeof legal.path !== "string" ||
    !legal.path.startsWith("third_party/npm/") ||
    legal.role !== "license" ||
    !Number.isSafeInteger(legal.size) || legal.size < 1 ||
    !/^[0-9a-f]{64}$/u.test(legal.sha256)
  ) {
    fail(`${key} legal-file binding is invalid`);
  }
  reportedPackages.set(key, entry);
}
for (const key of expectedPackages.keys()) {
  if (!reportedPackages.has(key)) fail(`package is missing: ${key}`);
}
const runtimePackages = [...reportedPackages.values()]
  .filter((entry) => entry.dependency_kind === "runtime")
  .map((entry) => `${entry.name}@${entry.version}`)
  .sort();
const expectedRuntime = [
  "@tanstack/query-core@5.101.4",
  "@tanstack/react-query@5.101.4",
  "react-dom@19.2.8",
  "react@19.2.8",
  "scheduler@0.27.0"
].sort();
if (JSON.stringify(runtimePackages) !== JSON.stringify(expectedRuntime)) {
  fail("runtime closure is not exact");
}

const expectedChunks = new Map([
  ["internal/controlweb/dist/assets/react.js", [
    "react-dom@19.2.8",
    "react@19.2.8",
    "scheduler@0.27.0"
  ]],
  ["internal/controlweb/dist/assets/tanstack-query.js", [
    "@tanstack/query-core@5.101.4",
    "@tanstack/react-query@5.101.4"
  ]]
]);
if (!Array.isArray(manifest.distributed_chunks) || manifest.distributed_chunks.length !== expectedChunks.size) {
  fail("distributed chunk set is invalid");
}
let previousChunk = "";
for (const chunk of manifest.distributed_chunks) {
  exactKeys(chunk, ["path", "sha256", "packages"], "distributed chunk");
  if (chunk.path <= previousChunk) fail("distributed chunks are not strictly sorted");
  previousChunk = chunk.path;
  const packages = expectedChunks.get(chunk.path);
  if (packages === undefined || JSON.stringify(chunk.packages) !== JSON.stringify(packages)) {
    fail(`distributed chunk membership differs: ${chunk.path}`);
  }
  const projectRelative = chunk.path.replace(/^internal\/controlweb\//u, "");
  if (chunk.sha256 !== sha256(readFileSync(projectRelative))) {
    fail(`distributed chunk differs: ${chunk.path}`);
  }
}

console.log(`PASS control-web license manifest: ${reportedPackages.size} npm packages; ${expectedChunks.size} third-party chunks`);
