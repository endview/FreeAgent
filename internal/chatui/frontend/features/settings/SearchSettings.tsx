import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { api } from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { clearFormDraft } from "@/lib/drafts";
import { loadEnvironment } from "@/lib/store/runtime";
import {
  DraftStatus,
  Field,
  Loading,
  Result,
  useFormDraft,
  useResource,
} from "./shared";

interface Connection {
  provider: string;
  key: string;
}
interface Policy extends Record<string, unknown> {
  _expected_revisions?: Record<string, number>;
  mode: string;
  default_connection?: string;
  connections?: Record<string, Connection>;
  timeout_seconds: number;
  max_results: number;
  max_output_bytes: number;
}
interface Data {
  config: Policy;
  legacy: boolean;
  providers: { id: string; name: string; base_url: string }[];
  network_enabled: boolean;
  expected_revisions: Record<string, number>;
}
interface Key {
  id: string;
  name: string;
  protocol: string;
  base_url: string;
  needs_reentry?: boolean;
}

export default function SearchSettings() {
  const resource = useResource<Data>("settings/web-search");
  const keys = useResource<{ profiles: Key[] }>("keys");
  if (!resource.value || !keys.value)
    return <Loading error={resource.error || keys.error} />;
  return (
    <Editor
      initial={resource.value}
      profiles={keys.value.profiles}
      onSaved={resource.reload}
      reloadKeys={keys.reload}
    />
  );
}

