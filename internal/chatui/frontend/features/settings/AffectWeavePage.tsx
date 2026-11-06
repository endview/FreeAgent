import { useState } from "react";
import { api, remote } from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { ControlPreview } from "@/components/ControlPreview";
import { Field, Result, useFormDraft, useResource } from "./shared";

type Persona = {
  id: string;
  name: string;
  identity_summary: string;
  boundaries?: string[];
  preferences?: string[];
  voice_notes?: string[];
  version: number;
  frozen: boolean;
  values?: Record<string, number>;
  stable_traits?: Record<string, number>;
};
type Role = {
  id: string;
  revision: number;
  persona: Persona;
  spaces: string[];
};
type State = {
  revision: number;
  persona: Persona;
  medium: Record<string, number>;
  relationship: Record<string, unknown>;
  emotions: Record<string, unknown>;
  threads: Record<string, unknown>;
};
type Read = {
  inputs?: { id: string; run_id: string; occurred_at: string }[];
  core_id: string;
  roles: Role[];
  state?: State;
  memories?: { id: string; summary: string; evidence_level: string }[];
  facts?: { id: string; kind: string; body: unknown }[];
};
export default function AffectWeavePage({
  id,
  enabled,
  storageReady,
  onSaved,
}: {
  id: string;
  enabled: boolean;
  storageReady: boolean;
  onSaved: () => Promise<void>;
}) {
  const [scopeDraft, setScopeDraft] = useFormDraft("affectweave:scope", {
    space: "local",
    roleID: "",
    actor: "owner",
  });
  const { space, roleID, actor } = scopeDraft;
  const setSpace = (value: string) =>
    setScopeDraft({ ...scopeDraft, space: value });
  const setRoleID = (value: string) =>
    setScopeDraft({ ...scopeDraft, roleID: value });
  const setActor = (value: string) =>
    setScopeDraft({ ...scopeDraft, actor: value });
  const [error, setError] = useState(""),
    [result, setResult] = useState(""),
    [busy, setBusy] = useState(false);
  const [form, setForm] = useFormDraft("affectweave:character", {
    id: "",
    name: "",
    identity: "",
    boundaries: "",
    preferences: "",
    voice: "",
    spaces: "local",
    values: "{}",
    traits: "{}",
  });
  const [draft, setDraft] = useFormDraft("affectweave:proposal", {
    action: "apply_proposal",
    source: "",
    payload: "{}",
  });
  const [preferences, setPreferences] = useFormDraft(
    "affectweave:preferences",
    { contextBytes: "6000", backup: "" },
  );
  const [syncCursor, setSyncCursor] = useState("");
  const prefix = `extensions/${encodeURIComponent(id)}`;
  const resource = useResource<Read>(
    `${prefix}/state?space_id=${encodeURIComponent(space)}&role_id=${encodeURIComponent(roleID)}&actor_id=${encodeURIComponent(actor)}`,
    enabled,
  );
  const roles = resource.value?.roles || [];
  const selected = roles.find((r) => r.id === roleID);
  const state = resource.value?.state;
  const scope = {
    space_id: space,
    role_id: roleID || form.id,
    actor_id: actor,
  };
  async function perform(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      await onSaved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function reload() {
    await resource.reload();
    await onSaved();
  }
  function loadRole(r: Role) {
    setRoleID(r.id);
    setForm({
      id: r.id,
      name: r.persona.name,
      identity: r.persona.identity_summary,
      boundaries: (r.persona.boundaries || []).join("\n"),
      preferences: (r.persona.preferences || []).join("\n"),
      voice: (r.persona.voice_notes || []).join("\n"),
      spaces: r.spaces.join("\n"),
      values: JSON.stringify(r.persona.values || {}),
      traits: JSON.stringify(r.persona.stable_traits || {}),
    });
  }
  const lines = (text: string) =>
    text
      .split(/\r?\n/)
      .map((v) => v.trim())
      .filter(Boolean);
  let persona: Record<string, unknown> | null = null,
    proposal: Record<string, unknown> | null = null;
  try {
    const values = JSON.parse(form.values),
      traits = JSON.parse(form.traits);
    if (values && traits && !Array.isArray(values) && !Array.isArray(traits))
      persona = {
        id: form.id,
        name: form.name,
        identity_summary: form.identity,
        boundaries: lines(form.boundaries),
        preferences: lines(form.preferences),
        voice_notes: lines(form.voice),
        values,
        stable_traits: traits,
        version: (selected?.persona.version || 0) + 1,
        frozen: true,
      };
  } catch {
    /* invalid draft remains editable */
  }
  try {
    const parsed: unknown = JSON.parse(draft.payload);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed))
      proposal = parsed as Record<string, unknown>;
  } catch {
    /* display draft validation */
  }
  return (
    <section className="settings-section" aria-label="AffectWeave 人物管理">
      <h3>人物、关系与经历</h3>
      <p className="form-hint">
        按空间与参与者隔离。启用后只登记运行事实与固定规则；模型提出的长期改动须在下方预览确认。生成、实际发送与用户反馈分别记录。
      </p>
      <fieldset disabled={busy || remote}>
        <Button
          variant="outline"
          disabled={storageReady}
          onClick={() =>
            void perform(async () => {
              await api(`${prefix}/storage-init`, {});
              setResult("存储已初始化，请启用扩展后读取人物。");
            })
          }
        >
          {storageReady
            ? "独立 PostgreSQL 存储已就绪"
            : "初始化独立 PostgreSQL 存储"}
        </Button>
        <div className="setting-grid">
          <Field label="空间 ID">
            <input value={space} onChange={(e) => setSpace(e.target.value)} />
          </Field>
          <Field label="参与者 ID">
            <input value={actor} onChange={(e) => setActor(e.target.value)} />
          </Field>
        </div>
        <Field label="人物">
          <select
            value={roleID}
            onChange={(e) => {
              const r = roles.find((r) => r.id === e.target.value);
              if (r) loadRole(r);
              else setRoleID("");
            }}
          >
            <option value="">新建人物</option>
            {roles.map((r) => (
              <option key={r.id} value={r.id}>
                {r.persona.name}
              </option>
            ))}
          </select>
        </Field>
        <Button
          variant="ghost"
          disabled={!enabled}
          onClick={() =>
            void perform(async () => {
              await resource.reload();
            })
          }
        >
          刷新状态
        </Button>
        <Button
          variant="outline"
          disabled={!enabled || !roleID}
          onClick={() =>
            void perform(async () => {
              const page = await api<{
                processed: number;
                skipped: number;
                next?: string;
              }>(`${prefix}/sync`, { role_id: roleID, cursor: syncCursor });
              setSyncCursor(page.next || "");
              setResult(
                `已补读 ${page.processed} 条原记录，跳过 ${page.skipped} 条。${page.next ? "还有记录，可继续补读。" : "本次补读完成。"}`,
              );
              await resource.reload();
            })
          }
        >
          {syncCursor ? "继续补读原记录" : "补读原始生成与交付记录"}
        </Button>
        <Field label="人物片段最大 UTF-8 字节数">
          <input
            type="number"
            min={1024}
            max={24000}
            value={preferences.contextBytes}
            onChange={(e) =>
              setPreferences({ ...preferences, contextBytes: e.target.value })
            }
          />
        </Field>
        <ControlPreview
          kind="extensions.set_policy"
          disabled={
            !enabled ||
            !roleID ||
            !Number.isInteger(Number(preferences.contextBytes))
          }
          arguments={{
            id,
            settings: {
              role_id: roleID,
              context_bytes: Number(preferences.contextBytes),
            },
          }}
          onSaved={onSaved}
          label="预览设为默认人物与片段预算"
        />
        <details open={!selected}>
          <summary>人物定义与授权范围</summary>
          <Field label="人物标识">
            <input
              value={form.id}
              onChange={(e) => {
                setRoleID("");
                setForm({ ...form, id: e.target.value });
              }}
            />
          </Field>
          <Field label="名字">
            <input
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label="身份与性格">
            <textarea
              rows={3}
              value={form.identity}
              onChange={(e) => setForm({ ...form, identity: e.target.value })}
            />
          </Field>
          {(
            [
              ["boundaries", "边界"],
              ["preferences", "稳定偏好"],
              ["voice", "说话风格"],
              ["spaces", "允许使用的空间 ID"],
            ] as const
          ).map(([key, label]) => (
            <Field key={key} label={label} hint="每行一项">
              <textarea
                rows={2}
                value={form[key]}
                onChange={(e) => setForm({ ...form, [key]: e.target.value })}
              />
            </Field>
          ))}
          <Field label="价值取向（JSON，0–1）">
            <textarea
              value={form.values}
              onChange={(e) => setForm({ ...form, values: e.target.value })}
            />
          </Field>
          <Field label="稳定特征（JSON，0–1）">
            <textarea
              value={form.traits}
              onChange={(e) => setForm({ ...form, traits: e.target.value })}
            />
          </Field>
          <ControlPreview
            kind="extensions.change_state"
            disabled={
              !enabled || !persona || !form.id || !form.name || !form.identity
            }
            arguments={{
              id,
              scope: { ...scope, role_id: form.id },
              action: selected ? "edit_role" : "create_role",
              expected_revision: selected?.revision || 0,
              persona,
              spaces: lines(form.spaces),
            }}
            onSaved={reload}
            label={selected ? "预览人物修改" : "预览新建人物"}
          />
        </details>
        {state && (
          <>
            <h4>当前状态 · 版本 {state.revision}</h4>
            <pre className="settings-output">
              {JSON.stringify(
                {
                  medium: state.medium,
                  relationship: state.relationship,
                  emotions: state.emotions,
                  threads: state.threads,
                },
                null,
                2,
              )}
            </pre>
            <div className="provider-actions">
              <ControlPreview
                kind="extensions.change_state"
                arguments={{
                  id,
                  scope,
                  action: "reset_state",
                  expected_revision: state.revision,
                }}
                onSaved={reload}
                label="预览重置状态"
              />
              <ControlPreview
                kind="extensions.change_state"
                arguments={{
                  id,
                  scope,
                  action: "delete_state",
                  expected_revision: state.revision,
                }}
                onSaved={reload}
                label="预览删除本空间人物状态"
              />
            </div>
            <h4>状态提案</h4>
            <Field label="操作">
              <select
                value={draft.action}
                onChange={(e) => setDraft({ ...draft, action: e.target.value })}
              >
                <option value="apply_proposal">关系、情绪或未决事件</option>
                <option value="record_outcome">已发生的用户反馈与经历</option>
              </select>
            </Field>
            <Field label="已存在的来源 ID">
              <input
                value={draft.source}
                onChange={(e) => setDraft({ ...draft, source: e.target.value })}
              />
            </Field>
            {resource.value?.inputs?.[0] && (
              <Button
                variant="ghost"
                onClick={() =>
                  setDraft({ ...draft, source: resource.value!.inputs![0].id })
                }
              >
                使用最近已采用输入
              </Button>
            )}
            <Field label="具体变更（JSON）">
              <textarea
                rows={6}
                value={draft.payload}
                onChange={(e) =>
                  setDraft({ ...draft, payload: e.target.value })
                }
              />
            </Field>
            <ControlPreview
              kind="extensions.change_state"
              disabled={!proposal || !draft.source}
              arguments={{
                id,
                scope,
                action: draft.action,
                expected_revision: state.revision,
                source_id: draft.source,
                [draft.action === "record_outcome" ? "outcome" : "proposal"]:
                  proposal,
              }}
              onSaved={reload}
              label="预览状态提案"
            />
            <h4>已确认经历</h4>
            {resource.value?.memories?.map((m) => (
              <div key={m.id} className="settings-section">
                <p>
                  {m.summary} <small>{m.evidence_level}</small>
                </p>
                <ControlPreview
                  kind="extensions.change_state"
                  arguments={{
                    id,
                    scope,
                    action: "delete_memory",
                    memory_id: m.id,
                    expected_revision: state.revision,
                  }}
                  onSaved={reload}
                  label="预览删除经历"
                />
              </div>
            ))}
            <details>
              <summary>原始生成与交付事实</summary>
              <pre className="settings-output">
                {JSON.stringify(
                  {
                    inputs: resource.value?.inputs || [],
                    facts: resource.value?.facts || [],
                  },
                  null,
                  2,
                )}
              </pre>
            </details>
          </>
        )}
        {selected && (
          <ControlPreview
            kind="extensions.change_state"
            arguments={{
              id,
              scope,
              action: "delete_role",
              expected_revision: selected.revision,
            }}
            onSaved={async () => {
              setRoleID("");
              await reload();
            }}
            label="预览删除人物及其所有空间状态"
          />
        )}
      </fieldset>
      <details className="settings-section">
        <summary>配套备份与恢复</summary>
        <p>
          备份短暂停止人物写回，普通任务继续。不会重跑模型或工具；未核清的修改会阻止完整备份。
        </p>
        <Field label="新备份目录的绝对路径">
          <input
            value={preferences.backup}
            onChange={(e) =>
              setPreferences({ ...preferences, backup: e.target.value })
            }
          />
        </Field>
        <Button
          disabled={busy || remote || !enabled || !preferences.backup}
          variant="outline"
          onClick={() =>
            void perform(async () => {
              await api(`${prefix}/backup`, { target: preferences.backup });
              setResult("Core 与人物的配套备份已完成。");
            })
          }
        >
          创建配套备份
        </Button>
        <p className="form-hint">
          恢复需要先停止原 Owner，再使用 CLI：freeagent ext restore --from
          备份目录 --data-dir 新数据目录 --package
          同版本扩展目录。恢复失败时保留 Core，并关闭扩展。
        </p>
      </details>
      <Result error={error || resource.error} text={result} />
      {!enabled && (
        <p className="form-hint">
          扩展尚未启用。初始化存储后，开启上层的“全局启用”，再创建人物。
        </p>
      )}
    </section>
  );
}
