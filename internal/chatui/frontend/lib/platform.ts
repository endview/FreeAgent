function allowedExternalURL(raw: string) {
  try {
    const value = new URL(raw, document.baseURI);
    return (
      !value.username &&
      !value.password &&
      ((["http:", "https:"].includes(value.protocol) &&
        Boolean(value.hostname)) ||
        (value.protocol === "mailto:" && Boolean(value.pathname)))
    );
  } catch {
    return false;
  }
}
export interface ViewPreferences {
  theme: "light" | "dark" | "system";
  sidebar: number;
  reading: number;
  composer: number;
}
declare global {
  interface Window {
    faOpenExternal?: (url: string) => Promise<void>;
    faSetBusy?: (busy: boolean) => Promise<void>;
    faSetTheme?: (theme: string) => Promise<void>;
    faChangeWorkspace?: () => Promise<{ changed?: boolean }>;
    faSelectFolder?: (current: string) => Promise<string>;
    faSelectFile?: (current: string) => Promise<string>;
    faLoadView?: (environment: string) => Promise<string>;
    faSaveView?: (environment: string, value: string) => Promise<void>;
    faLoadDrafts?: (environment: string) => Promise<string>;
    faSaveDrafts?: (environment: string, value: string) => Promise<void>;
    faAttention?: () => Promise<void>;
  }
}
export const defaultView: ViewPreferences = {
  theme: "system",
  sidebar: 252,
  reading: 820,
  composer: 190,
};
export function validateView(value: Partial<ViewPreferences>): ViewPreferences {
  const clamp = (n: unknown, min: number, max: number, fallback: number) =>
    typeof n === "number" && Number.isFinite(n)
      ? Math.max(min, Math.min(max, n))
      : fallback;
  return {
    theme: ["light", "dark", "system"].includes(value.theme || "")
      ? value.theme!
      : "system",
    sidebar: clamp(value.sidebar, 208, 380, 252),
    reading: clamp(value.reading, 540, 1120, 820),
    composer: clamp(value.composer, 88, 380, 190),
  };
}
export async function loadView(environment: string): Promise<ViewPreferences> {
  try {
    const raw = window.faLoadView
      ? await window.faLoadView(environment)
      : localStorage.getItem("freeagent.view:" + environment);
    return raw ? validateView(JSON.parse(raw)) : defaultView;
  } catch {
    return defaultView;
  }
}
export async function saveView(environment: string, value: ViewPreferences) {
  try {
    if (window.faSaveView)
      await window.faSaveView(environment, JSON.stringify(value));
    else
      localStorage.setItem(
        "freeagent.view:" + environment,
        JSON.stringify(value),
      );
  } catch {
    /* Optional device storage failure leaves the live layout usable. */
  }
}
export function applyTheme(theme: ViewPreferences["theme"]) {
  const dark =
    theme === "dark" ||
    (theme === "system" && matchMedia("(prefers-color-scheme:dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
  try {
    localStorage.setItem("freeagent.theme", theme);
  } catch {}
  void window.faSetTheme?.(theme).catch(() => {});
}
export function installPlatform() {
  const prevent = (event: DragEvent) => {
    if (event.dataTransfer?.types.includes("Files")) event.preventDefault();
  };
  window.addEventListener("dragover", prevent);
  window.addEventListener("drop", prevent);
  const openLink = (event: MouseEvent) => {
    const anchor = (event.target as Element)?.closest<HTMLAnchorElement>(
      "a[href]",
    );
    if (!anchor || !window.faOpenExternal) return;
    if (anchor.download || anchor.hasAttribute("data-internal")) return;
    const url = allowedExternalURL(anchor.href) ? anchor.href : "";
    if (!url) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    void window.faOpenExternal(url).catch(() => {});
  };
  document.addEventListener("click", openLink, true);
  document.addEventListener(
    "auxclick",
    (event) => {
      if (event.button === 1) openLink(event);
    },
    true,
  );
  if (window.visualViewport) {
    const update = () =>
      document.documentElement.style.setProperty(
        "--visible-height",
        `${window.visualViewport!.height}px`,
      );
    window.visualViewport.addEventListener("resize", update);
    update();
  }
}
