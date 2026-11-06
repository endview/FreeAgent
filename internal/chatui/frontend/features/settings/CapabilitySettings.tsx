import { lazy, Suspense, useState } from "react";
import { api, apiText, remote } from "@/lib/api/client";
import { loadEnvironment } from "@/lib/store/runtime";
import { clearFormDraft } from "@/lib/drafts";
import { Button } from "@/components/ui/button";
import { Toggle } from "@/components/Choice";
import {
  Field,
  Loading,
  Result,
  lines,
  useFormDraft,
  useResource,
} from "./shared";

const AffectWeavePage = lazy(() => import("./AffectWeavePage"));

interface MCP {
  id: string;
  enabled: boolean;
  state?: string;
  connected?: boolean;
  command: string;
  args?: string[];
  cwd?: string;
  env_from?: Record<string, string>;
  env_names?: string[];
  hidden_tools?: string[];
  trust_read_only_hint: boolean;
  tools?: { name: string; exposed_name?: string; description?: string }[];
  tool_count?: number;
}
interface Skill {
  name: string;
  description: string;
  trigger: string;
  source: string;
  enabled: boolean;
  available: boolean;
  workspace_trusted: boolean;
  body?: string;
  files?: { path: string }[];
}
interface Extension {
  storage_ready?: boolean;
  id: string;
  name: string;
  description: string;
  source: string;
  enabled: boolean;
  available: boolean;
  required: boolean;
  state: string;
  workspace_trusted: boolean;
  settings?: Record<string, unknown>;
  env_from?: Record<string, string>;
  version?: string;
}

