import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type MouseEvent
} from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  canonicalJSONString,
  scopeKey,
  type ControlSession,
  type PublishedBasis
} from "./contracts.ts";
import {
  ControlModulesError,
  clearModulesOperationCache,
  clearModulesTransportCache,
  createIdempotencyKey,
  dryRunModuleDisable,
  fetchModuleDetail,
  fetchModulesPage,
  isModulesPermissionDenied,
  isModulesSessionInvalid,
  isModulesStale,
  issueModuleDisableConfirmation,
  moduleDetailQueryKey,
  modulesQueryKey,
  mutateModuleDisable,
  withPublishedModulesBasis,
  type ModuleBindingSummary,
  type ModuleContext,
  type ModuleDetailResponse,
  type ModuleDisableBody,
  type ModuleDisableMutationResult,
  type ModulesPageResponse,
  type ModuleSummary
} from "./modules.ts";
import type { ScopeChoice } from "./overview.ts";

export type ModulesFailureKind =
  | "PERMISSION"
  | "SESSION"
  | "STALE"
  | "INTEGRITY";

export type ModulesFailClosedEvent = {
  kind: ModulesFailureKind;
  message: string;
  correlationID: string;
};

export type ModulesPageProps = {
  session: ControlSession;
  context: ModuleContext;
  scopeChoices: readonly ScopeChoice[];
  selectedScopeKey: string;
  navigationKey?: string;
  onScopeChange: (key: string) => void;
  onNavigateOverview?: () => void;
  onFailClosed?: (event: ModulesFailClosedEvent) => void;
  onMutationComplete?: (
    result: ModuleDisableMutationResult
  ) => void | Promise<void>;
};

type OperationStep = "DRY_RUN" | "CONFIRMATION" | "MUTATE";

type ConfirmationReview = {
  principalID: string;
  scopeKind: "TENANT" | "WORKSPACE";
  tenantID: string;
  workspaceID: string;
  scopeDigest: string;
  expectedKind: string;
  expectedResourceID: string;
  expectedRevision: number;
  expectedDigest: string;
  instanceID: string;
  targetProfileID: string;
  portName: string;
  portVersion: string;
  portBindingIndex: number;
  configRef: string;
  authorityCeilingRef: string;
  staticContextRefs: readonly string[];
  failurePolicy: "OPTIONAL";
  inputDigest: string;
  idempotencyKeyDigest: string;
  evaluationDigest: string;
  statementDigest: string;
};

type OperationState =
  | { phase: "IDLE" }
  | { phase: "DRY_RUNNING"; target: string }
  | {
      phase: "DRY_RESULT";
      target: string;
      disposition: "ALREADY_APPLIED" | "NO_CHANGE" | "WOULD_APPLY";
      planDigest: string;
      catalogChange: "NONE" | "RETAIN_INSTANCE" | "REMOVE_INSTANCE";
    }
  | { phase: "CONFIRMING"; target: string }
  | {
      phase: "AWAITING_EXPLICIT_CONFIRMATION";
      target: string;
      planDigest: string;
      statementDigest: string;
      catalogChange: "RETAIN_INSTANCE" | "REMOVE_INSTANCE";
      expiresAtUnixMicros: number;
      review: ConfirmationReview;
    }
  | { phase: "MUTATING"; target: string; exactRetry: boolean }
  | {
      phase: "MUTATION_UNCERTAIN";
      target: string;
      message: string;
      correlationID: string;
    }
  | {
      phase: "COMPLETE";
      target: string;
      status: "NO_CHANGE" | "APPLIED";
      receiptDigest: string;
      completedAtUnixMicros: number;
    }
  | {
      phase: "ERROR";
      target: string;
      step: OperationStep;
      retryable: boolean;
      message: string;
      correlationID: string;
    };

type PendingOperation = {
  identity: string;
  target: string;
  context: ModuleContext;
  body: ModuleDisableBody;
  selectedBinding: ModuleBindingSummary;
  idempotencyKey: string;
  evaluationDigest: string;
  confirmationProof: string;
  expiresAtUnixMicros: number;
  planDigest: string;
  catalogChange: "NONE" | "RETAIN_INSTANCE" | "REMOVE_INSTANCE";
  dryRunProjectionCanonical: string;
};

const shortDigest = (value: string) =>
  value.length <= 20 ? value : `${value.slice(0, 10)}...${value.slice(-8)}`;

const formatMicros = (value: number) => {
  const date = new Date(Math.floor(value / 1000));
  return Number.isNaN(date.getTime()) ? "Invalid time" : date.toLocaleString();
};

const contextIdentity = (context: ModuleContext) =>
  JSON.stringify([
    context.origin,
    context.bootID,
    context.sessionID,
    context.principalID,
    context.authorizationRevision,
    context.scopeSetDigest,
    context.scope.kind,
    context.scope.tenant_id,
    context.scope.workspace_id ?? ""
  ]);

const publishedIdentity = (
  source: Pick<ModulesPageResponse | ModuleDetailResponse, "published_pointer" | "basis">
) =>
  JSON.stringify([
    source.published_pointer.kind,
    source.published_pointer.resource_id,
    source.published_pointer.revision,
    source.published_pointer.digest,
    source.basis.tenant_id,
    source.basis.pointer_revision,
    source.basis.control.id,
    source.basis.control.revision,
    source.basis.control.digest,
    source.basis.catalog.id,
    source.basis.catalog.revision,
    source.basis.catalog.digest
  ]);

const samePublishedBasis = (
  left: Pick<ModulesPageResponse | ModuleDetailResponse, "published_pointer" | "basis">,
  right: Pick<ModulesPageResponse | ModuleDetailResponse, "published_pointer" | "basis">
) => publishedIdentity(left) === publishedIdentity(right);

const bindingTargetLabel = (binding: ModuleBindingSummary) =>
  binding.target.kind === "PROFILE"
    ? `Profile ${binding.target.profile_id}`
    : `Workspace ${binding.target.workspace_id} / endpoint ${binding.target.endpoint_id}`;

const operationTargetLabel = (summary: ModuleSummary, binding: ModuleBindingSummary) =>
  `${summary.instance_id} from ${bindingTargetLabel(binding)}`;

const detachBinding = (binding: ModuleBindingSummary): ModuleBindingSummary => ({
  target:
    binding.target.kind === "PROFILE"
      ? { kind: "PROFILE", profile_id: binding.target.profile_id }
      : {
          kind: "WORKSPACE_CHANNEL_ENDPOINT",
          workspace_id: binding.target.workspace_id,
          endpoint_id: binding.target.endpoint_id
        },
  port: { name: binding.port.name, exact_version: binding.port.exact_version },
  port_binding_index: binding.port_binding_index,
  config_ref: binding.config_ref,
  authority_ceiling_ref: binding.authority_ceiling_ref,
  static_context_refs: [...binding.static_context_refs],
  failure_policy: binding.failure_policy
});

