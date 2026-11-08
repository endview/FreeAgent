import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { report } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { ControlPreview } from "@/components/ControlPreview";
import { Field, useFormDraft } from "@/features/settings/shared";
interface Group {
  id: string;
  run_id: string;
  digest: string;
  status: string;
  created_at: string;
  cursor: string;
  after_ms?: number;
  expires_ms?: number;
  suppressed_reason?: string;
  parts: {
    id: string;
    sequence: number;
    kind: string;
    state: string;
    message_id?: string;
  }[];
}
export function DeliveryPanel({
  chat,
  run,
  revision,
}: {
  chat: string;
  run: string;
  revision: string;
}) {
  const [groups, setGroups] = useState<Group[]>([]),
    [busy, setBusy] = useState(false),
    [more, setMore] = useState(false);
  async function load() {
    const page = await api<Group[]>(
      `attach/deliveries?conversation_id=${encodeURIComponent(chat)}`,
    );
    setGroups(page);
    setMore(page.length === 50);
  }
  async function older() {
    if (!groups.length) return;
    setBusy(true);
    try {
      const page = await api<Group[]>(
        `attach/deliveries?conversation_id=${encodeURIComponent(chat)}&cursor=${encodeURIComponent(groups[groups.length - 1].cursor)}`,
      );
      setGroups((current) => [
        ...current,
        ...page.filter((group) => !current.some((old) => old.id === group.id)),
      ]);
      setMore(page.length === 50);
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    let disposed = false;
    setGroups([]);
    if (chat)
      api<Group[]>(
        `attach/deliveries?conversation_id=${encodeURIComponent(chat)}`,
      )
        .then((value) => {
          if (!disposed) {
            setGroups(value);
            setMore(value.length === 50);
          }
        })
        .catch(report);
    return () => {
      disposed = true;
    };
  }, [chat, revision]);
  async function control(group: Group, action: string) {
    setBusy(true);
    try {
      await api("attach/deliveries", {
        conversation_id: chat,
        command: { group: group.id, digest: group.digest, action },
      });
      await load();
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  if (!chat) return null;
  return (
    <details className="run-evidence">
      <summary>渠道投递记录</summary>
      {run && <ScheduleDelivery chat={chat} run={run} onSaved={load} />}
      <Button
        variant="outline"
        disabled={busy}
        onClick={() => void load().catch(report)}
      >
        刷新投递
      </Button>
      {groups.length === 0 ? (
        <p>本对话尚无渠道投递记录。</p>
      ) : (
        groups.map((group) => (
          <div key={group.id}>
            <p>
              {new Date(group.created_at).toLocaleString()} · 状态：
              {group.status === "preparing"
                ? "回答已完成，交付准备中"
                : group.status === "preparation_failed"
                  ? "交付准备失败，尚未发送"
                  : group.status}{" "}
              · {group.parts.length} 项
            </p>
            {group.after_ms && (
              <p>
                最早发送：{new Date(group.after_ms).toLocaleString()} · 到期：
                {group.expires_ms
                  ? new Date(group.expires_ms).toLocaleString()
                  : "未设置"}
              </p>
            )}
            {group.suppressed_reason && (
              <p>未发送原因：{group.suppressed_reason}</p>
            )}
            <ol>
              {group.parts.map((part) => (
                <li key={part.id}>
                  {part.sequence}. {part.kind} · {part.state}
                  {part.message_id ? " · 平台已返回消息引用" : ""}
                </li>
              ))}
            </ol>
            {group.parts.some((part) =>
              ["paused", "pending", "held", "scheduled", "reviewing"].includes(
                part.state,
              ),
            ) && (
              <>
                <p>续送会跳过失败或 UNKNOWN 项；只处理从未派发的尾段。</p>
                <Button
                  disabled={
                    busy ||
                    !group.parts.some((part) =>
                      ["paused", "pending"].includes(part.state),
                    )
                  }
                  variant="outline"
                  onClick={() => void control(group, "resume")}
                >
                  确认，仅续送未派发部分
                </Button>
                <Button
                  disabled={busy}
                  variant="outline"
                  onClick={() => void control(group, "abandon")}
                >
                  放弃未派发部分
                </Button>
              </>
            )}
          </div>
        ))
      )}
      {more && (
        <Button disabled={busy} variant="outline" onClick={() => void older()}>
          加载更早的投递
        </Button>
      )}
    </details>
  );
}

function ScheduleDelivery({
  chat,
  run,
  onSaved,
}: {
  chat: string;
  run: string;
  onSaved: () => Promise<void>;
}) {
  const [form, change] = useFormDraft("delivery-schedule:" + chat, {
    text: "",
    after: "",
    expires: "",
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
  });
  const [key] = useState(() => crypto.randomUUID());
  const after = Date.parse(form.after),
    expires = Date.parse(form.expires);
  return (
    <details>
      <summary>创建一次性消息</summary>
      <p>
        按本设备时间选择日期，并核对时区。仅向当前对话发送确认的固定文本；不会到时运行命令或重新调用模型。
      </p>
      <Field label="正文">
        <textarea
          value={form.text}
          rows={3}
          onChange={(event) => change({ ...form, text: event.target.value })}
        />
      </Field>
      <Field label="本设备发送时间">
        <input
          type="datetime-local"
          value={form.after}
          onChange={(event) => change({ ...form, after: event.target.value })}
        />
      </Field>
      <Field label="到期时间">
        <input
          type="datetime-local"
          value={form.expires}
          onChange={(event) => change({ ...form, expires: event.target.value })}
        />
      </Field>
      <p>时区：{form.timezone}</p>
      <ControlPreview
        kind="delivery.schedule"
        disabled={
          !form.text || !Number.isFinite(after) || !Number.isFinite(expires)
        }
        arguments={{
          run_id: run,
          candidate: {
            key,
            text: form.text,
            after: Number.isFinite(after) ? new Date(after).toISOString() : "",
            expires: Number.isFinite(expires)
              ? new Date(expires).toISOString()
              : "",
            timezone: form.timezone,
          },
        }}
        onSaved={onSaved}
        label="预览并确认一次性投递"
      />
    </details>
  );
}
