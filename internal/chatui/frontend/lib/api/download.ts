import { headers } from "./client";
import type { Attachment } from "./types";
export function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob),
    anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 30000);
}
export async function downloadAttachment(chat: string, ref: Attachment) {
  const response = await fetch(
    `/api/attachments/${encodeURIComponent(ref.id)}?conversation_id=${encodeURIComponent(chat)}`,
    { headers: headers(), credentials: "same-origin", cache: "no-store" },
  );
  if (!response.ok) throw new Error("附件下载失败，请检查访问身份和记录。");
  saveBlob(await response.blob(), ref.name);
}