const sameBinding = (
  selected: ModuleBindingSummary,
  returned: ModuleBindingSummary | undefined
) => {
  if (
    returned === undefined ||
    selected.target.kind !== returned.target.kind ||
    selected.port.name !== returned.port.name ||
    selected.port.exact_version !== returned.port.exact_version ||
    selected.port_binding_index !== returned.port_binding_index ||
    selected.config_ref !== returned.config_ref ||
    selected.authority_ceiling_ref !== returned.authority_ceiling_ref ||
    selected.failure_policy !== returned.failure_policy ||
    selected.static_context_refs.length !== returned.static_context_refs.length ||
    selected.static_context_refs.some(
      (reference, index) => reference !== returned.static_context_refs[index]
    )
  ) return false;
  if (selected.target.kind === "PROFILE" && returned.target.kind === "PROFILE") {
    return selected.target.profile_id === returned.target.profile_id;
  }
  return (
    selected.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" &&
    returned.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" &&
    selected.target.workspace_id === returned.target.workspace_id &&
    selected.target.endpoint_id === returned.target.endpoint_id
  );
};

const sameModuleSummary = (left: ModuleSummary, right: ModuleSummary) =>
  left.instance_id === right.instance_id &&
  left.module_id === right.module_id &&
  left.exact_version === right.exact_version &&
  left.artifact_digest === right.artifact_digest &&
  left.execution_class === right.execution_class &&
  left.adapter_identity === right.adapter_identity &&
  left.activation_revision === right.activation_revision &&
  left.visible_binding_count === right.visible_binding_count &&
  left.provides.length === right.provides.length &&
  left.provides.every(
    (port, index) =>
      port.name === right.provides[index].name &&
      port.exact_version === right.provides[index].exact_version
  );

export const isModuleDisableCandidate = (
  summary: ModuleSummary,
  binding: ModuleBindingSummary
) =>
  summary.execution_class === "DECLARATIVE" &&
  binding.target.kind === "PROFILE" &&
  binding.port.name === "context.provide" &&
  binding.port.exact_version === "v1" &&
  binding.failure_policy === "OPTIONAL";

const sameBindingTarget = (
  left: ModuleBindingSummary,
  right: ModuleBindingSummary
) => {
  if (left.target.kind !== right.target.kind) return false;
  if (left.target.kind === "PROFILE" && right.target.kind === "PROFILE") {
    return left.target.profile_id === right.target.profile_id;
  }
  return (
    left.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" &&
    right.target.kind === "WORKSPACE_CHANNEL_ENDPOINT" &&
    left.target.workspace_id === right.target.workspace_id &&
    left.target.endpoint_id === right.target.endpoint_id
  );
};

const isLowestDisableCandidate = (
  summary: ModuleSummary,
  bindings: readonly ModuleBindingSummary[],
  selected: ModuleBindingSummary
) =>
  bindings.some((binding) => sameBinding(selected, binding)) &&
  bindings
    .filter(
      (binding) =>
        isModuleDisableCandidate(summary, binding) &&
        sameBindingTarget(binding, selected) &&
        binding.port.name === selected.port.name &&
        binding.port.exact_version === selected.port.exact_version
    )
    .every(
      (binding) => selected.port_binding_index <= binding.port_binding_index
    );

const moduleSearchMatch = (summary: ModuleSummary, rawSearch: string) => {
  const search = rawSearch.trim().toLocaleLowerCase("en");
  if (search === "") return true;
  return [
    summary.instance_id,
    summary.module_id,
    summary.exact_version,
    summary.execution_class,
    summary.adapter_identity
  ]
    .join("\n")
    .toLocaleLowerCase("en")
    .includes(search);
};

