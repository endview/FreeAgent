import { useState } from "react";
import { Copy, FileText, History } from "lucide-react";
import { api } from "@/lib/api/client";
import { downloadAttachment } from "@/lib/api/download";
import type { ChatMessage as ChatMessageData } from "@/lib/api/types";
import { notify, report } from "@/lib/store/runtime";
import { Message } from "./Message";
const fullMessages = new Map<string, string>();
export function ChatMessage({
  message,
  chat,
  model,
  elapsed,
  run,
  onRestart,
}: {
  message: ChatMessageData;
  chat: string;
  model?: string;
  elapsed: number;
  run: string;
  onRestart: (key: string, mode: string) => void;
}) {
  const key = chat + ":" + message.key;
  const [raw, setRaw] = useState(fullMessages.get(key) || message.raw),
    [expanded, setExpanded] = useState(fullMessages.has(key)),
    [loading, setLoading] = useState(false);
  const [exportKey] = useState(() => crypto.randomUUID()),
    [exporting, setExporting] = useState(false);
  async function original() {
    if (!message.truncated || expanded) return raw;
    const value = await api<{ raw: string }>(
      `chats/${chat}/raw?kind=message&key=${encodeURIComponent(message.key)}`,
    );
    fullMessages.set(key, value.raw);
    if (fullMessages.size > 100)
      fullMessages.delete(fullMessages.keys().next().value!);
    return value.raw;
  }
  async function copy() {
    try {
      await navigator.clipboard.writeText(await original());
      notify("已复制原文");
    } catch (error) {
      report(error);
    }
  }
  async function exportMarkdown() {
    setExporting(true);
    try {
      const ref = await api<import("@/lib/api/types").Attachment>(
        "attachments/reply",
        { conversation_id: chat, run_id: run, request_key: exportKey },
      );
      await downloadAttachment(chat, ref);
    } catch (error) {
      report(error);
    } finally {
      setExporting(false);
    }
  }
  const assistant = message.role !== "user";
  return (
    <article className={assistant ? "assistant-message" : "user-message"}>
      {assistant && (
        <div className="assistant-label">
          <div className="small-mark">F</div>
          <strong>FreeAgent</strong>
          <span>{model}</span>
        </div>
      )}
      {assistant ? <Message text={raw} /> : <p>{raw}</p>}
      {message.truncated && !expanded && (
        <button
          className="load-earlier"
          disabled={loading}
          onClick={async () => {
            setLoading(true);
            try {
              setRaw(await original());
              setExpanded(true);
            } catch (error) {
              report(error);
            } finally {
              setLoading(false);
            }
          }}
        >
          {loading ? "正在读取原文…" : "显示完整原文"}
        </button>
      )}
      <div className="message-actions">
        {assistant && (
          <button disabled={exporting} onClick={exportMarkdown}>
            <FileText size={13} />
            {exporting ? "正在导出…" : "另存为 Markdown"}
          </button>
        )}
        <button onClick={copy}>
          <Copy size={13} />
          复制原文
        </button>
        <button
          onClick={() =>
            onRestart(message.key, assistant ? "continue" : "rerun")
          }
        >
          <History size={13} />
          从这里重新开始
        </button>
        {assistant && <span>{(elapsed / 1000).toFixed(1)} 秒</span>}
      </div>
      {message.attachments?.map((ref) => (
        <button
          key={ref.id}
          className="attachment-chip"
          onClick={() => downloadAttachment(chat, ref).catch(report)}
        >
          <FileText size={14} />
          {ref.name}
        </button>
      ))}
    </article>
  );
}
