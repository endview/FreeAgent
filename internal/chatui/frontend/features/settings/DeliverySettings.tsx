import { clearFormDraft } from "@/lib/drafts";
import { useState } from "react";
import { api } from "@/lib/api/client";
import { useRuntime, loadEnvironment, notify } from "@/lib/store/runtime";
import { ControlPreview } from "@/components/ControlPreview";
import { Button } from "@/components/ui/button";
import { Field, Loading, Result, useFormDraft, useResource } from "./shared";
interface Rule {
  generation?: boolean;
  resource?: string;
  free_topics?: boolean;
  source_refs?: string[];
  slots?: string[];
  idle_hours?: number;
  generation_deadline_seconds?: number;
  id: string;
  extension: string;
  conversation_id: string;
  enabled: boolean;
  timezone: string;
  quiet_start: string;
  quiet_end: string;
  daily_max: number;
  cooldown_hours: number;
  allow_sticker: boolean;
  cancel_on_input: boolean;
  max_delay_seconds: number;
}
interface Policy extends Record<string, unknown> {
  generation_enabled: Record<string, boolean>;
  sticker_limit: number;
  interval_ms: number;
  enabled: boolean;
  rules: Rule[];
}
function savedPolicy(value: Partial<Policy>): Policy {
  // Core omits false/zero/empty fields. A snapshot replaces the draft;
  // merging it into an edited policy would retain revoked grants.
  return {
    generation_enabled: {},
    sticker_limit: 1,
    interval_ms: 0,
    enabled: false,
    rules: [],
    ...value,
  };
}
export default function DeliverySettings({
  extension,
}: { extension?: string } = {}) {
  const catalog = useResource<{
    extensions: { id: string; name: string; scheduled_generation?: boolean }[];
  }>("extensions");
  const resource = useResource<Partial<Policy>>("settings/delivery");
  const { environment } = useRuntime();
  if (
    resource.value &&
    !environment?.delivery_available &&
    !resource.value.rules?.length
  )
    return (
      <p className="settings-intro">
        当前没有已启用的投递渠道。请先在渠道中配置连接；已有交付记录可在对应会话查看。
      </p>
    );
  return resource.value ? (
    <Editor
      key={JSON.stringify(resource.value.expected_revisions)}
      initial={resource.value}
      extensions={(catalog.value?.extensions || []).filter(
        (x) => x.scheduled_generation && (!extension || x.id === extension),
      )}
      onSaved={async () => {
        await resource.reload();
        await loadEnvironment();
        notify("投递授权已保存；每个待发项仍会重新检查授权。");
      }}
    />
  ) : (
    <Loading error={resource.error} />
  );
}
function Editor({
  initial,
  extensions,
  onSaved,
}: {
  initial: Partial<Policy>;
  extensions: { id: string; name: string }[];
  onSaved: () => Promise<void>;
}) {
  const state = useRuntime();
  const records = useResource<
    {
      id: string;
      state: string;
      reason?: string;
      text?: string;
      delivery_state?: string;
      run_id?: string;
      not_before: string;
      rule: { extension: string };
    }[]
  >(
    `attach/deliveries?scheduled=true&conversation_id=${encodeURIComponent(state.selected)}`,
  );
  const [policy, change] = useFormDraft<Policy>(
    "delivery-policy",
    savedPolicy(initial),
  );
  const { expected_revisions, ...argumentsPolicy } = policy;
  const [selected, setSelected] = useState(0);
  const [reloadBusy, setReloadBusy] = useState(false),
    [reloadError, setReloadError] = useState("");
  function update(index: number, patch: Partial<Rule>) {
    change({
      ...policy,
      rules: policy.rules.map((rule, i) =>
        i === index ? { ...rule, ...patch } : rule,
      ),
    });
  }
  return (
    <>
      <p className="settings-intro">
        管理发送间隔、资源上限，以及扩展向指定对话提出一次性跟进的授权。主动投递默认关闭；回复表达方式由已安装的扩展提供。
      </p>
      {(
        [
          ["sticker_limit", "本轮贴纸选择上限", 1, 3],
          ["interval_ms", "各部分最短间隔（毫秒）", 0, 2000],
        ] as const
      ).map(([field, label, min, max]) => (
        <Field key={field} label={label}>
          <input
            type="number"
            min={min}
            max={max}
            value={policy[field]}
            onChange={(event) =>
              change({ ...policy, [field]: Number(event.target.value) })
            }
          />
        </Field>
      ))}
      <label>
        <input
          type="checkbox"
          checked={policy.enabled}
          onChange={(event) =>
            change({ ...policy, enabled: event.target.checked })
          }
        />
        允许已授权扩展的一次性主动跟进
      </label>
      <p>
        关闭后，未派发的扩展候选取消。用户明确确认的一次性提醒仍按其原授权处理。
      </p>
      {extensions.map((x) => (
        <label className="check-line" key={x.id}>
          <input
            type="checkbox"
            checked={Boolean(policy.generation_enabled?.[x.id])}
            onChange={(e) =>
              change({
                ...policy,
                enabled: e.target.checked ? true : policy.enabled,
                generation_enabled: {
                  ...policy.generation_enabled,
                  [x.id]: e.target.checked,
                },
              })
            }
          />
          {x.name} · 主动对话总开关（当前生效：
          {initial.generation_enabled?.[x.id] ? "开启" : "关闭"}；修改需确认）
        </label>
      ))}
      {extensions.length > 0 && (
        <p className="form-hint">
          开启后仍需启用目标规则。每目标滚动 24
          小时最多评估两次、主动开场一次；新消息优先，上次未回复不追问。关闭取消未派发内容，不撤回已发送消息。
        </p>
      )}
      <Button
        variant="outline"
        onClick={() => {
          const rule: Rule = {
            id: "followup-" + Date.now(),
            extension: "",
            conversation_id: state.selected,
            enabled: false,
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
            quiet_start: "23:00",
            quiet_end: "08:00",
            daily_max: 1,
            cooldown_hours: 6,
            allow_sticker: false,
            cancel_on_input: true,
            max_delay_seconds: 86400,
          };
          setSelected(policy.rules.length);
          change({ ...policy, rules: [...policy.rules, rule] });
        }}
      >
        新增跟进规则
      </Button>
      {policy.rules.map((rule, index) => (
        <details
          key={rule.id}
          open={selected === index}
          onToggle={(event) => {
            if (event.currentTarget.open) setSelected(index);
          }}
        >
          <summary>
            {rule.id} · {rule.enabled ? "开启" : "关闭"}
          </summary>
          <Field label="规则标识">
            <input
              value={rule.id}
              onChange={(event) => update(index, { id: event.target.value })}
            />
          </Field>
          <Field label="扩展标识">
            <input
              value={rule.extension}
              onChange={(event) =>
                update(index, { extension: event.target.value })
              }
            />
          </Field>
          <Field label="目标对话">
            <input
              value={rule.conversation_id}
              onChange={(event) =>
                update(index, { conversation_id: event.target.value })
              }
            />
            <Button
              variant="outline"
              disabled={!state.selected}
              onClick={() => update(index, { conversation_id: state.selected })}
            >
              使用当前对话
            </Button>
          </Field>
          <Field label="时区">
            <input
              value={rule.timezone}
              onChange={(event) =>
                update(index, { timezone: event.target.value })
              }
            />
          </Field>
          {extensions.some((x) => x.id === rule.extension) && (
            <label className="check-line">
              <input
                type="checkbox"
                checked={Boolean(rule.generation)}
                onChange={(e) =>
                  update(index, {
                    generation: e.target.checked,
                    cancel_on_input: true,
                    slots: ["10:00", "18:00"],
                    idle_hours: 4,
                    generation_deadline_seconds: 30,
                  })
                }
              />
              到期判断并生成（额外模型用量）
            </label>
          )}
          {rule.generation && (
            <>
              <Field label="扩展资源（例如已选择的人物标识）">
                <input
                  value={rule.resource || ""}
                  onChange={(e) => update(index, { resource: e.target.value })}
                />
              </Field>
              <label className="check-line">
                <input
                  type="checkbox"
                  checked={Boolean(rule.free_topics)}
                  onChange={(e) =>
                    update(index, { free_topics: e.target.checked })
                  }
                />
                允许扩展从明确授权的话题中自由选择
              </label>
              <Field label="明确授权的来源引用，每行一个">
                <textarea
                  value={(rule.source_refs || []).join("\n")}
                  onChange={(e) =>
                    update(index, {
                      source_refs: e.target.value
                        .split(/\r?\n/)
                        .filter(Boolean),
                    })
                  }
                />
              </Field>
              <Field label="评估时间槽，每行 HH:mm">
                <textarea
                  value={(rule.slots || ["10:00", "18:00"]).join("\n")}
                  onChange={(e) =>
                    update(index, {
                      slots: e.target.value.split(/\r?\n/).filter(Boolean),
                    })
                  }
                />
              </Field>
              <Field label="至少空闲小时">
                <input
                  type="number"
                  min={1}
                  max={168}
                  value={rule.idle_hours || 4}
                  onChange={(e) =>
                    update(index, { idle_hours: Number(e.target.value) })
                  }
                />
              </Field>
              <Field label="生成期限（秒）">
                <input
                  type="number"
                  min={5}
                  max={120}
                  value={rule.generation_deadline_seconds || 30}
                  onChange={(e) =>
                    update(index, {
                      generation_deadline_seconds: Number(e.target.value),
                    })
                  }
                />
              </Field>
              <Button
                variant="outline"
                onClick={() =>
                  void api("attach/deliveries", {
                    conversation_id: rule.conversation_id,
                    command: {
                      action: "preview",
                      rule: rule.id,
                      key: crypto.randomUUID(),
                    },
                  })
                    .then(() =>
                      notify("已提交只生成预览，结果见主动记录；不会发送。"),
                    )
                    .catch((error) => notify((error as Error).message))
                }
              >
                预览一次（有模型用量，不发送）
              </Button>
            </>
          )}
          <Field label="静默开始">
            <input
              type="time"
              value={rule.quiet_start}
              onChange={(event) =>
                update(index, { quiet_start: event.target.value })
              }
            />
          </Field>
          <Field label="静默结束">
            <input
              type="time"
              value={rule.quiet_end}
              onChange={(event) =>
                update(index, { quiet_end: event.target.value })
              }
            />
          </Field>
          {(
            [
              ["daily_max", "目标每日上限", 1, 24],
              ["cooldown_hours", "目标冷却（小时）", 1, 168],
              ["max_delay_seconds", "最长预约间隔（秒）", 1, 604800],
            ] as const
          ).map(([field, label, min, max]) => (
            <Field key={field} label={label}>
              <input
                type="number"
                min={min}
                max={max}
                value={rule[field]}
                onChange={(event) =>
                  update(index, { [field]: Number(event.target.value) })
                }
              />
            </Field>
          ))}
          {(
            [
              ["enabled", "启用规则"],
              ["allow_sticker", "允许一张已授权贴纸"],
              ["cancel_on_input", "收到新输入就取消跟进"],
            ] as const
          ).map(([field, label]) => (
            <label key={field}>
              <input
                type="checkbox"
                checked={rule[field]}
                onChange={(event) =>
                  update(index, { [field]: event.target.checked })
                }
              />
              {label}
            </label>
          ))}
          <Button
            variant="ghost"
            onClick={() =>
              change({
                ...policy,
                rules: policy.rules.filter((_, i) => i !== index),
              })
            }
          >
            删除此规则
          </Button>
        </details>
      ))}
      <details>
        <summary>主动记录与只生成预览</summary>
        <Button variant="ghost" onClick={() => void records.reload()}>
          刷新记录
        </Button>
        {(records.value || [])
          .filter((o) => extensions.some((x) => x.id === o.rule.extension))
          .map((o) => (
            <details key={o.id}>
              <summary>
                {o.state} · {o.delivery_state || o.not_before}
              </summary>
              <p>{o.reason}</p>
              {o.text && <p>{o.text}</p>}
              <p>
                Run：{o.run_id || "尚未派发"}
                ；调用、用量和耗时保留在原运行记录。
              </p>
            </details>
          ))}
      </details>
      <ControlPreview
        kind="delivery.set_policy"
        arguments={argumentsPolicy}
        expectedRevisions={expected_revisions as Record<string, number>}
        onSaved={async () => {
          clearFormDraft("delivery-policy");
          await onSaved();
        }}
      />
      <Button
        variant="ghost"
        disabled={reloadBusy}
        onClick={async () => {
          if (!window.confirm("放弃投递授权草稿，读取当前已保存的配置？"))
            return;
          setReloadBusy(true);
          setReloadError("");
          try {
            const latest = await api<Partial<Policy>>("settings/delivery");
            change(savedPolicy(latest));
            setSelected(0);
          } catch (error) {
            setReloadError((error as Error).message);
          } finally {
            setReloadBusy(false);
          }
        }}
      >
        读取最新投递授权
      </Button>
      <Result error={reloadError} />
    </>
  );
}
