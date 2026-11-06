import { useState } from "react";
import { api, remote, setCSRF } from "@/lib/api/client";
import { clearDrafts, clearFormDraft } from "@/lib/drafts";
import { resetRuntime } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import {
  Field,
  Loading,
  Result,
  lines,
  useFormDraft,
  useResource,
} from "./shared";

interface Security {
  failure_lock: boolean;
  failure_limit: number;
  failure_window_minutes: number;
  lock_minutes: number;
  auto_blacklist: boolean;
  blacklist_after_locks: number;
  blacklist_minutes: number;
  global_guard: boolean;
  global_failure_limit: number;
  global_window_minutes: number;
  global_lock_minutes: number;
  allowlist_enabled: boolean;
  allowlist: string[] | null;
  denylist_enabled: boolean;
  denylist: string[] | null;
  cookie_check: boolean;
  session_binding: boolean;
}
interface Config extends Record<string, unknown> {
  enabled: boolean;
  origin: string;
  socket: string;
  socket_group: string;
  max_permission: string;
  max_sessions: number;
  idle_minutes: number;
  absolute_hours: number;
  security: Security;
}
interface PublicInfo {
  config: Config;
  credential_ready: boolean;
  listener_active: boolean;
  restart_required: boolean;
}
const switches = [
  ["failure_lock", "多次密码错误后临时锁定"],
  ["auto_blacklist", "多次锁定后自动加入黑名单"],
  ["global_guard", "全局失败保护"],
  ["allowlist_enabled", "启用 IP 白名单"],
  ["denylist_enabled", "启用 IP 黑名单"],
  ["cookie_check", "登录 Cookie 回传检测"],
  ["session_binding", "会话绑定来源与浏览器"],
] as const;
const numbers = [
  ["failure_limit", "失败次数", 1, 50],
  ["failure_window_minutes", "失败计数窗口（分钟）", 1, 1440],
  ["lock_minutes", "临时锁定分钟", 1, 10080],
  ["blacklist_after_locks", "累计锁定次数", 1, 100],
  ["blacklist_minutes", "自动黑名单分钟", 1, 43200],
  ["global_failure_limit", "全局失败次数", 1, 10000],
  ["global_window_minutes", "全局窗口分钟", 1, 1440],
  ["global_lock_minutes", "全局锁定分钟", 1, 1440],
] as const;
export default function PublicSettings() {
  const resource = useResource<PublicInfo>("public-settings");
  return resource.value ? (
    <Editor initial={resource.value} />
  ) : (
    <Loading error={resource.error} />
  );
}
function Editor({ initial }: { initial: PublicInfo }) {
  const [form, change] = useFormDraft<Config>(
    "public-settings",
    initial.config,
  );
  const [info, setInfo] = useState(initial),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const [passwords, changePasswords] = useFormDraft("public-passwords", {
    current: "",
    next: "",
    repeat: "",
  });
  const [events, setEvents] = useState("");
  const [allowlist, setAllowlist] = useState(
      (form.security.allowlist || []).join("\n"),
    ),
    [denylist, setDenylist] = useState(
      (form.security.denylist || []).join("\n"),
    );
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      await action();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  function security(next: Partial<Security>) {
    change({ ...form, security: { ...form.security, ...next } });
  }
  return (
    <>
      <p className="settings-intro">
        公网与 Telegram 共用此 Core。启用入口前需配置 HTTPS
        反向代理与管理员密码。
      </p>
      <p className="form-hint">
        监听：{info.listener_active ? "已启用" : "未启用"} · 密码：
        {info.credential_ready ? "已设置" : "尚未设置"}
        {info.restart_required ? " · 需要重启 Owner" : ""}
      </p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (
            !window.confirm(
              "保存以下公网设置？降低权限立即生效，认证变化可能要求重新登录；首次启用和监听变化需要在本机重启 Owner。",
            )
          )
            return;
          void perform(async () => {
            const next = await api<PublicInfo>(
              "public-settings",
              {
                ...form,
                security: {
                  ...form.security,
                  allowlist: lines(allowlist),
                  denylist: lines(denylist),
                },
              },
              "PUT",
            );
            setInfo(next);
            change(next.config);
            clearFormDraft("public-settings");
            setResult(
              next.restart_required
                ? "设置已保存，请在服务器本机重启 Owner 以启用新监听。"
                : "公网设置已保存。",
            );
          });
        }}
      >
        <fieldset disabled={busy}>
          <label className="check-line">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(event) =>
                change({ ...form, enabled: event.target.checked })
              }
            />
            开启公网 Web
          </label>
          <Field label="HTTPS 地址">
            <input
              type="url"
              required={form.enabled}
              value={form.origin}
              onChange={(event) =>
                change({ ...form, origin: event.target.value })
              }
              placeholder="https://bot.example.com"
            />
          </Field>
          <details>
            <summary>本机反向代理连接</summary>
            <Field label="Unix socket">
              <input
                disabled={remote}
                required
                value={form.socket}
                onChange={(event) =>
                  change({ ...form, socket: event.target.value })
                }
              />
            </Field>
            <Field label="Socket 用户组">
              <input
                disabled={remote}
                required
                value={form.socket_group}
                onChange={(event) =>
                  change({ ...form, socket_group: event.target.value })
                }
              />
            </Field>
          </details>
          <Field
            label="公网最高权限"
            hint="空间与原发起人的权限仍然生效，提高上限需要服务器本机确认。"
          >
            <Choice
              label="公网最高权限"
              value={form.max_permission}
              options={[
                ["none", "仅聊天"],
                ["ask", "每次询问"],
                ["workspace", "工作区"],
                ["full", "Full access"],
              ]}
              onChange={(max_permission) => change({ ...form, max_permission })}
            />
          </Field>
          <div className="form-grid">
            {(
              [
                ["max_sessions", "最多登录会话", 1, 8],
                ["idle_minutes", "无交互到期（分钟）", 1, 120],
                ["absolute_hours", "绝对有效期（小时）", 1, 24],
              ] as const
            ).map(([id, label, min, max]) => (
              <Field key={id} label={label}>
                <input
                  type="number"
                  required
                  min={min}
                  max={max}
                  value={form[id]}
                  onChange={(event) =>
                    change({ ...form, [id]: Number(event.target.value) })
                  }
                />
              </Field>
            ))}
          </div>
          <section className="settings-section">
            <h2>登录保护</h2>
            <p className="form-hint">
              按需选择保护项。Cookie 检测只检查登录回传；黑名单优先于白名单。
            </p>
            {switches.map(([id, label]) => (
              <label className="check-line" key={id}>
                <input
                  type="checkbox"
                  checked={form.security[id]}
                  onChange={(event) => security({ [id]: event.target.checked })}
                />
                {label}
              </label>
            ))}
            <div className="form-grid">
              {numbers.map(([id, label, min, max]) => (
                <Field key={id} label={label}>
                  <input
                    type="number"
                    required
                    min={min}
                    max={max}
                    value={form.security[id]}
                    onChange={(event) =>
                      security({ [id]: Number(event.target.value) })
                    }
                  />
                </Field>
              ))}
            </div>
            <Field label="IP 白名单（每行地址或 CIDR）">
              <textarea
                rows={4}
                value={allowlist}
                onChange={(event) => {
                  setAllowlist(event.target.value);
                  security({ allowlist: lines(event.target.value) });
                }}
              />
            </Field>
            <Field label="IP 黑名单（每行地址或 CIDR）">
              <textarea
                rows={4}
                value={denylist}
                onChange={(event) => {
                  setDenylist(event.target.value);
                  security({ denylist: lines(event.target.value) });
                }}
              />
            </Field>
          </section>
          <Button type="submit">保存公网设置</Button>
        </fieldset>
      </form>
      <Result error={error} text={result} />
      <form
        className="settings-section"
        onSubmit={(event) => {
          event.preventDefault();
          if (passwords.next !== passwords.repeat) {
            setError("两次新密码不一致。");
            return;
          }
          if (!window.confirm("修改管理员密码并撤销全部远程会话？")) return;
          void perform(async () => {
            const value = { ...passwords };
            changePasswords({ current: "", next: "", repeat: "" });
            clearFormDraft("public-passwords");
            await api(
              remote ? "public/password" : "public-password",
              remote
                ? { Current: value.current, Password: value.next }
                : { password: value.next },
            );
            setResult("密码已修改，远程会话已撤销。");
            if (remote) {
              setCSRF("");
              clearDrafts();
              resetRuntime();
              window.dispatchEvent(new Event("freeagent-auth-required"));
            }
          });
        }}
      >
        <h2>管理员密码</h2>
        <fieldset disabled={busy}>
          {remote && (
            <Field label="当前密码">
              <input
                type="password"
                required
                autoComplete="current-password"
                value={passwords.current}
                onChange={(event) =>
                  changePasswords({ ...passwords, current: event.target.value })
                }
              />
            </Field>
          )}
          <Field label="新密码">
            <input
              type="password"
              autoComplete="new-password"
              minLength={12}
              maxLength={1024}
              required
              value={passwords.next}
              onChange={(event) =>
                changePasswords({ ...passwords, next: event.target.value })
              }
            />
          </Field>
          <Field label="再次输入新密码">
            <input
              type="password"
              autoComplete="new-password"
              minLength={12}
              maxLength={1024}
              required
              value={passwords.repeat}
              onChange={(event) =>
                changePasswords({ ...passwords, repeat: event.target.value })
              }
            />
          </Field>
          <Button variant="outline" type="submit">
            更换密码
          </Button>
        </fieldset>
      </form>
      <section className="settings-section">
        <h2>安全事件与恢复</h2>
        <div className="provider-actions">
          <Button
            variant="outline"
            disabled={busy}
            onClick={() =>
              void perform(async () =>
                setEvents(
                  JSON.stringify(
                    await api(remote ? "public/security" : "public-recovery"),
                    null,
                    2,
                  ),
                ),
              )
            }
          >
            读取安全事件
          </Button>
          {!remote && (
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => {
                if (window.confirm("解除当前临时锁定？既有静态名单保留。"))
                  void perform(async () => {
                    await api("public-recovery", {});
                    setResult("临时锁定已解除。");
                  });
              }}
            >
              解除临时锁定
            </Button>
          )}
        </div>
        {events && <pre className="settings-output">{events}</pre>}
        <p className="form-hint">
          被名单或锁定阻止登录时，通过 SSH 本机执行 public clear-locks、public
          disable 或 public password。
        </p>
      </section>
    </>
  );
}