function Editor({
  initial,
  profiles,
  onSaved,
  reloadKeys,
}: {
  initial: Data;
  profiles: Key[];
  onSaved: () => Promise<Data>;
  reloadKeys: () => Promise<unknown>;
}) {
  const [form, change] = useFormDraft<Policy>("web-search", {
    ...initial.config,
    _expected_revisions: initial.expected_revisions,
  });
  const [savedConfig, setSavedConfig] = useState(initial.config);
  const { _expected_revisions: revisions, ...configDraft } = form;
  const dirty = JSON.stringify(configDraft) !== JSON.stringify(savedConfig);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const [id, setID] = useState(""),
    [provider, setProvider] = useState("deepseek"),
    [keyID, setKeyID] = useState("");
  const [keyName, setKeyName] = useState(""),
    [baseURL, setBaseURL] = useState("https://api.deepseek.com"),
    [secret, setSecret] = useState("");
  const [creatingKey, setCreatingKey] = useState(false);
  const connections = form.connections || {};
  const eligible = profiles.filter(
    (key) =>
      key.protocol === "search" ||
      (provider === "deepseek" && key.protocol === "openai-compatible"),
  );
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      await action();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function save() {
    await perform(async () => {
      const saved = await api<Data>(
        "settings/web-search",
        { config: configDraft, expected_revisions: revisions },
        "PATCH",
      );
      change({
        ...saved.config,
        _expected_revisions: saved.expected_revisions,
      });
      clearFormDraft("web-search");
      setSavedConfig(saved.config);
      await onSaved();
      await loadEnvironment();
      setResult("搜索设置已保存，用于后续任务。");
    });
  }
  return (
    <>
      <p className="settings-intro">
        搜索近期信息、阅读公共网页，并在回答中显示来源。浏览器和桌面控制在扩展中单独安装和授权。
      </p>
      {initial.legacy && (
        <p className="settings-scope-hint">
          尚未设置统一搜索策略；已有模型的原生搜索声明按旧规则工作。保存后使用下方策略。
        </p>
      )}
      {!initial.network_enabled && (
        <p className="inline-error" role="status">
          内置网络工具当前关闭。请在“能力管理 →
          内置工具”开启网络工具；连接仍可先保存。
        </p>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <section className="settings-section">
          <div className="section-title">
            <h2>联网搜索</h2>
          </div>
          <Field
            label="默认搜索方式"
            hint="会话可以单独关闭；搜索关闭不影响模型 API 和渠道连接。自动模式只优先采用已适配的官方原生搜索。"
          >
            <Choice
              label="默认搜索方式"
              value={form.mode}
              options={[
                ["off", "关闭"],
                ["auto", "自动"],
                ["api", "指定搜索 API"],
                ["native", "模型原生联网"],
              ]}
              onChange={(mode) => change({ ...form, mode })}
            />
          </Field>
          <Field
            label="默认搜索连接"
            hint="原生联网不可用时，自动模式使用此连接；不会自动切换到其他服务商。"
          >
            <Choice
              label="默认搜索连接"
              value={form.default_connection || ""}
              options={[
                ["", "未选择"],
                ...Object.keys(connections).map((id): [string, string] => [
                  id,
                  id,
                ]),
              ]}
              onChange={(default_connection) =>
                change({ ...form, default_connection })
              }
            />
          </Field>
          <details>
            <summary>搜索预算</summary>
            {(
              [
                ["max_results", "每次最多结果", 1, 10],
                ["timeout_seconds", "请求超时（秒）", 1, 60],
                ["max_output_bytes", "正文输出上限（UTF-8 字节）", 1024, 65536],
              ] as const
            ).map(([field, label, min, max]) => (
              <Field key={field} label={label}>
                <input
                  type="number"
                  required
                  min={min}
                  max={max}
                  value={form[field]}
                  onChange={(e) =>
                    change({ ...form, [field]: Number(e.target.value) })
                  }
                />
              </Field>
            ))}
          </details>
        </section>
        <section className="settings-section">
          <h2>搜索连接</h2>
          {!Object.keys(connections).length && (
            <p className="settings-scope-hint">
              尚无连接。选择服务商和已保存的 Key 后添加。
            </p>
          )}
          <ul className="settings-model-list">
            {Object.entries(connections).map(([id, c]) => (
              <li key={id}>
                <div>
                  <strong>{id}</strong>
                  <small>
                    {initial.providers.find((p) => p.id === c.provider)?.name ||
                      c.provider}{" "}
                    ·{" "}
                    {profiles.find((p) => p.id === c.key)?.name || "Key 不可用"}
                    {form.default_connection === id ? " · 默认" : ""}
                  </small>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  disabled={busy || dirty}
                  onClick={() =>
                    void perform(async () => {
                      const test = await api<{ result_count: number }>(
                        "settings/web-search/test",
                        { connection: id },
                      );
                      setResult(
                        `连接正常，固定查询返回 ${test.result_count} 条结果。测试不会自动更改搜索方式。`,
                      );
                    })
                  }
                >
                  测试连接
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  aria-label={`删除搜索连接 ${id}`}
                  onClick={() => {
                    const next = { ...connections };
                    delete next[id];
                    change({
                      ...form,
                      connections: next,
                      default_connection:
                        form.default_connection === id
                          ? ""
                          : form.default_connection,
                    });
                  }}
                >
                  <Trash2 size={16} />
                </Button>
              </li>
            ))}
          </ul>
          <p className="settings-scope-hint">
            测试已保存的连接会向所选服务商发送一次固定非敏感查询，可能消耗额度；没有自动重试。修改草稿后请先保存。
          </p>
          <Field label="连接名称">
            <input
              value={id}
              maxLength={64}
              placeholder="例如：日常搜索"
              onChange={(e) => setID(e.target.value)}
            />
          </Field>
          <Field label="搜索服务">
            <Choice
              label="搜索服务"
              value={provider}
              options={initial.providers.map((p) => [p.id, p.name])}
              onChange={(next) => {
                setProvider(next);
                setKeyID("");
                setBaseURL(
                  initial.providers.find((p) => p.id === next)?.base_url || "",
                );
              }}
            />
          </Field>
          <Field
            label="Key"
            hint="仅引用秘密存储中的 Key，不把密钥写入搜索配置。DeepSeek 可复用地址匹配的模型 Key。"
          >
            <Choice
              label="搜索 Key"
              value={keyID}
              options={[
                ["", "选择已保存的 Key"],
                ...eligible.map((p): [string, string] => [
                  p.id,
                  p.name + (p.needs_reentry ? "（需要重新输入）" : ""),
                ]),
              ]}
              onChange={setKeyID}
            />
          </Field>
          <Button
            type="button"
            variant="outline"
            disabled={!id.trim() || !keyID || busy}
            onClick={() => {
              change({
                ...form,
                connections: {
                  ...connections,
                  [id.trim()]: { provider, key: keyID },
                },
                default_connection: form.default_connection || id.trim(),
              });
              setID("");
            }}
          >
            <Plus size={15} />
            添加到草稿
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => setCreatingKey(!creatingKey)}
          >
            新增搜索 Key
          </Button>
        </section>
        <div className="settings-savebar">
          <DraftStatus dirty={dirty} busy={busy} />
          <Button type="submit" disabled={busy}>
            {busy ? "正在保存…" : "保存搜索设置"}
          </Button>
        </div>
        <Result error={error} text={result} />
      </form>
      {creatingKey && (
        <form
          className="settings-section"
          onSubmit={(e) => {
            e.preventDefault();
            void perform(async () => {
              const saved = await api<Key>("keys", {
                name: keyName,
                base_url: baseURL,
                protocol: "search",
                source: secret ? "saved" : "none",
                key: secret,
              });
              setSecret("");
              setKeyID(saved.id);
              await reloadKeys();
              setCreatingKey(false);
              setResult("Key 已保存。将它添加到连接后，再保存搜索设置。");
            });
          }}
        >
          <h2>新增搜索 Key</h2>
          <Field label="Key 名称">
            <input
              required
              value={keyName}
              onChange={(e) => setKeyName(e.target.value)}
            />
          </Field>
          <Field
            label="服务地址"
            hint="凭据只发送到此地址。自托管 SearXNG 可以填写本机地址；公共网页读取仍禁止访问内网。"
          >
            <input
              required
              type="url"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
            />
          </Field>
          <Field
            label="API Key"
            hint="空值只用于明确配置的本机免密服务；不会测试或请求 /models。"
          >
            <input
              type="password"
              autoComplete="new-password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
            />
          </Field>
          <Button type="submit" disabled={busy}>
            保存 Key
          </Button>
        </form>
      )}
    </>
  );
}
