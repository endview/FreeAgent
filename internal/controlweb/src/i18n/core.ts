export const SUPPORTED_LOCALES = ["en-US", "zh-CN"] as const;
export type Locale = (typeof SUPPORTED_LOCALES)[number];

export const FALLBACK_LOCALE: Locale = "en-US";

export type MessageCatalog = Readonly<Record<string, string>>;
export type MessageCatalogs = Readonly<Partial<Record<Locale, MessageCatalog>>>;
export type InterpolationValue = string | number;
export type TranslationValues = Readonly<Record<string, InterpolationValue>>;

export type TranslationOptions = {
  count?: number;
  values?: TranslationValues;
};

export type MissingMessageDiagnostic = {
  fallbackLocale: Locale;
  key: string;
  locale: Locale;
};
export type MissingMessageReporter = (diagnostic: MissingMessageDiagnostic) => void;

export type I18nRuntime = {
  locale: Locale;
  t: (key: string, options?: TranslationOptions) => string;
  formatNumber: (
    value: number | bigint,
    options?: Intl.NumberFormatOptions
  ) => string;
  formatCurrency: (
    value: number | bigint,
    currency: string,
    options?: Intl.NumberFormatOptions
  ) => string;
  formatTokenCount: (
    value: number | bigint,
    options?: Intl.NumberFormatOptions
  ) => string;
  formatCost: (
    value: number | bigint,
    currency: string,
    options?: Intl.NumberFormatOptions
  ) => string;
  formatDateTime: (
    value: Date | number,
    options?: Intl.DateTimeFormatOptions
  ) => string;
  formatRelativeTime: (
    value: number,
    unit: Intl.RelativeTimeFormatUnit,
    options?: Intl.RelativeTimeFormatOptions
  ) => string;
  formatList: (
    values: Iterable<string>,
    options?: Intl.ListFormatOptions
  ) => string;
};

const hasOwn = (value: object, key: string) =>
  Object.prototype.hasOwnProperty.call(value, key);

export function isSupportedLocale(value: unknown): value is Locale {
  return typeof value === "string" && SUPPORTED_LOCALES.some((locale) => locale === value);
}

export function matchSupportedLocale(value: string): Locale | null {
  let canonical: string;
  try {
    [canonical] = Intl.getCanonicalLocales(value);
  } catch {
    return null;
  }
  if (canonical === "zh" || canonical.startsWith("zh-")) return "zh-CN";
  if (canonical === "en" || canonical.startsWith("en-")) return "en-US";
  return null;
}

export function resolveLocale(candidates: readonly string[]): Locale {
  for (const candidate of candidates) {
    const locale = matchSupportedLocale(candidate);
    if (locale !== null) return locale;
  }
  return FALLBACK_LOCALE;
}

export function interpolateMessage(
  template: string,
  values: TranslationValues = {}
): string {
  return template.replace(
    /\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}/gu,
    (placeholder, name: string) => hasOwn(values, name) ? String(values[name]) : placeholder
  );
}

export function interpolationParameterNames(template: string): readonly string[] {
  return [...new Set(
    [...template.matchAll(/\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}/gu)]
      .map((match) => match[1])
  )].sort();
}

export function assertCatalogCompatibility(
  catalogs: MessageCatalogs,
  fallbackLocale: Locale = FALLBACK_LOCALE
): void {
  const fallbackCatalog = catalogs[fallbackLocale];
  if (fallbackCatalog === undefined) {
    throw new Error(`i18n fallback catalog is missing: ${fallbackLocale}`);
  }
  const expectedKeys = Object.keys(fallbackCatalog).sort();
  for (const locale of SUPPORTED_LOCALES) {
    const catalog = catalogs[locale];
    if (catalog === undefined) throw new Error(`i18n catalog is missing: ${locale}`);
    const actualKeys = Object.keys(catalog).sort();
    if (
      actualKeys.length !== expectedKeys.length ||
      actualKeys.some((key, index) => key !== expectedKeys[index])
    ) {
      throw new Error(`i18n catalog key mismatch: ${locale}`);
    }
    for (const key of expectedKeys) {
      const expectedParameters = interpolationParameterNames(fallbackCatalog[key]);
      const actualParameters = interpolationParameterNames(catalog[key]);
      if (
        actualParameters.length !== expectedParameters.length ||
        actualParameters.some((name, index) => name !== expectedParameters[index])
      ) {
        throw new Error(`i18n interpolation mismatch: ${locale}:${key}`);
      }
    }
  }
}

const messageCandidates = (key: string, count: number | undefined, locale: Locale) => {
  if (count === undefined) return [key];
  const category = new Intl.PluralRules(locale).select(count);
  return [`${key}_${category}`, `${key}_other`, key];
};

const findMessage = (
  catalog: MessageCatalog | undefined,
  candidates: readonly string[]
): string | null => {
  if (catalog === undefined) return null;
  for (const candidate of candidates) {
    if (hasOwn(catalog, candidate)) return catalog[candidate];
  }
  return null;
};

export function translateMessage(
  catalogs: MessageCatalogs,
  locale: Locale,
  fallbackLocale: Locale,
  key: string,
  options: TranslationOptions = {},
  onMissingMessage?: MissingMessageReporter
): string {
  const localMessage = findMessage(
    catalogs[locale],
    messageCandidates(key, options.count, locale)
  );
  const fallbackMessage = localMessage === null && locale !== fallbackLocale
    ? findMessage(
        catalogs[fallbackLocale],
        messageCandidates(key, options.count, fallbackLocale)
      )
    : null;
  const template = localMessage ?? fallbackMessage;
  if (template === null) {
    onMissingMessage?.({ fallbackLocale, key, locale });
    return key;
  }

  const values = options.count === undefined
    ? options.values
    : { ...options.values, count: options.count };
  return interpolateMessage(template, values);
}

export function createI18nRuntime(
  locale: Locale,
  catalogs: MessageCatalogs,
  fallbackLocale: Locale = FALLBACK_LOCALE,
  onMissingMessage?: MissingMessageReporter
): I18nRuntime {
  const currency = (
    value: number | bigint,
    currencyCode: string,
    options?: Intl.NumberFormatOptions
  ) => new Intl.NumberFormat(locale, {
    ...options,
    currency: currencyCode,
    style: "currency"
  }).format(value);
  return {
    locale,
    t: (key, options) => translateMessage(
      catalogs,
      locale,
      fallbackLocale,
      key,
      options,
      onMissingMessage
    ),
    formatNumber: (value, options) => new Intl.NumberFormat(locale, options).format(value),
    formatCurrency: currency,
    formatTokenCount: (value, options) =>
      new Intl.NumberFormat(locale, options).format(value),
    formatCost: currency,
    formatDateTime: (value, options) =>
      new Intl.DateTimeFormat(locale, options).format(
        value instanceof Date ? value : new Date(value)
      ),
    formatRelativeTime: (value, unit, options) =>
      new Intl.RelativeTimeFormat(locale, options).format(value, unit),
    formatList: (values, options) =>
      new Intl.ListFormat(locale, options).format([...values])
  };
}
