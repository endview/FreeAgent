import { useState } from "react";
import DeliverySettings from "./DeliverySettings";
import { api } from "@/lib/api/client";
import { useRuntime } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { ControlPreview } from "@/components/ControlPreview";
import { Field, Loading, Result, useFormDraft, useResource } from "./shared";

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
  parameters?: Record<string, unknown>;
  interactions?: { enabled: boolean; strength: number };
  evolution?: { enabled: boolean; frozen: boolean; baseline_version: number };
};
type Role = {
  effective_parameters?: Record<string, unknown>;
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
  versions?: { operation_id: string; persona: Persona }[];
  evidence?: {
    id: string;
    summary: string;
    source_id: string;
    feedback_input_id: string;
  }[];
  analyses?: {
    input: { id: string; feedback_source?: string };
    analysis: string;
    detail?: string;
    draft?: Record<string, unknown>;
    feedback_draft?: Record<string, unknown>;
  }[];
  inputs?: { id: string; run_id: string; occurred_at: string }[];
  core_id: string;
  roles: Role[];
  state?: State;
  memories?: { id: string; summary: string; evidence_level: string }[];
  facts?: { id: string; kind: string; body: unknown }[];
};
type PageProps = {
  id: string;
  enabled: boolean;
  storageReady: boolean;
  settings: Record<string, unknown>;
  onSaved: () => Promise<void>;
};
type Scope = { space_id: string; actor_id: string; resource?: string };
export default function AffectWeavePage(props: PageProps) {
  const { selected } = useRuntime();
  const scope = useResource<Scope>(
    `extensions/${encodeURIComponent(props.id)}/scope?conversation_id=${encodeURIComponent(selected)}`,
  );
  return scope.value ? (
    <AffectWeaveEditor
      key={props.id + ":" + selected}
      {...props}
      chat={selected}
      initialScope={scope.value}
    />
  ) : (
    <Loading error={scope.error} />
  );
}
function AffectWeaveEditor({
  id,
  enabled,
  storageReady,
  settings,
  onSaved,
  chat,
  initialScope,
}: PageProps & { chat: string; initialScope: Scope }) {
  const [evidence, setEvidence] = useState<string[]>([]);
  const [scopeDraft, setScopeDraft] = useFormDraft(
    "affectweave:scope:" + chat,
    {
      space: initialScope.space_id,
      roleID: initialScope.resource || "",
      actor: initialScope.actor_id,
    },
  );
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
  const [form, setForm] = useFormDraft("affectweave:character:" + chat, {
    id: "",
    name: "",
    identity: "",
    boundaries: "",
    preferences: "",
    voice: "",
    spaces: initialScope.space_id,
    values: "{}",
    traits: "{}",
    parameters: "null",
    interactions: false,
    interactionStrength: "1",
  });
  const [draft, setDraft] = useFormDraft("affectweave:proposal", {
    action: "apply_proposal",
    source: "",
    payload: "{}",
  });
  const [preferences, setPreferences] = useFormDraft(
    "affectweave:preferences",
    {
      contextBytes: String(settings.context_bytes ?? 6000),
      backup: "",
      analysis: Boolean(settings.analysis_enabled),
      analysisDeadline: String(settings.analysis_deadline_ms || 8000),
      expression: Boolean(settings.expression_enabled),
      followup: Boolean(settings.fixed_followup_enabled),
    },
  );
  const [syncCursor, setSyncCursor] = useState("");
  const prefix = `extensions/${encodeURIComponent(id)}`;
  const resource = useResource<Read>(
    `${prefix}/state?space_id=${encodeURIComponent(space)}&resource=${encodeURIComponent(roleID)}&actor_id=${encodeURIComponent(actor)}`,
    enabled,
  );
  const roles = resource.value?.roles || [];
  const selected = roles.find((r) => r.id === roleID);
  const state = resource.value?.state;
  const scope = {
    space_id: space,
    resource: roleID || form.id,
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
      parameters: JSON.stringify(
        r.effective_parameters || r.persona.parameters || null,
      ),
      interactions: Boolean(r.persona.interactions?.enabled),
      interactionStrength: String(r.persona.interactions?.strength ?? 1),
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
    const parameters = JSON.parse(form.parameters);
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
        ...(parameters ? { parameters } : {}),
        interactions: {
          enabled: form.interactions,
          strength: Number(form.interactionStrength),
        },
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
        {chat
          ? "默认使用当前对话的空间、参与者和人物选择。"
          : "当前未选择对话，正在管理本机默认空间；TG 人物请先打开对应会话。"}
      </p>
      <p className="form-hint">
        按空间与参与者隔离。启用后只登记运行事实与固定规则；模型提出的长期改动须在下方预览确认。生成、实际发送与用户反馈分别记录。
      </p>
      <fieldset disabled={busy}>
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
              }>(`${prefix}/sync`, { resource: "", cursor: syncCursor });
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
        <label className="check-line">
          <input
            type="checkbox"
            checked={preferences.analysis}
            disabled={!enabled}
            onChange={(e) =>
              setPreferences({ ...preferences, analysis: e.target.checked })
            }
          />
          自动输入分析
        </label>
        <p className="form-hint">
          默认关闭。开启后每条输入最多增加一次模型请求；临时情绪不会自动保存为长期状态。
        </p>
        <Field label="单次分析期限（毫秒）">
          <input
            type="number"
            min={2000}
            max={30000}
            value={preferences.analysisDeadline}
            onChange={(e) =>
              setPreferences({
                ...preferences,
                analysisDeadline: e.target.value,
              })
            }
          />
        </Field>
        <label className="check-line">
          <input
            type="checkbox"
            checked={preferences.expression}
            onChange={(e) =>
              setPreferences({ ...preferences, expression: e.target.checked })
            }
          />
          允许人物从已授权资源建议贴纸和表情（需要自动分析）
        </label>
        <label className="check-line">
          <input
            type="checkbox"
            checked={preferences.followup}
            onChange={(e) =>
              setPreferences({ ...preferences, followup: e.target.checked })
            }
          />
          允许提出明确事项的跟进候选（仍需单独的目标规则授权）
        </label>
        <ControlPreview
          kind="extensions.set_policy"
          arguments={{
            id,
            settings: {
              ...settings,
              analysis_enabled: preferences.analysis,
              analysis_deadline_ms: Number(preferences.analysisDeadline),
              expression_enabled: preferences.expression,
              fixed_followup_enabled: preferences.followup,
            },
          }}
          onSaved={onSaved}
          label="预览分析开关修改"
        />
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
              ...settings,
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
          <Field
            label="人物计算参数（JSON；null 使用中性值）"
            hint="sensitivity、recovery_speed、expression、regulation、directness、disclosure、repair_preference，均为 0–1。工程表达参数，不代表心理测量。"
          >
            <textarea
              rows={5}
              value={form.parameters}
              onChange={(e) => setForm({ ...form, parameters: e.target.value })}
            />
          </Field>
          <label className="check-line">
            <input
              type="checkbox"
              checked={form.interactions}
              onChange={(e) =>
                setForm({ ...form, interactions: e.target.checked })
              }
            />
            启用有界情绪交互（下一条输入生效）
          </label>
          <Field label="情绪交互强度">
            <input
              type="number"
              min={0}
              max={1}
              step={0.05}
              value={form.interactionStrength}
              onChange={(e) =>
                setForm({ ...form, interactionStrength: e.target.value })
              }
            />
          </Field>
          <ControlPreview
            kind="extensions.change_state"
            disabled={
              !enabled || !persona || !form.id || !form.name || !form.identity
            }
            arguments={{
              id,
              scope: { ...scope, resource: form.id },
              action: selected ? "edit_role" : "create_role",
              expected_revision: selected?.revision || 0,
              payload: { persona, spaces: lines(form.spaces) },
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
            <h4>本轮临时分析与待审阅草稿</h4>
            {(resource.value?.analyses || []).map((a) => (
              <details key={a.input.id}>
                <summary>
                  {a.analysis} · {a.input.id}
                </summary>
                <p>{a.detail}</p>
                {a.draft && (
                  <>
                    <pre className="settings-output">
                      {JSON.stringify(a.draft, null, 2)}
                    </pre>
                    <ControlPreview
                      kind="extensions.change_state"
                      arguments={{
                        id,
                        scope,
                        action: "apply_proposal",
                        expected_revision: state.revision,
                        payload: { source_id: a.input.id, proposal: a.draft },
                      }}
                      onSaved={reload}
                      label="审阅这条具体变化"
                    />
                  </>
                )}
                {a.feedback_draft && (
                  <>
                    <pre className="settings-output">
                      {JSON.stringify(a.feedback_draft, null, 2)}
                    </pre>
                    <ControlPreview
                      kind="extensions.change_state"
                      arguments={{
                        id,
                        scope,
                        action: "record_outcome",
                        expected_revision: state.revision,
                        payload: {
                          source_id: a.input.feedback_source,
                          outcome: a.feedback_draft,
                        },
                      }}
                      onSaved={reload}
                      label="审阅有明确关联的反馈"
                    />
                  </>
                )}
              </details>
            ))}
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
                payload: {
                  source_id: draft.source,
                  [draft.action === "record_outcome" ? "outcome" : "proposal"]:
                    proposal,
                },
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
                    payload: { memory_id: m.id },
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
          <details className="settings-section">
            <summary>人格演进、证据与版本</summary>
            <p>
              默认关闭。只有明确选取的已确认结果可支持参数提案；单次最多
              0.05，累计偏离基线最多 0.20。身份与边界不参与自动适应。
            </p>
            <ControlPreview
              kind="extensions.change_state"
              arguments={{
                id,
                scope,
                action: "set_evolution",
                expected_revision: selected.revision,
                payload: { flag: !selected.persona.evolution?.enabled },
              }}
              onSaved={reload}
              label={
                selected.persona.evolution?.enabled
                  ? "预览关闭演进提案"
                  : "预览允许演进提案"
              }
            />
            <ControlPreview
              kind="extensions.change_state"
              arguments={{
                id,
                scope,
                action: "freeze_persona",
                expected_revision: selected.revision,
                payload: { flag: !selected.persona.evolution?.frozen },
              }}
              onSaved={reload}
              label={
                selected.persona.evolution?.frozen
                  ? "预览解除冻结"
                  : "预览冻结人格演进"
              }
            />
            <Field label="选取已确认的独立结果">
              <select
                multiple
                value={evidence}
                onChange={(e) =>
                  setEvidence([...e.target.selectedOptions].map((x) => x.value))
                }
              >
                {(resource.value?.evidence || []).map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.summary} · {e.id}
                  </option>
                ))}
              </select>
            </Field>
            <Button
              variant="outline"
              disabled={
                evidence.length < 2 ||
                !selected.persona.evolution?.enabled ||
                Boolean(selected.persona.evolution?.frozen)
              }
              onClick={() =>
                void perform(async () => {
                  const chat = await api<{ id: string }>("chats", {
                    title: "AffectWeave 人格评估",
                    space_id: "local",
                  });
                  await api(`chats/${chat.id}/submit`, {
                    request_key: crypto.randomUUID(),
                    prompt: `请评估 AffectWeave 人物 ${roleID} 的数值参数变化。只使用我明确选择的已确认结果操作 ID：${evidence.join(", ")}。原结果作用域 ${JSON.stringify(scope)}。当前角色版本 ${selected.revision}，基线版本 ${selected.persona.evolution?.baseline_version}。先通过扩展状态核对证据；两个独立来源不足则说明不足。仅提出 adapt_persona 的具体控制提案，单次变化≤0.05、累计偏离基线≤0.20，不修改身份或边界，不直接生效。`,
                    permission: "none",
                    capabilities: {
                      extensions: [id],
                      extension_variants: { [id]: roleID },
                    },
                  });
                  history.pushState(
                    {},
                    "",
                    `?chat=${encodeURIComponent(chat.id)}`,
                  );
                  window.dispatchEvent(new PopStateEvent("popstate"));
                })
              }
            >
              评估人格变化（新本地对话，有模型用量）
            </Button>
            {(resource.value?.versions || []).map((v) => (
              <details key={v.operation_id}>
                <summary>人物版本 {v.persona.version}</summary>
                <pre className="settings-output">
                  {JSON.stringify(v.persona.evolution, null, 2)}
                </pre>
                <ControlPreview
                  kind="extensions.change_state"
                  arguments={{
                    id,
                    scope,
                    action: "rollback_persona",
                    expected_revision: selected.revision,
                    payload: { to_version: v.persona.version },
                  }}
                  onSaved={reload}
                  label="预览复制此版本的参数，创建新版本"
                />
              </details>
            ))}
          </details>
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
      {enabled && (
        <details className="settings-section">
          <summary>主动对话（默认关闭）</summary>
          <DeliverySettings extension={id} />
        </details>
      )}
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
          disabled={busy || !enabled || !preferences.backup}
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
