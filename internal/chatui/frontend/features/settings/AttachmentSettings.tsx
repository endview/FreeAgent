import { useState } from "react";
import { api } from "@/lib/api/client";
import { downloadAttachment } from "@/lib/api/download";
import type { Attachment } from "@/lib/api/types";
import { useRuntime } from "@/lib/store/runtime";
import { clearFormDraft } from "@/lib/drafts";
import { Button } from "@/components/ui/button";
import { Field, Loading, Result, useFormDraft, useResource } from "./shared";

interface Policy extends Record<string, unknown> {
  text: boolean;
  png: boolean;
  jpeg: boolean;
  max_files: number;
  max_batch_bytes: number;
  max_input_bytes: number;
  max_export_bytes: number;
  max_pixels: number;
  quota_bytes: number;
  staging_hours: number;
}
const numbers = [
  ["max_files", "每批最多文件数", 8],
  ["max_batch_bytes", "每批最多字节", 64 * 1024 ** 2],
  ["max_input_bytes", "单文件最多字节", 16 * 1024 ** 2],
  ["max_export_bytes", "导出文件最多字节", 64 * 1024 ** 2],
  ["max_pixels", "图片最多像素", 32_000_000],
  ["quota_bytes", "附件总配额（字节）", 16 * 1024 ** 3],
  ["staging_hours", "暂存有效小时数", 168],
] as const;
export default function AttachmentSettings() {
  const resource = useResource<Policy>("attachment-policy");
  return resource.value ? (
    <Editor initial={resource.value} />
  ) : (
    <Loading error={resource.error} />
  );
}
function Editor({ initial }: { initial: Policy }) {
  const [form, change] = useFormDraft<Policy>("attachment-policy", initial),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const state = useRuntime();
  const [files, setFiles] = useState<
    { ref: Attachment & { sha256: string }; state?: string }[] | null
  >(null);
  async function read() {
    if (state.selected)
      setFiles(
        await api(
          `attachments?conversation_id=${encodeURIComponent(state.selected)}`,
        ),
      );
  }
  return (
    <>
      <p className="settings-intro">
        附件不可变保存，历史引用由 Core
        校验。删除后不能通过历史链接恢复，请先备份。
      </p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          api<Policy>("attachment-policy", form, "PUT")
            .then((next) => {
              change(next);
              clearFormDraft("attachment-policy");
              setResult("附件策略已保存。");
            })
            .catch((error) => setError(error.message))
            .finally(() => setBusy(false));
        }}
      >
        <fieldset disabled={busy}>
          {(
            [
              ["text", "文本"],
              ["png", "PNG 图片"],
              ["jpeg", "JPEG 图片"],
            ] as const
          ).map(([id, label]) => (
            <label className="check-line" key={id}>
              <input
                type="checkbox"
                checked={form[id]}
                onChange={(event) =>
                  change({ ...form, [id]: event.target.checked })
                }
              />
              允许{label}
            </label>
          ))}
          <div className="form-grid">
            {numbers.map(([id, label, max]) => (
              <Field key={id} label={label}>
                <input
                  type="number"
                  required
                  min={1}
                  max={max}
                  step={1}
                  value={form[id]}
                  onChange={(event) =>
                    change({ ...form, [id]: Number(event.target.value) })
                  }
                />
              </Field>
            ))}
          </div>
          <Button type="submit">保存附件策略</Button>
        </fieldset>
      </form>
      <Result error={error} text={result} />
      <section className="settings-section">
        <h2>当前对话的附件</h2>
        <Button
          variant="outline"
          disabled={!state.selected || busy}
          onClick={() => read().catch((error) => setError(error.message))}
        >
          读取附件
        </Button>
        {!state.selected && <p className="form-hint">先打开一个对话。</p>}
        {files?.map((file) => (
          <div className="setting-line" key={file.ref.id}>
            <span>
              {file.ref.name} · {(file.ref.bytes / 1024).toFixed(1)} KiB
            </span>
            <Button
              variant="ghost"
              onClick={() =>
                downloadAttachment(state.selected, file.ref).catch((error) =>
                  setError(error.message),
                )
              }
            >
              下载
            </Button>
            <Button
              variant="ghost"
              disabled={busy}
              onClick={async () => {
                if (
                  !window.confirm(
                    `永久删除 ${file.ref.name}？历史引用将不可用，只能从备份恢复。`,
                  )
                )
                  return;
                setBusy(true);
                setError("");
                try {
                  await api("attachments/delete", {
                    conversation_id: state.selected,
                    id: file.ref.id,
                    sha256: file.ref.sha256,
                  });
                  await read();
                } catch (error) {
                  setError((error as Error).message);
                } finally {
                  setBusy(false);
                }
              }}
            >
              删除
            </Button>
          </div>
        ))}
      </section>
    </>
  );
}