const failureFromError = (error: unknown): ModulesFailClosedEvent | null => {
  if (isModulesSessionInvalid(error)) {
    return {
      kind: "SESSION",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  if (isModulesPermissionDenied(error)) {
    return {
      kind: "PERMISSION",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  if (isModulesStale(error)) {
    return {
      kind: "STALE",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  if (
    error instanceof ControlModulesError &&
    (error.code === "INVALID_RESPONSE" ||
      error.code === "INVALID_ERROR_RESPONSE" ||
      error.code === "INVALID_CLIENT_INPUT" ||
      error.code === "INTEGRITY_FAILURE" ||
      error.code === "CONTROL_ORIGIN_MISMATCH" ||
      error.code === "PUBLISHED_BASIS_REQUIRED" ||
      error.code === "CONFIRMATION_CONTEXT_MISSING")
  ) {
    return {
      kind: "INTEGRITY",
      message: error.message,
      correlationID: error.correlationID
    };
  }
  return null;
};

const safeErrorText = (
  error: unknown,
  pending: PendingOperation | null,
  fallback: string
) => {
  const message = error instanceof Error ? error.message : fallback;
  const correlationID =
    error instanceof ControlModulesError ? error.correlationID : "";
  const secrets = pending === null
    ? []
    : [
        pending.idempotencyKey,
        pending.confirmationProof,
        pending.context.csrfToken
      ].filter(
        (value) => value !== ""
      );
  if (
    secrets.some(
      (secret) => message.includes(secret) || correlationID.includes(secret)
    )
  ) {
    return { message: fallback, correlationID: "" };
  }
  return { message, correlationID };
};

const erasePending = (pending: PendingOperation | null) => {
  if (pending === null) return;
  pending.identity = "";
  pending.idempotencyKey = "";
  pending.evaluationDigest = "";
  pending.confirmationProof = "";
  pending.dryRunProjectionCanonical = "";
  pending.context.csrfToken = "";
};

function FailurePanel({
  failure,
  onRetry
}: {
  failure: ModulesFailClosedEvent;
  onRetry?: () => void;
}) {
  const title = {
    PERMISSION: "Modules permission denied",
    SESSION: "Control session unavailable",
    STALE: "Published basis changed",
    INTEGRITY: "Modules response was rejected"
  }[failure.kind];
  return (
    <main className="entry" aria-labelledby="modules-failure-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">Modules fail-closed boundary</p>
        <h1 id="modules-failure-title">{title}</h1>
        <p className="lede">{failure.message}</p>
        {failure.correlationID !== "" && (
          <p className="correlation">Correlation: {failure.correlationID}</p>
        )}
        {onRetry !== undefined && (
          <button className="button button--primary" type="button" onClick={onRetry}>
            Reload validated Modules data
          </button>
        )}
      </section>
    </main>
  );
}

function ModulesLoading() {
  return (
    <main className="entry" aria-busy="true" aria-labelledby="modules-loading-title">
      <section className="entry__panel entry__panel--compact">
        <p className="eyebrow">FreeAgent Control</p>
        <h1 id="modules-loading-title">Loading Modules...</h1>
        <div className="loading-bar" aria-hidden="true"><span /></div>
        <p className="muted">Waiting for one bounded, authenticated projection.</p>
      </section>
    </main>
  );
}

type OperationPanelProps = {
  state: OperationState;
  confirmationAccepted: boolean;
  onConfirmationAccepted: (accepted: boolean) => void;
  onRequestConfirmation: () => void;
  onAcknowledgeDryResult: () => void;
  onExplicitConfirm: () => void;
  onExactRetry: () => void;
  onRetryStep: () => void;
  onReset: () => void;
  onReload: () => void;
};

function OperationPanel({
  state,
  confirmationAccepted,
  onConfirmationAccepted,
  onRequestConfirmation,
  onAcknowledgeDryResult,
  onExplicitConfirm,
  onExactRetry,
  onRetryStep,
  onReset,
  onReload
}: OperationPanelProps) {
  if (state.phase === "IDLE") return null;
  if (state.phase === "DRY_RUNNING") {
    return (
      <section className="module-operation" aria-busy="true">
        <p className="eyebrow">Effect-free evaluation</p>
        <h3>Running server dry-run</h3>
        <p>No published state is being changed for {state.target}.</p>
      </section>
    );
  }
  if (state.phase === "DRY_RESULT") {
    return (
      <section className="module-operation" aria-live="polite">
        <p className="eyebrow">Authoritative dry-run result</p>
        <h3>{state.disposition.replaceAll("_", " ")}</h3>
        <p>
          Target: {state.target}. Catalog effect: {state.catalogChange.replaceAll("_", " ")}.
        </p>
        <p className="digest">Plan {shortDigest(state.planDigest)}</p>
        {state.disposition === "WOULD_APPLY" ? (
          <div className="module-operation__actions">
            <button
              className="button button--primary"
              type="button"
              onClick={onRequestConfirmation}
            >
              Request short-lived confirmation
            </button>
            <button className="button" type="button" onClick={onReset}>Cancel</button>
          </div>
        ) : (
          <>
            <p>No mutation request was sent.</p>
            <button className="button" type="button" onClick={onAcknowledgeDryResult}>
              Done and refresh authority
            </button>
          </>
        )}
      </section>
    );
  }
  if (state.phase === "CONFIRMING") {
    return (
      <section className="module-operation" aria-busy="true">
        <p className="eyebrow">Confirmation evaluation</p>
        <h3>Binding the exact request...</h3>
        <p>The server is re-evaluating {state.target}; no mutation is being sent.</p>
      </section>
    );
  }
  if (state.phase === "AWAITING_EXPLICIT_CONFIRMATION") {
    return (
      <section
        className="module-operation module-operation--warning"
        role="alertdialog"
        aria-labelledby="module-confirm-title"
      >
        <p className="eyebrow">Explicit operator confirmation</p>
        <h3 id="module-confirm-title">Disable this exact Profile binding?</h3>
        <p>
          The server evaluated {state.target} as WOULD APPLY. The projected catalog effect is
          {` ${state.catalogChange.replaceAll("_", " ")}`}.
        </p>
        <p>Confirmation expires {formatMicros(state.expiresAtUnixMicros)}.</p>
        <dl className="module-facts">
          <div><dt>Principal</dt><dd>{state.review.principalID}</dd></div>
          <div>
            <dt>Scope</dt>
            <dd>
              {state.review.scopeKind} / tenant {state.review.tenantID}
              {state.review.workspaceID === "" ? "" : ` / workspace ${state.review.workspaceID}`}
            </dd>
          </div>
          <div><dt>Published pointer revision</dt><dd>{state.review.expectedRevision}</dd></div>
          <div><dt>Instance</dt><dd>{state.review.instanceID}</dd></div>
          <div>
            <dt>Port / binding index</dt>
            <dd>
              {state.review.portName}@{state.review.portVersion} / {state.review.portBindingIndex}
            </dd>
          </div>
          <div><dt>Profile target</dt><dd>{state.review.targetProfileID}</dd></div>
          <div><dt>Failure policy</dt><dd>{state.review.failurePolicy}</dd></div>
          <div><dt>Catalog effect</dt><dd>{state.catalogChange.replaceAll("_", " ")}</dd></div>
        </dl>
        <div className="module-operation__digests">
          <p><strong>Scope digest</strong> <code>{state.review.scopeDigest}</code></p>
          <p>
            <strong>Expected ref</strong>{" "}
            <code>
              {state.review.expectedKind}:{state.review.expectedResourceID}:
              {state.review.expectedRevision}:{state.review.expectedDigest}
            </code>
          </p>
          <p><strong>Config ref</strong> <code>{state.review.configRef}</code></p>
          <p><strong>Authority ceiling</strong> <code>{state.review.authorityCeilingRef}</code></p>
          <p>
            <strong>Static context refs</strong>{" "}
            <code>
              {state.review.staticContextRefs.length === 0
                ? "none"
                : state.review.staticContextRefs.join(",")}
            </code>
          </p>
          <p><strong>Input digest</strong> <code>{state.review.inputDigest}</code></p>
          <p><strong>Plan digest</strong> <code>{state.planDigest}</code></p>
          <p><strong>Idempotency-key digest</strong> <code>{state.review.idempotencyKeyDigest}</code></p>
          <p><strong>Evaluation digest</strong> <code>{state.review.evaluationDigest}</code></p>
          <p><strong>Statement digest</strong> <code>{state.review.statementDigest}</code></p>
        </div>
        <label className="module-confirmation-check">
          <input
            type="checkbox"
            checked={confirmationAccepted}
            onChange={(event) => onConfirmationAccepted(event.currentTarget.checked)}
          />
          <span>I understand this sends one governed published-state mutation.</span>
        </label>
        <div className="module-operation__actions">
          <button
            className="button button--primary"
            type="button"
            disabled={!confirmationAccepted}
            onClick={onExplicitConfirm}
          >
            Confirm and disable binding
          </button>
          <button className="button" type="button" onClick={onReset}>Cancel</button>
        </div>
      </section>
    );
  }
  if (state.phase === "MUTATING") {
    return (
      <section className="module-operation" aria-busy="true">
        <p className="eyebrow">Governed mutation</p>
        <h3>{state.exactRetry ? "Replaying the exact request..." : "Waiting for an authoritative receipt..."}</h3>
        <p>The page will not update local authority optimistically for {state.target}.</p>
      </section>
    );
  }
  if (state.phase === "MUTATION_UNCERTAIN") {
    return (
      <section className="module-operation module-operation--warning" role="alert">
        <p className="eyebrow">Outcome not yet known</p>
        <h3>Do not construct a replacement mutation</h3>
        <p>{state.message}</p>
        <p>
          Only an exact replay of the original body, idempotency key, precondition, and evaluation
          digest is available. The confirmation proof will not be resent.
        </p>
        {state.correlationID !== "" && (
          <p className="correlation">Correlation: {state.correlationID}</p>
        )}
        <div className="module-operation__actions">
          <button className="button button--primary" type="button" onClick={onExactRetry}>
            Retry exact request
          </button>
          <button className="button" type="button" onClick={onReload}>
            Discard retry state and reload authority
          </button>
        </div>
      </section>
    );
  }
  if (state.phase === "COMPLETE") {
    return (
      <section className="module-operation module-operation--complete" aria-live="polite">
        <p className="eyebrow">Authoritative mutation receipt</p>
        <h3>{state.status.replaceAll("_", " ")}</h3>
        <p>
          The server completed {state.target} at {formatMicros(state.completedAtUnixMicros)}.
        </p>
        <p className="digest">Receipt {shortDigest(state.receiptDigest)}</p>
        <button className="button" type="button" onClick={onReset}>Close receipt</button>
      </section>
    );
  }
  return (
    <section className="module-operation notice notice--error" role="alert">
      <p className="eyebrow">{state.step.replaceAll("_", " ")} stopped</p>
      <h3>The operation did not advance</h3>
      <p>{state.message}</p>
      {state.correlationID !== "" && (
        <p className="correlation">Correlation: {state.correlationID}</p>
      )}
      <div className="module-operation__actions">
        {state.retryable && (
          <button className="button button--primary" type="button" onClick={onRetryStep}>
            Retry same step
          </button>
        )}
        <button className="button" type="button" onClick={onReset}>
          {state.retryable ? "Cancel" : "Restart review"}
        </button>
      </div>
    </section>
  );
}

export function ModulesPage({
  session,
  context,
  scopeChoices,
  selectedScopeKey,
  navigationKey = "modules",
  onScopeChange,
  onNavigateOverview,
  onFailClosed,
  onMutationComplete
}: ModulesPageProps) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [selectedInstanceID, setSelectedInstanceID] = useState<string | null>(null);
  const [operation, setOperation] = useState<OperationState>({ phase: "IDLE" });
  const [confirmationAccepted, setConfirmationAccepted] = useState(false);
  const [operationFailure, setOperationFailure] =
    useState<ModulesFailClosedEvent | null>(null);

  const pendingRef = useRef<PendingOperation | null>(null);
  const operationAbortRef = useRef<AbortController | null>(null);
  const operationEpochRef = useRef(0);
  const operationBusyRef = useRef(false);
  const publishedFailureRef = useRef("");

  const rawContextIdentity = useMemo(() => contextIdentity(context), [context]);
  const sessionIdentity = JSON.stringify([
    session.schema_version,
    session.boot_id,
    session.session_id,
    session.principal_id,
    session.authorization_revision,
    session.scope_set_digest,
    session.issued_at_unix_micros,
    session.expires_at_unix_micros,
    [...session.capabilities].sort()
  ]);
  const selectedChoice = useMemo(
    () => scopeChoices.find((choice) => choice.key === selectedScopeKey) ?? null,
    [scopeChoices, selectedScopeKey]
  );
  const sessionExpired = session.expires_at_unix_micros <= Date.now() * 1000;
  const contextIsValid =
    session.schema_version === "control-session/v1" &&
    session.boot_id === context.bootID &&
    session.session_id === context.sessionID &&
    session.principal_id === context.principalID &&
    session.authorization_revision === context.authorizationRevision &&
    session.scope_set_digest === context.scopeSetDigest &&
    (context.sessionExpiresAtUnixMicros === undefined ||
      context.sessionExpiresAtUnixMicros === session.expires_at_unix_micros) &&
    session.capabilities.includes("OBSERVE") &&
    selectedChoice !== null &&
    selectedChoice.key === scopeKey(context.scope) &&
    selectedChoice.key === scopeKey(selectedChoice.scope) &&
    !sessionExpired;

  const modules = useInfiniteQuery({
    queryKey: modulesQueryKey(context),
    queryFn: ({ pageParam, signal }) =>
      fetchModulesPage(context, pageParam === "" ? undefined : pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) =>
      lastPage.has_more ? lastPage.next_cursor : undefined,
    enabled: contextIsValid,
    staleTime: 30_000,
    retry: false
  });

  const summaries = useMemo(
    () => modules.data?.pages.flatMap((page) => page.items) ?? [],
    [modules.data]
  );
  const selectedSummary = useMemo(
    () => summaries.find((item) => item.instance_id === selectedInstanceID) ?? null,
    [selectedInstanceID, summaries]
  );

  const detail = useQuery<ModuleDetailResponse, ControlModulesError>({
    queryKey:
      selectedInstanceID === null
        ? ["module-detail", "disabled"]
        : moduleDetailQueryKey(context, selectedInstanceID),
    queryFn: ({ signal }) => {
      if (selectedInstanceID === null) {
        throw new ControlModulesError(
          "Module detail query is disabled",
          0,
          "INVALID_CLIENT_INPUT"
        );
      }
      return fetchModuleDetail(context, selectedInstanceID, signal);
    },
    enabled: contextIsValid && selectedInstanceID !== null,
    staleTime: 30_000,
    retry: false
  });

  const firstPage = modules.data?.pages[0];
  const pagesAligned = useMemo(() => {
    if (firstPage === undefined || modules.data === undefined) return true;
    return modules.data.pages.every(
      (page) =>
        samePublishedBasis(firstPage, page) &&
        page.source_revision === firstPage.source_revision &&
        page.source_digest === firstPage.source_digest &&
        page.view_snapshot_digest === firstPage.view_snapshot_digest &&
        page.sort_version === firstPage.sort_version &&
        page.filter_digest === firstPage.filter_digest
    );
  }, [firstPage, modules.data]);
  const detailAligned =
    detail.data === undefined ||
    firstPage === undefined ||
    (samePublishedBasis(firstPage, detail.data) &&
      detail.data.source_revision === firstPage.source_revision &&
      detail.data.source_digest === firstPage.source_digest &&
      selectedSummary !== null &&
      sameModuleSummary(selectedSummary, detail.data.module.summary));
  const detailBasisIdentity = detail.data === undefined ? "" : publishedIdentity(detail.data);
  const currentOperationIdentity = JSON.stringify([
    rawContextIdentity,
    context.sessionEpoch,
    sessionIdentity,
    navigationKey,
    selectedInstanceID ?? "",
    detailBasisIdentity
  ]);

  const destroyPending = useCallback((clearOperationBindings: boolean) => {
    operationEpochRef.current += 1;
    operationBusyRef.current = false;
    operationAbortRef.current?.abort();
    operationAbortRef.current = null;
    erasePending(pendingRef.current);
    pendingRef.current = null;
    setConfirmationAccepted(false);
    if (clearOperationBindings) clearModulesOperationCache();
  }, []);

  const resetOperation = useCallback(() => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
  }, [destroyPending]);

  useEffect(() => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    return () => destroyPending(true);
  }, [currentOperationIdentity, destroyPending]);

  useEffect(() => {
    if (
      selectedInstanceID !== null &&
      modules.data !== undefined &&
      !summaries.some((item) => item.instance_id === selectedInstanceID)
    ) {
      setSelectedInstanceID(null);
    }
  }, [modules.data, selectedInstanceID, summaries]);

  useEffect(() => {
    const remaining = Math.ceil(session.expires_at_unix_micros / 1000 - Date.now());
    if (remaining > 2_147_483_647) return;
    const timer = window.setTimeout(() => {
      destroyPending(true);
      setOperation({ phase: "IDLE" });
      setOperationFailure({
        kind: "SESSION",
        message: "The control session expired before this operation completed.",
        correlationID: ""
      });
    }, Math.max(0, remaining));
    return () => window.clearTimeout(timer);
  }, [destroyPending, session.expires_at_unix_micros]);

  useEffect(() => {
    if (operation.phase !== "AWAITING_EXPLICIT_CONFIRMATION") return;
    const remaining = Math.ceil(
      operation.expiresAtUnixMicros / 1000 - Date.now()
    );
    const timer = window.setTimeout(() => {
      destroyPending(true);
      setOperation({
        phase: "ERROR",
        target: operation.target,
        step: "CONFIRMATION",
        retryable: false,
        message: "The short-lived confirmation expired. Start a new dry-run.",
        correlationID: ""
      });
    }, Math.max(0, remaining));
    return () => window.clearTimeout(timer);
  }, [destroyPending, operation]);

  const contextFailure = useMemo<ModulesFailClosedEvent | null>(() => {
    if (contextIsValid) return null;
    if (sessionExpired) {
      return {
        kind: "SESSION",
        message: "The control session has expired. Open a new handoff.",
        correlationID: ""
      };
    }
    if (!session.capabilities.includes("OBSERVE")) {
      return {
        kind: "PERMISSION",
        message: "This session does not hold OBSERVE for the Modules surface.",
        correlationID: ""
      };
    }
    return {
      kind: "INTEGRITY",
      message: "The Modules session, scope, and transport context do not match exactly.",
      correlationID: ""
    };
  }, [contextIsValid, session.capabilities, sessionExpired]);

  const readFailure = useMemo<ModulesFailClosedEvent | null>(() => {
    if (!pagesAligned || !detailAligned) {
      return {
        kind: "STALE",
        message: "The Modules list and detail are not bound to one published basis.",
        correlationID: ""
      };
    }
    const error = failureFromError(modules.error) !== null
      ? modules.error
      : failureFromError(detail.error) !== null
        ? detail.error
        : null;
    const failure = failureFromError(error);
    if (failure === null) return null;
    const safe = safeErrorText(
      error,
      pendingRef.current,
      "The Modules response failed its fail-closed boundary."
    );
    return { ...failure, ...safe };
  }, [detail.error, detailAligned, modules.error, pagesAligned]);

  const displayedFailure = operationFailure ?? contextFailure ?? readFailure;

  useEffect(() => {
    if (displayedFailure === null) {
      publishedFailureRef.current = "";
      return;
    }
    const key = JSON.stringify(displayedFailure);
    if (publishedFailureRef.current === key) return;
    publishedFailureRef.current = key;
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    onFailClosed?.(displayedFailure);
  }, [destroyPending, displayedFailure, onFailClosed]);

  const enterCriticalFailure = useCallback(
    (failure: ModulesFailClosedEvent) => {
      destroyPending(true);
      setOperation({ phase: "IDLE" });
      setOperationFailure(failure);
    },
    [destroyPending]
  );

  const handleStepError = useCallback(
    (
      step: OperationStep,
      error: unknown,
      epoch: number,
      fallback: string
    ) => {
      if (epoch !== operationEpochRef.current) return;
      const pending = pendingRef.current;
      const safe = step === "MUTATE"
        ? { message: fallback, correlationID: "" }
        : safeErrorText(error, pending, fallback);
      if (
        step === "MUTATE" &&
        error instanceof ControlModulesError &&
        error.outcomeMayBeCommitted
      ) {
        if (pending !== null) pending.confirmationProof = "";
        setOperation({
          phase: "MUTATION_UNCERTAIN",
          target: pending?.target ?? "the selected binding",
          message: safe.message,
          correlationID: safe.correlationID
        });
        return;
      }
      const critical = failureFromError(error);
      if (critical !== null) {
        enterCriticalFailure({
          ...critical,
          message: safe.message,
          correlationID: safe.correlationID
        });
        return;
      }
      if (step === "MUTATE") {
        erasePending(pending);
        pendingRef.current = null;
        clearModulesOperationCache();
      }
      setOperation({
        phase: "ERROR",
        target: pending?.target ?? "the selected binding",
        step,
        retryable: step !== "MUTATE" && pending !== null,
        message: safe.message,
        correlationID: safe.correlationID
      });
    },
    [enterCriticalFailure]
  );

  const runDryRun = useCallback(async () => {
    const pending = pendingRef.current;
    if (
      pending === null ||
      pending.identity !== currentOperationIdentity ||
      operationBusyRef.current
    ) return;
    operationBusyRef.current = true;
    const epoch = operationEpochRef.current;
    const controller = new AbortController();
    operationAbortRef.current = controller;
    setOperation({ phase: "DRY_RUNNING", target: pending.target });
    try {
      const result = await dryRunModuleDisable(
        pending.context,
        pending.body,
        controller.signal
      );
      if (epoch !== operationEpochRef.current) return;
      if (
        result.projection.disposition === "WOULD_APPLY" &&
        !sameBinding(pending.selectedBinding, result.projection.binding_removal)
      ) {
        enterCriticalFailure({
          kind: "INTEGRITY",
          message:
            "The dry-run selected a different binding than the exact binding reviewed by the operator.",
          correlationID: ""
        });
        return;
      }
      pending.planDigest = result.projection.plan_digest;
      pending.catalogChange = result.projection.catalog_change;
      pending.dryRunProjectionCanonical = canonicalJSONString(result.projection);
      setOperation({
        phase: "DRY_RESULT",
        target: pending.target,
        disposition: result.projection.disposition,
        planDigest: result.projection.plan_digest,
        catalogChange: result.projection.catalog_change
      });
    } catch (error) {
      handleStepError(
        "DRY_RUN",
        error,
        epoch,
        "The MODULE_DISABLE dry-run did not return a trusted result."
      );
    } finally {
      if (epoch === operationEpochRef.current) {
        operationBusyRef.current = false;
        operationAbortRef.current = null;
      }
    }
  }, [currentOperationIdentity, enterCriticalFailure, handleStepError]);

  const startDryRun = useCallback(
    (summary: ModuleSummary, binding: ModuleBindingSummary) => {
      if (
        detail.data === undefined ||
        !contextIsValid ||
        context.scope.kind !== "TENANT" ||
        !session.capabilities.includes("OPERATE_MODULES") ||
        modules.error !== null ||
        detail.error !== null ||
        modules.isFetching ||
        detail.isFetching ||
        !pagesAligned ||
        !detailAligned ||
        !isModuleDisableCandidate(summary, binding) ||
        !isLowestDisableCandidate(
          summary,
          detail.data.module.bindings,
          binding
        ) ||
        binding.target.kind !== "PROFILE"
      ) return;
      destroyPending(true);
      const operationContext = withPublishedModulesBasis(context, detail.data);
      const nextPending: PendingOperation = {
        identity: currentOperationIdentity,
        target: operationTargetLabel(summary, binding),
        context: operationContext,
        body: {
          schema_version: "control-module-disable-dry-run-input/v1",
          expected_pointer_revision: detail.data.basis.pointer_revision,
          binding_target: {
            kind: "PROFILE",
            profile_id: binding.target.profile_id
          },
          instance_id: summary.instance_id,
          port: { name: "context.provide", exact_version: "v1" }
        },
        selectedBinding: detachBinding(binding),
        idempotencyKey: "",
        evaluationDigest: "",
        confirmationProof: "",
        expiresAtUnixMicros: 0,
        planDigest: "",
        catalogChange: "NONE",
        dryRunProjectionCanonical: ""
      };
      pendingRef.current = nextPending;
      setOperationFailure(null);
      setConfirmationAccepted(false);
      void runDryRun();
    },
    [
      context,
      contextIsValid,
      currentOperationIdentity,
      destroyPending,
      detail.data,
      detail.error,
      detail.isFetching,
      detailAligned,
      modules.error,
      modules.isFetching,
      pagesAligned,
      runDryRun,
      session.capabilities
    ]
  );

  const runConfirmation = useCallback(async () => {
    const pending = pendingRef.current;
    const confirmationStepAllowed =
      (operation.phase === "DRY_RESULT" &&
        operation.disposition === "WOULD_APPLY") ||
      (operation.phase === "ERROR" && operation.step === "CONFIRMATION");
    if (
      pending === null ||
      pending.identity !== currentOperationIdentity ||
      pending.planDigest === "" ||
      pending.catalogChange === "NONE" ||
      !confirmationStepAllowed ||
      operationBusyRef.current
    ) return;
    if (pending.idempotencyKey === "") {
      try {
        pending.idempotencyKey = createIdempotencyKey();
      } catch (error) {
        handleStepError(
          "CONFIRMATION",
          error,
          operationEpochRef.current,
          "A secure confirmation request could not be prepared."
        );
        return;
      }
    }
    operationBusyRef.current = true;
    const epoch = operationEpochRef.current;
    const controller = new AbortController();
    operationAbortRef.current = controller;
    setOperation({ phase: "CONFIRMING", target: pending.target });
    try {
      const result = await issueModuleDisableConfirmation(
        pending.context,
        pending.body,
        pending.idempotencyKey,
        controller.signal
      );
      if (epoch !== operationEpochRef.current) {
        result.confirmation_proof = "";
        return;
      }
      if (result.expires_at_unix_micros <= Date.now() * 1000) {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "STALE",
          message: "The confirmation expired before it could be presented for explicit approval.",
          correlationID: ""
        });
        return;
      }
      if (result.evaluation.projection.disposition !== "WOULD_APPLY") {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "STALE",
          message: "Confirmation no longer evaluates the exact request as WOULD APPLY.",
          correlationID: ""
        });
        return;
      }
      if (
        !sameBinding(
          pending.selectedBinding,
          result.evaluation.projection.binding_removal
        )
      ) {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "INTEGRITY",
          message:
            "Confirmation selected a different binding than the exact binding reviewed by the operator.",
          correlationID: ""
        });
        return;
      }
      if (
        pending.dryRunProjectionCanonical === "" ||
        canonicalJSONString(result.evaluation.projection) !==
          pending.dryRunProjectionCanonical
      ) {
        result.confirmation_proof = "";
        enterCriticalFailure({
          kind: "STALE",
          message: "Confirmation drifted from the exact authoritative dry-run projection.",
          correlationID: ""
        });
        return;
      }
      const proof = result.confirmation_proof;
      result.confirmation_proof = "";
      pending.confirmationProof = proof;
      pending.evaluationDigest = result.evaluation_digest;
      pending.expiresAtUnixMicros = result.expires_at_unix_micros;
      pending.planDigest = result.evaluation.projection.plan_digest;
      pending.catalogChange = result.evaluation.projection.catalog_change;
      if (
        pending.catalogChange !== "RETAIN_INSTANCE" &&
        pending.catalogChange !== "REMOVE_INSTANCE"
      ) {
        enterCriticalFailure({
          kind: "INTEGRITY",
          message: "Confirmation returned an invalid MODULE_DISABLE catalog effect.",
          correlationID: ""
        });
        return;
      }
      setConfirmationAccepted(false);
      setOperation({
        phase: "AWAITING_EXPLICIT_CONFIRMATION",
        target: pending.target,
        planDigest: pending.planDigest,
        statementDigest: result.statement_digest,
        catalogChange: pending.catalogChange,
        expiresAtUnixMicros: pending.expiresAtUnixMicros,
        review: {
          principalID: result.statement.principal_id,
          scopeKind: result.statement.scope.kind,
          tenantID: result.statement.scope.tenant_id,
          workspaceID: result.statement.scope.workspace_id ?? "",
          scopeDigest: result.statement.scope_digest,
          expectedKind: result.statement.expected_ref.kind,
          expectedResourceID: result.statement.expected_ref.resource_id,
          expectedRevision: result.statement.expected_ref.revision,
          expectedDigest: result.statement.expected_ref.digest,
          instanceID: result.evaluation.projection.instance_id,
          targetProfileID:
            pending.selectedBinding.target.kind === "PROFILE"
              ? pending.selectedBinding.target.profile_id
              : "",
          portName: pending.selectedBinding.port.name,
          portVersion: pending.selectedBinding.port.exact_version,
          portBindingIndex: pending.selectedBinding.port_binding_index,
          configRef: pending.selectedBinding.config_ref,
          authorityCeilingRef: pending.selectedBinding.authority_ceiling_ref,
          staticContextRefs: [...pending.selectedBinding.static_context_refs],
          failurePolicy: "OPTIONAL",
          inputDigest: result.statement.input_digest,
          idempotencyKeyDigest: result.statement.idempotency_key_digest,
          evaluationDigest: result.evaluation_digest,
          statementDigest: result.statement_digest
        }
      });
    } catch (error) {
      handleStepError(
        "CONFIRMATION",
        error,
        epoch,
        "The confirmation endpoint did not return a trusted exact evaluation."
      );
    } finally {
      if (epoch === operationEpochRef.current) {
        operationBusyRef.current = false;
        operationAbortRef.current = null;
      }
    }
  }, [
    currentOperationIdentity,
    enterCriticalFailure,
    handleStepError,
    operation
  ]);

  const runMutation = useCallback(
    async (exactRetry: boolean) => {
      const pending = pendingRef.current;
      if (
        !exactRetry &&
        pending !== null &&
        pending.identity === currentOperationIdentity &&
        operation.phase === "AWAITING_EXPLICIT_CONFIRMATION" &&
        (pending.confirmationProof === "" ||
          pending.expiresAtUnixMicros <= Date.now() * 1000)
      ) {
        const target = pending.target;
        destroyPending(true);
        setOperation({
          phase: "ERROR",
          target,
          step: "CONFIRMATION",
          retryable: false,
          message: "The short-lived confirmation expired. Start a new dry-run.",
          correlationID: ""
        });
        return;
      }
      if (
        pending === null ||
        pending.identity !== currentOperationIdentity ||
        pending.idempotencyKey === "" ||
        pending.evaluationDigest === "" ||
        operationBusyRef.current ||
        (exactRetry
          ? operation.phase !== "MUTATION_UNCERTAIN"
          : operation.phase !== "AWAITING_EXPLICIT_CONFIRMATION" ||
            !confirmationAccepted) ||
        (!exactRetry &&
          (pending.confirmationProof === "" ||
            pending.expiresAtUnixMicros <= Date.now() * 1000))
      ) return;
      operationBusyRef.current = true;
      const epoch = operationEpochRef.current;
      const controller = new AbortController();
      operationAbortRef.current = controller;
      setConfirmationAccepted(false);
      setOperation({ phase: "MUTATING", target: pending.target, exactRetry });
      try {
        let proof = exactRetry ? undefined : pending.confirmationProof;
        const request = mutateModuleDisable(
          pending.context,
          pending.body,
          pending.idempotencyKey,
          pending.evaluationDigest,
          proof,
          controller.signal
        );
        proof = undefined;
        pending.confirmationProof = "";
        const result = await request;
        if (epoch !== operationEpochRef.current) return;
        if (
          result.receipt.status !== "NO_CHANGE" &&
          result.receipt.status !== "APPLIED"
        ) {
          enterCriticalFailure({
            kind: "INTEGRITY",
            message: "The MODULE_DISABLE mutation returned a non-mutation receipt status.",
            correlationID: ""
          });
          return;
        }
        const completed: OperationState = {
          phase: "COMPLETE",
          target: pending.target,
          status: result.receipt.status,
          receiptDigest: result.receipt_digest,
          completedAtUnixMicros: result.receipt.completed_at_unix_micros
        };
        erasePending(pending);
        pendingRef.current = null;
        clearModulesTransportCache();
        setSelectedInstanceID(null);
        setOperation(completed);
        await Promise.all([
          queryClient.resetQueries({ queryKey: ["modules"] }),
          queryClient.invalidateQueries({ queryKey: ["overview"] })
        ]);
        queryClient.removeQueries({ queryKey: ["module-detail"] });
        try {
          await onMutationComplete?.(result);
        } catch {
          // The trusted receipt remains authoritative even if a parent refresh hook fails.
        }
      } catch (error) {
        handleStepError(
          "MUTATE",
          error,
          epoch,
          "The mutation outcome could not be established from a trusted receipt."
        );
      } finally {
        if (epoch === operationEpochRef.current) {
          operationBusyRef.current = false;
          operationAbortRef.current = null;
        }
      }
    },
    [
      currentOperationIdentity,
      confirmationAccepted,
      destroyPending,
      enterCriticalFailure,
      handleStepError,
      onMutationComplete,
      operation.phase,
      queryClient
    ]
  );

  const reloadAuthority = useCallback(() => {
    destroyPending(true);
    clearModulesTransportCache();
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    setConfirmationAccepted(false);
    setSelectedInstanceID(null);
    queryClient.removeQueries({ queryKey: ["module-detail"] });
    void queryClient.resetQueries({ queryKey: ["modules"] });
  }, [destroyPending, queryClient]);

  const acknowledgeTerminalDryRun = useCallback(() => {
    if (
      operation.phase !== "DRY_RESULT" ||
      operation.disposition === "WOULD_APPLY"
    ) return;
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ["modules"] }),
      queryClient.invalidateQueries({ queryKey: ["module-detail"] }),
      queryClient.invalidateQueries({ queryKey: ["overview"] })
    ]);
  }, [destroyPending, operation, queryClient]);

  const retryReads = useCallback(() => {
    setOperationFailure(null);
    publishedFailureRef.current = "";
    reloadAuthority();
  }, [reloadAuthority]);

  const retryStep = useCallback(() => {
    if (operation.phase !== "ERROR" || !operation.retryable) return;
    if (operation.step === "DRY_RUN") void runDryRun();
    if (operation.step === "CONFIRMATION") void runConfirmation();
  }, [operation, runConfirmation, runDryRun]);

  const onSelectScope = (event: ChangeEvent<HTMLSelectElement>) => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    setSelectedInstanceID(null);
    setSearch("");
    onScopeChange(event.currentTarget.value);
  };

  const navigateOverview = (event: MouseEvent<HTMLAnchorElement>) => {
    destroyPending(true);
    setOperation({ phase: "IDLE" });
    setOperationFailure(null);
    if (onNavigateOverview !== undefined) {
      event.preventDefault();
      onNavigateOverview();
    }
  };

  if (displayedFailure !== null) {
    const canRetry =
      displayedFailure.kind === "STALE" || displayedFailure.kind === "INTEGRITY";
    return (
      <FailurePanel
        failure={displayedFailure}
        onRetry={canRetry && contextIsValid ? retryReads : undefined}
      />
    );
  }
  if (modules.isPending || modules.data === undefined) {
    if (modules.error !== null) {
      const safe = safeErrorText(
        modules.error,
        null,
        "The Modules projection could not be read."
      );
      return (
        <FailurePanel
          failure={{ kind: "INTEGRITY", ...safe }}
          onRetry={retryReads}
        />
      );
    }
    return <ModulesLoading />;
  }

  const filteredSummaries = summaries.filter((summary) =>
    moduleSearchMatch(summary, search)
  );
  const pageBasis: PublishedBasis = modules.data.pages[0].basis;
  const canOperate =
    session.capabilities.includes("OPERATE_MODULES") &&
    context.scope.kind === "TENANT" &&
    modules.error === null &&
    !modules.isFetching;
  const statusStale = modules.isFetching || modules.error !== null;

  return (
    <div className="control-shell control-shell--modules">
      <header className="topbar">
        <a
          className="brand"
          href="#overview"
          aria-label="FreeAgent Control Overview"
          onClick={navigateOverview}
        >
          <span className="brand__mark" aria-hidden="true">F</span>
          <span>FreeAgent Control</span>
        </a>
        <div className="topbar__status">
          <span
            className={`status-dot${statusStale ? " status-dot--stale" : ""}`}
            aria-hidden="true"
          />
          {modules.isFetching ? "Refreshing" : modules.error !== null ? "Stale" : "Current"}
        </div>
      </header>

      <aside className="sidebar" aria-label="Control navigation">
        <p className="sidebar__label">Control</p>
        <nav>
          <a href="#overview" onClick={navigateOverview}>Overview</a>
          <a href="#modules" aria-current="page">Modules</a>
        </nav>
        <div className="session-card">
          <span>Session</span>
          <strong>{session.principal_id}</strong>
          <small>Expires {formatMicros(session.expires_at_unix_micros)}</small>
        </div>
      </aside>

      <main className="content modules-content" id="modules">
        <section className="page-heading">
          <div>
            <p className="eyebrow">Authorized configuration surface</p>
            <h1>Modules</h1>
            <p className="lede">
              Validated module instances and bindings at published pointer revision
              {` ${pageBasis.pointer_revision}`}.
            </p>
          </div>
          <button className="button" type="button" onClick={reloadAuthority}>
            {modules.isFetching ? "Refreshing..." : "Refresh exact basis"}
          </button>
        </section>

        <section className="toolbar modules-toolbar" aria-label="Modules filters">
          <label>
            <span>Authorized scope</span>
            <select value={selectedScopeKey} onChange={onSelectScope}>
              {scopeChoices.map((choice) => (
                <option value={choice.key} key={choice.key}>{choice.label}</option>
              ))}
            </select>
          </label>
          <label>
            <span>Search current pages</span>
            <input
              type="search"
              value={search}
              maxLength={256}
              onChange={(event) => setSearch(event.currentTarget.value)}
              placeholder="Instance, module, version, class"
            />
          </label>
        </section>

        {!session.capabilities.includes("OPERATE_MODULES") && (
          <div className="notice notice--warning" role="status">
            <strong>Read-only Modules session.</strong>
            <span>OPERATE_MODULES is not present, so no mutation control is rendered.</span>
          </div>
        )}
        {session.capabilities.includes("OPERATE_MODULES") && context.scope.kind !== "TENANT" && (
          <div className="notice notice--warning" role="status">
            <strong>Workspace scope is read-only for MODULE_DISABLE.</strong>
            <span>Select an authorized Tenant scope to review a narrow Profile binding.</span>
          </div>
        )}

        <div className="modules-layout">
          <section className="data-section modules-list" aria-labelledby="modules-list-title">
            <header>
              <div>
                <p className="eyebrow">Current validated pages</p>
                <h2 id="modules-list-title">Module instances</h2>
              </div>
              <span className="section-meta">{summaries.length} loaded</span>
            </header>
            {modules.error !== null && (
              <div className="notice notice--error" role="alert">
                <strong>Background refresh failed.</strong>
                <span>{safeErrorText(modules.error, null, "Modules refresh failed.").message}</span>
                <button className="button" type="button" onClick={reloadAuthority}>Retry</button>
              </div>
            )}
            {filteredSummaries.length === 0 ? (
              <p className="empty-copy">No loaded module instance matches this search.</p>
            ) : (
              <ul className="result-list modules-result-list">
                {filteredSummaries.map((summary) => (
                  <li key={summary.instance_id}>
                    <button
                      className="result-link module-result"
                      type="button"
                      aria-current={
                        selectedInstanceID === summary.instance_id ? "true" : undefined
                      }
                      onClick={() => {
                        destroyPending(true);
                        setOperation({ phase: "IDLE" });
                        setOperationFailure(null);
                        setSelectedInstanceID(summary.instance_id);
                      }}
                    >
                      <span className="result-link__title">{summary.instance_id}</span>
                      <span className="result-link__summary">
                        {summary.module_id} @ {summary.exact_version} / {summary.execution_class}
                        {` / ${summary.visible_binding_count} visible bindings`}
                      </span>
                      <span className="result-link__arrow" aria-hidden="true">&gt;</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {modules.hasNextPage && (
              <button
                className="button modules-load-more"
                type="button"
                disabled={modules.isFetchingNextPage}
                onClick={() => { void modules.fetchNextPage(); }}
              >
                {modules.isFetchingNextPage ? "Loading..." : "Load next validated page"}
              </button>
            )}
          </section>

          <section className="data-section module-detail" aria-labelledby="module-detail-title">
            <header>
              <div>
                <p className="eyebrow">Exact instance detail</p>
                <h2 id="module-detail-title">
                  {selectedSummary?.instance_id ?? "Select a module"}
                </h2>
              </div>
              {selectedInstanceID !== null && (
                <button
                  className="button"
                  type="button"
                  onClick={() => {
                    destroyPending(true);
                    setOperation({ phase: "IDLE" });
                    setOperationFailure(null);
                    setSelectedInstanceID(null);
                  }}
                >
                  Close
                </button>
              )}
            </header>
            {selectedInstanceID === null ? (
              <p className="empty-copy">
                Select an instance to fetch its independently validated binding detail.
              </p>
            ) : detail.isPending ? (
              <p className="empty-copy" aria-busy="true">Loading exact module detail...</p>
            ) : detail.error !== null || detail.data === undefined ? (
              <div className="notice notice--error" role="alert">
                <strong>Detail unavailable.</strong>
                <span>
                  {safeErrorText(detail.error, null, "Module detail could not be read.").message}
                </span>
                <button
                  className="button"
                  type="button"
                  onClick={() => {
                    resetOperation();
                    void detail.refetch();
                  }}
                >
                  Retry detail
                </button>
              </div>
            ) : (
              <>
                <dl className="module-facts">
                  <div><dt>Module</dt><dd>{detail.data.module.summary.module_id}</dd></div>
                  <div><dt>Version</dt><dd>{detail.data.module.summary.exact_version}</dd></div>
                  <div><dt>Execution</dt><dd>{detail.data.module.summary.execution_class}</dd></div>
                  <div><dt>Adapter</dt><dd>{detail.data.module.summary.adapter_identity}</dd></div>
                  <div>
                    <dt>Artifact</dt>
                    <dd className="digest">{shortDigest(detail.data.module.summary.artifact_digest)}</dd>
                  </div>
                </dl>
                <div className="module-bindings">
                  <h3>Visible bindings</h3>
                  {detail.data.module.bindings.length === 0 ? (
                    <p className="empty-copy">No binding is visible in this authorized scope.</p>
                  ) : (
                    <ul className="module-binding-list">
                      {detail.data.module.bindings.map((binding) => {
                        const candidate = isModuleDisableCandidate(
                          detail.data.module.summary,
                          binding
                        );
                        const lowestCandidate =
                          candidate &&
                          isLowestDisableCandidate(
                            detail.data.module.summary,
                            detail.data.module.bindings,
                            binding
                          );
                        const key = [
                          binding.target.kind,
                          binding.target.kind === "PROFILE"
                            ? binding.target.profile_id
                            : `${binding.target.workspace_id}:${binding.target.endpoint_id}`,
                          binding.port.name,
                          binding.port.exact_version,
                          binding.port_binding_index
                        ].join(":");
                        return (
                          <li key={key} className="module-binding">
                            <div>
                              <strong>{bindingTargetLabel(binding)}</strong>
                              <span>
                                {binding.port.name}/{binding.port.exact_version}
                                {` / ${binding.failure_policy} / index ${binding.port_binding_index}`}
                              </span>
                              <small>{binding.static_context_refs.length} static context refs</small>
                            </div>
                            {candidate && lowestCandidate && canOperate ? (
                              <button
                                className="button"
                                type="button"
                                disabled={
                                  detail.isFetching || operation.phase !== "IDLE"
                                }
                                onClick={() => startDryRun(detail.data.module.summary, binding)}
                              >
                                Review disable
                              </button>
                            ) : candidate && !lowestCandidate ? (
                              <span className="tag">Higher duplicate binding index</span>
                            ) : candidate ? (
                              <span className="tag">Tenant OPERATE_MODULES required</span>
                            ) : (
                              <span className="tag">Outside narrow disable candidate</span>
                            )}
                          </li>
                        );
                      })}
                    </ul>
                  )}
                </div>
                <OperationPanel
                  state={operation}
                  confirmationAccepted={confirmationAccepted}
                  onConfirmationAccepted={setConfirmationAccepted}
                  onRequestConfirmation={() => { void runConfirmation(); }}
                  onAcknowledgeDryResult={acknowledgeTerminalDryRun}
                  onExplicitConfirm={() => {
                    if (confirmationAccepted) void runMutation(false);
                  }}
                  onExactRetry={() => { void runMutation(true); }}
                  onRetryStep={retryStep}
                  onReset={resetOperation}
                  onReload={reloadAuthority}
                />
              </>
            )}
          </section>
        </div>

        <footer className="page-footer">
          <span>
            Reads are bounded and same-origin. MODULE_DISABLE is server-authoritative and
            non-optimistic.
          </span>
          <span className="digest">
            Pointer {shortDigest(modules.data.pages[0].published_pointer.digest)}
          </span>
        </footer>
      </main>
    </div>
  );
}
