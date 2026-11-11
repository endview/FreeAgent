import { useSyncExternalStore } from "react";
import { api, headers, remote } from "@/lib/api/client";
import type {
  ChatSnapshot,
  Conversation,
  CoreEvent,
  Environment,
  Space,
} from "@/lib/api/types";
import { prepareClientDrafts } from "@/lib/drafts";
import { decodeSnapshot, decodeEvent } from "@/lib/api/decode";
import { policyFor } from "@/lib/events/policy";

export interface RuntimeState {
  ready: boolean;
  environment: Environment | null;
  chats: Conversation[];
  spaces: Space[];
  selected: string;
  snapshot: ChatSnapshot | null;
  connected: boolean;
  error: string;
  notice: string;
  refreshRequired: boolean;
  loading: boolean;
  inputRevision: number;
  navQuery: string;
  navSpace: string;
  navCursor: string;
  navMore: boolean;
  navLoading: boolean;
}
const initial: RuntimeState = {
  ready: false,
  environment: null,
  chats: [],
  spaces: [],
  selected:
    typeof location === "undefined"
      ? ""
      : new URLSearchParams(location.search).get("chat") || "",
  snapshot: null,
  connected: false,
  error: "",
  notice: "",
  refreshRequired: false,
  loading: false,
  inputRevision: 0,
  navQuery: "",
  navSpace: "local",
  navCursor: "",
  navMore: false,
  navLoading: false,
};
let state: RuntimeState = initial;
const listeners = new Set<() => void>();
export function update(values: Partial<RuntimeState>) {
  state = { ...state, ...values };
  for (const fn of listeners) fn();
}
export function useRuntime() {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => state,
  );
}
export function current() {
  return state;
}
let loadVersion = 0,
  refreshTimer: number | undefined,
  noticeTimer: number | undefined;
let snapshotLoading = false,
  bufferOverflow = false;
let snapshotBuffer: CoreEvent[] = [];
let serverVersion = "",
  generation = "",
  lastSeq = 0;
let navigationDirty = false,
  environmentDirty = false;

export function notify(notice: string) {
  clearTimeout(noticeTimer);
  update({ notice });
  noticeTimer = window.setTimeout(() => update({ notice: "" }), 2000);
}
export function report(error: unknown) {
  if (error instanceof DOMException && error.name === "AbortError") return;
  update({ error: error instanceof Error ? error.message : "操作未完成。" });
}
export async function loadEnvironment() {
  const session = epoch;
  const environment = await api<Environment>("environment");
  if (session !== epoch) return;
  await prepareClientDrafts(environment.environment);
  if (session === epoch) update({ environment });
}
interface NavigationPage {
  items: Conversation[];
  next_cursor?: string;
  has_more: boolean;
}
let navigationVersion = 0;
function navigationPath(cursor = "") {
  const query = new URLSearchParams({
    page: "1",
    limit: "60",
    q: state.navQuery,
    space_id: state.navSpace,
  });
  if (cursor) query.set("cursor", cursor);
  return "chats?" + query;
}
export async function loadNavigation() {
  const version = ++navigationVersion;
  update({ navLoading: true });
  try {
    const [page, result] = await Promise.all([
      api<NavigationPage>(navigationPath()),
      api<Space[] | { spaces: Space[] }>("spaces"),
    ]);
    if (version !== navigationVersion) return;
    update({
      chats: page.items,
      spaces: Array.isArray(result) ? result : result.spaces,
      navCursor: page.next_cursor || "",
      navMore: page.has_more,
    });
  } finally {
    if (version === navigationVersion) update({ navLoading: false });
  }
}
export async function searchNavigation(query: string, space = state.navSpace) {
  update({ navQuery: query, navSpace: space });
  await loadNavigation();
}
export async function loadMoreNavigation() {
  if (!state.navMore || !state.navCursor || state.navLoading) return;
  const version = navigationVersion;
  update({ navLoading: true });
  try {
    const page = await api<NavigationPage>(navigationPath(state.navCursor));
    if (version !== navigationVersion) return;
    const items = new Map(
      [...state.chats, ...page.items].map((chat) => [chat.id, chat]),
    );
    update({
      chats: [...items.values()],
      navCursor: page.next_cursor || "",
      navMore: page.has_more,
    });
  } finally {
    if (version === navigationVersion) update({ navLoading: false });
  }
}
export async function selectConversation(id: string) {
  ++loadVersion;
  snapshotBuffer = [];
  snapshotLoading = false;
  bufferOverflow = false;
  update({ selected: id, snapshot: null, loading: false });
  if (id) await loadSnapshot(id);
}
function observeVersion(value?: string) {
  if (!value) return;
  if (serverVersion && serverVersion !== value)
    update({ refreshRequired: true });
  serverVersion = value;
}
export async function loadSnapshot(id = state.selected) {
  if (!id) return;
  const version = ++loadVersion;
  snapshotLoading = true;
  update({ loading: true });
  try {
    const snapshot = decodeSnapshot(
      await api<unknown>(`chats/${encodeURIComponent(id)}`),
    );
    if (version !== loadVersion || id !== state.selected) return;
    if (snapshot.snapshot_stable === false) {
      scheduleRefresh();
      return;
    }
    observeVersion(snapshot.server_version);
    if (snapshot.generation) {
      generation = snapshot.generation;
      lastSeq = snapshot.snapshot_seq || 0;
    }
    update({ snapshot });
  } finally {
    if (version === loadVersion) {
      snapshotLoading = false;
      update({ loading: false });
      const buffered = snapshotBuffer;
      snapshotBuffer = [];
      for (const event of buffered) enqueue(event);
      if (bufferOverflow) {
        bufferOverflow = false;
        scheduleRefresh();
      }
    }
  }
}
function scheduleRefresh() {
  if (refreshTimer !== undefined || stopped) return;
  refreshTimer = window.setTimeout(() => {
    refreshTimer = undefined;
    const reads: Promise<unknown>[] = [loadSnapshot()];
    if (navigationDirty) {
      navigationDirty = false;
      reads.push(loadNavigation());
    }
    if (environmentDirty) {
      environmentDirty = false;
      reads.push(loadEnvironment());
    }
    Promise.all(reads).catch(report);
  }, 160);
}
let pending: CoreEvent[] = [],
  frame: number | undefined,
  fallback: number | undefined;
