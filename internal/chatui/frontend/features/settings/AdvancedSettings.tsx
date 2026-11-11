import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { ControlPreview } from "@/components/ControlPreview";
import { Choice } from "@/components/Choice";
import { loadEnvironment, notify } from "@/lib/store/runtime";
import { clearFormDraft } from "@/lib/drafts";
import {
  Field,
  Loading,
  Result,
  lines,
  useFormDraft,
  useResource,
} from "./shared";

type Data = Record<string, unknown>;
type FieldSpec = {
  key: string;
  label: string;
  type?: "number" | "toggle" | "lines" | "text" | "area";
  options?: string[];
  min?: number;
  max?: number;
};
interface Policy {
  id: string;
  title: string;
  kind: string;
  source: string;
  fields: FieldSpec[];
  hint?: string;
}
const policies: Policy[] = [
  {
    id: "routing",
    title: "自动模型路由",
    kind: "auto.set_policy",
    source: "auto",
    hint: "自动选择和升级有独立的有限次数，只有显式开启后才使用。",
    fields: [
      { key: "default_selection", label: "默认选择（模型 ID 或 auto）" },
      { key: "enabled", label: "开启自动路由", type: "toggle" },
      { key: "models", label: "候选模型 ID（每行一个）", type: "lines" },
      { key: "assessment_model", label: "评估模型 ID" },
      {
        key: "max_candidates",
        label: "最多候选",
        type: "number",
        min: 1,
        max: 32,
      },
      {
        key: "max_assessments",
        label: "最多评估",
        type: "number",
        min: 0,
        max: 4,
      },
      {
        key: "max_upgrades",
        label: "最多升级",
        type: "number",
        min: 0,
        max: 4,
      },
    ],
  },
  {
    id: "memory-policy",
    title: "Memory 预算",
    kind: "memory.set_policy",
    source: "memory",
    fields: [
      { key: "knowledge_enabled", label: "允许显式知识检索", type: "toggle" },
      {
        key: "max_entries",
        label: "最多条目",
        type: "number",
        min: 1,
        max: 64,
      },
      {
        key: "max_context_bytes",
        label: "上下文最多字节",
        type: "number",
        min: 1024,
        max: 65536,
      },
      {
        key: "max_document_bytes",
        label: "单条目最多字节",
        type: "number",
        min: 1024,
        max: 65536,
      },
    ],
  },
  {
    id: "http",
    title: "HTTP 工具策略",
    kind: "http.set_policy",
    source: "http",
    fields: [
      { key: "enabled", label: "启用 HTTP 请求", type: "toggle" },
      {
        key: "allowed_origins",
        label: "允许来源（每行 http(s)://host[:port]）",
        type: "lines",
      },
      {
        key: "timeout_seconds",
        label: "单请求超时秒数",
        type: "number",
        min: 1,
        max: 600,
      },
      {
        key: "max_output_bytes",
        label: "输出最多字节",
        type: "number",
        min: 1024,
        max: 1048576,
      },
    ],
  },
  {
    id: "limits",
    title: "上下文读取与工具结果",
    kind: "runtime.set_limits",
    source: "limits",
    hint: "普通任务没有累计总步数或总时长截断。以下只控制单次请求和上下文读取。",
    fields: [
      {
        key: "extension_max_context_chars",
        label: "扩展上下文最多字符",
        type: "number",
        min: 0,
        max: 1048576,
      },
      {
        key: "extension_max_model_calls_per_turn",
        label: "扩展每轮最多额外模型调用",
        type: "number",
        min: 0,
        max: 128,
      },
      {
        key: "history_page_size",
        label: "上下文历史读取页大小",
        type: "number",
        min: 1,
        max: 1000,
      },
      {
        key: "max_tool_result_bytes",
        label: "单次工具结果字节上限",
        type: "number",
        min: 1024,
        max: 16777216,
      },
    ],
  },
  {
    id: "collaboration",
    title: "可选协作策略",
    kind: "collaboration.set_policy",
    source: "collaboration",
    hint: "当前 GUI 普通对话保持单 Core 执行。显式协作任务同时遵守子任务预算和父任务累计预算。",
    fields: [
      { key: "mode", label: "协作模式", options: ["off", "ask", "auto"] },
      { key: "models", label: "授权模型 ID", type: "lines" },
      { key: "tools", label: "授权工具", type: "lines" },
      { key: "paths", label: "授权相对路径", type: "lines" },
      {
        key: "max_replans",
        label: "最多重新规划",
        type: "number",
        min: 0,
        max: 4,
      },
      { key: "synthesis", label: "合并结果", type: "toggle" },
      ...[
        ["max_children", "最多子任务", 16],
        ["max_model_calls", "父任务模型调用", 1000],
        ["max_tool_calls", "父任务工具调用", 1000],
        ["max_output_bytes", "父任务输出字节", 64 * 1024 ** 2],
        ["max_concurrent_children", "同时子任务", 4],
        ["max_active_seconds", "父任务累计执行秒数", 3600],
      ].map(([id, label, max]) => ({
        key: "limits." + id,
        label: String(label),
        type: "number" as const,
        min: 1,
        max: Number(max),
      })),
      ...[
        ["max_steps", "子任务最多步数", 100],
        ["max_seconds", "子任务最多秒数", 3600],
        ["max_output_bytes", "子任务输出字节", 16 * 1024 ** 2],
        ["max_call_output_bytes", "子任务单调用输出字节", 1024 ** 2],
      ].map(([id, label, max]) => ({
        key: "child_limits." + id,
        label: String(label),
        type: "number" as const,
        min: 1,
        max: Number(max),
      })),
      {
        key: "shared_memory.read",
        label: "读取任务共享 Memory",
        type: "toggle",
      },
      {
        key: "shared_memory.append",
        label: "追加任务共享 Memory",
        type: "toggle",
      },
      {
        key: "shared_memory.modify",
        label: "修改任务共享 Memory",
        type: "toggle",
      },
      { key: "shared_memory.kinds", label: "共享条目类型", type: "lines" },
      { key: "shared_memory.entries", label: "限定共享条目 ID", type: "lines" },
    ],
  },
];
function get(data: Data, path: string): unknown {
  let value: unknown = data;
  for (const part of path.split(".")) {
    if (!value || typeof value !== "object") return undefined;
    value = (value as Data)[part];
  }
  return value;
}
function set(data: Data, path: string, value: unknown): Data {
  const next = structuredClone(data);
  const parts = path.split(".");
  let target = next;
  for (const part of parts.slice(0, -1)) {
    if (!target[part] || typeof target[part] !== "object") target[part] = {};
    target = target[part] as Data;
  }
  target[parts.at(-1)!] = value;
  return next;
}
export default function AdvancedSettings() {
  const resource = useResource<Data>("control/config"),
    prompt = useResource<{
      system_prompt: string;
      instructions: string[] | null;
      revision: string;
      expected_revisions: Record<string, number>;
    }>("settings/prompt");
  if (!resource.value) return <Loading error={resource.error} />;
  const revisions = resource.value.control_revisions as Record<string, number>;
  const revision = (name: string) => ({ [name]: revisions?.[name] || 1 });
  const permissions = {
    value: {
      execution: resource.value.execution as {
        sandbox_mode?: string;
        network_policy?: Data;
      },
    },
    reload: resource.reload,
  };

  return (
    <>
      <p className="settings-intro">
        这些设置作用于后续任务。先核对具体变更，再确认；拒绝或版本冲突保留原配置。
      </p>
      {policies.map((policy) => (
        <PolicyEditor
          key={policy.id + ":" + (revisions?.["config." + policy.source] || 1)}
          expectedRevisions={revision("config." + policy.source)}
          policy={policy}
          initial={
            policy.id === "routing"
              ? {
                  ...(resource.value!.auto as Data),
                  default_selection: resource.value!.default_selection,
                }
              : ((resource.value![policy.source] || {}) as Data)
          }
          onSaved={async () => {
            await resource.reload();
            await loadEnvironment();
          }}
        />
      ))}
      {permissions.value?.execution.sandbox_mode && (
        <PolicyEditor
          policy={{
            id: "sandbox",
            title: "默认命令沙箱模式",
            kind: "execution.set_sandbox",
            source: "",
            hint: "沙箱模式与空间授权分别约束执行；宿主模式仍要求 Full 权限。",
            fields: [
              {
                key: "mode",
                label: "沙箱模式",
                options: [
                  "read_only",
                  "workspace_write",
                  "selected_roots",
                  "host_full",
                ],
              },
            ],
          }}
          key={"sandbox:" + (revisions?.["config.execution.set_sandbox"] || 1)}
          expectedRevisions={revision("config.execution.set_sandbox")}
          initial={{ mode: permissions.value.execution.sandbox_mode }}
          onSaved={async () => {
            await permissions.reload();
            await loadEnvironment();
          }}
        />
      )}
      {permissions.value?.execution.network_policy && (
        <PolicyEditor
          policy={{
            id: "network",
            title: "执行环境网络策略",
            kind: "execution.set_network",
            source: "",
            fields: [
              {
                key: "mode",
                label: "网络模式",
                options: ["off", "internet", "allowlist", "host_inherited"],
              },
              {
                key: "allowed_domains",
                label: "允许域名（每行一个）",
                type: "lines",
              },
            ],
          }}
          key={"network:" + (revisions?.["config.execution.set_network"] || 1)}
          expectedRevisions={revision("config.execution.set_network")}
          initial={permissions.value.execution.network_policy}
          onSaved={async () => {
            await permissions.reload();
            await loadEnvironment();
          }}
        />
      )}
      {prompt.value ? (
        <PolicyEditor
          policy={{
            id: "prompt",
            title: "基础指令",
            kind: "prompt.set_policy",
            source: "",
            hint: "基础指令也受权限边界约束。指令文件路径和正文是用户数据，不进入诊断包。",
            fields: [
              { key: "system_prompt", label: "系统指令", type: "area" },
              {
                key: "instructions",
                label: "指令文件路径（每行一个）",
                type: "lines",
              },
            ],
          }}
          key={"prompt:" + JSON.stringify(prompt.value.expected_revisions)}
          expectedRevisions={prompt.value.expected_revisions}
          initial={{
            system_prompt: prompt.value.system_prompt,
            instructions: prompt.value.instructions || [],
          }}
          onSaved={async () => {
            await prompt.reload();
          }}
        />
      ) : (
        <Loading error={prompt.error} />
      )}
    </>
  );
}
function PolicyEditor({
  policy,
  initial,
  onSaved,
  expectedRevisions,
}: {
  expectedRevisions: Record<string, number>;
  policy: Policy;
  initial: Data;
  onSaved: () => Promise<void>;
}) {
  const draftID = "advanced:" + policy.id;
  const [value, change] = useFormDraft<Data>(draftID, {
    ...initial,
    _expected_revisions: expectedRevisions,
  });
  const { _expected_revisions, ...argumentsValue } = value;
  const latestValue = useRef(value);
  const reloadController = useRef<AbortController | null>(null);
  latestValue.current = value;
  useEffect(() => () => reloadController.current?.abort(), []);
  const [reloading, setReloading] = useState(false),
    [reloadError, setReloadError] = useState("");
  async function reload() {
    if (!window.confirm("放弃此项草稿并读取已保存的配置？")) return;
    const requestedValue = latestValue.current;
    const controller = new AbortController();
    reloadController.current?.abort();
    reloadController.current = controller;
    setReloading(true);
    setReloadError("");
    try {
      // Read first. A failed fetch must not discard the only copy of the draft.
      let next: Data;
      if (policy.id === "prompt") {
        const data = await api<Data>(
          "settings/prompt",
          undefined,
          undefined,
          controller.signal,
        );
        next = {
          system_prompt: data.system_prompt,
          instructions: data.instructions || [],
          _expected_revisions: data.expected_revisions,
        };
      } else {
        const data = await api<Data>(
            "control/config",
            undefined,
            undefined,
            controller.signal,
          ),
          execution = data.execution as Data;
        const resource =
          policy.id === "sandbox" || policy.id === "network"
            ? "config.execution.set_" + policy.id
            : "config." + policy.source;
        const revisions = data.control_revisions as Record<string, number>;
        const fields =
          policy.id === "sandbox"
            ? { mode: execution.sandbox_mode }
            : policy.id === "network"
              ? (execution.network_policy as Data)
              : policy.id === "routing"
                ? {
                    ...(data.auto as Data),
                    default_selection: data.default_selection,
                  }
                : (data[policy.source] as Data);
        next = {
          ...fields,
          _expected_revisions: { [resource]: revisions?.[resource] || 1 },
        };
      }
      if (controller.signal.aborted) return;
      if (latestValue.current !== requestedValue) {
        setReloadError("读取期间草稿已修改，已保留新输入；请核对后重新读取。");
        return;
      }
      change(next);
    } catch (error) {
      if (!controller.signal.aborted) setReloadError((error as Error).message);
    } finally {
      setReloading(false);
    }
  }
  const fields = policy.fields;
  return (
    <details className="settings-section">
      <summary>{policy.title}</summary>
      {policy.hint && <p className="form-hint">{policy.hint}</p>}
      <div className="form-grid">
        {fields.map((field) => {
          const raw = get(value, field.key),
            changeField = (next: unknown) =>
              change(set(value, field.key, next));
          if (field.type === "toggle")
            return (
              <label className="check-line" key={field.key}>
                <input
                  type="checkbox"
                  checked={Boolean(raw)}
                  onChange={(event) => changeField(event.target.checked)}
                />
                {field.label}
              </label>
            );
          return (
            <Field key={field.key} label={field.label}>
              {field.options ? (
                <Choice
                  label={field.label}
                  value={String(raw || field.options[0])}
                  options={field.options.map((item) => [item, item])}
                  onChange={changeField}
                />
              ) : field.type === "area" || field.type === "lines" ? (
                <textarea
                  rows={field.type === "area" ? 8 : 3}
                  value={
                    Array.isArray(raw) ? raw.join("\n") : String(raw || "")
                  }
                  onChange={(event) =>
                    changeField(
                      field.type === "lines"
                        ? lines(event.target.value)
                        : event.target.value,
                    )
                  }
                />
              ) : (
                <input
                  type={field.type === "number" ? "number" : "text"}
                  min={field.min}
                  max={field.max}
                  step={field.type === "number" ? 1 : undefined}
                  value={
                    field.type === "number"
                      ? Number(raw) || 0
                      : String(raw || "")
                  }
                  onChange={(event) =>
                    changeField(
                      field.type === "number"
                        ? Number(event.target.value)
                        : event.target.value,
                    )
                  }
                />
              )}
            </Field>
          );
        })}
      </div>
      <Button
        variant="ghost"
        disabled={reloading}
        onClick={() => void reload()}
      >
        重新读取此项
      </Button>
      <Result error={reloadError} />
      <ControlPreview
        kind={policy.kind}
        disabled={reloading}
        expectedRevisions={_expected_revisions as Record<string, number>}
        arguments={
          policy.id === "limits"
            ? Object.fromEntries(
                fields.map((field) => [field.key, value[field.key]]),
              )
            : argumentsValue
        }
        onSaved={async () => {
          clearFormDraft(draftID);
          await onSaved();
          notify("已确认并保存设置");
        }}
      />
    </details>
  );
}
