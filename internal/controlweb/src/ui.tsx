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

type HandoffPanelProps = {
  busy: boolean;
  error: string;
  onFile: (event: ChangeEvent<HTMLInputElement>) => void;
};

export function HandoffPanel({ busy, error, onFile }: HandoffPanelProps) {
  return (
    <main className="entry" aria-labelledby="entry-title">
      <section className="entry__panel">
        <p className="eyebrow">FreeAgent Control</p>
        <h1 id="entry-title">Open a Control session</h1>
        <p className="lede">
          Select the short-lived handoff JSON created by this exact local Control process.
          The capability is exchanged once and is never retained by this page.
        </p>
        {error !== "" && (
          <div className="notice notice--error" role="alert">
            <strong>Session could not be opened.</strong>
            <span>{error}</span>
          </div>
        )}
        <label className={`file-picker${busy ? " file-picker--busy" : ""}`}>
          <span>{busy ? "Opening session…" : "Choose handoff JSON"}</span>
          <input
            type="file"
            accept="application/json,.json"
            disabled={busy}
            onChange={onFile}
          />
        </label>
        <dl className="security-notes">
          <div>
            <dt>Authority</dt>
            <dd>Only server-authorized scopes are selectable.</dd>
          </div>
          <div>
            <dt>Credentials</dt>
            <dd>CSRF stays in memory; resume data stays in this tab session.</dd>
          </div>
          <div>
            <dt>Surface</dt>
            <dd>Overview stays read-only; Modules exposes only confirmed disable operations.</dd>
          </div>
        </dl>
      </section>
    </main>
  );
}

export function LoadingPanel({ label = "Loading Overview…" }: { label?: string }) {
  return (
    <main className="entry" aria-busy="true" aria-labelledby="loading-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">FreeAgent Control</p>
        <h1 id="loading-title">{label}</h1>
        <div className="loading-bar" aria-hidden="true"><span /></div>
        <p className="muted">Waiting for one bounded, authenticated response.</p>
      </section>
    </main>
  );
}

