import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import {
  ControlModulesError,
  fetchModuleUpgradeReviewDetail,
  fetchModuleUpgradeReviews,
  isModulesPermissionDenied,
  isModulesSessionInvalid,
  isModulesStale,
  type ModuleContext,
  type ModuleUpgradeReviewDetailResponse,
  type ModuleUpgradeReviewItem
} from "./modules.ts";
import type { ControlSession } from "./contracts.ts";
import type { ScopeChoice } from "./overview.ts";
import { useOptionalI18n, type I18nRuntime } from "./i18n/index.ts";
import type { ModulesFailClosedEvent } from "./modules-ui.tsx";

export type ModuleUpgradeReviewsPageProps = {
  session: ControlSession;
  context: ModuleContext;
  scopeChoices: readonly ScopeChoice[];
  selectedScopeKey: string;
  onScopeChange: (key: string) => void;
  onNavigateOverview?: () => void;
  onNavigateModules?: () => void;
  onNavigateManagement?: () => void;
  onFailClosed?: (event: ModulesFailClosedEvent) => void;
};

const formatMicros = (
  value: number,
  formatDateTime: I18nRuntime["formatDateTime"],
  invalidTime: string
) => {
  const date = new Date(Math.floor(value / 1000));
  return Number.isNaN(date.getTime()) ? invalidTime : formatDateTime(date);
};

const shortDigest = (value: string) =>
  value.length <= 20 ? value : `${value.slice(0, 10)}...${value.slice(-8)}`;

const reviewQueryKey = (context: ModuleContext) => [
  "module-upgrade-reviews",
  context.origin,
  context.bootID,
  context.sessionID,
  context.principalID,
  context.authorizationRevision,
  context.scopeSetDigest,
  context.scope.kind,
  context.scope.tenant_id,
  context.scope.workspace_id ?? ""
] as const;

const detailQueryKey = (context: ModuleContext, reviewID: string) => [
  ...reviewQueryKey(context),
  "detail",
  reviewID
] as const;

const failureFromError = (error: unknown): ModulesFailClosedEvent => {
  const message = error instanceof Error ? error.message : "Review response was rejected";
  const correlationID = error instanceof ControlModulesError ? error.correlationID : "";
  if (isModulesSessionInvalid(error)) return { kind: "SESSION", message, correlationID };
  if (isModulesPermissionDenied(error)) return { kind: "PERMISSION", message, correlationID };
  if (isModulesStale(error)) return { kind: "STALE", message, correlationID };
  return { kind: "INTEGRITY", message, correlationID };
};

const targetLabel = (item: ModuleUpgradeReviewItem, t: I18nRuntime["t"]) => {
  const target = item.binding_target;
  return target.kind === "PROFILE"
    ? t("modules.binding.profile", { values: { id: target.profile_id } })
    : t("modules.binding.workspaceEndpoint", { values: { workspace: target.workspace_id, endpoint: target.endpoint_id } });
};

