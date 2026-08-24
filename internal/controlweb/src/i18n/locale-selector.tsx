import { useId } from "react";

import { SUPPORTED_LOCALES } from "./core.ts";
import { useI18n } from "./provider.tsx";

export function LocaleSelector() {
  const id = useId();
  const { locale, setLocale, t } = useI18n();

  return (
    <div className="locale-selector">
      <label htmlFor={id}>{t("locale.selector.label")}</label>
      <select
        id={id}
        value={locale}
        onChange={(event) => setLocale(event.target.value)}
      >
        {SUPPORTED_LOCALES.map((option) => (
          <option lang={option} value={option} key={option}>
            {t(`locale.name.${option}`)}
          </option>
        ))}
      </select>
    </div>
  );
}
