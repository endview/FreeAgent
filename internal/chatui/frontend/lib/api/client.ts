import { consumeLaunchKey } from "@/app/bootstrap";
import type { PublicSession } from "./types";

const key = consumeLaunchKey();
let csrf = "";
export const remote = location.protocol === "https:";
const clientID =
  (globalThis as typeof globalThis & { __freeagentClientID?: string })
    .__freeagentClientID || crypto.randomUUID();
export class APIError extends Error {
  constructor(
    message: string,
    public status: number,
    public code: string,
    public data: Record<string, unknown> = {},
  ) {
    super(message);
  }
}
export function apiPath(path: string): string {
  if (!remote || !path.startsWith("attach/")) return "/api/" + path;
  if (
    !["context", "commands", "cancel", "inputs", "branch"].includes(
      path.slice(7).split("?")[0],
    )
  )
    throw new APIError("此操作需要本机管理入口。", 403, "local_only");
  return "/api/remote/" + path.slice(7);
}
export function headers(
  extra: Record<string, string> = {},
  active = false,
): Record<string, string> {
  return remote
    ? {
        ...(csrf ? { "X-FreeAgent-CSRF": csrf } : {}),
        ...(active ? { "X-FreeAgent-Activity": "1" } : {}),
        "X-FreeAgent-Client": clientID,
        ...extra,
      }
    : { "X-FreeAgent-Key": key, "X-FreeAgent-Client": clientID, ...extra };
}
export function setCSRF(value: string) {
  csrf = value;
}
export async function api<T = Record<string, unknown>>(
  path: string,
  body?: unknown,
  method?: string,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(apiPath(path), {
    method: method || (body === undefined ? "GET" : "POST"),
    headers: headers(
      body === undefined ? {} : { "Content-Type": "application/json" },
      body !== undefined,
    ),
    credentials: "same-origin",
    cache: "no-store",
    signal,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response
    .json()
    .catch(() => ({ error: "服务器没有返回有效响应。" }));
  if (!response.ok) {
    if (remote && response.status === 401 && path !== "public/login") {
      setCSRF("");
      window.dispatchEvent(new Event("freeagent-auth-required"));
    }
    throw new APIError(
      data.error || "请求未完成。",
      response.status,
      data.code || "request",
      data,
    );
  }
  return data as T;
}
export async function publicSession(): Promise<PublicSession> {
  return api<PublicSession>("public/session");
}
export async function apiText(path: string): Promise<string> {
  const response = await fetch(apiPath(path), {
    headers: headers(),
    credentials: "same-origin",
    cache: "no-store",
  });
  if (!response.ok) {
    const value = await response.json().catch(() => ({}));
    throw new APIError(
      value.error || "读取日志失败。",
      response.status,
      value.code || "request",
    );
  }
  return response.text();
}
export async function login(password: string): Promise<PublicSession> {
  await publicSession();
  const info = await api<PublicSession>("public/login", { password });
  setCSRF(info.csrf || "");
  return info;
}
export async function upload(chat: string, file: File): Promise<unknown> {
  const response = await fetch(
    `/api/attachments?conversation_id=${encodeURIComponent(chat)}&name=${encodeURIComponent(file.name)}&request_key=${crypto.randomUUID()}`,
    {
      method: "POST",
      headers: headers({ "Content-Type": "application/octet-stream" }, true),
      body: file,
      credentials: "same-origin",
      cache: "no-store",
    },
  );
  const data = await response.json();
  if (!response.ok)
    throw new APIError(
      data.error || "附件上传失败。",
      response.status,
      data.code || "upload",
      data,
    );
  return data;
}
