import type {
  Attachment,
  CapabilitySelection,
  Permission,
  ReasoningSelection,
} from "./api/types.ts";
// Secrets and settings drafts are volatile, and never enter client storage.
const forms = new Map<string, Record<string, unknown>>();
const messages = new Map<string, string>();
const requests = new Map<string, PendingSend>();
const attachments = new Map<string, Attachment[]>();
const branches = new Map<string, { signature: string; request_key: string }>();
export interface PendingSend {
  request_key: string;
  prompt: string;
  attachments: string[];
  kind: "submit" | "queue";
  permission: Permission;
  confirm_full_access: boolean;
  capabilities: CapabilitySelection;
  model: string;
  reasoning?: ReasoningSelection;
}
let scope = "",
  timer: ReturnType<typeof setTimeout> | undefined;
let storage: Pick<Storage, "getItem" | "setItem" | "removeItem"> | undefined;
const storageKey = () => "freeagent.drafts.v81:" + scope;
function persist() {
  if (!scope || !storage) return;
  const text = JSON.stringify({
    messages: [...messages].slice(-100),
    requests: [...requests].slice(-100),
    attachments: [...attachments].slice(-100),
    branches: [...branches].slice(-100),
  });
  try {
    storage.setItem(storageKey(), text);
  } catch {
    /* Denied or full client storage must not prevent input. */
  }
}
function schedule() {
  clearTimeout(timer);
  timer = setTimeout(persist, 180);
}
export function configureDrafts(
  environment: string,
  source?: Pick<Storage, "getItem" | "setItem" | "removeItem">,
) {
  if (environment === scope) return;
  clearTimeout(timer);
  persist();
  forms.clear();
  messages.clear();
  requests.clear();
  attachments.clear();
  branches.clear();
  scope = environment;
  try {
    storage =
      source ||
      (typeof sessionStorage !== "undefined" ? sessionStorage : undefined);
    const raw = storage?.getItem(storageKey());
    if (!raw || raw.length > 4 * 1048576) return;
    const parsed = JSON.parse(raw);
    for (const pair of (parsed.branches || []).slice(-100)) {
      if (
        Array.isArray(pair) &&
        typeof pair[0] === "string" &&
        typeof pair[1]?.signature === "string" &&
        typeof pair[1]?.request_key === "string"
      )
        branches.set(pair[0], pair[1]);
    }
    for (const pair of (parsed.messages || []).slice(-100))
      if (
        Array.isArray(pair) &&
        typeof pair[0] === "string" &&
        typeof pair[1] === "string"
      )
        messages.set(pair[0], pair[1]);
    for (const pair of (parsed.attachments || []).slice(-100))
      if (
        Array.isArray(pair) &&
        typeof pair[0] === "string" &&
        Array.isArray(pair[1])
      )
        attachments.set(
          pair[0],
          pair[1].filter((item: unknown) => {
            const ref = item as Attachment;
            return (
              ref &&
              typeof ref.id === "string" &&
              typeof ref.name === "string" &&
              typeof ref.bytes === "number"
            );
          }),
        );
    for (const pair of (parsed.requests || []).slice(-100)) {
      const value = pair?.[1];
      if (
        Array.isArray(pair) &&
        typeof pair[0] === "string" &&
        value &&
        typeof value.request_key === "string" &&
        typeof value.prompt === "string" &&
        Array.isArray(value.attachments) &&
        (value.kind === "submit" || value.kind === "queue")
      )
        requests.set(pair[0], value);
    }
  } catch {}
}
export function formDraft<T extends Record<string, unknown>>(
  id: string,
  initial: T,
): T {
  return (forms.get(id) || initial) as T;
}
export function saveFormDraft(id: string, value: Record<string, unknown>) {
  forms.set(id, value);
}
export function clearFormDraft(id: string) {
  forms.delete(id);
}
export function messageDraft(chat: string) {
  return messages.get(chat) || "";
}
export function saveMessageDraft(chat: string, value: string) {
  if (value) messages.set(chat, value);
  else messages.delete(chat);
  schedule();
}
export function attachmentDraft(chat: string) {
  return attachments.get(chat) || [];
}
export function saveAttachmentDraft(chat: string, value: Attachment[]) {
  attachments.set(chat, value);
  schedule();
}
export function pendingSend(chat: string) {
  return requests.get(chat);
}
export function savePendingSend(chat: string, value: PendingSend | null) {
  if (value) requests.set(chat, value);
  else requests.delete(chat);
  persist();
}
export function branchRequestKey(chat: string, signature: string) {
  const previous = branches.get(chat);
  if (previous && previous.signature !== signature)
    throw new Error("上一条分支创建结果尚未核对，请先用原消息和原文重试。");
  if (previous) return previous.request_key;
  const request_key = crypto.randomUUID();
  branches.set(chat, { signature, request_key });
  persist();
  return request_key;
}
export function clearBranchRequest(chat: string) {
  branches.delete(chat);
  persist();
}
// Consume only the submitted text and attachments. New edits made while the
// request was in flight, including edits in a different chat, remain intact.
export function consumeSentDraft(chat: string, request: PendingSend) {
  if (messageDraft(chat).trim() === request.prompt) saveMessageDraft(chat, "");
  const submitted = new Set(request.attachments);
  saveAttachmentDraft(
    chat,
    attachmentDraft(chat).filter((ref) => !submitted.has(ref.id)),
  );
  savePendingSend(chat, null);
}
export function clearDrafts() {
  clearTimeout(timer);
  forms.clear();
  messages.clear();
  requests.clear();
  attachments.clear();
  branches.clear();
  try {
    storage?.removeItem(storageKey());
  } catch {}
}
if (typeof window !== "undefined") window.addEventListener("pagehide", persist);

export async function prepareClientDrafts(environment: string) {
  if (scope === environment) return;
  if (
    typeof window === "undefined" ||
    !window.faLoadDrafts ||
    !window.faSaveDrafts
  ) {
    configureDrafts(environment);
    return;
  }
  let raw = "";
  try {
    raw = await window.faLoadDrafts(environment);
  } catch {}
  if (scope === environment) return;
  const empty = '{"messages":[],"requests":[],"attachments":[]}';
  configureDrafts(environment, {
    getItem: () => raw || null,
    setItem: (_key, value) => {
      raw = value;
      void window.faSaveDrafts!(environment, value).catch(() => {});
    },
    removeItem: () => {
      raw = "";
      void window.faSaveDrafts!(environment, empty).catch(() => {});
    },
  });
}
