import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import {
  ControlModulesError,
  isModulesPermissionDenied,
  isModulesSessionInvalid,
  isModulesStale,
  type ModuleContext
} from "./modules.ts";
import {
  fetchStoreManagement,
  fetchUnknownOutcomeDetail,
  fetchUnknownOutcomes,
  managementQueryKey,
  unknownDetailQueryKey,
  type ArtifactAdmission,
  type StoreManagementResponse,
  type UnknownAttemptKind,
  type UnknownOutcome
} from "./management.ts";
import type { ControlSession } from "./contracts.ts";
import type { ScopeChoice } from "./overview.ts";
import { useOptionalI18n, type I18nRuntime } from "./i18n/index.ts";
import type { ModulesFailClosedEvent } from "./modules-ui.tsx";

export type ManagementPageProps = {
  session: ControlSession;
  context: ModuleContext;
  scopeChoices: readonly ScopeChoice[];
  selectedScopeKey: string;
  onScopeChange: (key: string) => void;
  onNavigateOverview?: () => void;
  onNavigateModules?: () => void;
  onNavigateReviews?: () => void;
  onFailClosed?: (event: ModulesFailClosedEvent) => void;
};

const shortDigest = (value: string) =>
  value.length <= 20 ? value : `${value.slice(0, 10)}...${value.slice(-8)}`;

const formatMicros = (
  value: number,
  formatDateTime: I18nRuntime["formatDateTime"],
  invalidTime: string
) => {
  const date = new Date(Math.floor(value / 1000));
  return Number.isNaN(date.getTime()) ? invalidTime : formatDateTime(date);
};

const failureFromError = (error: unknown): ModulesFailClosedEvent => {
  const message = error instanceof Error ? error.message : "Management response was rejected";
  const correlationID = error instanceof ControlModulesError ? error.correlationID : "";
  if (isModulesSessionInvalid(error)) return { kind: "SESSION", message, correlationID };
  if (isModulesPermissionDenied(error)) return { kind: "PERMISSION", message, correlationID };
  if (isModulesStale(error)) return { kind: "STALE", message, correlationID };
  return { kind: "INTEGRITY", message, correlationID };
};

const scopeLabel = (choice: ScopeChoice, t: I18nRuntime["t"]) =>
  choice.scope.kind === "TENANT"
    ? t("scope.tenant", { values: { tenant: choice.scope.tenant_id } })
    : t("scope.workspace", {
        values: {
          tenant: choice.scope.tenant_id,
          workspace: choice.scope.workspace_id ?? ""
        }
      });

const unknownTitle = (item: UnknownOutcome, t: I18nRuntime["t"]) =>
  t("management.unknown.itemTitle", {
    values: { kind: item.kind, attempt: shortDigest(item.attempt_id) }
  });

