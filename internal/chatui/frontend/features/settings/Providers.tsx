import { useState } from "react";
import { Plus, RefreshCw, Trash2, Zap } from "lucide-react";
import { api } from "@/lib/api/client";
import type { Model, ReasoningCapabilities } from "@/lib/api/types";
import {
  ReasoningChoice,
  reasoningSelection,
  reasoningValue,
} from "@/components/ReasoningChoice";
import { loadEnvironment, notify, useRuntime } from "@/lib/store/runtime";
import { clearFormDraft } from "@/lib/drafts";
import { ControlPreview } from "@/components/ControlPreview";
import { Button } from "@/components/ui/button";
import { Choice, Toggle } from "@/components/Choice";
import { Field, Loading, Result, useFormDraft, useResource } from "./shared";

interface Profile {
  id: string;
  name: string;
  base_url: string;
  protocol: string;
  source: string;
  note?: string;
  hint?: string;
  env_name?: string;
  needs_reentry?: boolean;
  last_check?: { result?: string };
}
interface Preset {
  id: string;
  name: string;
  base_url: string;
  protocol: string;
  model: string;
  models?: string[];
}
interface Keys {
  profiles: Profile[];
  presets: Preset[];
}
interface KeyForm extends Record<string, unknown> {
  id: string;
  name: string;
  base_url: string;
  protocol: string;
  source: string;
  key: string;
  note: string;
  enable: boolean;
}
interface Probe {
  category: string;
  models?: string[];
  detail?: string;
}
const probes = new Map<string, Probe>();
let lastSelected = "";

export default function Providers() {
  const resource = useResource<Keys>("keys");
  const [selected, setSelected] = useState(lastSelected);
  function choose(id: string) {
    lastSelected = id;
    setSelected(id);
  }
  if (!resource.value) return <Loading error={resource.error} />;
  const profile = resource.value.profiles.find((item) => item.id === selected);
  return (
    <>
      <p className="settings-intro">
        按需开启 Provider。保存配置不会自动发送模型请求。
      </p>
      <div className="providers-layout">
        <nav className="provider-list" aria-label="Provider 配置">
          {resource.value.profiles.map((item) => (
            <button
              key={item.id}
              className={selected === item.id ? "selected" : ""}
              onClick={() => choose(item.id)}
            >
              <strong>{item.name}</strong>
              <small>{item.base_url}</small>
            </button>
          ))}
          <Button variant="outline" onClick={() => choose("")}>
            <Plus size={15} />
            新增 Provider
          </Button>
        </nav>
        <ProviderEditor
          key={profile?.id || "new"}
          profile={profile}
          presets={resource.value.presets}
          onSaved={async (id) => {
            await resource.reload();
            choose(id);
          }}
        />
      </div>
    </>
  );
}

