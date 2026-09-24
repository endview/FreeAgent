import { useCallback, useEffect, useMemo, useState, type ChangeEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import {
  MAX_HANDOFF_BYTES,
  decodeHandoff,
  scopeKey,
  type ControlScope,
  type ControlSession,
  type OverviewResponse,
  type SessionExchange,
  type WorkspaceRef
} from "./contracts.ts";
import {
  ControlOverviewError,
  buildScopeChoices,
  captureDetailSnapshot,
  clearOverviewTransportCache,
  fetchOverview,
  isPermissionDenied,
  isSessionInvalid,
  overviewQueryKey,
  parseDetailHash,
  type DetailSnapshot,
  type OverviewContext
} from "./overview.ts";
import {
  clearModulesTransportCache,
  type ModuleContext
} from "./modules.ts";
import { clearManagementTransportCache } from "./management.ts";
import {
  clearStoredResume,
  exchangeHandoff,
  readStoredResume,
  resumeSession,
  shouldDiscardResume,
  storeResume
} from "./session.ts";
import {
  FatalOverviewPanel,
  HandoffPanel,
  LoadingPanel,
  OverviewPage,
  PermissionPanel,
  SessionInvalidBoundary
} from "./ui.tsx";
import {
  ModulesPage,
  type ModulesFailClosedEvent
} from "./modules-ui.tsx";
import { ModuleUpgradeReviewsPage } from "./upgrade-reviews-ui.tsx";
import { ManagementPage } from "./management-ui.tsx";
import { useOptionalI18n } from "./i18n/index.ts";

type ActiveSession = {
  origin: string;
  session: ControlSession;
  authorizedScopes: ControlScope[];
  csrfToken: string;
};

const activeSessionFromExchange = (
  origin: string,
  exchange: SessionExchange
): ActiveSession => ({
  origin,
  session: exchange.session,
  csrfToken: exchange.csrf_token,
  authorizedScopes: exchange.authorized_scopes,
});

const errorMessage = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const currentHash = () => (typeof window === "undefined" ? "" : window.location.hash);

export function App() {
  const { t } = useOptionalI18n();
  const queryClient = useQueryClient();
  const [checkingResume, setCheckingResume] = useState(true);
  const [openingSession, setOpeningSession] = useState(false);
  const [sessionError, setSessionError] = useState("");
  const [storageWarning, setStorageWarning] = useState("");
  const [active, setActive] = useState<ActiveSession | null>(null);
  const [sessionEpoch, setSessionEpoch] = useState(0);
  const [selectedScopeKey, setSelectedScopeKey] = useState("");
  const [workspacesByTenant, setWorkspacesByTenant] = useState<
    Map<string, readonly WorkspaceRef[]>
  >(new Map());
  const [search, setSearch] = useState("");
  const [hash, setHash] = useState(currentHash);
  const [detailSnapshot, setDetailSnapshot] = useState<DetailSnapshot | null>(null);
  const [permissionRevoked, setPermissionRevoked] = useState(false);

  const acceptExchange = useCallback(
    (origin: string, exchange: SessionExchange) => {
      clearOverviewTransportCache();
      clearModulesTransportCache();
      clearManagementTransportCache();
      queryClient.clear();
      setWorkspacesByTenant(new Map());
      setSearch("");
      setDetailSnapshot(null);
      setPermissionRevoked(false);
      const stored = storeResume(origin, exchange.resume_credential);
      if (!stored) {
        clearStoredResume();
        setStorageWarning(t("session.error.storageWarning"));
      } else {
        setStorageWarning("");
      }
      const next = activeSessionFromExchange(origin, exchange);
      setSessionEpoch((value) => value + 1);
      setActive(next);
      setSelectedScopeKey(scopeKey(next.authorizedScopes[0]));
      setSessionError("");
    },
    [queryClient, t]
  );

  const closeActiveSession = useCallback(
    (message: string) => {
      clearStoredResume();
      clearOverviewTransportCache();
      clearModulesTransportCache();
      clearManagementTransportCache();
      queryClient.clear();
      setActive(null);
      setSelectedScopeKey("");
      setWorkspacesByTenant(new Map());
      setSearch("");
      setDetailSnapshot(null);
      setPermissionRevoked(false);
      setSessionError(message);
    },
    [queryClient]
  );

  useEffect(() => {
    let cancelled = false;
    const stored = readStoredResume();
    if (stored === null) {
      setCheckingResume(false);
      return () => { cancelled = true; };
    }
    void resumeSession(stored.origin, stored.resume_credential)
      .then((exchange) => {
        if (!cancelled) acceptExchange(stored.origin, exchange);
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        if (shouldDiscardResume(error)) clearStoredResume();
        setSessionError(t("session.error.resume", {
          values: { message: errorMessage(error, t("session.open.error")) }
        }));
      })
      .finally(() => {
        if (!cancelled) setCheckingResume(false);
      });
    return () => { cancelled = true; };
  }, [acceptExchange, t]);

  useEffect(() => {
    const onHashChange = () => setHash(window.location.hash);
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const detailLink = useMemo(() => parseDetailHash(hash), [hash]);

  const onHandoffFile = useCallback(
    async (event: ChangeEvent<HTMLInputElement>) => {
      const input = event.currentTarget;
      const file = input.files?.[0];
      input.value = "";
      if (file === undefined) return;
      setOpeningSession(true);
      setSessionError("");
      let text = "";
      let handoff: ReturnType<typeof decodeHandoff> | null = null;
      try {
        if (file.size > MAX_HANDOFF_BYTES) {
          throw new Error(t("session.error.handoffTooLarge"));
        }
        text = await file.text();
        handoff = decodeHandoff(text);
        const exchange = await exchangeHandoff(handoff);
        acceptExchange(handoff.origin, exchange);
      } catch (error) {
        setSessionError(errorMessage(error, t("session.open.error")));
      } finally {
        if (handoff !== null) handoff.capability = "";
        text = "";
        setOpeningSession(false);
      }
    },
    [acceptExchange, t]
  );

  const observes = active?.session.capabilities.includes("OBSERVE") ?? false;
  const scopeChoices = useMemo(
    () => buildScopeChoices(active?.authorizedScopes ?? [], workspacesByTenant, t),
    [active?.authorizedScopes, t, workspacesByTenant]
  );
  const selectedScope = useMemo(
    () => scopeChoices.find((choice) => choice.key === selectedScopeKey)?.scope ?? null,
    [scopeChoices, selectedScopeKey]
  );

  useEffect(() => {
    if (scopeChoices.length === 0) return;
    if (!scopeChoices.some((choice) => choice.key === selectedScopeKey)) {
      setSearch("");
      setSelectedScopeKey(scopeChoices[0].key);
    }
  }, [scopeChoices, selectedScopeKey]);
  const modulesSelected = hash === "#modules";
  const reviewsSelected = hash === "#upgrade-reviews";
  const managementSelected = hash === "#management";
  const overviewContext: OverviewContext | null =
    active !== null && observes && selectedScope !== null
      ? {
          origin: active.origin,
          bootID: active.session.boot_id,
          sessionID: active.session.session_id,
          principalID: active.session.principal_id,
          authorizationRevision: active.session.authorization_revision,
          scopeSetDigest: active.session.scope_set_digest,
          csrfToken: active.csrfToken,
          scope: selectedScope
        }
      : null;
  const moduleContext: ModuleContext | null =
    active !== null && overviewContext !== null
      ? {
          ...overviewContext,
          sessionEpoch,
          sessionExpiresAtUnixMicros: active.session.expires_at_unix_micros
        }
      : null;

  const overview = useQuery<OverviewResponse, ControlOverviewError>({
    queryKey:
      overviewContext === null
        ? ["overview", "disabled"]
        : overviewQueryKey(overviewContext),
    queryFn: ({ signal }) => {
      if (overviewContext === null) throw new Error("overview query is disabled");
      return fetchOverview(overviewContext, signal);
    },
    enabled: overviewContext !== null,
    staleTime: 30_000,
    retry: false
  });

  useEffect(() => {
    if (
      overview.data === undefined ||
      selectedScope === null ||
      selectedScope.kind !== "TENANT"
    ) return;
    setWorkspacesByTenant((current) => {
      const existing = current.get(selectedScope.tenant_id);
      if (existing === overview.data?.workspaces) return current;
      const next = new Map(current);
      next.set(selectedScope.tenant_id, overview.data.workspaces);
      return next;
    });
  }, [overview.data, selectedScope]);

  useEffect(() => {
    if (detailLink === null) {
      setDetailSnapshot(null);
      return;
    }
    if (overview.data === undefined) return;
    setDetailSnapshot((current) => {
      if (
        current !== null &&
        current.link.section === detailLink.section &&
        current.link.id === detailLink.id
      ) return current;
      return captureDetailSnapshot(overview.data, detailLink, t);
    });
  }, [detailLink, overview.data, t]);

  useEffect(() => {
    if (overview.error === null || !isSessionInvalid(overview.error)) return;
    closeActiveSession(
      t("operation.client.sessionExpired")
    );
  }, [closeActiveSession, overview.error, t]);

  useEffect(() => {
    if (overview.error === null || !isPermissionDenied(overview.error)) return;
    setPermissionRevoked(true);
    clearOverviewTransportCache();
    clearModulesTransportCache();
    queryClient.removeQueries({ queryKey: ["overview"] });
    queryClient.removeQueries({ queryKey: ["modules"] });
    queryClient.removeQueries({ queryKey: ["module-detail"] });
  }, [overview.error, queryClient]);

  const changeScope = useCallback(
    (key: string) => {
      clearModulesTransportCache();
      queryClient.removeQueries({ queryKey: ["modules"] });
      queryClient.removeQueries({ queryKey: ["module-detail"] });
      queryClient.removeQueries({ queryKey: ["module-upgrade-reviews"] });
      queryClient.removeQueries({ queryKey: ["management"] });
      queryClient.removeQueries({ queryKey: ["store-management"] });
      queryClient.removeQueries({ queryKey: ["management-unknown-detail"] });
      setPermissionRevoked(false);
      setSelectedScopeKey(key);
      setSearch("");
      setDetailSnapshot(null);
    },
    [queryClient]
  );

  const handleModulesFailure = useCallback(
    (event: ModulesFailClosedEvent) => {
      if (event.kind === "SESSION") {
        closeActiveSession(t("operation.client.sessionExpired"));
        return;
      }
      if (event.kind !== "PERMISSION") return;
      setPermissionRevoked(true);
      clearOverviewTransportCache();
      clearModulesTransportCache();
      queryClient.removeQueries({ queryKey: ["overview"] });
      queryClient.removeQueries({ queryKey: ["modules"] });
      queryClient.removeQueries({ queryKey: ["module-detail"] });
      queryClient.removeQueries({ queryKey: ["module-upgrade-reviews"] });
      queryClient.removeQueries({ queryKey: ["management"] });
      queryClient.removeQueries({ queryKey: ["store-management"] });
      queryClient.removeQueries({ queryKey: ["management-unknown-detail"] });
    },
    [closeActiveSession, queryClient, t]
  );

  if (checkingResume) return <LoadingPanel labelKey="loading.checkingSession" />;
  if (active === null) {
    return <HandoffPanel busy={openingSession} error={sessionError} onFile={onHandoffFile} />;
  }
  if (!observes) return <PermissionPanel principalID={active.session.principal_id} />;
  if (permissionRevoked || (overview.error !== null && isPermissionDenied(overview.error))) {
    const denied = overview.error;
    return (
      <FatalOverviewPanel
        permissionDenied={true}
        message={denied?.message ?? t("error.overview.scopeDenied")}
        correlationID={denied?.correlationID ?? ""}
        onRetry={() => undefined}
      />
    );
  }
  if (modulesSelected && moduleContext !== null) {
    return (
      <ModulesPage
        key={`${active.session.session_id}:${selectedScopeKey}:modules`}
        session={active.session}
        context={moduleContext}
        scopeChoices={scopeChoices}
        selectedScopeKey={selectedScopeKey}
        navigationKey={hash}
        onScopeChange={changeScope}
        onNavigateOverview={() => {
          window.location.hash = "overview";
        }}
        onNavigateReviews={() => {
          window.location.hash = "upgrade-reviews";
        }}
        onNavigateManagement={() => {
          window.location.hash = "management";
        }}
        onFailClosed={handleModulesFailure}
      />
    );
  }
  if (reviewsSelected && moduleContext !== null) {
    return (
      <ModuleUpgradeReviewsPage
        key={`${active.session.session_id}:${selectedScopeKey}:upgrade-reviews`}
        session={active.session}
        context={moduleContext}
        scopeChoices={scopeChoices}
        selectedScopeKey={selectedScopeKey}
        onScopeChange={changeScope}
        onNavigateOverview={() => {
          window.location.hash = "overview";
        }}
        onNavigateModules={() => {
          window.location.hash = "modules";
        }}
        onNavigateManagement={() => {
          window.location.hash = "management";
        }}
        onFailClosed={handleModulesFailure}
      />
    );
  }
  if (managementSelected && moduleContext !== null) {
    return (
      <ManagementPage
        key={`${active.session.session_id}:${selectedScopeKey}:management`}
        session={active.session}
        context={moduleContext}
        scopeChoices={scopeChoices}
        selectedScopeKey={selectedScopeKey}
        onScopeChange={changeScope}
        onNavigateOverview={() => {
          window.location.hash = "overview";
        }}
        onNavigateModules={() => {
          window.location.hash = "modules";
        }}
        onNavigateReviews={() => {
          window.location.hash = "upgrade-reviews";
        }}
        onFailClosed={handleModulesFailure}
      />
    );
  }
  if (overview.isPending || overview.data === undefined) {
    if (overview.error !== null) {
      return (
        <FatalOverviewPanel
          permissionDenied={isPermissionDenied(overview.error)}
          message={overview.error.message}
          correlationID={overview.error.correlationID}
          onRetry={() => { void overview.refetch(); }}
        />
      );
    }
    return <LoadingPanel />;
  }

  const backgroundError = overview.error?.message ?? storageWarning;
  return (
    <SessionInvalidBoundary error={overview.error}>
      <OverviewPage
      session={active.session}
      overview={overview.data}
      scopeChoices={scopeChoices}
      selectedScopeKey={selectedScopeKey}
      search={search}
      stale={overview.isStale || overview.error !== null}
      refreshing={overview.isFetching}
      backgroundError={backgroundError}
      detailSnapshot={detailSnapshot}
      onScopeChange={changeScope}
      onSearchChange={setSearch}
      onRefresh={() => { void overview.refetch(); }}
      onNavigateManagement={() => {
        window.location.hash = "management";
      }}
      />
    </SessionInvalidBoundary>
  );
}