function ReviewDetail({
  detail,
  onRetry
}: {
  detail: ModuleUpgradeReviewDetailResponse;
  onRetry: () => void;
}) {
  const { t, formatDateTime } = useOptionalI18n();
  const invalidTime = t("reviews.invalidTime");
  const review = detail.review;
  const targetLabelValue = review.binding_target.kind === "PROFILE"
    ? t("modules.binding.profile", { values: { id: review.binding_target.profile_id } })
    : t("modules.binding.workspaceEndpoint", { values: { workspace: review.binding_target.workspace_id, endpoint: review.binding_target.endpoint_id } });
  return (
    <>
      <dl className="module-facts">
        <div><dt>{t("reviews.detail.reviewID")}</dt><dd className="digest">{shortDigest(detail.review_id)}</dd></div>
        <div><dt>{t("reviews.detail.conclusion")}</dt><dd>{review.conclusion}</dd></div>
        <div><dt>{t("reviews.detail.candidate")}</dt><dd className="digest">{shortDigest(review.candidate_id)}</dd></div>
        <div><dt>{t("reviews.detail.reviewKey")}</dt><dd className="digest">{shortDigest(review.review_key)}</dd></div>
        <div><dt>{t("reviews.detail.target")}</dt><dd>{targetLabelValue}</dd></div>
        <div><dt>{t("reviews.detail.instance")}</dt><dd>{review.target_instance_id}</dd></div>
        <div><dt>{t("reviews.detail.module")}</dt><dd>{review.target_module.id}</dd></div>
        <div><dt>{t("reviews.detail.version")}</dt><dd>{review.target_module.version}</dd></div>
        <div><dt>{t("reviews.detail.artifact")}</dt><dd className="digest">{shortDigest(review.target_artifact_digest)}</dd></div>
        <div><dt>{t("reviews.detail.artifactSize")}</dt><dd>{review.target_artifact_size_bytes}</dd></div>
        <div><dt>{t("reviews.detail.port")}</dt><dd>{review.port.name} / {review.port.exact_version} / {review.port_binding_index}</dd></div>
        <div><dt>{t("reviews.detail.created")}</dt><dd>{formatMicros(detail.created_at_unix_micros, formatDateTime, invalidTime)}</dd></div>
        <div><dt>{t("reviews.detail.operator")}</dt><dd>{review.operator_principal_id}</dd></div>
      </dl>
      <section className="review-subsection">
        <h3>{t("reviews.detail.admission")}</h3>
        <dl className="module-facts">
          <div><dt>{t("reviews.detail.admissionID")}</dt><dd className="digest">{shortDigest(detail.admission.admission_id)}</dd></div>
          <div><dt>{t("reviews.detail.source")}</dt><dd>{detail.admission.source_id}</dd></div>
          <div><dt>{t("reviews.detail.snapshot")}</dt><dd className="digest">{shortDigest(detail.admission.snapshot_id)}</dd></div>
          <div><dt>{t("reviews.detail.manifest")}</dt><dd className="digest">{shortDigest(detail.admission.manifest_ref)}</dd></div>
          <div><dt>{t("reviews.detail.fileCount")}</dt><dd>{detail.admission.covered_file_count}</dd></div>
          <div><dt>{t("reviews.detail.admitted")}</dt><dd>{formatMicros(detail.admission.admitted_at_unix_micros, formatDateTime, invalidTime)}</dd></div>
        </dl>
      </section>
      <section className="review-subsection">
        <h3>{t("reviews.detail.decision")}</h3>
        {detail.decision === undefined ? (
          <p className="empty-copy">{t("reviews.detail.noDecision")}</p>
        ) : (
          <dl className="module-facts">
            <div><dt>{t("reviews.detail.decisionValue")}</dt><dd>{detail.decision.decision}</dd></div>
            <div><dt>{t("reviews.detail.decisionID")}</dt><dd className="digest">{shortDigest(detail.decision.decision_id)}</dd></div>
            <div><dt>{t("reviews.detail.decisionOperator")}</dt><dd>{detail.decision.operator_principal_id}</dd></div>
            <div><dt>{t("reviews.detail.decisionTime")}</dt><dd>{formatMicros(detail.decision.decided_at_unix_micros, formatDateTime, invalidTime)}</dd></div>
            <div><dt>{t("reviews.detail.reason")}</dt><dd>{detail.decision.reason}</dd></div>
          </dl>
        )}
      </section>
      <section className="review-subsection">
        <h3>{t("reviews.detail.reasonCodes")}</h3>
        {review.reason_codes.length === 0 ? (
          <p className="empty-copy">{t("common.none")}</p>
        ) : (
          <ul className="tag-list">
            {review.reason_codes.map((code) => <li className="tag" key={code}>{code}</li>)}
          </ul>
        )}
      </section>
      <footer className="page-footer">
        <span>{t("reviews.footer.projection", { values: { digest: shortDigest(detail.projection_digest) } })}</span>
        <button className="button" type="button" onClick={onRetry}>{t("common.retry")}</button>
      </footer>
    </>
  );
}