function ProviderEditor({
  profile,
  presets,
  onSaved,
}: {
  profile?: Profile;
  presets: Preset[];
  onSaved: (id: string) => Promise<void>;
}) {
  const env = useRuntime().environment;
  const draftID = `provider:${profile?.id || "new"}`;
  const [form, change] = useFormDraft<KeyForm>(draftID, {
    id: profile?.id || "",
    name: profile?.name || "",
    base_url: profile?.base_url || "",
    protocol: profile?.protocol || "openai-compatible",
    source: profile?.source || "saved",
    key: "",
    note: profile?.note || "",
    enable: false,
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const [probe, setProbe] = useState<Probe | undefined>(
    probes.get(profile?.id || "new"),
  );
  const [manual, setManual] = useState(""),
    [query, setQuery] = useState("");
  const [presetID, setPresetID] = useState("");
  const models = Object.entries(env?.models || {}).filter(
    ([, model]) => model.key === profile?.id,
  );
  function field<K extends keyof KeyForm>(name: K, value: KeyForm[K]) {
    change({ ...form, [name]: value });
  }
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      await action();
    } catch (error) {
      setError(error instanceof Error ? error.message : "操作失败。");
    } finally {
      setBusy(false);
    }
  }
  function request() {
    const { enable, ...request } = form;
    return request;
  }
  async function save() {
    await perform(async () => {
      const saved = await api<Profile>(
        profile ? `keys/${encodeURIComponent(profile.id)}/update` : "keys",
        request(),
      );
      clearFormDraft(draftID);
      change({ ...form, key: "", enable: false });
      clearFormDraft(draftID);
      if (!profile && form.enable) {
        try {
          await api("providers", { id: saved.id, enabled: true }, "PUT");
        } catch (error) {
          setError(
            `Key 已保存，Provider 未启用：${error instanceof Error ? error.message : "请重新开启"}`,
          );
        }
      }
      if (probe) probes.set(saved.id, probe);
      await loadEnvironment();
      await onSaved(saved.id);
      setResult("配置已保存。");
    });
  }
  async function test() {
    await perform(async () => {
      const unchanged =
        profile &&
        !form.key &&
        form.name === profile.name &&
        form.protocol === profile.protocol &&
        form.base_url === profile.base_url &&
        form.source === profile.source &&
        form.note === (profile.note || "");
      const value = await api<Probe>(
        "keys/test",
        unchanged ? { id: profile.id } : request(),
      );
      probes.set(profile?.id || "new", value);
      setProbe(value);
      setResult(
        value.category === "ok"
          ? `连接成功，返回 ${value.models?.length || 0} 个模型。`
          : `连接测试：${value.category}${value.detail ? "，" + value.detail : ""}`,
      );
    });
  }
  async function add(model: string) {
    if (!profile || !model.trim()) return;
    await perform(async () => {
      await api("models/enable", {
        key: profile.id,
        model: model.trim(),
        default: !Object.keys(env?.models || {}).length,
      });
      await loadEnvironment();
      setManual("");
      setResult("模型已添加。");
    });
  }
  async function removeModel(id: string) {
    const remaining = Object.keys(env?.models || {}).filter(
      (key) => key !== id,
    );
    let next = "";
    if (env?.default_model === id && remaining.length) {
      next =
        window.prompt(
          `删除默认模型，请选择新的默认模型 ID：\n${remaining.join("\n")}`,
          remaining[0],
        ) || "";
      if (!next) return;
    } else if (
      !window.confirm(
        `删除模型 ${env?.models[id]?.model || id}？历史记录保留。`,
      )
    )
      return;
    await perform(async () => {
      await api(`models/${encodeURIComponent(id)}/disable`, {
        new_default: next,
      });
      await loadEnvironment();
    });
  }
  async function removeProvider() {
    if (
      !profile ||
      !window.confirm(
        `删除 Provider“${profile.name}”和引用它的 ${models.length} 个模型？历史记录保留。`,
      )
    )
      return;
    let next = "";
    if (models.some(([id]) => id === env?.default_model)) {
      const remaining = Object.keys(env?.models || {}).filter(
        (id) => !models.some(([other]) => id === other),
      );
      if (remaining.length) {
        next =
          window.prompt(
            "请输入替代默认模型 ID：\n" + remaining.join("\n"),
            remaining[0],
          ) || "";
        if (!next) return;
      }
    }
    await perform(async () => {
      await api(`keys/${encodeURIComponent(profile.id)}/delete`, {
        disable_models: Boolean(models.length),
        new_default: next,
      });
      clearFormDraft(draftID);
      probes.delete(profile.id);
      await loadEnvironment();
      await onSaved("");
    });
  }
  const available = [...new Set(probe?.models || [])].filter(
    (name) => !models.some(([, model]) => model.model === name),
  );
  const recommended = presets.find(
    (item) =>
      item.base_url === profile?.base_url &&
      item.protocol === profile?.protocol,
  );
  const recommendations = (
    recommended?.models || (recommended ? [recommended.model] : [])
  ).filter((name) => !models.some(([, model]) => model.model === name));
  return (
    <div className="provider-editor">
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <fieldset disabled={busy}>
          <div className="section-title">
            <h2>{profile?.name || "新增 Provider"}</h2>
            {profile && (
              <button
                type="button"
                aria-label="删除 Provider"
                onClick={removeProvider}
              >
                <Trash2 size={16} />
              </button>
            )}
          </div>
          {!profile && (
            <Field label="预设">
              <Choice
                label="Provider 预设"
                value={presetID || "custom"}
                options={[
                  ["custom", "自定义"],
                  ...presets.map(
                    (item) => [item.id, item.name] as [string, string],
                  ),
                ]}
                onChange={(id) => {
                  setPresetID(id);
                  const preset = presets.find((item) => item.id === id);
                  if (preset)
                    change({
                      ...form,
                      name: preset.name,
                      base_url: preset.base_url,
                      protocol: preset.protocol,
                    });
                }}
              />
            </Field>
          )}
          {profile && (
            <div className="setting-line">
              <span>启用此 Provider</span>
              <Toggle
                label="启用此 Provider"
                checked={Boolean(env?.providers?.[profile.id])}
                onChange={(enabled) =>
                  void perform(async () => {
                    await api("providers", { id: profile.id, enabled }, "PUT");
                    await loadEnvironment();
                  })
                }
              />
            </div>
          )}
          <div className="form-grid">
            <Field label="名称">
              <input
                required
                maxLength={64}
                value={form.name}
                onChange={(event) => field("name", event.target.value)}
              />
            </Field>
            <Field label="协议">
              <Choice
                label="Provider 协议"
                disabled={Boolean(profile && models.length)}
                value={form.protocol}
                options={[
                  ["openai-compatible", "OpenAI 兼容"],
                  ["anthropic", "Anthropic Messages"],
                ]}
                onChange={(value) => field("protocol", value)}
              />
            </Field>
            <Field label="服务地址">
              <input
                required
                type="url"
                value={form.base_url}
                onChange={(event) => field("base_url", event.target.value)}
                placeholder="https://api.example.com/v1"
              />
            </Field>
            {profile?.source === "env" ? (
              <Field label="Key 来源">
                <span>环境变量：{profile.env_name}</span>
              </Field>
            ) : (
              <Field
                label="API Key"
                hint={
                  profile?.hint
                    ? `已保存 ****${profile.hint}，留空保持不变`
                    : "秘密草稿只保留在当前页面内存中"
                }
              >
                <input
                  type="password"
                  autoComplete="new-password"
                  value={form.key}
                  onChange={(event) => field("key", event.target.value)}
                />
              </Field>
            )}
          </div>
          {profile?.source !== "env" && (
            <label className="check-line">
              <input
                type="checkbox"
                checked={form.source === "none"}
                onChange={(event) =>
                  field("source", event.target.checked ? "none" : "saved")
                }
              />
              无需 Key（仅回环服务）
            </label>
          )}
          <Field label="备注">
            <textarea
              rows={2}
              maxLength={200}
              value={form.note}
              onChange={(event) => field("note", event.target.value)}
            />
          </Field>
          {!profile && (
            <label className="check-line">
              <input
                type="checkbox"
                checked={form.enable}
                onChange={(event) => field("enable", event.target.checked)}
              />
              保存后启用 Provider
            </label>
          )}
          <div className="provider-actions">
            <Button variant="outline" type="button" onClick={test}>
              <Zap size={15} />
              测试连接
            </Button>
            <Button type="submit">{busy ? "处理中…" : "保存配置"}</Button>
          </div>
        </fieldset>
      </form>
      <Result error={error} text={result} />
      <section className="settings-section">
        <div className="section-title">
          <h2>模型</h2>
          {profile && (
            <button
              aria-label="重新获取模型"
              disabled={busy}
              onClick={() =>
                void perform(async () => {
                  const value = await api<Probe>(
                    `keys/${encodeURIComponent(profile.id)}/models`,
                    {},
                  );
                  probes.set(profile.id, value);
                  setProbe(value);
                  setResult(`获取到 ${value.models?.length || 0} 个模型。`);
                })
              }
            >
              <RefreshCw size={15} />
            </button>
          )}
        </div>
        {probe && (
          <Field label={`返回的模型（${probe.models?.length || 0}）`}>
            <input
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="筛选具体型号"
            />
          </Field>
        )}
        <ul className="settings-model-list">
          {models.map(([id, model]) => (
            <li key={id}>
              <div>
                <strong>{model.model || id}</strong>
                <small>
                  {id}
                  {env?.default_model === id ? " · 默认模型" : ""}
                </small>
              </div>
              <Button
                variant="ghost"
                disabled={busy || env?.default_model === id}
                onClick={() =>
                  void perform(async () => {
                    await api("models/default", { id });
                    await loadEnvironment();
                  })
                }
              >
                设为默认
              </Button>
              <ModelOptions id={id} model={model} onChange={loadEnvironment} />
              <button
                aria-label={`删除模型 ${model.model || id}`}
                disabled={busy}
                onClick={() => removeModel(id)}
              >
                <Trash2 size={15} />
              </button>
            </li>
          ))}
          {(probe?.models || [])
            .filter(
              (name) =>
                !models.some(([, model]) => model.model === name) &&
                (!query || name.toLowerCase().includes(query.toLowerCase())),
            )
            .map((name) => (
              <li key={name}>
                <strong>{name}</strong>
                <Button
                  variant="ghost"
                  disabled={!profile || busy}
                  onClick={() => add(name)}
                >
                  添加
                </Button>
              </li>
            ))}
        </ul>
        {!profile && (
          <p className="form-hint">
            先保存 Provider，再添加可用模型。测试不会清空当前表单。
          </p>
        )}
        {recommendations.length > 0 && (
          <div className="inline-form">
            {recommendations.map((name) => (
              <Button
                key={name}
                variant="outline"
                disabled={!profile || busy}
                onClick={() => add(name)}
              >
                添加预设 · {name}
              </Button>
            ))}
          </div>
        )}
        <div className="inline-form">
          <Field label="手动填写模型">
            <input
              value={manual}
              onChange={(event) => setManual(event.target.value)}
              maxLength={256}
              placeholder={
                presets.find((item) => item.id === presetID)?.model ||
                "服务商模型名称"
              }
            />
          </Field>
          <Button
            variant="outline"
            disabled={!profile || busy || !manual.trim()}
            onClick={() => add(manual)}
          >
            添加
          </Button>
        </div>
        {available.length > 0 && (
          <Button
            variant="outline"
            disabled={!profile || busy}
            onClick={() =>
              void perform(async () => {
                await api("models/enable-batch", {
                  key: profile!.id,
                  models: available,
                });
                await loadEnvironment();
                setResult("可用模型已添加。");
              })
            }
          >
            自动填充 {available.length} 个模型
          </Button>
        )}
      </section>
    </div>
  );
}