function flush() {
  if (frame !== undefined) cancelAnimationFrame(frame);
  clearTimeout(fallback);
  frame = undefined;
  fallback = undefined;
  const events = pending;
  pending = [];
  if (snapshotLoading) {
    snapshotBuffer.push(...events);
    if (snapshotBuffer.length > 512) {
      snapshotBuffer = [];
      bufferOverflow = true;
    }
    return;
  }
  let snapshot = state.snapshot,
    refresh = false,
    inputChanged = false;
  for (const event of events) {
    observeVersion(event.server_version);
    const policy = policyFor(event.kind);
    if (policy === "ignore") continue;
    if (policy === "unknown") {
      update({ refreshRequired: true });
      refresh = true;
      continue;
    }
    if (event.kind === "reconnect") {
      navigationDirty = true;
      environmentDirty = true;
      refresh = true;
      continue;
    }
    if (event.generation && event.generation !== generation) {
      environmentDirty = true;
      navigationDirty = true;
      // Settings can be open without a selected chat. There is no chat
      // snapshot in that case to establish the authenticated Owner epoch.
      if (!state.selected) {
        generation = event.generation;
        lastSeq = 0;
      }
      refresh = true;
      continue;
    }
    if (
      ["refresh", "run_started", "run_completed", "run_settled"].includes(
        event.kind,
      )
    )
      navigationDirty = true;
    if (event.kind === "refresh") environmentDirty = true;
    if (
      event.kind === "request_error" &&
      (!event.conversation_id || event.conversation_id === state.selected)
    )
      update({ error: event.message || "请求未完成，请核对运行记录。" });
    if (event.kind === "input_changed" || event.kind === "input_adopted")
      inputChanged = true;
    if (event.seq) {
      if (event.seq <= lastSeq) continue;
      if (lastSeq && event.seq > lastSeq + 1) {
        refresh = true;
        continue;
      }
      lastSeq = event.seq;
    }
    if (
      event.kind === "model_text" &&
      !event.purpose &&
      event.conversation_id === state.selected &&
      event.attempt_id &&
      snapshot
    ) {
      const streams = [...snapshot.streams],
        index = streams.findIndex((item) => item.attempt === event.attempt_id);
      const old =
        index < 0
          ? {
              attempt: event.attempt_id,
              run: event.run_id || "",
              raw: "",
              interrupted: false,
            }
          : streams[index];
      const value = {
        ...old,
        raw: (old.raw + (event.message || "")).slice(0, 1048576),
      };
      if (index < 0) streams.push(value);
      else streams[index] = value;
      snapshot = { ...snapshot, streams };
    } else if (
      !event.conversation_id ||
      event.conversation_id === state.selected
    )
      refresh = true;
  }
  if (snapshot !== state.snapshot || inputChanged)
    update({
      snapshot,
      ...(inputChanged ? { inputRevision: state.inputRevision + 1 } : {}),
    });
  if (refresh || navigationDirty || environmentDirty) scheduleRefresh();
}
function enqueue(event: CoreEvent) {
  pending.push(event);
  if (pending.length > 512) {
    pending = [];
    bufferOverflow = true;
    navigationDirty = true;
    scheduleRefresh();
    return;
  }
  if (frame !== undefined) return;
  frame = requestAnimationFrame(flush);
  fallback = window.setTimeout(flush, 100);
}
let epoch = 0,
  streamController: AbortController | undefined,
  reconnectTimer: number | undefined,
  stopped = true;