function UnknownDetail({
  item,
  onRetry
}: {
  item: UnknownOutcome;
  onRetry: () => void;
}) {
  const { t, formatDateTime, formatTokenCount } = useOptionalI18n();
  const invalidTime = t("management.invalidTime");
  const usageValue = (value: number | null) =>
    value === null ? t("management.unknownValue") : formatTokenCount(value);
  return (
    <>
      <dl className="module-facts">
        <div><dt>{t("management.unknown.kind")}</dt><dd>{item.kind}</dd></div>
        <div><dt>{t("management.unknown.attempt")}</dt><dd className="digest">{shortDigest(item.attempt_id)}</dd></div>
        <div><dt>{t("management.unknown.run")}</dt><dd>{item.run_id}</dd></div>
        <div><dt>{t("management.unknown.state")}</dt><dd>{item.state}</dd></div>
        <div><dt>{t("management.unknown.provider")}</dt><dd>{item.provider ?? t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.model")}</dt><dd>{item.model ?? t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.requestID")}</dt><dd>{item.provider_request_id ?? t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.externalID")}</dt><dd>{item.external_operation_id ?? t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.endpoint")}</dt><dd>{item.endpoint_id ?? t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.classification")}</dt><dd>{item.error_classification ?? t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.reason")}</dt><dd>{item.unknown_reason ?? t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.evidence")}</dt><dd>{item.has_reconciliation_evidence ? t("management.yes") : t("management.no")}</dd></div>
        <div><dt>{t("management.unknown.evidenceRef")}</dt><dd className="digest">{item.reconciliation_evidence_ref ? shortDigest(item.reconciliation_evidence_ref) : t("management.unknownValue")}</dd></div>
        <div><dt>{t("management.unknown.created")}</dt><dd>{formatMicros(item.created_at_unix_micros, formatDateTime, invalidTime)}</dd></div>
        <div><dt>{t("management.unknown.updated")}</dt><dd>{formatMicros(item.updated_at_unix_micros, formatDateTime, invalidTime)}</dd></div>
      </dl>
      <section className="review-subsection">
        <h3>{t("management.unknown.usage")}</h3>
        <dl className="module-facts">
          <div><dt>{t("management.unknown.inputTokens")}</dt><dd>{usageValue(item.usage.input_tokens)}</dd></div>
          <div><dt>{t("management.unknown.cachedInputTokens")}</dt><dd>{usageValue(item.usage.cached_input_tokens)}</dd></div>
          <div><dt>{t("management.unknown.uncachedInputTokens")}</dt><dd>{usageValue(item.usage.uncached_input_tokens)}</dd></div>
          <div><dt>{t("management.unknown.outputTokens")}</dt><dd>{usageValue(item.usage.output_tokens)}</dd></div>
          <div><dt>{t("management.unknown.reasoningTokens")}</dt><dd>{usageValue(item.usage.reasoning_tokens)}</dd></div>
        </dl>
      </section>
      <footer className="page-footer">
        <span>{t("management.unknown.readOnly")}</span>
        <button className="button" type="button" onClick={onRetry}>{t("common.retry")}</button>
      </footer>
    </>
  );
}

function ArtifactList({
  artifacts,
  t,
  formatDateTime
}: {
  artifacts: readonly ArtifactAdmission[];
  t: I18nRuntime["t"];
  formatDateTime: I18nRuntime["formatDateTime"];
}) {
  const invalidTime = t("management.invalidTime");
  if (artifacts.length === 0) return <p className="empty-copy">{t("management.artifacts.empty")}</p>;
  return (
    <ul className="result-list">
      {artifacts.map((artifact) => (
        <li key={artifact.admission_id}>
          <div className="management-artifact">
            <strong>{artifact.module.id} @ {artifact.module.version}</strong>
            <span className="digest">{shortDigest(artifact.artifact_digest)}</span>
            <small>{t("management.artifacts.summary", {
              values: {
                source: artifact.source_id,
                files: artifact.covered_file_count,
                time: formatMicros(artifact.admitted_at_unix_micros, formatDateTime, invalidTime)
              }
            })}</small>
          </div>
        </li>
      ))}
    </ul>
  );
}

