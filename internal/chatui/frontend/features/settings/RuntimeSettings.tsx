import { useState } from "react";
import { api } from "@/lib/api/client";
import { loadEnvironment } from "@/lib/store/runtime";
import { clearFormDraft } from "@/lib/drafts";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import type { Permission } from "@/lib/api/types";
import {
  Field,
  Loading,
  Result,
  lines,
  useFormDraft,
  useResource,
} from "./shared";

interface CommandRule {
  program: string;
  args?: string[];
  cwd?: string;
  decision: string;
}
interface Settings extends Record<string, unknown> {
  revision: string;
  default_permission_mode: Permission;
  enabled_tools: {
    file: boolean;
    command_run: boolean;
    network: boolean;
    memory: boolean;
  };
  file_access: {
    scope: string;
    allowed_roots: string[] | null;
    global_blocked_paths: string[] | null;
    protected_paths: string[];
  };
  command_run: {
    enabled: boolean;
    available: boolean;
    status: string;
    backend: string;
    execution: string;
    sandbox_supported: boolean;
    rules: CommandRule[] | null;
    allowed_executables: string[] | null;
    timeout_seconds: number;
    max_output_bytes: number;
    inherit_environment: boolean;
    services: {
      enabled: boolean;
      allow_local_listen: boolean;
      default_seconds: number;
      max_total_seconds: number;
    };
  };
  network: {
    allowed_origins: string[] | null;
    mode: string;
    allowed_domains: string[] | null;
  };
  context: {
    automatic: boolean;
    task_summary_bytes: number;
    auto_compact_percent: number;
    max_output_tokens: number;
    max_context_bytes: number;
  };
  recovery: { model_retries: number };
}
export default function RuntimeSettings({ section }: { section: string }) {
  const resource = useResource<Settings>("settings");
  return resource.value ? (
    <Editor key={section} initial={resource.value} section={section} />
  ) : (
    <Loading error={resource.error} />
  );
}
function Editor({ initial, section }: { initial: Settings; section: string }) {
  const draftID = `settings:${section}`;
  const [value, change] = useFormDraft<Settings>(draftID, initial);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const [roots, setRoots] = useState(
    (value.file_access.allowed_roots || []).join("\n"),
  );
  const [blocked, setBlocked] = useState(
    (value.file_access.global_blocked_paths || []).join("\n"),
  );
  const [origins, setOrigins] = useState(
    (value.network.allowed_origins || []).join("\n"),
  );
  const [executables, setExecutables] = useState(
    (value.command_run.allowed_executables || []).join("\n"),
  );
  const [newRule, setNewRule] = useState<CommandRule>({
    program: "",
    decision: "ask",
    args: [],
    cwd: "workspace",
  });
  function field<K extends keyof Settings>(key: K, next: Settings[K]) {
    change({ ...value, [key]: next });
  }
  async function save() {
    setBusy(true);
    setError("");
    setResult("");
    try {
      let path = "",
        body: Record<string, unknown> = {};
      if (section === "permissions") {
        if (
          value.default_permission_mode === "full" &&
          initial.default_permission_mode !== "full" &&
          !window.confirm("将运行时基础模式设为 Full？空间权限仍单独控制。")
        )
          return;
        path = "permissions";
        body = { default_permission_mode: value.default_permission_mode };
      } else if (section === "files") {
        if (
          !window.confirm(
            "保存全局文件范围和禁止路径？新授权不会解除系统保护路径。",
          )
        )
          return;
        path = "file-access";
        body = {
          scope: value.file_access.scope,
          allowed_roots: lines(roots),
          global_blocked_paths: lines(blocked),
        };
      } else if (section === "tools") {
        path = "tools";
        body = {
          ...value.enabled_tools,
          network_allowed_origins: lines(origins),
        };
      } else if (section === "commands") {
        if (
          value.command_run.execution === "host_explicit" &&
          initial.command_run.execution !== "host_explicit" &&
          !window.confirm(
            "保存高级宿主机执行模式？只有 Full 任务能使用，命令拥有当前操作系统用户权限。",
          )
        )
          return;
        const {
          enabled,
          execution,
          rules,
          timeout_seconds,
          max_output_bytes,
          inherit_environment,
          services,
        } = value.command_run;
        path = "command-run";
        body = {
          enabled,
          execution,
          rules: rules || [],
          allowed_executables: lines(executables),
          timeout_seconds,
          max_output_bytes,
          inherit_environment,
          services,
        };
      } else if (section === "context") {
        path = "context";
        body = {
          task_summary_bytes: value.context.automatic
            ? value.context.task_summary_bytes || 4096
            : 0,
          auto_compact_percent: value.context.auto_compact_percent,
          max_output_tokens: value.context.max_output_tokens,
          max_context_bytes: value.context.max_context_bytes,
          model_retries: value.recovery.model_retries,
        };
      }
      const next = await api<Settings>(
        `settings/${path}`,
        { ...body, expected_revision: value.revision },
        "PATCH",
      );
      change(next);
      clearFormDraft(draftID);
      await loadEnvironment();
      setResult("设置已保存。新任务使用新配置，已有任务保留自己的执行快照。");
    } catch (error) {
      setError(
        error instanceof Error ? error.message : "保存失败。草稿已保留。",
      );
    } finally {
      setBusy(false);
    }
  }
  const command = value.command_run,
    context = value.context;
  const commandChange = (next: Partial<Settings["command_run"]>) =>
    field("command_run", { ...command, ...next });
  async function pickPath(blockedPath: boolean, file: boolean) {
    const picker = file ? window.faSelectFile : window.faSelectFolder;
    if (!picker) return;
    try {
      const path = await picker("");
      if (!path) return;
      if (blockedPath) {
        const next = [...lines(blocked), path];
        setBlocked(next.join("\n"));
        field("file_access", {
          ...value.file_access,
          global_blocked_paths: next,
        });
      } else {
        const next = [...lines(roots), path];
        setRoots(next.join("\n"));
        field("file_access", { ...value.file_access, allowed_roots: next });
      }
    } catch (error) {
      setError((error as Error).message);
    }
  }
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <fieldset disabled={busy}>
        {section === "permissions" && (
          <>
            <p className="settings-intro">
              运行时基础策略不会授予空间权限。当前空间的权限在对话输入区调整；新空间从
              None 开始。
            </p>
            <Field label="运行时基础模式">
              <Choice
                label="运行时基础模式"
                value={value.default_permission_mode}
                options={[
                  ["ask", "每次询问"],
                  ["workspace", "工作区"],
                  ["full", "Full access"],
                ]}
                onChange={(mode) =>
                  field("default_permission_mode", mode as Permission)
                }
              />
            </Field>
            <Field label="网络策略">
              <Choice
                label="网络策略"
                value={value.network.mode || "restricted"}
                options={[
                  [
                    value.network.mode || "restricted",
                    value.network.mode || "restricted",
                  ],
                ]}
                disabled
                onChange={() => {}}
              />
            </Field>
            <p className="form-hint">
              网络请求的允许来源在“内置工具”中设置；本页不改变当前空间的授权。
            </p>
          </>
        )}
        {section === "files" && (
          <>
            <p className="settings-intro">
              全局文件范围与工具权限分别生效。禁止路径优先；系统保护目录始终拒绝访问。
            </p>
            <Field label="文件访问范围">
              <Choice
                label="文件访问范围"
                value={value.file_access.scope}
                options={[
                  ["workspace", "当前工作区"],
                  ["workspace_plus_allowed_roots", "工作区和授权路径"],
                  ["all_non_protected", "所有非保护文件"],
                ]}
                onChange={(scope) =>
                  field("file_access", { ...value.file_access, scope })
                }
              />
            </Field>
            <Field label="授权路径（每行一个）">
              <textarea
                rows={4}
                value={roots}
                onChange={(event) => {
                  setRoots(event.target.value);
                  field("file_access", {
                    ...value.file_access,
                    allowed_roots: lines(event.target.value),
                  });
                }}
              />
            </Field>
            {window.faSelectFolder && (
              <div className="provider-actions">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => void pickPath(false, false)}
                >
                  选择授权文件夹
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => void pickPath(false, true)}
                >
                  选择授权文件
                </Button>
              </div>
            )}
            <Field label="全局禁止路径（每行一个）">
              <textarea
                rows={4}
                value={blocked}
                onChange={(event) => {
                  setBlocked(event.target.value);
                  field("file_access", {
                    ...value.file_access,
                    global_blocked_paths: lines(event.target.value),
                  });
                }}
              />
            </Field>
            {window.faSelectFolder && (
              <div className="provider-actions">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => void pickPath(true, false)}
                >
                  选择禁止文件夹
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => void pickPath(true, true)}
                >
                  选择禁止文件
                </Button>
              </div>
            )}
            <details>
              <summary>系统保护路径</summary>
              <ul>
                {value.file_access.protected_paths.map((path) => (
                  <li key={path}>
                    <code>{path}</code>
                  </li>
                ))}
              </ul>
            </details>
          </>
        )}
        {section === "tools" && (
          <>
            <p className="settings-intro">
              只有启用的工具会进入新任务。开启工具仍需满足当前空间权限。
            </p>
            {(
              [
                ["file", "文件工具"],
                ["command_run", "命令工具"],
                ["network", "网络工具"],
                ["memory", "记忆工具"],
              ] as const
            ).map(([id, label]) => (
              <label className="check-line" key={id}>
                <input
                  type="checkbox"
                  checked={value.enabled_tools[id]}
                  onChange={(event) =>
                    field("enabled_tools", {
                      ...value.enabled_tools,
                      [id]: event.target.checked,
                    })
                  }
                />
                {label}
              </label>
            ))}
            <Field
              label="允许的网络来源（每行一个）"
              hint="填写完整 http(s)://host[:port]，不含路径或查询。网络工具至少需要一个允许来源。"
            >
              <textarea
                rows={5}
                value={origins}
                onChange={(event) => {
                  setOrigins(event.target.value);
                  field("network", {
                    ...value.network,
                    allowed_origins: lines(event.target.value),
                  });
                }}
                placeholder="https://docs.example.com"
              />
            </Field>
          </>
        )}
        {section === "commands" && (
          <>
            <p className="settings-intro">
              沙箱不可用时会明确拒绝执行。高级宿主机需 Full
              权限，按当前操作系统用户身份运行。
            </p>
            <p className="form-hint">
              当前后端：{command.backend || "尚未选择"} · {command.status} ·{" "}
              {command.available ? "可用" : "不可用"}
            </p>
            <label className="check-line">
              <input
                type="checkbox"
                checked={command.enabled}
                onChange={(event) =>
                  commandChange({ enabled: event.target.checked })
                }
              />
              启用 command_run
            </label>
            <Field label="执行后端">
              <Choice
                label="执行后端"
                value={command.execution || "sandboxed"}
                options={[
                  ["sandboxed", "安全沙箱"],
                  ["host_explicit", "高级宿主机"],
                ]}
                onChange={(execution) => commandChange({ execution })}
              />
            </Field>
            <div className="form-grid">
              <Field label="单次命令超时（秒）">
                <input
                  type="number"
                  min={1}
                  max={600}
                  required
                  value={command.timeout_seconds}
                  onChange={(event) =>
                    commandChange({
                      timeout_seconds: Number(event.target.value),
                    })
                  }
                />
              </Field>
              <Field label="输出上限（字节）">
                <input
                  type="number"
                  min={1024}
                  max={1048576}
                  required
                  value={command.max_output_bytes}
                  onChange={(event) =>
                    commandChange({
                      max_output_bytes: Number(event.target.value),
                    })
                  }
                />
              </Field>
            </div>
            <Field label="可执行文件白名单（每行一个绝对路径）">
              <textarea
                rows={4}
                value={executables}
                onChange={(event) => {
                  setExecutables(event.target.value);
                  commandChange({
                    allowed_executables: lines(event.target.value),
                  });
                }}
              />
            </Field>
            <label className="check-line">
              <input
                type="checkbox"
                checked={command.inherit_environment}
                onChange={(event) =>
                  commandChange({ inherit_environment: event.target.checked })
                }
              />
              请求继承环境变量（运行时仍过滤秘密）
            </label>
            <section className="settings-section">
              <h2>有限期服务</h2>
              {(
                [
                  ["enabled", "允许有限期服务"],
                  ["allow_local_listen", "允许服务监听"],
                ] as const
              ).map(([id, label]) => (
                <label className="check-line" key={id}>
                  <input
                    type="checkbox"
                    checked={Boolean(command.services?.[id])}
                    onChange={(event) =>
                      commandChange({
                        services: {
                          ...command.services,
                          [id]: event.target.checked,
                        },
                      })
                    }
                  />
                  {label}
                </label>
              ))}
              <p className="form-hint">
                Full 无法强制把监听限制在本机；当前沙箱不支持服务监听。
              </p>
              <div className="form-grid">
                {(
                  [
                    ["default_seconds", "默认服务秒数"],
                    ["max_total_seconds", "最长总秒数"],
                  ] as const
                ).map(([id, label]) => (
                  <Field key={id} label={label}>
                    <input
                      type="number"
                      min={1}
                      max={28800}
                      required
                      value={command.services?.[id] ?? 1800}
                      onChange={(event) =>
                        commandChange({
                          services: {
                            ...command.services,
                            [id]: Number(event.target.value),
                          },
                        })
                      }
                    />
                  </Field>
                ))}
              </div>
            </section>
            <details className="settings-section">
              <summary>高级命令规则</summary>
              {(command.rules || []).map((rule, index) => (
                <div className="rule-row" key={index}>
                  <code>
                    {rule.program} {(rule.args || []).join(" ")}
                  </code>
                  <span>{rule.decision}</span>
                  <button
                    type="button"
                    onClick={() =>
                      commandChange({
                        rules: command.rules!.filter((_, i) => i !== index),
                      })
                    }
                  >
                    删除
                  </button>
                </div>
              ))}
              <Field label="规则程序">
                <input
                  value={newRule.program}
                  onChange={(event) =>
                    setNewRule({ ...newRule, program: event.target.value })
                  }
                />
              </Field>
              <Field label="参数前缀（每行一个参数）">
                <textarea
                  rows={2}
                  value={(newRule.args || []).join("\n")}
                  onChange={(event) =>
                    setNewRule({ ...newRule, args: lines(event.target.value) })
                  }
                />
              </Field>
              <Field label="规则工作目录">
                <Choice
                  label="规则工作目录"
                  value={newRule.cwd || "workspace"}
                  options={[
                    ["workspace", "工作区"],
                    ["selected_roots", "授权路径"],
                  ]}
                  onChange={(cwd) => setNewRule({ ...newRule, cwd })}
                />
              </Field>
              <Field label="规则处理">
                <Choice
                  label="规则处理"
                  value={newRule.decision}
                  options={[
                    ["ask", "询问"],
                    ["allow", "允许"],
                    ["deny", "拒绝"],
                  ]}
                  onChange={(decision) => setNewRule({ ...newRule, decision })}
                />
              </Field>
              <Button
                variant="outline"
                type="button"
                disabled={!newRule.program.trim()}
                onClick={() => {
                  commandChange({ rules: [...(command.rules || []), newRule] });
                  setNewRule({ ...newRule, program: "", args: [] });
                }}
              >
                添加规则到草稿
              </Button>
            </details>
          </>
        )}
        {section === "context" && (
          <>
            <p className="settings-intro">
              压缩保留原始历史；摘要请求计入模型用量。当前对话的手动压缩在输入区打开。
            </p>
            <label className="check-line">
              <input
                type="checkbox"
                checked={context.automatic}
                onChange={(event) =>
                  field("context", {
                    ...context,
                    automatic: event.target.checked,
                  })
                }
              />
              自动压缩上下文
            </label>
            <div className="form-grid">
              {(
                [
                  ["max_output_tokens", "默认输出 token 上限", 1, 32768],
                  ["max_context_bytes", "请求字节上限", 4096, 16777216],
                  ["auto_compact_percent", "自动触发占比（%）", 50, 95],
                  ["task_summary_bytes", "摘要上限（UTF-8 字节）", 1, 16384],
                ] as const
              ).map(([id, label, min, max]) => (
                <Field key={id} label={label}>
                  <input
                    type="number"
                    min={min}
                    max={max}
                    step={1}
                    required
                    value={
                      id === "task_summary_bytes" && !context[id]
                        ? 4096
                        : context[id]
                    }
                    onChange={(event) =>
                      field("context", {
                        ...context,
                        [id]: Number(event.target.value),
                      })
                    }
                  />
                </Field>
              ))}
              <Field label="模型请求自动恢复次数">
                <input
                  type="number"
                  min={0}
                  max={6}
                  step={1}
                  required
                  value={value.recovery.model_retries}
                  onChange={(event) =>
                    field("recovery", {
                      model_retries: Number(event.target.value),
                    })
                  }
                />
              </Field>
            </div>
            <p className="form-hint">
              模型重试可能增加费用；不确定的工具结果只做有界只读核查。
            </p>
          </>
        )}
        <Result error={error} text={result} />
        <div className="provider-actions">
          <Button type="submit">{busy ? "正在保存…" : "保存设置"}</Button>
          {error && (
            <Button
              type="button"
              variant="outline"
              onClick={async () => {
                if (!window.confirm("读取最新设置并放弃本页草稿？")) return;
                try {
                  const next = await api<Settings>("settings");
                  change(next);
                  clearFormDraft(draftID);
                  setRoots((next.file_access.allowed_roots || []).join("\n"));
                  setBlocked(
                    (next.file_access.global_blocked_paths || []).join("\n"),
                  );
                  setOrigins((next.network.allowed_origins || []).join("\n"));
                  setExecutables(
                    (next.command_run.allowed_executables || []).join("\n"),
                  );
                  setError("");
                } catch (error) {
                  setError((error as Error).message);
                }
              }}
            >
              读取最新并放弃草稿
            </Button>
          )}
        </div>
      </fieldset>
    </form>
  );
}