let startPromise: Promise<void> | undefined;
async function openEvents(session: number) {
  if (stopped || session !== epoch) return;
  const controller = new AbortController();
  streamController = controller;
  try {
    const response = await fetch("/api/events", {
      headers: headers(),
      cache: "no-store",
      credentials: "same-origin",
      signal: controller.signal,
    });
    if (stopped || session !== epoch) {
      await response.body?.cancel();
      return;
    }
    if (response.status === 401 && remote) {
      window.dispatchEvent(new Event("freeagent-auth-required"));
      return;
    }
    if (!response.ok || !response.body) throw new Error("事件连接失败。");
    update({ connected: true });
    navigationDirty = true;
    environmentDirty = true;
    scheduleRefresh();
    const reader = response.body.getReader(),
      decoder = new TextDecoder();
    let buffer = "";
    try {
      while (!stopped && session === epoch) {
        const item = await reader.read();
        if (item.done) break;
        buffer += decoder.decode(item.value, { stream: true });
        if (buffer.length > 2 * 1048576) throw new Error("事件超过大小限制。");
        let end: RegExpExecArray | null;
        while ((end = /\r?\n\r?\n/.exec(buffer))) {
          const block = buffer.slice(0, end.index);
          buffer = buffer.slice(end.index + end[0].length);
          const raw = block
            .split(/\r?\n/)
            .filter((line) => line.startsWith("data:"))
            .map((line) => line.slice(5).trimStart())
            .join("\n");
          if (raw) {
            try {
              enqueue(decodeEvent(JSON.parse(raw)));
            } catch {
              scheduleRefresh();
            }
          }
        }
      }
    } finally {
      await reader.cancel().catch(() => {});
    }
  } catch {
    /* The authoritative snapshot is read again after reconnect. */
  }
  if (session !== epoch || stopped) return;
  update({ connected: false });
  reconnectTimer = window.setTimeout(() => void openEvents(session), 1500);
}
export function startRuntime(): Promise<void> {
  if (startPromise) return startPromise;
  stopped = false;
  const session = ++epoch;
  startPromise = (async () => {
    await loadEnvironment();
    if (session !== epoch) return;
    await loadNavigation();
    if (session !== epoch) return;
    const chat =
      state.selected || new URLSearchParams(location.search).get("chat") || "";
    if (chat) await selectConversation(chat);
    if (session !== epoch) return;
    update({ ready: true });
    void openEvents(session);
  })().catch((error) => {
    if (session === epoch) {
      startPromise = undefined;
      stopped = true;
    }
    throw error;
  });
  return startPromise;
}
export function stopRuntime() {
  stopped = true;
  ++epoch;
  ++loadVersion;
  ++navigationVersion;
  clearTimeout(noticeTimer);
  startPromise = undefined;
  streamController?.abort();
  clearTimeout(reconnectTimer);
  clearTimeout(refreshTimer);
  clearTimeout(fallback);
  if (frame !== undefined) cancelAnimationFrame(frame);
  refreshTimer = undefined;
  frame = undefined;
  fallback = undefined;
  pending = [];
  snapshotBuffer = [];
  snapshotLoading = false;
  update({ connected: false, loading: false });
}
export function resetRuntime() {
  stopRuntime();
  generation = "";
  lastSeq = 0;
  serverVersion = "";
  state = {
    ...initial,
    selected: new URLSearchParams(location.search).get("chat") || "",
  };
  update({});
}
window.addEventListener("online", () => {
  navigationDirty = true;
  scheduleRefresh();
});
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && !stopped) {
    navigationDirty = true;
    scheduleRefresh();
  }
});
window.addEventListener("pagehide", (event) => {
  stopRuntime();
  if (!event.persisted)
    fetch("/api/client/detach", {
      method: "POST",
      headers: headers(),
      keepalive: true,
    }).catch(() => {});
});
window.addEventListener("pageshow", (event) => {
  if (event.persisted) startRuntime().catch(report);
});
window.addEventListener("freeagent-auth-required", () => {
  if (remote) stopRuntime();
});
