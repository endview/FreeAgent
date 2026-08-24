import { assertCatalogCompatibility, type MessageCatalogs } from "./core.ts";
import { enUSMessages } from "./catalogs/en-US.ts";
import { zhCNMessages } from "./catalogs/zh-CN.ts";

export const i18nResources = {
  "en-US": enUSMessages,
  "zh-CN": zhCNMessages
} as const satisfies MessageCatalogs;

assertCatalogCompatibility(i18nResources);
