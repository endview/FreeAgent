import { isSupportedLocale, type Locale } from "./core.ts";

export const LOCALE_PREFERENCE_STORAGE_KEY = "freeagent.ui.locale.v1";

export type LocaleStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;
export type LocalePreference = {
  read: () => Locale | null;
  write: (locale: Locale) => boolean;
  clear: () => boolean;
};

const browserStorage = (): LocaleStorage | null => {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
};

export class LocalePreferenceStore implements LocalePreference {
  readonly #storage: LocaleStorage | null;

  constructor(storage: LocaleStorage | null = browserStorage()) {
    this.#storage = storage;
  }

  read(): Locale | null {
    try {
      const stored = this.#storage?.getItem(LOCALE_PREFERENCE_STORAGE_KEY) ?? null;
      return isSupportedLocale(stored) ? stored : null;
    } catch {
      return null;
    }
  }

  write(locale: Locale): boolean {
    if (!isSupportedLocale(locale) || this.#storage === null) return false;
    try {
      this.#storage.setItem(LOCALE_PREFERENCE_STORAGE_KEY, locale);
      return true;
    } catch {
      return false;
    }
  }

  clear(): boolean {
    if (this.#storage === null) return false;
    try {
      this.#storage.removeItem(LOCALE_PREFERENCE_STORAGE_KEY);
      return true;
    } catch {
      return false;
    }
  }
}
