import type { ChangeEvent, ReactNode } from "react";

import {
  detailHash,
  isSessionInvalid,
  overviewSearchResults,
  type ControlOverviewError,
  type DetailSnapshot,
  type DetailSection,
  type ScopeChoice,
  type SearchResult
} from "./overview.ts";
import type { ControlSession, OverviewResponse } from "./contracts.ts";
import { useOptionalI18n, type I18nRuntime } from "./i18n/index.ts";

type HandoffPanelProps = {
  busy: boolean;
  error: string;
  onFile: (event: ChangeEvent<HTMLInputElement>) => void;
};

export function HandoffPanel({ busy, error, onFile }: HandoffPanelProps) {
  const { t } = useOptionalI18n();
  return (
    <main className="entry" aria-labelledby="entry-title">
      <section className="entry__panel">
        <p className="eyebrow">{t("session.open.eyebrow")}</p>
        <h1 id="entry-title">{t("session.open.title")}</h1>
        <p className="lede">{t("session.open.description")}</p>
        {error !== "" && (
          <div className="notice notice--error" role="alert">
            <strong>{t("session.open.error")}</strong>
            <span>{error}</span>
          </div>
        )}
        <label className={`file-picker${busy ? " file-picker--busy" : ""}`}>
          <span>{busy ? t("session.opening") : t("session.chooseHandoff")}</span>
          <input
            type="file"
            accept="application/json,.json"
            disabled={busy}
            onChange={onFile}
          />
        </label>
        <dl className="security-notes">
          <div>
            <dt>{t("session.security.authority")}</dt>
            <dd>{t("session.security.authorityValue")}</dd>
          </div>
          <div>
            <dt>{t("session.security.credentials")}</dt>
            <dd>{t("session.security.credentialsValue")}</dd>
          </div>
          <div>
            <dt>{t("session.security.surface")}</dt>
            <dd>{t("session.security.surfaceValue")}</dd>
          </div>
        </dl>
      </section>
    </main>
  );
}

export function LoadingPanel({ label, labelKey }: { label?: string; labelKey?: string }) {
  const { t } = useOptionalI18n();
  const resolvedLabel = labelKey !== undefined ? t(labelKey) : label ?? t("loading.overview");
  return (
    <main className="entry" aria-busy="true" aria-labelledby="loading-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">{t("loading.brand")}</p>
        <h1 id="loading-title">{resolvedLabel}</h1>
        <div className="loading-bar" aria-hidden="true"><span /></div>
        <p className="muted">{t("loading.waiting")}</p>
      </section>
    </main>
  );
}

export function PermissionPanel({ principalID }: { principalID: string }) {
  const { t } = useOptionalI18n();
  return (
    <main className="entry" aria-labelledby="permission-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">{t("permission.eyebrow")}</p>
        <h1 id="permission-title">{t("permission.title")}</h1>
        <p className="lede">{t("permission.description", { values: { principal: principalID } })}</p>
      </section>
    </main>
  );
}

export function SessionInvalidBoundary({
  error,
  children
}: {
  error: ControlOverviewError | null;
  children: ReactNode;
}) {
  if (error !== null && isSessionInvalid(error)) {
    return <LoadingPanel labelKey="loading.closingSession" />;
  }
  return <>{children}</>;
}

export function FatalOverviewPanel({
  permissionDenied,
  message,
  correlationID,
  onRetry
}: {
  permissionDenied: boolean;
  message: string;
  correlationID: string;
  onRetry: () => void;
}) {
  const { t } = useOptionalI18n();
  return (
    <main className="entry" aria-labelledby="overview-error-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">{t("error.overview.eyebrow")}</p>
        <h1 id="overview-error-title">
          {permissionDenied ? t("error.overview.scopeDenied") : t("error.overview.unavailable")}
        </h1>
        <p className="lede">{message}</p>
        {correlationID !== "" && <p className="correlation">{t("error.overview.correlation", { values: { id: correlationID } })}</p>}
        {!permissionDenied && (
          <button className="button button--primary" type="button" onClick={onRetry}>
            {t("error.overview.retry")}
          </button>
        )}
      </section>
    </main>
  );
}

const formatMicros = (value: number, formatDateTime: I18nRuntime["formatDateTime"]) => {
  const date = new Date(Math.floor(value / 1000));
  return Number.isNaN(date.getTime()) ? "Invalid time" : formatDateTime(date);
};

const shortDigest = (value: string) => `${value.slice(0, 10)}…${value.slice(-8)}`;

const sectionLabelKeys: Record<DetailSection, string> = {
  workspaces: "overview.section.workspaces",
  runs: "overview.section.runs",
  unknown: "overview.section.unknown",
  learning: "overview.section.learning",
  "module-candidates": "overview.section.moduleCandidates",
  usage: "overview.section.usage"
};

