import { useState } from "react";
import { api, remote } from "@/lib/api/client";
import { clearFormDraft } from "@/lib/drafts";
import { loadNavigation } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { TelegramGroupPolicy } from "./TelegramGroupPolicy";
import {
  Field,
  Loading,
  Result,
  lines,
  useFormDraft,
  useResource,
} from "./shared";

interface Connection {
  id: string;
  source: string;
  application_id: string;
  credential: string;
  enabled: boolean;
  listen?: string;
  plain_text?: boolean;
  disable_typing?: boolean;
  reply_stickers?: boolean;
  autonomous_reactions?: boolean;
}
interface Channels {
  expected_revisions: Record<string, number>;
  config: { connections: Connection[] };
  connections?: { id: string; running: boolean; diagnostic?: Diagnostic }[];
}
interface Diagnostic {
  since: string;
  updated: string;
  can_read_all?: boolean;
  counts: Record<string, number>;
  groups: Record<string, Record<string, number>>;
  ingress?: {
    advanced_at?: string;
    pending_since?: string;
    waiting_ms?: number;
    retries?: number;
  };
}

const diagnosticLabels: Record<string, string> = {
  received: "收到平台更新",
  filtered: "适配器过滤",
  other_bot_command: "发给其他 Bot 的命令",
  backlog_ignored: "忽略过期更新",
  poll_failed: "平台轮询失败",
  admitted: "已准入",
  not_admitted: "未准入或身份无法核实",
  admission_failed: "准入核查暂时失败，未确认接收",
  inbound_rejected: "路由无效，已记录拒绝",
  background_ignored: "未唤起，未保存正文",
  triggered: "已唤起",
  run_finished: "执行轮次已结束",
  run_failed: "执行出错",
  delivery_sent: "确认已送达",
  delivery_failed: "投递明确失败",
  delivery_unknown: "投递结果未知",
  queue_full: "队列已满，输入未采用",
  background_saved: "临时背景已保存（24 小时）",
};

