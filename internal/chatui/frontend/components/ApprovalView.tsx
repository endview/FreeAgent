import { FileText, ShieldCheck } from "lucide-react";
import type { Confirmation } from "@/lib/api/types";
import { Button } from "@/components/ui/button";
import { diffLines } from "../lib/presentation/diff.mjs";

export function ApprovalView({
  preview,
  busy = false,
  error = "",
  ready = true,
  onDecide,
  onRetry,
  onEnd,
}: {
  preview: Confirmation | null;
  busy?: boolean;
  error?: string;
  ready?: boolean;
  onDecide: (approved: boolean) => void;
  onRetry?: () => void;
  onEnd?: () => void;
}) {
  return (
    <section className="approval-card" aria-label="待确认操作">
      <div className="approval-heading">
        <div className="approval-icon">
          <FileText size={18} />
        </div>
        <div>
          <strong>
            {preview?.control_operation_id
              ? "确认产品设置变更"
              : "确认本次工具操作"}
          </strong>
          <p>批准仅绑定下方内容与当前版本</p>
        </div>
        <span className="risk-label">
          {preview?.tool || (error ? "预览未就绪" : "等待预览")}
        </span>
      </div>
      <pre className="approval-preview">
        {preview?.preview ||
          (error
            ? "未能取得操作预览，尚未执行本次待确认操作。"
            : "正在读取精确变更…")}
      </pre>
      {preview?.items?.map((item, index) => (
        <details className="approval-item" key={index} open={index === 0}>
          <summary>
            {item.kind} · {item.target}
          </summary>
          {["file_edit", "file_create", "file_delete", "setting"].includes(
            item.kind,
          ) && <Diff oldText={item.old || ""} newText={item.new || ""} />}
          <dl>
            {Object.entries(item.fields || {}).map(([name, value]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd>
                  <pre>{value}</pre>
                </dd>
              </div>
            ))}
          </dl>
        </details>
      ))}
      {error && (
        <p className="inline-error" role="alert">
          {error}
        </p>
      )}
      <div className="approval-footer">
        <span>
          <ShieldCheck size={14} />
          内容变化后重新确认
        </span>
        {error && onRetry && (
          <Button variant="outline" disabled={busy} onClick={onRetry}>
            重新读取预览
          </Button>
        )}
        {error && onEnd && (
          <Button variant="ghost" disabled={busy} onClick={onEnd}>
            结束任务
          </Button>
        )}
        <Button
          variant="ghost"
          disabled={busy || !preview}
          onClick={() => onDecide(false)}
        >
          拒绝
        </Button>
        <Button
          disabled={busy || !preview || !ready}
          onClick={() => onDecide(true)}
        >
          批准并继续
        </Button>
      </div>
    </section>
  );
}
function Diff({ oldText, newText }: { oldText: string; newText: string }) {
  const diff = diffLines(oldText, newText);
  return diff.bounded ? (
    <div className="approval-diff">
      <pre className="diff-remove">{oldText}</pre>
      <pre className="diff-add">{newText}</pre>
    </div>
  ) : (
    <pre className="approval-diff">
      {(diff.lines || []).map(
        (line: { kind: string; text: string }, index: number) => (
          <span key={index} className={"diff-" + line.kind}>
            {line.kind === "remove" ? "-" : line.kind === "add" ? "+" : " "}
            {line.text}
            {"\n"}
          </span>
        ),
      )}
    </pre>
  );
}
