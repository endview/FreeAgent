// Runs before React mounts. Only non-secret, explicitly named preferences are read.
try {
  const native = (
    window as Window & { __freeagentInitialView?: { theme?: string } }
  ).__freeagentInitialView;
  const theme =
    native?.theme || localStorage.getItem("freeagent.theme") || "system";
  document.documentElement.classList.toggle(
    "dark",
    theme === "dark" ||
      (theme === "system" && matchMedia("(prefers-color-scheme:dark)").matches),
  );
} catch {
  document.documentElement.classList.toggle(
    "dark",
    matchMedia("(prefers-color-scheme:dark)").matches,
  );
}

const fragment = new URLSearchParams(location.hash.slice(1));
let launchKey = "";
if (location.protocol !== "https:") {
  try {
    launchKey =
      fragment.get("key") || sessionStorage.getItem("freeagent-key") || "";
    if (launchKey) sessionStorage.setItem("freeagent-key", launchKey);
  } catch {
    launchKey = fragment.get("key") || "";
  }
}
if (fragment.has("key")) {
  fragment.delete("key");
  history.replaceState(
    history.state,
    "",
    location.pathname +
      location.search +
      (fragment.size ? "#" + fragment.toString() : ""),
  );
}
export function consumeLaunchKey(): string {
  const key = launchKey;
  launchKey = "";
  return key;
}