export function MCPSettings() {
  const resource = useResource<{ servers: MCP[] }>("mcp");
  const [selected, setSelected] = useState("");
  if (!resource.value) return <Loading error={resource.error} />;
  const server = resource.value.servers.find((item) => item.id === selected);
  return (
    <>
      <p className="settings-intro">
        MCP 服务按需运行；新配置默认不信任服务声明的只读提示。
      </p>
      {remote && (
        <p className="inline-warning">
          公网只显示 MCP 状态，修改请使用本机工作台或 CLI。
        </p>
      )}
      <div className="providers-layout">
        <nav className="provider-list" aria-label="MCP 服务">
          {resource.value.servers.map((item) => (
            <button
              key={item.id}
              className={item.id === selected ? "selected" : ""}
              onClick={() => setSelected(item.id)}
            >
              <strong>{item.id}</strong>
              <small>
                {item.state} · {item.tool_count || 0} 个工具
              </small>
            </button>
          ))}
          <Button
            variant="outline"
            disabled={remote}
            onClick={() => setSelected("")}
          >
            新增 MCP
          </Button>
        </nav>
        <MCPEditor
          key={server?.id || "new"}
          server={server}
          onSaved={async (id) => {
            await resource.reload();
            setSelected(id);
            await loadEnvironment();
          }}
        />
      </div>
    </>
  );
}
function objectJSON(raw: string, label: string): Record<string, unknown> {
  const value: unknown = JSON.parse(raw || "{}");
  if (!value || Array.isArray(value) || typeof value !== "object")
    throw new Error(label + "必须是 JSON 对象。");
  return value as Record<string, unknown>;
}
function MCPEditor({
  server,
  onSaved,
}: {
  server?: MCP;
  onSaved: (id: string) => Promise<void>;
}) {
  const draftID = "mcp:" + (server?.id || "new");
  const [form, change] = useFormDraft(draftID, {
    id: server?.id || "",
    enabled: server?.enabled || false,
    command: server?.command || "",
    args: (server?.args || []).join("\n"),
    cwd: server?.cwd || "",
    envFrom: JSON.stringify(server?.env_from || {}, null, 2),
    environmentJSON: "",
    hiddenTools: (server?.hidden_tools || []).join("\n"),
    trustReadOnlyHint: server?.trust_read_only_hint || false,
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState(""),
    [logs, setLogs] = useState("");
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
  function config() {
    const envFrom = objectJSON(form.envFrom, "环境变量映射");
    if (Object.values(envFrom).some((value) => typeof value !== "string"))
      throw new Error("环境变量映射的值必须是变量名。");
    return {
      enabled: form.enabled,
      command: form.command,
      args: lines(form.args),
      cwd: form.cwd,
      envFrom,
      ...(form.environmentJSON.trim()
        ? { env: objectJSON(form.environmentJSON, "秘密环境变量") }
        : {}),
      hiddenTools: lines(form.hiddenTools),
      trustReadOnlyHint: form.trustReadOnlyHint,
    };
  }
  return (
    <form
      className="provider-editor"
      onSubmit={(event) => {
        event.preventDefault();
        void perform(async () => {
          const value = config();
          await api(
            server ? `mcp/${encodeURIComponent(server.id)}/update` : "mcp",
            server ? value : { id: form.id, config: value },
          );
          clearFormDraft(draftID);
          await onSaved(form.id);
          setResult("MCP 配置已保存。");
        });
      }}
    >
      <fieldset disabled={busy || remote}>
        <Field label="服务 ID">
          <input
            required
            disabled={Boolean(server)}
            value={form.id}
            onChange={(event) => change({ ...form, id: event.target.value })}
          />
        </Field>
        <label className="check-line">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(event) =>
              change({ ...form, enabled: event.target.checked })
            }
          />
          启用服务
        </label>
        <Field label="启动程序">
          <input
            required
            value={form.command}
            onChange={(event) =>
              change({ ...form, command: event.target.value })
            }
          />
        </Field>
        <Field label="启动参数（每行一个）">
          <textarea
            rows={3}
            value={form.args}
            onChange={(event) => change({ ...form, args: event.target.value })}
          />
        </Field>
        <Field label="工作目录">
          <input
            value={form.cwd}
            onChange={(event) => change({ ...form, cwd: event.target.value })}
          />
        </Field>
        <Field
          label="环境变量映射（JSON）"
          hint="仅填写已有环境变量的名称，不在这里填写秘密。"
        >
          <textarea
            rows={4}
            value={form.envFrom}
            onChange={(event) =>
              change({ ...form, envFrom: event.target.value })
            }
          />
        </Field>
        <Field
          label="直接环境变量（JSON；留空保留）"
          hint={`仅当前内存保存草稿，已保存值不回读。已有名称：${(server?.env_names || []).join("、") || "无"}；提交 {} 可清除。AI 不能填写这些秘密。`}
        >
          <input
            type="password"
            autoComplete="off"
            value={form.environmentJSON}
            onChange={(event) =>
              change({ ...form, environmentJSON: event.target.value })
            }
          />
        </Field>
        <Field label="隐藏工具（每行一个）">
          <textarea
            rows={3}
            value={form.hiddenTools}
            onChange={(event) =>
              change({ ...form, hiddenTools: event.target.value })
            }
          />
        </Field>
        <label className="check-line">
          <input
            type="checkbox"
            checked={form.trustReadOnlyHint}
            onChange={(event) =>
              change({ ...form, trustReadOnlyHint: event.target.checked })
            }
          />
          信任服务声明的只读提示
        </label>
        <div className="provider-actions">
          <Button type="submit">保存 MCP</Button>
          <Button
            type="button"
            variant="outline"
            disabled={!form.id || !form.command}
            onClick={() =>
              void perform(async () => {
                const status = await api<{
                  connected?: boolean;
                  tool_count?: number;
                }>(`mcp/${encodeURIComponent(form.id)}/test`, config());
                setResult(
                  status.connected
                    ? `连接成功，发现 ${status.tool_count || 0} 个工具。`
                    : "连接未就绪，请查看日志。",
                );
              })
            }
          >
            测试连接
          </Button>
          {server && (
            <>
              <Button
                type="button"
                variant="ghost"
                onClick={() =>
                  void perform(async () => {
                    await api(
                      `mcp/${encodeURIComponent(server.id)}/restart`,
                      {},
                    );
                    await onSaved(server.id);
                    setResult("服务已重启。");
                  })
                }
              >
                重启
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() =>
                  void perform(async () =>
                    setLogs(
                      await apiText(
                        `mcp/${encodeURIComponent(server.id)}/logs`,
                      ),
                    ),
                  )
                }
              >
                日志
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  if (window.confirm("删除此 MCP 配置？"))
                    void perform(async () => {
                      await api(
                        `mcp/${encodeURIComponent(server.id)}/delete`,
                        {},
                      );
                      clearFormDraft(draftID);
                      await onSaved("");
                    });
                }}
              >
                删除
              </Button>
            </>
          )}
        </div>
      </fieldset>
      <Result error={error} text={result} />
      {server?.tools?.length ? (
        <details className="settings-section">
          <summary>{server.tools.length} 个工具</summary>
          <ul>
            {server.tools.map((tool) => (
              <li key={tool.name}>
                <code>{tool.exposed_name || tool.name}</code> {tool.description}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {logs && <pre className="settings-output">{logs}</pre>}
    </form>
  );
}

export function SkillSettings() {
  const resource = useResource<{ skills: Skill[] }>("skills");
  const [selected, setSelected] = useState("");
  const [detail, setDetail] = useState<Skill | null>(null);
  const [path, setPath] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
      await resource.reload();
      await loadEnvironment();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const skill =
    detail?.name === selected
      ? detail
      : resource.value?.skills.find((item) => item.name === selected);
  if (!resource.value) return <Loading error={resource.error} />;
  return (
    <>
      <p className="settings-intro">
        全局启用后，在输入区选择本轮使用的 Skill。工作区内容需要显式信任。
      </p>
      {remote && (
        <p className="inline-warning">
          公网只显示状态；导入和修改请使用本机入口。
        </p>
      )}
      <form
        className="inline-form"
        onSubmit={(event) => {
          event.preventDefault();
          void perform(async () => {
            const item = await api<Skill>("skills", { path });
            setSelected(item.name);
            setDetail(item);
            setPath("");
          });
        }}
      >
        <Field label="Skill 文件夹绝对路径">
          <input
            required
            value={path}
            onChange={(event) => setPath(event.target.value)}
            disabled={remote || busy}
          />
        </Field>
        <Button
          type="submit"
          variant="outline"
          disabled={remote || busy || !path.trim()}
        >
          导入 Skill
        </Button>
      </form>
      <Result error={error} />
      <div className="providers-layout">
        <nav className="provider-list" aria-label="Skill 列表">
          {resource.value.skills.map((item) => (
            <button
              key={item.name}
              className={selected === item.name ? "selected" : ""}
              onClick={() => {
                setSelected(item.name);
                api<Skill>(`skills/${encodeURIComponent(item.name)}/view`)
                  .then(setDetail)
                  .catch((error) => setError(error.message));
              }}
            >
              <strong>{item.name}</strong>
              <small>
                {item.enabled ? "已开启" : "已关闭"} · {item.source}
              </small>
            </button>
          ))}
        </nav>
        <section className="provider-editor">
          {skill ? (
            <>
              <h2>{skill.name}</h2>
              <p>{skill.description}</p>
              <p className="form-hint">
                {skill.source} · {skill.trigger}
              </p>
              <div className="setting-line">
                <span>全局启用</span>
                <Toggle
                  label="启用 Skill"
                  checked={skill.enabled}
                  onChange={(enabled) =>
                    void perform(async () => {
                      await api(
                        `skills/${encodeURIComponent(skill.name)}/update`,
                        { enabled },
                      );
                      setDetail(null);
                    })
                  }
                />
              </div>
              {skill.source === "workspace" && (
                <Button
                  variant="outline"
                  disabled={remote || busy}
                  onClick={() => {
                    if (
                      window.confirm(
                        skill.workspace_trusted
                          ? "取消信任当前工作区？"
                          : "信任当前工作区的 Skill？其中内容可以影响模型上下文。",
                      )
                    )
                      void perform(async () => {
                        await api("skills/trust", {
                          trusted: !skill.workspace_trusted,
                        });
                        setDetail(null);
                      });
                  }}
                >
                  {skill.workspace_trusted ? "取消信任工作区" : "信任工作区"}
                </Button>
              )}
              <details className="settings-section">
                <summary>查看 Skill 内容</summary>
                <pre className="settings-output">
                  {skill.body || "正在读取…"}
                </pre>
              </details>
              <Button
                variant="ghost"
                disabled={remote || busy || skill.source !== "user"}
                onClick={() => {
                  if (
                    window.confirm(
                      "删除导入的 Skill？停用不会删除，删除后需重新导入。",
                    )
                  )
                    void perform(async () => {
                      await api(
                        `skills/${encodeURIComponent(skill.name)}/delete`,
                        {},
                      );
                      setSelected("");
                      setDetail(null);
                    });
                }}
              >
                删除 Skill
              </Button>
            </>
          ) : (
            <p className="form-hint">选择一个 Skill 查看内容和状态。</p>
          )}
        </section>
      </div>
    </>
  );
}

export function ExtensionSettings() {
  const resource = useResource<{ extensions: Extension[] }>("extensions");
  const [selected, setSelected] = useState("");
  const [path, setPath] = useState(""),
    [error, setError] = useState("");
  if (!resource.value) return <Loading error={resource.error} />;
  const extension = resource.value.extensions.find(
    (item) => item.id === selected,
  );
  async function reload() {
    await resource.reload();
    await loadEnvironment();
  }
  return (
    <>
      <p className="settings-intro">
        扩展按需启动。停用保留数据；启用扩展不会扩大工具权限。
      </p>
      {remote && (
        <p className="inline-warning">公网只显示状态，管理操作需要本机入口。</p>
      )}
      <form
        className="inline-form"
        onSubmit={async (event) => {
          event.preventDefault();
          setError("");
          try {
            const item = await api<Extension>("extensions", { path });
            await reload();
            setSelected(item.id);
            setPath("");
          } catch (error) {
            setError((error as Error).message);
          }
        }}
      >
        <Field label="扩展文件夹绝对路径">
          <input
            disabled={remote}
            required
            value={path}
            onChange={(event) => setPath(event.target.value)}
          />
        </Field>
        <Button
          type="submit"
          variant="outline"
          disabled={remote || !path.trim()}
        >
          导入扩展
        </Button>
      </form>
      <Result error={error} />
      <div className="providers-layout">
        <nav className="provider-list" aria-label="扩展列表">
          {resource.value.extensions.map((item) => (
            <button
              key={item.id}
              className={selected === item.id ? "selected" : ""}
              onClick={() => setSelected(item.id)}
            >
              <strong>{item.name || item.id}</strong>
              <small>{item.state || "已关闭"}</small>
            </button>
          ))}
        </nav>
        <section className="provider-editor">
          {extension ? (
            <ExtensionEditor
              key={extension.id}
              item={extension}
              onSaved={reload}
            />
          ) : (
            <p className="form-hint">选择一个扩展查看状态和配置。</p>
          )}
        </section>
      </div>
    </>
  );
}
function ExtensionEditor({
  item,
  onSaved,
}: {
  item: Extension;
  onSaved: () => Promise<void>;
}) {
  const draftID = "extension:" + item.id;
  const [form, change] = useFormDraft(draftID, {
    settings: JSON.stringify(item.settings || {}, null, 2),
    envFrom: JSON.stringify(item.env_from || {}, null, 2),
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState(""),
    [logs, setLogs] = useState("");
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      await action();
      await onSaved();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const prefix = `extensions/${encodeURIComponent(item.id)}`;
  return (
    <>
      <h2>{item.name || item.id}</h2>
      <p>{item.description}</p>
      <p className="form-hint">
        {item.state} · {item.version} · {item.source}
      </p>
      {item.id === "cc.endview.affectweave" && (
        <Suspense fallback={<Loading />}>
          <AffectWeavePage
            id={item.id}
            enabled={item.enabled}
            storageReady={Boolean(item.storage_ready)}
            onSaved={onSaved}
          />
        </Suspense>
      )}
      <fieldset disabled={busy || remote}>
        <div className="setting-line">
          <span>全局启用</span>
          <Toggle
            label="启用扩展"
            checked={item.enabled}
            onChange={(enabled) =>
              void perform(async () => {
                await api(prefix + "/update", { enabled });
              })
            }
          />
        </div>
        <label className="check-line">
          <input
            type="checkbox"
            checked={item.required}
            onChange={(event) =>
              void perform(async () => {
                await api(prefix + "/update", {
                  required: event.target.checked,
                });
              })
            }
          />
          扩展失败时阻止本轮
        </label>
        {item.source === "workspace" && (
          <Button
            variant="outline"
            onClick={() => {
              if (
                window.confirm(
                  item.workspace_trusted
                    ? "取消信任当前工作区扩展？"
                    : "信任此工作区扩展？启用后会运行其中的程序。",
                )
              )
                void perform(async () => {
                  await api("extensions/trust", {
                    trusted: !item.workspace_trusted,
                  });
                });
            }}
          >
            {item.workspace_trusted ? "取消信任工作区" : "信任工作区"}
          </Button>
        )}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void perform(async () => {
              await api(prefix + "/update", {
                settings: objectJSON(form.settings, "设置"),
                env_from: objectJSON(form.envFrom, "环境变量映射"),
              });
              clearFormDraft(draftID);
              setResult("扩展配置已保存。");
            });
          }}
        >
          <Field label="扩展设置（JSON）">
            <textarea
              rows={7}
              disabled={!item.available}
              value={form.settings}
              onChange={(event) =>
                change({ ...form, settings: event.target.value })
              }
            />
          </Field>
          <Field
            label="环境变量映射（JSON）"
            hint="只引用已有变量名，不直接填写秘密。"
          >
            <textarea
              rows={4}
              disabled={!item.available}
              value={form.envFrom}
              onChange={(event) =>
                change({ ...form, envFrom: event.target.value })
              }
            />
          </Field>
          <div className="provider-actions">
            <Button type="submit" disabled={!item.available}>
              保存扩展配置
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={() =>
                void perform(async () => {
                  await api(prefix + "/restart", {});
                  setResult("扩展已重启。");
                })
              }
            >
              重启
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() =>
                void perform(async () =>
                  setLogs(await apiText(prefix + "/logs")),
                )
              }
            >
              日志
            </Button>
          </div>
        </form>
        <div className="settings-section">
          <Button
            variant="ghost"
            disabled={item.source !== "user" || item.enabled || item.required}
            onClick={() => {
              if (
                window.confirm(
                  "移除此已停用的用户扩展？扩展持久数据保留；工作区扩展请在原目录管理。",
                )
              )
                void perform(async () => {
                  await api(prefix + "/delete", {});
                  setResult("扩展已移除，持久数据保留。");
                });
            }}
          >
            移除扩展
          </Button>
          <Button
            variant="ghost"
            onClick={() => {
              if (
                window.confirm(
                  "清空此扩展的全部持久数据？此操作不可撤销，请先备份。",
                )
              )
                void perform(async () => {
                  await api(prefix + "/clear-data", {});
                  setResult("扩展数据已清空。");
                });
            }}
          >
            清空扩展数据
          </Button>
        </div>
      </fieldset>
      <Result error={error} text={result} />
      {logs && <pre className="settings-output">{logs}</pre>}
    </>
  );
}
