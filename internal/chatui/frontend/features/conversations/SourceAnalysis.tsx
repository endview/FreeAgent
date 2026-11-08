import { lazy, Suspense, useState } from "react";
import { api } from "@/lib/api/client";
import { notify, report, useRuntime } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import type { Turn } from "@/lib/api/types";

const AnalysisDraft = lazy(() => import("./AnalysisDraft"));
interface Preview {
  selection: { skill: string; run_ids: string[]; model_id?: string };
  digest: string;
  source: string;
  bytes: number;
}

export function SourceAnalysis({ chat, turn }: { chat: string; turn: Turn }) {
  const { environment, snapshot } = useRuntime();
  const [method, setMethod] = useState("");
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const view = snapshot?.view;
  const methods = (environment?.capabilities?.skills || []).filter(
    (item) =>
      item.enabled &&
      item.invocation === "selected_sources" &&
      (view?.conversation.space_id === "local" ||
        view?.capabilities.skills?.includes(item.id)),
  );
  const selected = methods.some((item) => item.id === method)
    ? method
    : methods[0]?.id || "";
  if (!turn.analysis && methods.length === 0) return null;

  async function inspect() {
    setBusy(true);
    try {
      setPreview(
        await api<Preview>(
          `attach/analyze?conversation_id=${encodeURIComponent(chat)}&skill=${encodeURIComponent(selected)}&run_ids=${encodeURIComponent(turn.key)}`,
        ),
      );
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  async function start() {
    if (!preview) return;
    setBusy(true);
    try {
      await api("attach/analyze", {
        conversation_id: chat,
        request_key: "analysis/" + preview.digest,
        selection: preview.selection,
        digest: preview.digest,
      });
      setPreview(null);
      notify("分析已开始；结果仍需核对后保存。");
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="run-evidence">
      <summary>
        {turn.analysis ? "分析草稿与保存" : "使用 Skill 分析本轮"}
      </summary>
      {turn.analysis ? (
        turn.state === "completed" ? (
          <Suspense fallback={<p>正在载入草稿…</p>}>
            <AnalysisDraft
              chat={chat}
              run={turn.key}
              text={turn.messages
                .filter((message) => message.role === "assistant")
                .map((message) => message.raw)
                .join("\n")}
            />
          </Suspense>
        ) : (
          <p>分析状态：{turn.state}。完成后可核对并保存。</p>
        )
      ) : (
        <>
          <Choice
            label="分析方法"
            value={selected}
            options={methods.map((item) => [item.id, item.name || item.id])}
            onChange={(value) => {
              setMethod(value);
              setPreview(null);
            }}
          />
          <p>仅分析明确选取的记录；不执行工具，不自动保存永久内容。</p>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => void inspect()}
          >
            预览本轮来源
          </Button>
          {preview && (
            <>
              <pre className="settings-output">{preview.source}</pre>
              <p>
                {preview.bytes} 字节 · 模型 {preview.selection.model_id}
              </p>
              <Button disabled={busy} onClick={() => void start()}>
                确认来源，开始分析
              </Button>
            </>
          )}
        </>
      )}
    </details>
  );
}