export function ModuleUpgradeReviewsPage({
  session,
  context,
  scopeChoices,
  selectedScopeKey,
  onScopeChange,
  onNavigateOverview,
  onNavigateModules,
  onNavigateManagement,
  onFailClosed
}: ModuleUpgradeReviewsPageProps) {
  const { t, formatDateTime } = useOptionalI18n();
  const invalidTime = t("reviews.invalidTime");
  const [selectedReviewID, setSelectedReviewID] = useState<string | null>(null);
  const reviews = useQuery({
    queryKey: reviewQueryKey(context),
    queryFn: ({ signal }) => fetchModuleUpgradeReviews(context, signal),
    staleTime: 30_000,
    retry: false
  });
  const detail = useQuery({
    queryKey: selectedReviewID === null ? ["module-upgrade-review-detail", "disabled"] : detailQueryKey(context, selectedReviewID),
    queryFn: ({ signal }) => {
      if (selectedReviewID === null) throw new Error("review detail is disabled");
      return fetchModuleUpgradeReviewDetail(context, selectedReviewID, signal);
    },
    enabled: selectedReviewID !== null,
    retry: false
  });

  useEffect(() => {
    if (reviews.error !== null) onFailClosed?.(failureFromError(reviews.error));
  }, [onFailClosed, reviews.error]);
  useEffect(() => {
    if (detail.error !== null) onFailClosed?.(failureFromError(detail.error));
  }, [detail.error, onFailClosed]);

  const items = useMemo(() => reviews.data?.items ?? [], [reviews.data]);
  useEffect(() => {
    if (selectedReviewID !== null && !items.some((item) => item.review_id === selectedReviewID)) {
      setSelectedReviewID(null);
    }
  }, [items, selectedReviewID]);

  const navigateOverview = () => onNavigateOverview?.();
  const navigateModules = () => onNavigateModules?.();

  if (reviews.isPending) {
    return <main className="entry" aria-busy="true"><section className="entry__panel entry__panel--compact"><p className="eyebrow">{t("loading.brand")}</p><h1>{t("reviews.loading")}</h1><div className="loading-bar" aria-hidden="true"><span /></div></section></main>;
  }
  if (reviews.error !== null || reviews.data === undefined) {
    return <main className="entry"><section className="entry__panel entry__panel--compact"><p className="eyebrow">{t("reviews.eyebrow")}</p><h1>{t("reviews.unavailable")}</h1><p className="lede">{reviews.error instanceof Error ? reviews.error.message : t("reviews.readFailed")}</p><button className="button button--primary" type="button" onClick={() => { void reviews.refetch(); }}>{t("common.retry")}</button></section></main>;
  }

  return (
    <div className="control-shell control-shell--modules">
      <header className="topbar">
        <a className="brand" href="#overview" aria-label={t("brand.overviewAria")} onClick={navigateOverview}>
          <span className="brand__mark" aria-hidden="true">F</span><span>{t("brand.name")}</span>
        </a>
        <div className="topbar__status"><span className="status-dot" aria-hidden="true" />{t("common.current")}</div>
      </header>
      <aside className="sidebar" aria-label={t("overview.nav.aria")}>
        <p className="sidebar__label">{t("overview.nav.control")}</p>
        <nav>
          <a href="#overview" onClick={navigateOverview}>{t("overview.nav.status")}</a>
          <a href="#modules" onClick={navigateModules}>{t("overview.nav.modules")}</a>
          <a href="#upgrade-reviews" aria-current="page">{t("overview.nav.reviews")}</a>
          <a href="#management" onClick={onNavigateManagement}>{t("overview.nav.management")}</a>
        </nav>
        <div className="session-card"><span>{t("session.open.title")}</span><strong>{session.principal_id}</strong><small>{t("common.expires", { values: { time: formatMicros(session.expires_at_unix_micros, formatDateTime, invalidTime) } })}</small></div>
      </aside>
      <main className="content modules-content" id="upgrade-reviews">
        <section className="page-heading">
          <div><p className="eyebrow">{t("reviews.eyebrow")}</p><h1>{t("reviews.title")}</h1><p className="lede">{t("reviews.description")}</p></div>
          <button className="button" type="button" onClick={() => { void reviews.refetch(); }}>{reviews.isFetching ? t("common.refreshing") : t("reviews.refresh")}</button>
        </section>
        <section className="toolbar modules-toolbar" aria-label={t("reviews.controls.aria")}>
          <label><span>{t("overview.scope")}</span><select value={selectedScopeKey} onChange={(event) => onScopeChange(event.currentTarget.value)}>{scopeChoices.map((choice) => <option value={choice.key} key={choice.key}>{choice.scope.kind === "TENANT" ? t("scope.tenant", { values: { tenant: choice.scope.tenant_id } }) : t("scope.workspace", { values: { tenant: choice.scope.tenant_id, workspace: choice.scope.workspace_id ?? "" } })}</option>)}</select></label>
        </section>
        <div className="reviews-layout modules-layout">
          <section className="data-section modules-list" aria-labelledby="reviews-list-title">
            <header><div><p className="eyebrow">{t("reviews.list.eyebrow")}</p><h2 id="reviews-list-title">{t("reviews.list.title")}</h2></div><span className="section-meta">{t("reviews.list.loaded", { values: { count: items.length } })}</span></header>
            {items.length === 0 ? <p className="empty-copy">{t("reviews.list.empty")}</p> : <ul className="result-list">
              {items.map((item) => <li key={item.review_id}><button className="result-link module-result" type="button" aria-current={selectedReviewID === item.review_id ? "true" : undefined} onClick={() => setSelectedReviewID(item.review_id)}><span className="result-link__title">{item.target_module.id} @ {item.target_module.version}</span><span className="result-link__summary">{t("reviews.list.summary", { values: { target: targetLabel(item, t), conclusion: item.conclusion, decision: item.decision?.decision ?? t("reviews.list.noDecision") } })}</span><span className="result-link__arrow" aria-hidden="true">&gt;</span></button></li>)}
            </ul>}
          </section>
          <section className="data-section module-detail" aria-labelledby="review-detail-title">
            <header><div><p className="eyebrow">{t("reviews.detail.eyebrow")}</p><h2 id="review-detail-title">{selectedReviewID === null ? t("reviews.detail.select") : shortDigest(selectedReviewID)}</h2></div>{selectedReviewID !== null && <button className="button" type="button" onClick={() => setSelectedReviewID(null)}>{t("common.close")}</button>}</header>
            {selectedReviewID === null ? <p className="empty-copy">{t("reviews.detail.selectDescription")}</p> : detail.isPending ? <p className="empty-copy" aria-busy="true">{t("reviews.detail.loading")}</p> : detail.error !== null || detail.data === undefined ? <div className="notice notice--error" role="alert"><strong>{t("reviews.detail.unavailable")}</strong><span>{detail.error instanceof Error ? detail.error.message : t("reviews.detail.readFailed")}</span><button className="button" type="button" onClick={() => { void detail.refetch(); }}>{t("common.retry")}</button></div> : <ReviewDetail detail={detail.data} onRetry={() => { void detail.refetch(); }} />}
          </section>
        </div>
      </main>
    </div>
  );
}
