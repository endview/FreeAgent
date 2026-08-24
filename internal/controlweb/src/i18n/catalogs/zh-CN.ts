import type { DefaultMessageKey } from "./en-US.ts";

export const zhCNMessages = {
  "app.title": "FreeAgent 控制台",
  "locale.selector.label": "语言",
  "locale.name.en-US": "English",
  "locale.name.zh-CN": "简体中文"
} as const satisfies Readonly<Record<DefaultMessageKey, string>>;
