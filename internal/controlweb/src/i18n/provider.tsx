import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode
} from "react";

import {
  createI18nRuntime,
  isSupportedLocale,
  resolveLocale,
  type I18nRuntime,
  type Locale,
  type MessageCatalogs,
  type MissingMessageReporter
} from "./core.ts";
import {
  LocalePreferenceStore,
  type LocalePreference
} from "./locale-preference-store.ts";
import { i18nResources } from "./resources.ts";

export type I18nContextValue = I18nRuntime & {
  setLocale: (locale: unknown) => boolean;
};

export type LocaleDocument = {
  documentElement: { lang: string };
  title: string;
};

const I18nContext = createContext<I18nContextValue | null>(null);

const defaultRuntime: I18nRuntime = createI18nRuntime(
  "en-US",
  i18nResources
);

const browserLocaleCandidates = (): readonly string[] => {
  if (typeof navigator === "undefined") return [];
  return navigator.languages.length > 0 ? navigator.languages : [navigator.language];
};

export function syncDocumentLocale(
  target: LocaleDocument,
  locale: Locale,
  title: string
): void {
  target.documentElement.lang = locale;
  target.title = title;
}

export function I18nProvider({
  children,
  initialLocale,
  onMissingMessage,
  preferenceStore,
  catalogs = i18nResources
}: {
  children: ReactNode;
  initialLocale?: Locale;
  onMissingMessage?: MissingMessageReporter;
  preferenceStore?: LocalePreference;
  catalogs?: MessageCatalogs;
}) {
  const [store] = useState<LocalePreference>(
    () => preferenceStore ?? new LocalePreferenceStore()
  );
  const [locale, setLocaleState] = useState<Locale>(
    () => store.read() ?? initialLocale ?? resolveLocale(browserLocaleCandidates())
  );
  const runtime = useMemo(
    () => createI18nRuntime(locale, catalogs, undefined, onMissingMessage),
    [catalogs, locale, onMissingMessage]
  );
  const setLocale = useCallback((nextLocale: unknown) => {
    if (!isSupportedLocale(nextLocale)) return false;
    setLocaleState(nextLocale);
    store.write(nextLocale);
    return true;
  }, [store]);

  useEffect(() => {
    if (typeof document === "undefined") return;
    syncDocumentLocale(document, locale, runtime.t("app.title"));
  }, [locale, runtime]);

  const value = useMemo<I18nContextValue>(
    () => ({ ...runtime, setLocale }),
    [runtime, setLocale]
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  const value = useContext(I18nContext);
  if (value === null) throw new Error("useI18n must be used within I18nProvider");
  return value;
}

export function useOptionalI18n(): I18nContextValue {
  const value = useContext(I18nContext);
  return value ?? { ...defaultRuntime, setLocale: () => false };
}
