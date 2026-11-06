export function apiPath(path, remote) {
  if (!remote || !path.startsWith("attach/")) return `/api/${path}`;
  const name = path.slice(7).split("?")[0];
  if (!["context", "commands", "cancel", "inputs", "branch"].includes(name))
    throw new Error("此本机接口不向公网开放。");
  return `/api/remote/${path.slice(7)}`;
}

export function authHeaders({ remote, key, csrf, active = false }, extra = {}) {
  return remote
    ? {
        ...(csrf ? { "X-FreeAgent-CSRF": csrf } : {}),
        ...(active ? { "X-FreeAgent-Activity": "1" } : {}),
        ...extra,
      }
    : { "X-FreeAgent-Key": key, ...extra };
}

export const publicSecuritySwitches = [
  ["failure_lock", "登录失败锁定"],
  ["auto_blacklist", "自动黑名单"],
  ["global_guard", "全局登录保护"],
  ["allowlist_enabled", "启用 IP 白名单"],
  ["denylist_enabled", "启用 IP 黑名单"],
  ["cookie_check", "登录 Cookie 检测"],
  ["session_binding", "会话绑定（来源网段和浏览器）"],
];
export const publicSecurityNumbers = [
  ["failure_limit", "失败次数", 1, 100],
  ["failure_window_minutes", "失败窗口（分钟）", 1, 1440],
  ["lock_minutes", "锁定时长（分钟）", 1, 1440],
  ["blacklist_after_locks", "几次锁定后加入黑名单", 1, 100],
  ["blacklist_minutes", "自动黑名单时长（分钟）", 1, 43200],
  ["global_failure_limit", "全局失败阈值", 1, 10000],
  ["global_window_minutes", "全局窗口（分钟）", 1, 1440],
  ["global_lock_minutes", "全局暂停时长（分钟）", 1, 1440],
];

export async function publicSession(fetcher = fetch) {
  const response = await fetcher("/api/public/session", {
    cache: "no-store",
    credentials: "same-origin",
  });
  if (!response.ok)
    throw new Error("公网入口拒绝访问，请通过服务器本机 CLI 检查安全设置。");
  return response.json();
}

export async function loginPublicPassword(password, fetcher = fetch) {
  // A login page may remain open longer than the one-use detection cookie.
  // Refresh only on this explicit submit, never with an authentication timer.
  const existing = await publicSession(fetcher);
  if (existing.authenticated) return existing;
  const response = await fetcher("/api/public/login", {
    method: "POST",
    credentials: "same-origin",
    cache: "no-store",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
  });
  const info = await response.json();
  if (!response.ok) throw new Error(info.error || "登录失败。");
  return info;
}