function ModelOptions({
  id,
  model,
  onChange,
}: {
  id: string;
  model: Model;
  onChange: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [tokens, setTokens] = useState(
    Number(model.capabilities?.context_window_tokens) || 0,
  );
  const initial = model.capabilities || {};
  const [caps, change] = useFormDraft<
    Record<string, unknown> & {
      vision: {
        enabled: boolean;
        png: boolean;
        jpeg: boolean;
        max_image_encoded_bytes: number;
        max_side: number;
        max_images: number;
        max_request_bytes: number;
      };
    }
  >("model-caps:" + id, {
    ...initial,
    vision: {
      enabled: false,
      png: true,
      jpeg: true,
      max_image_encoded_bytes: 8 * 1024 ** 2,
      max_side: 4096,
      max_images: 8,
      max_request_bytes: 32 * 1024 ** 2,
      ...((initial.vision as Record<string, unknown>) || {}),
    },
  });
  const vision = caps.vision;
  const reasoning = (caps.reasoning || {}) as ReasoningCapabilities;
  const argumentsForSave = {
    id,
    provider: model.provider,
    key: model.key || "",
    model: model.model || "",
    capabilities: { ...caps, vision: vision.enabled ? vision : {} },
  };
  return (
    <details
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
      className="model-options"
    >
      <summary>能力</summary>
      <Field label="上下文窗口（token，0 为自动）">
        <input
          type="number"
          min={0}
          step={1}
          value={tokens}
          onChange={(event) => setTokens(Number(event.target.value))}
        />
      </Field>
      <Button
        variant="outline"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError("");
          try {
            await api("models/context", { id, tokens });
            await onChange();
            notify("模型窗口已保存");
          } catch (error) {
            setError(error instanceof Error ? error.message : "保存失败");
          } finally {
            setBusy(false);
          }
        }}
      >
        保存窗口
      </Button>
      <p className="form-hint">
        下面是显式能力声明，不代表真实 Provider 已验收。
      </p>
      <div className="form-grid">
        {(
          [
            ["protocol", "请求协议", ["chat", "responses"]],
            ["streaming", "流式输出", ["none", "supported"]],
            ["tools", "工具调用", ["none", "supported"]],
            ["web_search", "搜索工具", ["none", "supported"]],
            ["parallel_tools", "并行工具声明", ["none", "supported"]],
            ["embeddings", "向量接口声明（无自动检索）", ["none", "supported"]],
            ["usage", "用量返回", ["optional", "required"]],
            ["finish_reason", "结束原因", ["standard"]],
            ["error_policy", "错误契约", ["manual"]],
            [
              "token_limit",
              "输出 token 字段",
              ["max_tokens", "max_completion_tokens"],
            ],
            ["strength", "路由强度", ["light", "standard", "strong"]],
          ] as const
        ).map(([field, label, values]) => (
          <Field key={field} label={label}>
            <Choice
              label={label}
              value={String(caps[field] || "__default")}
              options={[
                ["__default", "使用默认声明"],
                ...values.map((value) => [value, value] as [string, string]),
              ]}
              onChange={(value) =>
                change({ ...caps, [field]: value === "__default" ? "" : value })
              }
            />
          </Field>
        ))}
        {(
          [
            ["context_bytes", "模型上下文字节上限"],
            ["output_tokens", "模型输出 token 上限"],
          ] as const
        ).map(([field, label]) => (
          <Field key={field} label={label}>
            <input
              type="number"
              min={0}
              value={Number(caps[field]) || 0}
              onChange={(event) =>
                change({ ...caps, [field]: Number(event.target.value) })
              }
            />
          </Field>
        ))}
      </div>
      <label className="check-line">
        <input
          type="checkbox"
          checked={Boolean(vision.enabled)}
          onChange={(event) =>
            change({
              ...caps,
              vision: { ...vision, enabled: event.target.checked },
            })
          }
        />
        启用图片输入声明
      </label>
      <fieldset className="form-grid">
        <legend>思考控制（按具体模型声明）</legend>
        <Field label="思考协议">
          <Choice
            label="思考协议"
            value={reasoning.control || "none"}
            options={[
              ["none", "未声明 / 不发送参数"],
              ["deepseek", "DeepSeek 思考协议"],
              ["openai", "OpenAI 思考等级"],
              ["anthropic", "Anthropic 自适应思考"],
            ]}
            onChange={(control) =>
              change({
                ...caps,
                reasoning: control === "none" ? {} : { control },
              })
            }
          />
        </Field>
        {reasoning.control && reasoning.control !== "none" && (
          <>
            <Field label="声明的等级（逗号分隔；新值可自行声明）">
              <input
                value={reasoning.efforts || ""}
                placeholder={
                  reasoning.control === "deepseek"
                    ? "low,high,max"
                    : "none,low,medium,high"
                }
                onChange={(event) =>
                  change({
                    ...caps,
                    reasoning: { ...reasoning, efforts: event.target.value },
                  })
                }
              />
            </Field>
            <ReasoningChoice
              caps={reasoning}
              info={model.reasoning_info}
              protocol={String(caps.protocol || "chat")}
              value={reasoningValue(reasoning.default)}
              inherit={false}
              label="主回答默认思考"
              onChange={(value) =>
                change({
                  ...caps,
                  reasoning: {
                    ...reasoning,
                    default: reasoningSelection(value),
                  },
                })
              }
            />
            <ReasoningChoice
              caps={reasoning}
              info={model.reasoning_info}
              protocol={String(caps.protocol || "chat")}
              value={reasoningValue(reasoning.auxiliary)}
              inherit={false}
              label="压缩 / 扩展分析思考"
              onChange={(value) =>
                change({
                  ...caps,
                  reasoning: {
                    ...reasoning,
                    auxiliary: reasoningSelection(value),
                  },
                })
              }
            />
            <p className="form-hint">
              辅助设置用于上下文压缩和已开启的扩展分析；主回答独立。服务商默认表示不发送思考参数，不代表关闭。摘要字节上限不等于模型输出额度。这里只记录请求档位，服务端未返回时不视为已验证。
            </p>
            <p className="form-hint">
              {model.reasoning_info ? (
                <>
                  官方能力核对于 {model.reasoning_info.checked_at}；
                  <a
                    href={model.reasoning_info.source}
                    target="_blank"
                    rel="noreferrer"
                  >
                    查看来源
                  </a>
                  。未覆盖的声明标为“未经官方核对”，不会据此自动探测或扩大输出额度。
                </>
              ) : (
                "此型号未在官方核对清单中；声明的等级均未经官方核对。"
              )}
            </p>
          </>
        )}
      </fieldset>
      {vision.enabled && (
        <>
          {(
            [
              ["png", "PNG"],
              ["jpeg", "JPEG"],
            ] as const
          ).map(([field, label]) => (
            <label className="check-line" key={field}>
              <input
                type="checkbox"
                checked={Boolean(vision[field])}
                onChange={(event) =>
                  change({
                    ...caps,
                    vision: { ...vision, [field]: event.target.checked },
                  })
                }
              />
              {label}
            </label>
          ))}
          <div className="form-grid">
            {(
              [
                ["max_image_encoded_bytes", "单图 Base64 最多字节"],
                ["max_side", "最长边像素"],
                ["max_images", "单请求最多图片"],
                ["max_request_bytes", "整个请求最多字节"],
              ] as const
            ).map(([field, label]) => (
              <Field key={field} label={label}>
                <input
                  type="number"
                  min={1}
                  step={1}
                  value={Number(vision[field]) || 1}
                  onChange={(event) =>
                    change({
                      ...caps,
                      vision: {
                        ...vision,
                        [field]: Number(event.target.value),
                      },
                    })
                  }
                />
              </Field>
            ))}
          </div>
        </>
      )}
      <ControlPreview
        kind="model.set_profile"
        arguments={argumentsForSave}
        onSaved={async () => {
          clearFormDraft("model-caps:" + id);
          await onChange();
          notify("模型能力声明已保存");
        }}
        label="预览能力声明"
      />
      <Result error={error} />
    </details>
  );
}