// Keep diagnostics inside the existing connection section: same type, spacing,
// surfaces and controls; counters are facts, never a success badge for a Run.
function ReceptionDiagnostic({
  value,
  reload,
  telegram,
}: {
  value?: Diagnostic;
  reload: () => void;
  telegram: boolean;
}) {
  const ingress = value?.ingress;
  const waiting = Boolean(ingress?.pending_since);
  const seconds = Math.max(0, Math.floor((ingress?.waiting_ms ?? 0) / 1000));
  const elapsed =
    seconds >= 60
      ? `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
      : `${seconds} 秒`;
  return (
    <details className="settings-section">
      <summary>接收与投递诊断</summary>
      <p className="form-hint">
        {value?.since
          ? `本次连接的观测起点：${new Date(value.since).toLocaleString()}`
          : "尚无观测。未收到更新不代表模型或群权限有错。"}
      </p>
      {telegram && (
        <div role="status" aria-atomic="true">
          <p className={waiting ? "inline-warning" : "form-hint"}>
            <strong>
              入站：
              {waiting ? "等待确认" : ingress ? "无待确认更新" : "尚无观测"}
            </strong>
            {waiting &&
              ` · 已等待 ${elapsed} · 连续重试 ${ingress?.retries ?? 0} 次`}
          </p>
          <p className="form-hint">
            最近一次入站推进：
            {ingress?.advanced_at ? (
              <time dateTime={ingress.advanced_at}>
                {new Date(ingress.advanced_at).toLocaleString()}
              </time>
            ) : (
              "本次连接尚未观测到"
            )}
          </p>
        </div>
      )}
      <p className="form-hint">
        全局隐私模式：
        {value?.can_read_all === undefined
          ? "未知，等待平台身份核实"
          : value.can_read_all
            ? "已关闭"
            : "已开启"}
        。Bot
        成为某个群的管理员后，可见范围会扩大；这不会授予工具权限或开启背景保存。
      </p>
      <Button type="button" variant="outline" onClick={reload}>
        刷新诊断
      </Button>
      <dl className="channel-diagnostics">
        {Object.entries(diagnosticLabels).map(([key, label]) => (
          <div key={key}>
            <dt>{label}</dt>
            <dd>{value?.counts?.[key] || 0}</dd>
          </div>
        ))}
      </dl>
      {Object.entries(value?.groups || {}).map(([chat, counts]) => (
        <details key={chat}>
          <summary>聊天 {chat}</summary>
          <p className="form-hint">
            仅列此聊天实际观测到的事实；其他聊天的流量不能证明这里已收到消息。
          </p>
          <dl className="channel-diagnostics">
            {Object.entries(counts)
              .filter(([key]) => diagnosticLabels[key])
              .map(([key, count]) => (
                <div key={key}>
                  <dt>{diagnosticLabels[key]}</dt>
                  <dd>{count}</dd>
                </div>
              ))}
          </dl>
        </details>
      ))}
    </details>
  );
}
interface Credential {
  id: string;
  name: string;
  protocol: string;
  hint?: string;
}
interface Target {
  source: string;
  application_id: string;
  chat_id: string;
}
interface AccessRequest {
  id: string;
  code: string;
  target: Target;
  kind: string;
  person_id: string;
  display_name: string;
  expires_at: string;
}
interface Grant {
  target: Target;
  space_id: string;
  kind: string;
  enabled: boolean;
  whole_group: boolean;
  members: string[];
  revision: number;
  space_permission?: string;
  verified_people?: string[];
  people?: { id: string; mode: string }[];
  effective_permissions?: Record<string, string>;
}
const platforms: [string, string][] = [
  ["telegram", "Telegram"],
  ["lark", "Lark / 飞书"],
  ["qq", "QQ 官方机器人"],
];
const origins: Record<string, string> = {
  telegram: "https://api.telegram.org",
  lark: "https://open.feishu.cn",
  qq: "https://api.sgroup.qq.com",
};
export default function ChannelSettings() {
  const resource = useResource<Channels>("channels");
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const [form, change] = useFormDraft("channel-connection", {
    id: "",
    source: "telegram",
    application_id: "",
    credential: "",
    enabled: false,
    listen: "",
  });
  const [keys, setKeys] = useState<Credential[]>([]);
  const [pair, setPair] = useState("");
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      await action();
      await resource.reload();
    } catch (error) {
      setError((error as Error).message);
      await resource.reload().catch(() => {});
    } finally {
      setBusy(false);
    }
  }
  async function save(connection: Connection, remove = false) {
    if (!resource.value) return;
    const connections = (resource.value.config.connections || []).filter(
      (item) => item.id !== connection.id,
    );
    if (!remove) connections.push(connection);
    await api(
      "channels",
      { connections, expected_revisions: resource.value.expected_revisions },
      "PATCH",
    );
    setResult("渠道配置已保存；实际连接状态以运行记录为准。");
  }
  return (
    <>
      <p className="settings-intro">
        默认一个 Bot 连接一个
        Core。只有显式配置的账号才会启动渠道进程；模型与权限仍由 Core 管理。
      </p>
      {resource.value ? (
        <>
          {(resource.value.config.connections || []).map((item) => (
            <article className="settings-section" key={item.id}>
              <div className="section-title">
                <h2>{item.id}</h2>
                <span>
                  {resource.value!.connections?.find(
                    (state) => state.id === item.id,
                  )?.running
                    ? "在线"
                    : item.enabled
                      ? "等待连接"
                      : "已关闭"}
                </span>
              </div>
              <p className="form-hint">
                {item.source} · {item.application_id}
              </p>
              <ReceptionDiagnostic
                telegram={item.source === "telegram"}
                value={
                  resource.value!.connections?.find(
                    (state) => state.id === item.id,
                  )?.diagnostic
                }
                reload={() =>
                  void resource
                    .reload()
                    .catch((error) => setError((error as Error).message))
                }
              />
              {item.source === "telegram" && (
                <fieldset className="channel-options" disabled={busy}>
                  <legend>消息与互动</legend>
                  <p className="form-hint">勾选后即时保存，仅影响此连接。</p>
                  <div className="channel-options-grid">
                    <label className="check-line">
                      <input
                        type="checkbox"
                        checked={!item.plain_text}
                        onChange={(event) =>
                          void perform(() =>
                            save({
                              ...item,
                              plain_text: !event.target.checked,
                            }),
                          )
                        }
                      />
                      使用 Telegram 富文本
                    </label>
                    <label className="check-line">
                      <input
                        type="checkbox"
                        checked={!item.disable_typing}
                        onChange={(event) =>
                          void perform(() =>
                            save({
                              ...item,
                              disable_typing: !event.target.checked,
                            }),
                          )
                        }
                      />
                      私聊处理中显示“正在输入”
                    </label>
                    <label className="check-line">
                      <input
                        type="checkbox"
                        checked={Boolean(item.reply_stickers)}
                        onChange={(event) =>
                          void perform(() =>
                            save({
                              ...item,
                              reply_stickers: event.target.checked,
                            }),
                          )
                        }
                      />
                      允许本轮模型从已知贴纸中选择回复（每轮最多 1 张）
                    </label>
                    <label className="check-line">
                      <input
                        type="checkbox"
                        checked={Boolean(item.autonomous_reactions)}
                        onChange={(event) =>
                          void perform(() =>
                            save({
                              ...item,
                              autonomous_reactions: event.target.checked,
                            }),
                          )
                        }
                      />
                      允许模型自主回应当前消息表情
                    </label>
                  </div>
                </fieldset>
              )}
              <div className="provider-actions">
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() =>
                    void perform(() =>
                      save({ ...item, enabled: !item.enabled }),
                    )
                  }
                >
                  {item.enabled ? "停用连接" : "启用连接"}
                </Button>
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    if (window.confirm("删除此连接？空间和已有对话保留。"))
                      void perform(() => save(item, true));
                  }}
                >
                  删除连接
                </Button>
                {item.source !== "telegram" && (
                  <Button
                    variant="ghost"
                    disabled={busy}
                    onClick={() =>
                      void perform(async () => {
                        const value = await api<{
                          code: string;
                          expires_at: string;
                        }>(
                          `channels/${item.source}?connection=${encodeURIComponent(item.id)}`,
                          {},
                        );
                        setPair(`${value.code} · 到期 ${value.expires_at}`);
                      })
                    }
                  >
                    生成配对码
                  </Button>
                )}
              </div>
            </article>
          ))}
          {pair && <p role="status">{pair}</p>}
          <form
            className="settings-section"
            onSubmit={(event) => {
              event.preventDefault();
              void perform(async () => {
                await save(form);
                clearFormDraft("channel-connection");
                change({
                  ...form,
                  id: "",
                  application_id: "",
                  credential: "",
                  enabled: false,
                });
                clearFormDraft("channel-connection");
              });
            }}
          >
            <h2>添加渠道账号</h2>
            <fieldset disabled={busy}>
              <div className="form-grid">
                <Field label="连接 ID">
                  <input
                    required
                    value={form.id}
                    onChange={(event) =>
                      change({ ...form, id: event.target.value })
                    }
                    placeholder="telegram-main"
                  />
                </Field>
                <Field label="平台">
                  <Choice
                    label="渠道平台"
                    value={form.source}
                    options={platforms}
                    onChange={(source) => change({ ...form, source })}
                  />
                </Field>
                <Field label="Bot 数字 ID / 应用 ID">
                  <input
                    required
                    value={form.application_id}
                    onChange={(event) =>
                      change({ ...form, application_id: event.target.value })
                    }
                  />
                </Field>
                <Field label="渠道凭据">
                  <Choice
                    label="渠道凭据"
                    value={form.credential || "__none"}
                    options={[
                      ["__none", "先读取或保存渠道凭据"],
                      ...keys
                        .filter((item) => item.protocol === form.source)
                        .map(
                          (item) => [item.id, item.name] as [string, string],
                        ),
                    ]}
                    onChange={(credential) =>
                      change({
                        ...form,
                        credential: credential === "__none" ? "" : credential,
                      })
                    }
                  />
                </Field>
              </div>
              {!remote && (
                <Button
                  variant="ghost"
                  type="button"
                  onClick={() =>
                    void perform(async () =>
                      setKeys(await api<Credential[]>("channels/credentials")),
                    )
                  }
                >
                  读取渠道凭据列表
                </Button>
              )}
              {remote && (
                <Field label="已在本机保存的凭据 ID">
                  <input
                    required
                    value={form.credential}
                    onChange={(event) =>
                      change({ ...form, credential: event.target.value })
                    }
                  />
                </Field>
              )}
              {form.source === "qq" && (
                <Field label="QQ 本机回调地址">
                  <input
                    value={form.listen}
                    onChange={(event) =>
                      change({ ...form, listen: event.target.value })
                    }
                    placeholder="127.0.0.1:8098"
                  />
                </Field>
              )}
              <label className="check-line">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(event) =>
                    change({ ...form, enabled: event.target.checked })
                  }
                />
                保存后启用并连接平台
              </label>
              <Button type="submit" disabled={!form.credential}>
                保存连接
              </Button>
            </fieldset>
          </form>
          {(resource.value.config.connections || []).some(
            (item) => item.source === "telegram" && item.enabled,
          ) && (
            <>
              <TelegramAccess />
              <TelegramConfirmers />
            </>
          )}
        </>
      ) : (
        <Loading error={resource.error} />
      )}
      {!remote ? (
        <ChannelCredentials
          onChanged={async () =>
            setKeys(await api<Credential[]>("channels/credentials"))
          }
        />
      ) : (
        <p className="form-hint">
          渠道秘密在服务器本机独立保存，公网不会读取或显示。
        </p>
      )}
      <Result error={error} text={result} />
      {error && (
        <p className="form-hint">
          当前连接列表已重新读取；新增连接草稿保留。请核对后重新操作。
        </p>
      )}
    </>
  );
}
function ChannelCredentials({ onChanged }: { onChanged: () => Promise<void> }) {
  const resource = useResource<Credential[]>("channels/credentials");
  const [form, change] = useFormDraft("channel-credential", {
    name: "",
    protocol: "telegram",
    base_url: origins.telegram,
    secret: "",
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
      await resource.reload();
      await onChanged();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="settings-section">
      <h2>渠道凭据</h2>
      <p className="form-hint">
        独立于模型 Provider，Bot Token 不进入模型上下文。
      </p>
      {resource.value?.map((item) => (
        <div className="setting-line" key={item.id}>
          <span>
            {item.name} · {item.protocol}
          </span>
          <Button
            variant="ghost"
            disabled={busy}
            onClick={() => {
              if (window.confirm("删除此渠道凭据？被连接引用时会拒绝。"))
                void perform(async () => {
                  await api("channels/credentials", {
                    id: item.id,
                    remove: true,
                  });
                });
            }}
          >
            删除
          </Button>
        </div>
      ))}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void perform(async () => {
            await api("channels/credentials", { ...form, source: "saved" });
            change({ ...form, secret: "" });
            clearFormDraft("channel-credential");
          });
        }}
      >
        <fieldset disabled={busy}>
          <Field label="凭据名称">
            <input
              required
              maxLength={64}
              value={form.name}
              onChange={(event) =>
                change({ ...form, name: event.target.value })
              }
            />
          </Field>
          <Field label="凭据平台">
            <Choice
              label="凭据平台"
              value={form.protocol}
              options={platforms}
              onChange={(protocol) =>
                change({ ...form, protocol, base_url: origins[protocol] })
              }
            />
          </Field>
          <Field label="平台 API 地址">
            <input
              required
              type="url"
              value={form.base_url}
              onChange={(event) =>
                change({ ...form, base_url: event.target.value })
              }
            />
          </Field>
          <Field label="Bot Token / 应用 Secret">
            <input
              required
              type="password"
              autoComplete="new-password"
              value={form.secret}
              onChange={(event) =>
                change({ ...form, secret: event.target.value })
              }
            />
          </Field>
          <Button type="submit">保存渠道凭据</Button>
        </fieldset>
      </form>
      <Result error={error || resource.error} />
    </section>
  );
}

interface ConfirmersState {
  revision: number;
  members: string[];
}
function confirmerLines(state: ConfirmersState) {
  return state.members
    .map((id) => id.replace(/^telegram:user:/, ""))
    .join("\n");
}
interface ConfirmersPage {
  bots: { application_id: string; name: string; state: ConfirmersState }[];
}

function TelegramConfirmers() {
  const resource = useResource<ConfirmersPage>("channels/telegram/confirmers");
  return (
    <section className="settings-section">
      <h2>设置确认人员</h2>
      <p className="form-hint">
        每个 Bot
        一份名单，默认空。仅允许确认支持的会话设置，不改变聊天准入和工具权限，不能替别人审批命令。群内只支持当前会话已选扩展的一个已有选项，确认人与发起人可不同。
      </p>
      {resource.value ? (
        resource.value.bots.map((bot) => (
          <ConfirmersForm
            key={bot.application_id}
            bot={bot}
            reload={resource.reload}
          />
        ))
      ) : (
        <Loading error={resource.error} />
      )}
    </section>
  );
}

function ConfirmersForm({
  bot,
  reload,
}: {
  bot: ConfirmersPage["bots"][number];
  reload: () => Promise<unknown>;
}) {
  const draftID = "telegram-confirmers:" + bot.application_id;
  const [form, change] = useFormDraft(draftID, {
    members: confirmerLines(bot.state),
    revision: bot.state.revision,
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  function accept(state: ConfirmersState) {
    change({ members: confirmerLines(state), revision: state.revision });
    clearFormDraft(draftID);
  }
  async function save() {
    setBusy(true);
    setError("");
    setResult("");
    try {
      const members = lines(form.members);
      const bytes = await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(
          JSON.stringify([bot.application_id, form.revision, members]),
        ),
      );
      const operation_id = Array.from(new Uint8Array(bytes), (n) =>
        n.toString(16).padStart(2, "0"),
      ).join("");
      const saved = await api<{ state: ConfirmersState }>(
        "channels/telegram/confirmers",
        {
          bot: bot.application_id,
          members,
          expected_revision: form.revision,
          operation_id,
        },
      );
      accept(saved.state);
      await reload();
      setResult("确认名单已保存；聊天准入、工具权限及其他 Bot 保持原值。");
    } catch (e) {
      setError((e as Error).message);
      await reload().catch(() => {});
    } finally {
      setBusy(false);
    }
  }
  async function readSaved() {
    setBusy(true);
    setError("");
    try {
      const page = await api<ConfirmersPage>("channels/telegram/confirmers");
      const latest = page.bots.find(
        (item) => item.application_id === bot.application_id,
      );
      if (latest) accept(latest.state);
      await reload();
      setResult("已读取保存的名单。");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className="settings-section"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <h3>{bot.name}</h3>
      <p className="form-hint">
        Bot {bot.application_id} · 名单修订 {form.revision}
        。移除人员后，旧确认卡不会因重新加入而恢复。
      </p>
      <Field label="设置确认人员（每行一个 TG 数字用户 ID）">
        <textarea
          value={form.members}
          rows={4}
          onChange={(e) => change({ ...form, members: e.target.value })}
          spellCheck={false}
        />
      </Field>
      <div className="provider-actions">
        <Button type="submit" disabled={busy}>
          保存确认名单
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={() => void readSaved()}
        >
          放弃草稿并读取当前名单
        </Button>
      </div>
      <Result error={error} text={result} />
    </form>
  );
}

function TelegramAccess() {
  const resource = useResource<{ requests: AccessRequest[]; access: Grant[] }>(
    "channels/telegram/access",
  );
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  async function update(change: Record<string, unknown>) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      await api("channels/telegram/access", change);
      await resource.reload();
      await loadNavigation();
      setResult("准入设置已保存，工具权限独立控制。");
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="settings-section">
      <h2>Telegram 接入管理</h2>
      <p className="form-hint">
        用户发送 /start 后核对申请码和数字身份。允许聊天不会自动授予工具权限。
      </p>
      <Button
        variant="outline"
        disabled={busy}
        onClick={() =>
          resource.reload().catch((error) => setError(error.message))
        }
      >
        刷新申请
      </Button>
      {resource.value ? (
        <>
          {resource.value.requests.map((request) => (
            <AccessApplication
              key={request.id}
              request={request}
              busy={busy}
              onApprove={update}
            />
          ))}
          {!resource.value.requests.length && (
            <p className="form-hint">暂无有效申请。</p>
          )}
          <h3>接入名单</h3>
          {resource.value.access.map((grant) => (
            <AccessGrant
              key={grant.space_id}
              grant={grant}
              busy={busy}
              onChange={update}
            />
          ))}
        </>
      ) : (
        <Loading error={resource.error} />
      )}
      <Result error={error} text={result} />
    </section>
  );
}
function AccessApplication({
  request,
  busy,
  onApprove,
}: {
  request: AccessRequest;
  busy: boolean;
  onApprove: (value: Record<string, unknown>) => Promise<void>;
}) {
  const [whole, setWhole] = useState(false);
  return (
    <article className="settings-section">
      <h3>
        {request.code} · {request.display_name}
      </h3>
      <p className="form-hint">
        Bot {request.target.application_id} · {request.kind}{" "}
        {request.target.chat_id} · {request.person_id}
      </p>
      <p className="form-hint">
        到期 {new Date(request.expires_at).toLocaleString()}
      </p>
      {request.kind === "group" && (
        <label className="check-line">
          <input
            type="checkbox"
            checked={whole}
            onChange={(event) => setWhole(event.target.checked)}
          />
          允许群内所有成员聊天（会消耗实例模型额度）
        </label>
      )}
      <Button
        disabled={busy}
        onClick={() => {
          if (
            window.confirm(
              `允许 ${request.display_name}（${request.person_id}）通过 Bot ${request.target.application_id} 聊天？${whole ? "本次允许全群聊天。" : "仅批准此身份。"}`,
            )
          )
            void onApprove({
              request_id: request.id,
              target: request.target,
              kind: request.kind,
              enabled: true,
              whole_group: whole,
              members: [request.person_id],
            });
        }}
      >
        批准此身份聊天
      </Button>
    </article>
  );
}
function AccessGrant({
  grant,
  busy,
  onChange,
}: {
  grant: Grant;
  busy: boolean;
  onChange: (value: Record<string, unknown>) => Promise<void>;
}) {
  const [form, change] = useFormDraft("telegram:" + grant.space_id, {
    members: (grant.members?.length
      ? grant.members
      : grant.verified_people || []
    ).join("\n"),
    whole_group: grant.whole_group,
  });
  const [mode, setMode] = useState(grant.space_permission || "none"),
    [person, setPerson] = useState(""),
    [personMode, setPersonMode] = useState("none"),
    [error, setError] = useState("");
  return (
    <article className="settings-section">
      <h3>
        {grant.kind} {grant.target.chat_id} ·{" "}
        {grant.enabled ? "已允许" : "已停用"}
      </h3>
      <p className="form-hint">空间权限：{grant.space_permission || "none"}</p>
      {grant.kind === "group" && <TelegramGroupPolicy target={grant.target} />}
      <Field label="允许的人员 ID（每行 telegram:user:数字）">
        <textarea
          rows={3}
          value={form.members}
          onChange={(event) => change({ ...form, members: event.target.value })}
        />
      </Field>
      {grant.kind === "group" && (
        <label className="check-line">
          <input
            type="checkbox"
            checked={form.whole_group}
            onChange={(event) =>
              change({ ...form, whole_group: event.target.checked })
            }
          />
          全群可聊天（不授予工具）
        </label>
      )}
      <div className="provider-actions">
        <Button
          variant="outline"
          disabled={busy}
          onClick={() => {
            if (window.confirm("按下方名单允许聊天？工具权限不变。"))
              void onChange({
                target: grant.target,
                kind: grant.kind,
                enabled: true,
                whole_group: form.whole_group,
                members: lines(form.members),
              });
          }}
        >
          允许 / 保存名单
        </Button>
        <Button
          variant="ghost"
          disabled={busy || !grant.enabled}
          onClick={() => {
            if (window.confirm("停用这个聊天的接入？空间和历史保留。"))
              void onChange({
                target: grant.target,
                kind: grant.kind,
                enabled: false,
                whole_group: form.whole_group,
                members: lines(form.members),
              });
          }}
        >
          停用访问
        </Button>
      </div>
      <details>
        <summary>空间与人员工具权限</summary>
        <Field label="该空间权限">
          <Choice
            label="该空间权限"
            value={mode}
            options={[
              ["none", "仅聊天"],
              ["ask", "每次询问"],
              ["workspace", "工作区"],
              ["full", "Full access"],
            ]}
            onChange={setMode}
          />
        </Field>
        <Button
          variant="outline"
          onClick={async () => {
            if (!window.confirm(`设置此 Telegram 空间权限为 ${mode}？`)) return;
            try {
              await api("spaces", {
                kind: "space.set_permission",
                arguments: { space_id: grant.space_id, mode },
                confirmed: true,
              });
              await loadNavigation();
            } catch (error) {
              setError((error as Error).message);
            }
          }}
        >
          保存空间权限
        </Button>
        <Field label="人员 ID">
          <input
            value={person}
            onChange={(event) => setPerson(event.target.value)}
            placeholder="telegram:user:123456"
          />
        </Field>
        <Field label="人员全局权限">
          <Choice
            label="人员全局权限"
            value={personMode}
            options={[
              ["none", "仅聊天"],
              ["ask", "每次询问"],
              ["workspace", "工作区"],
              ["full", "Full access"],
            ]}
            onChange={setPersonMode}
          />
        </Field>
        <Button
          variant="outline"
          disabled={!person.trim()}
          onClick={async () => {
            if (
              !window.confirm(
                `修改 ${person} 的全局权限为 ${personMode}？会影响其在其他空间的能力，空间上限仍生效。`,
              )
            )
              return;
            try {
              await api("spaces", {
                kind: "person.set_permission",
                arguments: { person_id: person, mode: personMode },
                confirmed: true,
              });
            } catch (error) {
              setError((error as Error).message);
            }
          }}
        >
          保存人员权限
        </Button>
        <Result error={error} />
      </details>
    </article>
  );
}