export function ManagementPage({
  session,
  context,
  scopeChoices,
  selectedScopeKey,
  onScopeChange,
  onNavigateOverview,
  onNavigateModules,
  onNavigateReviews,
  onFailClosed
}: ManagementPageProps) {
  const { t, formatDateTime } = useOptionalI18n();
  const [selectedUnknown, setSelectedUnknown] = useState<{
    kind: UnknownAttemptKind;
    attemptID: string;
  } | null>(null);
  const unknowns = useQuery({
    queryKey: managementQueryKey(context),
    queryFn: ({ signal }) => fetchUnknownOutcomes(context, signal),
    staleTime: 30_000,
    retry: false
  });
  const store = useQuery({
    queryKey: ["store-management", ...managementQueryKey(context)],
    queryFn: ({ signal }) => fetchStoreManagement(context, signal),
    staleTime: 30_000,
    retry: false
  });
  const detail = useQuery({
    queryKey: selectedUnknown === null
      ? ["management-unknown-detail", "disabled"]
      : unknownDetailQueryKey(context, selectedUnknown.kind, selectedUnknown.attemptID),
    queryFn: ({ signal }) => {
      if (selectedUnknown === null) throw new Error("UNKNOWN detail is disabled");
      return fetchUnknownOutcomeDetail(
        context,
        selectedUnknown.kind,
        selectedUnknown.attemptID,
        signal
      );
    },
    enabled: selectedUnknown !== null,
    retry: false
  });

  useEffect(() => {
    for (const error of [unknowns.error, store.error, detail.error]) {
      if (error !== null) {
        onFailClosed?.(failureFromError(error));
        break;
      }
    }
  }, [detail.error, onFailClosed, store.error, unknowns.error]);

  const unknownItems = useMemo(() => unknowns.data?.items ?? [], [unknowns.data]);
  useEffect(() => {
    if (
      selectedUnknown !== null &&
      !unknownItems.some((item) =>
        item.kind === selectedUnknown.kind && item.attempt_id === selectedUnknown.attemptID
      )
    ) setSelectedUnknown(null);
  }, [selectedUnknown, unknownItems]);

  if (unknowns.isPending || store.isPending) {
    return <main className="entry" aria-busy="true"><section className="entry__panel entry__panel--compact"><p className="eyebrow">{t("loading.brand")}</p><h1>{t("management.loading")}</h1><div className="loading-bar" aria-hidden="true"><span /></div></section></main>;
  }
  if (unknowns.error !== null || store.error !== null || unknowns.data === undefined || store.data === undefined) {
    const error = unknowns.error ?? store.error;
    return <main className="entry"><section className="entry__panel entry__panel--compact"><p className="eyebrow">{t("management.eyebrow")}</p><h1>{t("management.unavailable")}</h1><p className="lede">{error instanceof Error ? error.message : t("management.readFailed")}</p><button className="button button--primary" type="button" onClick={() => { void unknowns.refetch(); void store.refetch(); }}>{t("common.retry")}</button></section></main>;
  }

  const storeData: StoreManagementResponse = store.data;
  const navigate = (callback?: () => void) => callback?.();
  return (
    <div className="control-shell control-shell--modules">
      <header className="topbar">
        <a className="brand" href="#overview" aria-label={t("brand.overviewAria")} onClick={() => navigate(onNavigateOverview)}>
          <span className="brand__mark" aria-hidden="true">F</span><span>{t("brand.name")}</span>
        </a>
        <div className="topbar__status"><span className="status-dot" aria-hidden="true" />{t("common.current")}</div>
      </header>
      <aside className="sidebar" aria-label={t("overview.nav.aria")}>
        <p className="sidebar__label">{t("overview.nav.control")}</p>
        <nav>
          <a href="#overview" onClick={() => navigate(onNavigateOverview)}>{t("overview.nav.status")}</a>
          <a href="#modules" onClick={() => navigate(onNavigateModules)}>{t("overview.nav.modules")}</a>
          <a href="#upgrade-reviews" onClick={() => navigate(onNavigateReviews)}>{t("overview.nav.reviews")}</a>
          <a href="#management" aria-current="page">{t("overview.nav.management")}</a>
        </nav>
        <div className="session-card"><span>{t("session.open.title")}</span><strong>{session.principal_id}</strong><small>{t("common.expires", { values: { time: formatMicros(session.expires_at_unix_micros, formatDateTime, t("management.invalidTime")) } })}</small></div>
      </aside>
      <main className="content modules-content" id="management">
        <section className="page-heading">
          <div><p className="eyebrow">{t("management.eyebrow")}</p><h1>{t("management.title")}</h1><p className="lede">{t("management.description")}</p></div>
          <button className="button" type="button" onClick={() => { void unknowns.refetch(); void store.refetch(); }}>{unknowns.isFetching || store.isFetching ? t("common.refreshing") : t("management.refresh")}</button>
        </section>
        <section className="toolbar modules-toolbar" aria-label={t("management.controls.aria")}>
          <label><span>{t("overview.scope")}</span><select value={selectedScopeKey} onChange={(event) => onScopeChange(event.currentTarget.value)}>{scopeChoices.map((choice) => <option value={choice.key} key={choice.key}>{scopeLabel(choice, t)}</option>)}</select></label>
        </section>
        <div className="notice notice--warning" role="status"><strong>{t("management.readOnly.title")}</strong><span>{t("management.readOnly.description")}</span></div>
        <div className="management-grid">
          <section className="data-section" aria-labelledby="unknown-title">
            <header><div><p className="eyebrow">{t("management.unknown.eyebrow")}</p><h2 id="unknown-title">{t("management.unknown.title")}</h2></div><span className="section-meta">{t("management.loaded", { values: { count: unknownItems.length } })}</span></header>
            {unknownItems.length === 0 ? <p className="empty-copy">{t("management.unknown.empty")}</p> : <ul className="result-list">{unknownItems.map((item) => <li key={`${item.kind}:${item.attempt_id}`}><button className="result-link module-result" type="button" aria-current={selectedUnknown?.kind === item.kind && selectedUnknown.attemptID === item.attempt_id ? "true" : undefined} onClick={() => setSelectedUnknown({ kind: item.kind, attemptID: item.attempt_id })}><span className="result-link__title">{unknownTitle(item, t)}</span><span className="result-link__summary">{t("management.unknown.summary", { values: { state: item.state, run: item.run_id } })}</span><span className="result-link__arrow" aria-hidden="true">&gt;</span></button></li>)}</ul>}
          </section>
          <section className="data-section module-detail" aria-labelledby="unknown-detail-title">
            <header><div><p className="eyebrow">{t("management.unknown.detailEyebrow")}</p><h2 id="unknown-detail-title">{selectedUnknown === null ? t("management.unknown.select") : shortDigest(selectedUnknown.attemptID)}</h2></div>{selectedUnknown !== null && <button className="button" type="button" onClick={() => setSelectedUnknown(null)}>{t("common.close")}</button>}</header>
            {selectedUnknown === null ? <p className="empty-copy">{t("management.unknown.selectDescription")}</p> : detail.isPending ? <p className="empty-copy" aria-busy="true">{t("management.unknown.detailLoading")}</p> : detail.error !== null || detail.data === undefined ? <div className="notice notice--error" role="alert"><strong>{t("management.unknown.detailUnavailable")}</strong><span>{detail.error instanceof Error ? detail.error.message : t("management.unknown.detailReadFailed")}</span><button className="button" type="button" onClick={() => { void detail.refetch(); }}>{t("common.retry")}</button></div> : <UnknownDetail item={detail.data.item} onRetry={() => { void detail.refetch(); }} />}
          </section>
          <section className="data-section" aria-labelledby="store-title">
            <header><div><p className="eyebrow">{t("management.store.eyebrow")}</p><h2 id="store-title">{t("management.store.title")}</h2></div><span className="section-meta">{storeData.backup.state}</span></header>
            <dl className="module-facts">
              <div><dt>{t("management.store.instance")}</dt><dd className="digest">{shortDigest(storeData.verification.store_instance_id)}</dd></div>
              <div><dt>{t("management.store.schema")}</dt><dd>{storeData.verification.schema_identity}</dd></div>
              <div><dt>{t("management.store.schemaVersion")}</dt><dd>{storeData.verification.schema_version}</dd></div>
              <div><dt>{t("management.store.fingerprint")}</dt><dd className="digest">{shortDigest(storeData.verification.schema_fingerprint)}</dd></div>
              <div><dt>{t("management.store.generator")}</dt><dd>{storeData.verification.generator_id}</dd></div>
              <div><dt>{t("management.store.backupFormat")}</dt><dd>{storeData.backup.format_version}</dd></div>
              <div><dt>{t("management.store.onlineCreate")}</dt><dd>{storeData.backup.online_create ? t("management.yes") : t("management.no")}</dd></div>
              <div><dt>{t("management.store.onlineRestore")}</dt><dd>{storeData.backup.online_restore ? t("management.yes") : t("management.no")}</dd></div>
              <div><dt>{t("management.store.restoreMode")}</dt><dd>{storeData.backup.restore_mode}</dd></div>
            </dl>
          </section>
          <section className="data-section" aria-labelledby="artifacts-title">
            <header><div><p className="eyebrow">{t("management.artifacts.eyebrow")}</p><h2 id="artifacts-title">{t("management.artifacts.title")}</h2></div><span className="section-meta">{t("management.loaded", { values: { count: storeData.artifacts.length } })}</span></header>
            <ArtifactList artifacts={storeData.artifacts} t={t} formatDateTime={formatDateTime} />
          </section>
        </div>
        <footer className="page-footer"><span>{t("management.footer.projection", { values: { digest: shortDigest(storeData.projection_digest) } })}</span><span>{t("management.footer.noPath")}</span></footer>
      </main>
    </div>
  );
}