export function PermissionPanel({ principalID }: { principalID: string }) {
  return (
    <main className="entry" aria-labelledby="permission-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">Read-only access</p>
        <h1 id="permission-title">Overview permission denied</h1>
        <p className="lede">
          Session principal <code>{principalID}</code> does not currently hold the OBSERVE
          capability for this view. No Overview request was sent.
        </p>
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
    return <LoadingPanel label="Closing an expired session…" />;
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
  return (
    <main className="entry" aria-labelledby="overview-error-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">Read-only Overview</p>
        <h1 id="overview-error-title">
          {permissionDenied ? "Scope permission denied" : "Overview unavailable"}
        </h1>
        <p className="lede">{message}</p>
        {correlationID !== "" && <p className="correlation">Correlation: {correlationID}</p>}
        {!permissionDenied && (
          <button className="button button--primary" type="button" onClick={onRetry}>
            Retry read
          </button>
        )}
      </section>
    </main>
  );
}

const formatMicros = (value: number) => {
  const date = new Date(Math.floor(value / 1000));
  return Number.isNaN(date.getTime()) ? "Invalid time" : date.toLocaleString();
};

const shortDigest = (value: string) => `${value.slice(0, 10)}…${value.slice(-8)}`;

const sectionLabels: Record<DetailSection, string> = {
  workspaces: "Workspaces",
  runs: "Recent runs",
  unknown: "Unknown outcomes",
  learning: "Learning proposals",
  "module-candidates": "Module candidates",
  usage: "Usage reconciliation"
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

function ResultList({ results }: { results: SearchResult[] }) {
  if (results.length === 0) {
    return <p className="empty-copy">No items in this current response.</p>;
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
            <p className="eyebrow">Current response detail</p>
            <h2 id="detail-title">{result?.title ?? "Detail unavailable"}</h2>
          </div>
          <a className="drawer__close" href="#" aria-label="Close detail">×</a>
        </header>
        {result === null ? (
          <p className="empty-copy">
            This deep link is not present in the current authorized scope response.
          </p>
        ) : (
          <>
            <p className="drawer__summary">{sectionLabels[result.section]} · {result.summary}</p>
            <p className="drawer__scope">Frozen {scope.kind.toLowerCase()} scope: {frozenScope}</p>
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
  onRefresh
}: OverviewPageProps) {
  const allResults = overviewSearchResults(overview, search);
  const unfilteredResults = overviewSearchResults(overview, "");
  const itemCount = unfilteredResults.length;
  const groups = (Object.keys(sectionLabels) as DetailSection[]).map((section) => ({
    section,
    results: allResults
      .filter((result) => result.section === section)
      .slice(0, search.trim() === "" ? 12 : undefined)
  }));

  return (
    <div className="control-shell">
      <header className="topbar">
        <a className="brand" href="#overview" aria-label="FreeAgent Control Overview">
          <span className="brand__mark" aria-hidden="true">F</span>
          <span>FreeAgent Control</span>
        </a>
        <div className="topbar__status">
          <span className={`status-dot${stale ? " status-dot--stale" : ""}`} aria-hidden="true" />
          {refreshing ? "Refreshing" : stale ? "Stale" : "Current"}
        </div>
      </header>

      <aside className="sidebar" aria-label="Control navigation">
        <p className="sidebar__label">Control</p>
        <nav>
          <a href="#overview">Status</a>
          <a href="#modules">Modules</a>
          {groups.map(({ section }) => (
            <a href={`#section-${section}`} key={section}>{sectionLabels[section]}</a>
          ))}
        </nav>
        <div className="session-card">
          <span>Session</span>
          <strong>{session.principal_id}</strong>
          <small>Expires {formatMicros(session.expires_at_unix_micros)}</small>
        </div>
      </aside>

      <main className="content" id="overview">
        <section className="page-heading">
          <div>
            <p className="eyebrow">Authorized read-only surface</p>
            <h1>Overview</h1>
            <p className="lede">A bounded projection of the current published basis and recent safe facts.</p>
          </div>
          <button className="button" type="button" onClick={onRefresh} disabled={refreshing}>
            {refreshing ? "Reading…" : "Refresh read"}
          </button>
        </section>

        <section className="toolbar" aria-label="Overview controls">
          <label>
            <span>Authorized scope</span>
            <select value={selectedScopeKey} onChange={(event) => onScopeChange(event.target.value)}>
              {scopeChoices.map((choice) => (
                <option value={choice.key} key={choice.key}>{choice.label}</option>
              ))}
            </select>
          </label>
          <label>
            <span>Search this response</span>
            <input
              type="search"
              value={search}
              onChange={(event) => onSearchChange(event.target.value)}
              placeholder="ID, state, kind, version…"
              autoComplete="off"
            />
          </label>
        </section>

        {backgroundError !== "" && (
          <div className="notice notice--warning" role="status">
            <strong>The last refresh failed.</strong>
            <span>{backgroundError} The prior verified response remains visible as stale.</span>
          </div>
        )}

        <section className="metric-grid" aria-label="Overview status">
          <article>
            <span>Pointer revision</span>
            <strong>{overview.basis.pointer_revision.toLocaleString()}</strong>
            <small>{overview.published_pointer.kind}</small>
          </article>
          <article>
            <span>Control generation</span>
            <strong>{overview.basis.control.id} · r{overview.basis.control.revision}</strong>
            <small className="digest">{shortDigest(overview.basis.control.digest)}</small>
          </article>
          <article>
            <span>Catalog generation</span>
            <strong>{overview.basis.catalog.id} · r{overview.basis.catalog.revision}</strong>
            <small className="digest">{shortDigest(overview.basis.catalog.digest)}</small>
          </article>
          <article>
            <span>Current response</span>
            <strong>{itemCount.toLocaleString()}</strong>
            <small>authorized safe items</small>
          </article>
          <article>
            <span>Observed</span>
            <strong>{formatMicros(overview.view.observed_at_unix_micros)}</strong>
            <small>{stale ? "stale local representation" : "verified representation"}</small>
          </article>
          <article>
            <span>Projection</span>
            <strong className="digest">{shortDigest(overview.projection_digest)}</strong>
            <small>semantic digest verified</small>
          </article>
        </section>

        {itemCount === 0 && search.trim() === "" && (
          <section className="empty-state" aria-labelledby="empty-title">
            <p className="eyebrow">Current response</p>
            <h2 id="empty-title">No recent items</h2>
            <p>The selected authorized scope returned an empty, verified Overview.</p>
          </section>
        )}

        {search.trim() !== "" && allResults.length === 0 && (
          <section className="empty-state" aria-labelledby="search-empty-title">
            <p className="eyebrow">Local search</p>
            <h2 id="search-empty-title">No response items match</h2>
            <p>The search examined only the items already present in this authorized response.</p>
          </section>
        )}

        <div className="section-grid">
          {groups.map(({ section, results }) => (
            <section className="data-section" id={`section-${section}`} key={section}>
              <header>
                <div>
                  <p className="eyebrow">Recent safe facts</p>
                  <h2>{sectionLabels[section]}</h2>
                </div>
                <div className="section-meta">
                  <span>{results.length}</span>
                  {sectionTruncated(overview, section) && <span className="tag">truncated</span>}
                </div>
              </header>
              <ResultList results={results} />
            </section>
          ))}
        </div>

        <footer className="page-footer">
          <span>View {shortDigest(overview.view_snapshot_digest)}</span>
          <span>Scope {shortDigest(overview.view.scope_digest)}</span>
        </footer>
      </main>
      <DetailDrawer snapshot={detailSnapshot} />
    </div>
  );
}
