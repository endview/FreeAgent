import { useState } from "react";
import { api, remote } from "@/lib/api/client";
import { decodeControlOperation } from "@/lib/api/decode";
import type { ControlOperation } from "@/lib/api/types";
import { ControlPreview } from "@/components/ControlPreview";
import { Button } from "@/components/ui/button";
import { Loading, Result, useResource } from "./shared";

export default function OperationHistory() {
  const resource = useResource<ControlOperation[]>("control/operations");
  const id = new URLSearchParams(location.search).get("operation") || "";
  const selected = useResource<ControlOperation>(
    "control/operations?operation_id=" + encodeURIComponent(id),
    /^[a-f0-9]{32}$/.test(id),
  );
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
      {id && (
        <>
          <h3>原设置提案</h3>
          {selected.value ? (
            <Proposal
              item={selected.value}
              reload={async () => {
                await selected.reload();
                await resource.reload();
              }}
              onRepreview={setPreview}
              onError={setError}
            />
          ) : (
            <Loading
              error={
                selected.error ||
                (!/^[a-f0-9]{32}$/.test(id) ? "提案编号无效。" : "")
              }
            />
          )}
        </>
      )}
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
          resource.value
            .filter((item) => item.operation_id !== id)
            .map((item) => (
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
                {item.status === "pending" &&
                  (!item.attempt_id || item.detached) && (
                    <Proposal
                      key={item.operation_id}
                      item={item}
                      reload={async () => {
                        await resource.reload();
                      }}
                      onRepreview={setPreview}
                      onError={setError}
                    />
                  )}
                {item.status === "pending" &&
                  item.attempt_id &&
                  !item.detached &&
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

function Proposal({
  item,
  reload,
  onRepreview,
  onError,
}: {
  item: ControlOperation;
  reload: () => Promise<void>;
  onRepreview: (operation: ControlOperation) => void;
  onError: (error: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  if (item.status !== "pending") {
    const canPreview =
      item.request_entry === "telegram_admin" &&
      item.status === "stale" &&
      ["expired", "stale_revision"].includes(item.error_code || "");
    return (
      <>
        <Result
          text={
            item.status === "applied"
              ? "已经保存。"
              : item.status === "rejected"
                ? "已拒绝。"
                : "原提案已结束，未再次应用。"
          }
        />
        {canPreview && (
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              onError("");
              try {
                onRepreview(
                  decodeControlOperation(
                    await api("control/repreview", {
                      operation_id: item.operation_id,
                    }),
                  ),
                );
              } catch (error) {
                onError((error as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            按当前值生成新预览
          </Button>
        )}
      </>
    );
  }
  if (item.local_approval_required && remote)
    return (
      <Result
        text={
          "等待本机批准：freeagent control confirm --operation " +
          item.operation_id
        }
      />
    );
  return (
    <ControlPreview
      key={item.operation_id}
      kind={item.kind}
      arguments={{}}
      initialOperation={item}
      onSaved={reload}
      onRejected={() => void reload().catch((error) => onError(error.message))}
    />
  );
}
