import { useState } from "react";
import { ApprovalView } from "@/components/ApprovalView";
import { Button } from "@/components/ui/button";
import { Message } from "@/features/messages/Message";
import sample from "../../test/fixtures/core-contract.json";
import type { Confirmation } from "@/lib/api/types";

// Only imported by the development build. The sample is exported by Go from
// the real projection, and contains no private conversations or credentials.
export default function Gallery() {
  const [decision, setDecision] = useState("");
  const preview: Confirmation = {
    run_id: "sample",
    attempt_id: "sample",
    digest: "synthetic",
    tool: "file_replace",
    preview:
      "路径：文档/发布说明.md\n- 原有说明\n+ 已确认的新说明\n\n内容变化后必须重新确认。",
  };
  return (
    <main className="gallery">
      <header>
        <h1>FreeAgent 组件状态</h1>
        <Button
          variant="outline"
          onClick={() => document.documentElement.classList.toggle("dark")}
        >
          切换主题
        </Button>
      </header>
      <p>开发状态页 · 合成样本</p>
      <Message
        text={sample.snapshot.view.turns[0].messages
          .map((message) => message.raw)
          .join("\n\n")}
      />
      <Message
        text={
          '中文、代码与公式：\n\n```go\nfmt.Println("你好")\n```\n\n$E=mc^2$\n\n| 状态 | 含义 |\n|---|---|\n| UNKNOWN | 需要核查 |'
        }
      />
      <ApprovalView
        preview={preview}
        onDecide={(approved) =>
          setDecision(approved ? "已批准（演示）" : "已拒绝（演示）")
        }
      />
      <p role="status">{decision}</p>
      <ApprovalView
        preview={preview}
        error="变更内容已经更新，请重新预览。"
        ready={false}
        onDecide={() => {}}
      />
      <ApprovalView preview={null} busy onDecide={() => {}} />
      {[
        ["运行中", "正在读取文档"],
        ["排队", "消息等待采用"],
        ["UNKNOWN", "操作可能已产生作用，请先核查"],
        ["失败", "认证失败，请检查模型配置"],
        ["取消", "保留已完成的操作"],
        ["完成", "结果已经保存"],
      ].map(([title, text]) => (
        <section
          className={`state-notice ${title === "UNKNOWN" ? "unknown" : title === "失败" ? "failure" : ""}`}
          key={title}
        >
          <div>
            <strong>{title}</strong>
            <p>{text}</p>
          </div>
        </section>
      ))}
      <p>
        长路径：C:\用户\工作区\文档\一份用于验证窄窗口断行而不会撑开整个页面的非常长的文件名称.md
      </p>
    </main>
  );
}
