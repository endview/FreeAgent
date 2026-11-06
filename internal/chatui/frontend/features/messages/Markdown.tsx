import { memo, useMemo } from "react";
import { renderMarkdown, splitStable } from "../../lib/presentation/render.mjs";
import "katex/dist/katex.min.css";
import "@/styles/markdown.css";
import { notify, report } from "@/lib/store/runtime";

// The only HTML insertion boundary; all text goes through the existing tested
// renderer with HTML disabled and bounded, untrusted math/link handling.
function Markdown({
  text,
  streaming = false,
}: {
  text: string;
  streaming?: boolean;
}) {
  const parts = useMemo(
    () => (streaming ? splitStable(text) : { stable: text, tail: "" }),
    [text, streaming],
  );
  const html = useMemo(() => renderMarkdown(parts.stable), [parts.stable]);
  return (
    <>
      <div
        className="markdown"
        onClick={async (event) => {
          const button = (event.target as Element).closest<HTMLElement>(
            "button[data-copy]",
          );
          if (!button || !event.currentTarget.contains(button)) return;
          try {
            await navigator.clipboard.writeText(
              decodeURIComponent(button.dataset.copy || ""),
            );
            notify("已复制原文");
          } catch (error) {
            report(error);
          }
        }}
        dangerouslySetInnerHTML={{ __html: html }}
      />
      {parts.tail && <div className="stream-tail">{parts.tail}</div>}
    </>
  );
}
export default memo(Markdown);
