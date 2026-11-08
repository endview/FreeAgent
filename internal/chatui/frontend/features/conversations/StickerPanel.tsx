import { useEffect, useRef, useState } from "react";
import { api, headers } from "@/lib/api/client";
import type { Attachment } from "@/lib/api/types";
import { notify, report } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { ControlPreview } from "@/components/ControlPreview";
import { Field } from "@/features/settings/shared";
interface Sticker {
  id: string;
  emoji?: string;
  set_name?: string;
  format: string;
}
interface Catalog {
  name?: string;
  title?: string;
  total: number;
  offset: number;
  items: Sticker[];
}
interface SavedSets {
  space_id: string;
  target: { application_id: string; chat_id: string };
  revision: number;
  sets: string[];
}
interface ReactionTarget {
  ref: string;
  message_id: string;
  kind: string;
  preview?: string;
}
export function StickerPanel({ chat }: { chat: string }) {
  const [set, changeSet] = useState(""),
    [fromSaved, setFromSaved] = useState(false),
    [savedSets, setSavedSets] = useState<SavedSets | null>(null),
    [savedDraft, setSavedDraft] = useState(""),
    [targets, setTargets] = useState<ReactionTarget[]>([]),
    [messageRef, setMessageRef] = useState(""),
    [catalog, setCatalog] = useState<Catalog | null>(null),
    [busy, setBusy] = useState(false),
    [emoji, setEmoji] = useState("👍"),
    [preview, setPreview] = useState<{ id: string; url: string } | null>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(
    () => () => {
      if (preview) URL.revokeObjectURL(preview.url);
    },
    [preview],
  );
  async function execute(action: string, extra: Record<string, unknown> = {}) {
    return api("attach/stickers", {
      conversation_id: chat,
      command: { action, ...extra },
    });
  }
  async function loadSets() {
    const result = (await execute("sets")) as unknown as SavedSets;
    if (alive.current) {
      setSavedSets(result);
      setSavedDraft(result.sets.join("\n"));
    }
  }
  async function refreshSets() {
    setBusy(true);
    try {
      await loadSets();
    } catch (error) {
      report(error);
    } finally {
      if (alive.current) setBusy(false);
    }
  }
  async function loadTargets() {
    setBusy(true);
    try {
      const result = await execute("targets");
      if (alive.current) setTargets(result as unknown as ReactionTarget[]);
    } catch (error) {
      report(error);
    } finally {
      if (alive.current) setBusy(false);
    }
  }
  async function list(offset = 0, name = set, saved = fromSaved) {
    setBusy(true);
    try {
      const result = await execute("list", { set: name, offset, saved });
      if (alive.current) {
        changeSet(name);
        setFromSaved(saved);
        setCatalog(result as unknown as Catalog);
        setPreview(null);
      }
    } catch (error) {
      report(error);
    } finally {
      if (alive.current) setBusy(false);
    }
  }
  async function show(item: Sticker) {
    setBusy(true);
    try {
      const ref = (await execute("preview", {
        resource: item.id,
      })) as unknown as Attachment;
      const response = await fetch(
        `/api/attachments/${encodeURIComponent(ref.id)}?conversation_id=${encodeURIComponent(chat)}`,
        { headers: headers(), credentials: "same-origin", cache: "no-store" },
      );
      if (!response.ok) throw new Error("预览下载失败");
      const url = URL.createObjectURL(await response.blob());
      if (alive.current) setPreview({ id: item.id, url });
      else URL.revokeObjectURL(url);
    } catch (error) {
      report(error);
    } finally {
      if (alive.current) setBusy(false);
    }
  }
  async function send(action: string, extra: Record<string, unknown>) {
    setBusy(true);
    try {
      await execute(action, { ...extra, request_key: crypto.randomUUID() });
      notify("已进入投递记录；平台回执确认后才算送达。");
    } catch (error) {
      report(error);
    } finally {
      if (alive.current) setBusy(false);
    }
  }
  return (
    <details className="run-evidence">
      <summary>Telegram 贴纸与表情</summary>
      <p>只操作当前 Telegram 对话。动画和视频显示的是平台预览帧。</p>
      <Button
        variant="outline"
        disabled={busy}
        onClick={() => void refreshSets()}
      >
        读取空间可用贴纸包
      </Button>
      {savedSets && (
        <>
          <p>
            空间 {savedSets.space_id} · Bot {savedSets.target.application_id} /
            聊天 {savedSets.target.chat_id} · {savedSets.sets.length}/32 包
          </p>
          <ul>
            {savedSets.sets.map((name) => (
              <li key={name}>
                {name}
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => void list(0, name, true)}
                >
                  读取可用包
                </Button>
              </li>
            ))}
          </ul>
          <Field label="空间可用包（每行一个包名或链接，最多 32 个）">
            <textarea
              value={savedDraft}
              onChange={(event) => setSavedDraft(event.target.value)}
              rows={4}
            />
          </Field>
          <p>
            确认后长期保存到本空间。移除会使从可用包取得的引用失效；收到的贴纸和单独查询引用仍各自限时
            24 小时。
          </p>
          <ControlPreview
            kind="telegram.set_sticker_sets"
            arguments={{
              conversation_id: chat,
              expected_revision: savedSets.revision,
              sets: savedDraft
                .split(/\r?\n/)
                .map((name) => name.trim())
                .filter(Boolean),
            }}
            disabled={
              busy ||
              savedDraft.split(/\r?\n/).filter((name) => name.trim()).length >
                32
            }
            label="预览并确认可用包变更"
            onSaved={async () => {
              await loadSets();
              if (alive.current) {
                setCatalog(null);
                setPreview(null);
                notify("空间可用贴纸包已保存。");
              }
            }}
          />
        </>
      )}
      <Field label="贴纸包名或 t.me/addstickers 链接">
        <input
          value={set}
          onChange={(event) => {
            changeSet(event.target.value);
            setFromSaved(false);
            setCatalog(null);
          }}
          placeholder="留空查看本会话收到的贴纸"
        />
      </Field>
      <Button variant="outline" disabled={busy} onClick={() => void list()}>
        读取贴纸
      </Button>
      {catalog && (
        <>
          <p>
            {catalog.title || "本会话贴纸"} · {catalog.total} 项
          </p>
          {catalog.name && (
            <a
              href={`https://t.me/addstickers/${encodeURIComponent(catalog.name)}`}
              target="_blank"
              rel="noreferrer"
            >
              查看 / 分享贴纸包链接
            </a>
          )}
          <ul>
            {catalog.items.map((item) => (
              <li key={item.id}>
                {item.emoji || "贴纸"} · {item.format}
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => void show(item)}
                >
                  预览
                </Button>
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => void send("send", { resource: item.id })}
                >
                  确认发送这张贴纸
                </Button>
                {preview?.id === item.id && (
                  <img
                    src={preview.url}
                    alt={item.emoji ? "贴纸预览：" + item.emoji : "贴纸预览帧"}
                    width={160}
                    height={160}
                  />
                )}
              </li>
            ))}
          </ul>
          <Button
            variant="ghost"
            disabled={busy || catalog.offset === 0}
            onClick={() => void list(Math.max(0, catalog.offset - 24))}
          >
            上一页
          </Button>
          <Button
            variant="ghost"
            disabled={
              busy || catalog.offset + catalog.items.length >= catalog.total
            }
            onClick={() => void list(catalog.offset + 24)}
          >
            下一页
          </Button>
        </>
      )}
      <Button
        variant="outline"
        disabled={busy}
        onClick={() => void loadTargets()}
      >
        读取本会话可操作消息
      </Button>
      <Field label="表情目标（仅本会话已核对的消息）">
        <select
          value={messageRef}
          onChange={(event) => setMessageRef(event.target.value)}
        >
          <option value="">当前正在回复的用户消息</option>
          {targets.map((target) => (
            <option key={target.ref} value={target.ref}>
              {target.kind === "incoming" ? "收到的消息" : "Bot 已发送消息"}{" "}
              {target.message_id} · {target.preview || target.ref}
            </option>
          ))}
        </select>
      </Field>
      <Field label="普通表情">
        <input
          value={emoji}
          onChange={(event) => setEmoji(event.target.value)}
          maxLength={32}
        />
      </Field>
      <Button
        variant="outline"
        disabled={busy}
        onClick={() =>
          void send("reaction", { emoji, message_ref: messageRef })
        }
      >
        确认设置表情
      </Button>
      <Button
        variant="ghost"
        disabled={busy}
        onClick={() =>
          void send("reaction", { emoji: "", message_ref: messageRef })
        }
      >
        移除 Bot 的表情
      </Button>
    </details>
  );
}
