import { useState } from "react";
import { api } from "@/lib/api/client";
import { decodeControlOperation } from "@/lib/api/decode";
import type { ControlOperation } from "@/lib/api/types";
import { ControlPreview } from "@/components/ControlPreview";
import { Button } from "@/components/ui/button";
import { Loading, Result, useResource } from "./shared";

export default function OperationHistory() {
  const resource = useResource<ControlOperation[]>("control/operations");
  const [preview, setPreview] = useState<ControlOperation | null>(null),
    [error, setError] = useState("");
  return (
    <section className="settings-section">
      <h2>设置变更记录</h2>
      <p className="form-hint">
        撤回也先预览具体内容。资源已变化时，Core
        会拒绝旧版本撤回。工具和文件执行记录仍在原对话中。
      </p>
      <Button variant="outline" onClick={() => void resource.reload()}>
        刷新变更记录
      </Button>
      <Result error={error} />
      {preview && (
        <ControlPreview
          key={preview.operation_id}
          kind={preview.kind}
          arguments={{}}
          initialOperation={preview}
          onSaved={async () => {
            setPreview(null);
            await resource.reload();
          }}
        />
      )}
      {resource.value ? (
        resource.value.length ? (
          resource.value.map((item) => (
            <details className="settings-section" key={item.operation_id}>
              <summary>
                {item.kind} · {item.status} · {item.created_at}
              </summary>
              <pre className="settings-output">{item.preview}</pre>
              {item.kind === "extensions.change_state" &&
                item.status !== "applied" && (
                  <Button
                    variant="outline"
                    onClick={async () => {
                      setError("");
                      try {
                        await api("control/reconcile-extension", {
                          operation_id: item.operation_id,
                        });
                        await resource.reload();
                      } catch (e) {
                        setError((e as Error).message);
                      }
                    }}
                  >
                    核对原操作结果
                  </Button>
                )}
              {item.status === "pending" && !item.attempt_id && (
                <ControlPreview
                  key={item.operation_id}
                  kind={item.kind}
                  arguments={{}}
                  initialOperation={item}
                  onSaved={async () => {
                    await resource.reload();
                  }}
                  onRejected={() =>
                    void resource
                      .reload()
                      .catch((error) => setError(error.message))
                  }
                />
              )}
              {item.status === "pending" &&
                item.attempt_id &&
                item.conversation_id && (
                  <a
                    data-internal
                    href={`/?chat=${encodeURIComponent(item.conversation_id)}`}
                  >
                    在原对话中确认
                  </a>
                )}
              {item.status === "applied" &&
                item.revert_class &&
                item.revert_class !== "none" && (
                  <Button
                    variant="outline"
                    onClick={async () => {
                      setError("");
                      try {
                        setPreview(
                          decodeControlOperation(
                            await api("control/revert-plan", {
                              operation_id: item.operation_id,
                            }),
                          ),
                        );
                      } catch (error) {
                        setError((error as Error).message);
                      }
                    }}
                  >
                    预览撤回
                  </Button>
                )}
            </details>
          ))
        ) : (
          <p className="form-hint">还没有设置变更。</p>
        )
      ) : (
        <Loading error={resource.error} />
      )}
    </section>
  );
}