const sectionTruncated = (overview: OverviewResponse, section: DetailSection) => {
  switch (section) {
    case "workspaces": return overview.workspaces_truncated;
    case "runs": return overview.runs_truncated;
    case "unknown": return overview.unknown_truncated;
    case "learning": return overview.learning_truncated;
    case "module-candidates": return overview.module_candidates_truncated;
    case "usage": return overview.usage_truncated;
  }
};

function ResultList({ results, t }: { results: SearchResult[]; t: I18nRuntime["t"] }) {
  if (results.length === 0) {
    return <p className="empty-copy">{t("overview.result.empty")}</p>;
  }
  return (
    <ul className="result-list">
      {results.map((result) => (
        <li key={`${result.section}:${result.id}`}>
          <a href={detailHash(result)} className="result-link">
            <span className="result-link__title">{result.title}</span>
            <span className="result-link__summary">{result.summary}</span>
            <span className="result-link__arrow" aria-hidden="true">↗</span>
          </a>
        </li>
      ))}
    </ul>
  );
}

export function DetailDrawer({
  snapshot
}: {
  snapshot: DetailSnapshot | null;
}) {
  const { t } = useOptionalI18n();
  if (snapshot === null) return null;
  const { result, scope } = snapshot;
  const frozenScope = scope.kind === "WORKSPACE"
    ? `${scope.tenant_id} / ${scope.workspace_id}`
    : scope.tenant_id;
  return (
    <aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="detail-title">
      <div className="drawer__scrim" aria-hidden="true" />
      <section className="drawer__panel">
        <header className="drawer__header">
          <div>
            <p className="eyebrow">{t("overview.detail.eyebrow")}</p>
            <h2 id="detail-title">{result?.title ?? t("overview.detail.unavailable")}</h2>
          </div>
          <a className="drawer__close" href="#" aria-label={t("overview.detail.close")}>×</a>
        </header>
        {result === null ? (
          <p className="empty-copy">{t("overview.detail.missing")}</p>
        ) : (
          <>
            <p className="drawer__summary">{t(sectionLabelKeys[result.section])} · {result.summary}</p>
            <p className="drawer__scope">{t("overview.detail.frozenScope", {
              values: {
                kind: scope.kind === "TENANT" ? t("common.tenantLower") : t("common.workspaceLower"),
                scope: frozenScope
              }
            })}</p>
            <pre>{JSON.stringify(result.value, null, 2)}</pre>
          </>
        )}
      </section>
    </aside>
  );
}

type OverviewPageProps = {
  session: ControlSession;
  overview: OverviewResponse;
  scopeChoices: ScopeChoice[];
  selectedScopeKey: string;
  search: string;
  stale: boolean;
  refreshing: boolean;
  backgroundError: string;
  detailSnapshot: DetailSnapshot | null;
  onScopeChange: (key: string) => void;
  onSearchChange: (value: string) => void;
  onRefresh: () => void;
  onNavigateManagement?: () => void;
};

export function OverviewPage({
  session,
  overview,
  scopeChoices,
  selectedScopeKey,
  search,
  stale,
  refreshing,
  backgroundError,
  detailSnapshot,
  onScopeChange,
  onSearchChange,
  onRefresh,
  onNavigateManagement
}: OverviewPageProps) {
  const { t, formatDateTime, formatNumber } = useOptionalI18n();
  const allResults = overviewSearchResults(overview, search, t);
  const unfilteredResults = overviewSearchResults(overview, "", t);
  const itemCount = unfilteredResults.length;
  const groups = (Object.keys(sectionLabelKeys) as DetailSection[]).map((section) => ({
    section,
    results: allResults
      .filter((result) => result.section === section)
      .slice(0, search.trim() === "" ? 12 : undefined)
  }));

  return (
    <div className="control-shell">
      <header className="topbar">
        <a className="brand" href="#overview" aria-label={t("brand.overviewAria")}>
          <span className="brand__mark" aria-hidden="true">F</span>
          <span>{t("brand.name")}</span>
        </a>
        <div className="topbar__status">
          <span className={`status-dot${stale ? " status-dot--stale" : ""}`} aria-hidden="true" />
          {refreshing ? t("common.refreshing") : stale ? t("common.stale") : t("common.current")}
        </div>
      </header>

      <aside className="sidebar" aria-label={t("overview.nav.aria")}>
        <p className="sidebar__label">{t("overview.nav.control")}</p>
        <nav>
          <a href="#overview">{t("overview.nav.status")}</a>
          <a href="#modules">{t("overview.nav.modules")}</a>
          <a href="#management" onClick={onNavigateManagement}>{t("overview.nav.management")}</a>
          {groups.map(({ section }) => (
            <a href={`#section-${section}`} key={section}>{t(sectionLabelKeys[section])}</a>
          ))}
        </nav>
        <div className="session-card">
          <span>{t("session.open.title")}</span>
          <strong>{session.principal_id}</strong>
          <small>{t("common.expires", { values: { time: formatMicros(session.expires_at_unix_micros, formatDateTime) } })}</small>
        </div>
      </aside>

      <main className="content" id="overview">
        <section className="page-heading">
          <div>
            <p className="eyebrow">{t("overview.eyebrow")}</p>
            <h1>{t("overview.title")}</h1>
            <p className="lede">{t("overview.description")}</p>
          </div>
          <button className="button" type="button" onClick={onRefresh} disabled={refreshing}>
            {refreshing ? t("overview.reading") : t("overview.refresh")}
          </button>
        </section>

        <section className="toolbar" aria-label={t("overview.controls.aria")}>
          <label>
            <span>{t("overview.scope")}</span>
            <select value={selectedScopeKey} onChange={(event) => onScopeChange(event.target.value)}>
              {scopeChoices.map((choice) => (
                <option value={choice.key} key={choice.key}>{choice.scope.kind === "TENANT"
                  ? t("scope.tenant", { values: { tenant: choice.scope.tenant_id } })
                  : t("scope.workspace", { values: { tenant: choice.scope.tenant_id, workspace: choice.scope.workspace_id ?? "" } })}</option>
              ))}
            </select>
          </label>
          <label>
            <span>{t("overview.search")}</span>
            <input
              type="search"
              value={search}
              onChange={(event) => onSearchChange(event.target.value)}
              placeholder={t("overview.searchPlaceholder")}
              autoComplete="off"
            />
          </label>
        </section>

        {backgroundError !== "" && (
          <div className="notice notice--warning" role="status">
            <strong>{t("overview.refreshFailed")}</strong>
            <span>{t("overview.staleNotice", { values: { message: backgroundError } })}</span>
          </div>
        )}

        <section className="metric-grid" aria-label={t("overview.status.aria")}>
          <article>
            <span>{t("overview.metric.pointerRevision")}</span>
            <strong>{formatNumber(overview.basis.pointer_revision)}</strong>
            <small>{overview.published_pointer.kind}</small>
          </article>
          <article>
            <span>{t("overview.metric.controlGeneration")}</span>
            <strong>{overview.basis.control.id} · r{overview.basis.control.revision}</strong>
            <small className="digest">{shortDigest(overview.basis.control.digest)}</small>
          </article>
          <article>
            <span>{t("overview.metric.catalogGeneration")}</span>
            <strong>{overview.basis.catalog.id} · r{overview.basis.catalog.revision}</strong>
            <small className="digest">{shortDigest(overview.basis.catalog.digest)}</small>
          </article>
          <article>
            <span>{t("overview.metric.currentResponse")}</span>
            <strong>{formatNumber(itemCount)}</strong>
            <small>{t("overview.metric.authorizedItems")}</small>
          </article>
          <article>
            <span>{t("overview.metric.observed")}</span>
            <strong>{formatMicros(overview.view.observed_at_unix_micros, formatDateTime)}</strong>
            <small>{stale ? t("overview.metric.staleRepresentation") : t("overview.metric.verifiedRepresentation")}</small>
          </article>
          <article>
            <span>{t("overview.metric.projection")}</span>
            <strong className="digest">{shortDigest(overview.projection_digest)}</strong>
            <small>{t("overview.metric.semanticDigest")}</small>
          </article>
        </section>

        {itemCount === 0 && search.trim() === "" && (
          <section className="empty-state" aria-labelledby="empty-title">
            <p className="eyebrow">{t("overview.empty.eyebrow")}</p>
            <h2 id="empty-title">{t("overview.empty.title")}</h2>
            <p>{t("overview.empty.description")}</p>
          </section>
        )}

        {search.trim() !== "" && allResults.length === 0 && (
          <section className="empty-state" aria-labelledby="search-empty-title">
            <p className="eyebrow">{t("overview.searchEmpty.eyebrow")}</p>
            <h2 id="search-empty-title">{t("overview.searchEmpty.title")}</h2>
            <p>{t("overview.searchEmpty.description")}</p>
          </section>
        )}

        <div className="section-grid">
          {groups.map(({ section, results }) => (
            <section className="data-section" id={`section-${section}`} key={section}>
              <header>
                <div>
                  <p className="eyebrow">{t("overview.facts.eyebrow")}</p>
                  <h2>{t(sectionLabelKeys[section])}</h2>
                </div>
                <div className="section-meta">
                  <span>{results.length}</span>
                  {sectionTruncated(overview, section) && <span className="tag">{t("overview.truncated")}</span>}
                </div>
              </header>
              <ResultList results={results} t={t} />
            </section>
          ))}
        </div>

        <footer className="page-footer">
          <span>{t("overview.footer.view", { values: { digest: shortDigest(overview.view_snapshot_digest) } })}</span>
          <span>{t("overview.footer.scope", { values: { digest: shortDigest(overview.view.scope_digest) } })}</span>
        </footer>
      </main>
      <DetailDrawer snapshot={detailSnapshot} />
    </div>
  );
}
