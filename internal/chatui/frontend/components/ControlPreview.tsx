import { useEffect, useRef, useState } from "react";
import { api, APIError } from "@/lib/api/client";
import { ApprovalView } from "./ApprovalView";
import { Button } from "./ui/button";
import type { Confirmation } from "@/lib/api/types";

import type { ControlOperation as Operation } from "@/lib/api/types";
import { decodeControlOperation } from "@/lib/api/decode";
import { notify } from "@/lib/store/runtime";
export function ControlPreview({
  kind,
  arguments: args,
  disabled = false,
  onSaved,
  label = "预览并保存",
  initialOperation = null,
  onRejected,
  expectedRevisions,
  prepareArguments,
}: {
  kind: string;
  arguments: Record<string, unknown>;
  disabled?: boolean;
  onSaved: () => Promise<void>;
  label?: string;
  initialOperation?: Operation | null;
  onRejected?: () => void;
  expectedRevisions?: Record<string, number>;
  prepareArguments?: () => Promise<Record<string, unknown>>;
}) {
  const [operation, setOperation] = useState<Operation | null>(
      initialOperation,
    ),
    [code, setCode] = useState("");
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [confirmationRetry, setConfirmationRetry] = useState(0),
    [confirmationLoading, setConfirmationLoading] = useState(false);
  const signature = JSON.stringify([kind, args, expectedRevisions]);
  const currentSignature = useRef(signature),
    previewSignature = useRef(signature);
  currentSignature.current = signature;
  useEffect(() => {
    if (previewSignature.current !== signature) {
      setOperation(null);
      setCode("");
      previewSignature.current = signature;
    }
  }, [signature]);
  useEffect(() => {
    let disposed = false;
    setCode("");
    setConfirmationLoading(Boolean(operation));
    if (operation) {
      setError("");
      api<{ code: string; operation_id: string }>(
        `control/confirmation?operation_id=${encodeURIComponent(operation.operation_id)}&conversation_id=${encodeURIComponent(operation.conversation_id || "")}`,
      )
        .then((value) => {
          if (!disposed && value.operation_id === operation.operation_id)
            setCode(value.code);
        })
        .catch((error) => {
          if (!disposed) setError(error.message);
        })
        .finally(() => {
          if (!disposed) setConfirmationLoading(false);
        });
    }
    return () => {
      disposed = true;
    };
  }, [operation?.operation_id, confirmationRetry]);
  async function preview() {
    setBusy(true);
    setError("");
    const requestedSignature = currentSignature.current;
    try {
      const preparedArgs = prepareArguments ? await prepareArguments() : args;
      if (currentSignature.current !== requestedSignature) return;
      const next = decodeControlOperation(
        await api<unknown>("control/plan", {
          kind,
          arguments: preparedArgs,
          expected_revisions: expectedRevisions,
        }),
      );
      if (currentSignature.current === requestedSignature) {
        previewSignature.current = requestedSignature;
        setOperation(next);
      }
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function decide(approved: boolean) {
    if (!operation) return;
    if (previewSignature.current !== currentSignature.current) {
      setOperation(null);
      setCode("");
      return;
    }
    setBusy(true);
    setError("");
    const decidedSignature = currentSignature.current;
    try {
      try {
        await api(approved ? "control/confirm" : "control/reject", {
          operation_id: operation.operation_id,
          conversation_id: operation.conversation_id || "",
          ...(approved ? { code } : {}),
        });
      } catch (failure) {
        // A missing response is not proof that the setting was not saved.
        // Read the existing operation receipt; never replay the mutation.
        if (
          failure instanceof APIError &&
          failure.code === "reauthentication_required"
        )
          throw failure;
        const receipt = await api<Operation>(
          "control/operations?operation_id=" +
            encodeURIComponent(operation.operation_id),
        ).catch(() => null);
        if (
          receipt?.digest !== operation.digest ||
          receipt?.status !== (approved ? "applied" : "rejected")
        )
          throw failure;
      }
      setOperation(null);
      notify(approved ? "变更已保存并确认。" : "已拒绝，设置未更改。");
      if (approved && decidedSignature === currentSignature.current)
        await onSaved();
      else if (!approved) onRejected?.();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const confirmation: Confirmation | null = operation
    ? {
        run_id: "",
        attempt_id: "",
        digest: operation.digest,
        tool: operation.kind,
        preview: operation.preview,
        control_operation_id: operation.operation_id,
        items: Object.entries(operation.proposed_values || {}).map(
          ([target, value]) => ({
            kind: "setting",
            target,
            old: operation.old_values?.[target] || "",
            new: value,
          }),
        ),
      }
    : null;
  return (
    <div className="control-preview">
      {operation ? (
        <>
          <ApprovalView
            preview={confirmation}
            busy={busy}
            error={error}
            ready={Boolean(code)}
            onDecide={(approved) => void decide(approved)}
          />
          {!code && error && (
            <Button
              type="button"
              variant="outline"
              disabled={busy || confirmationLoading}
              onClick={() => setConfirmationRetry((value) => value + 1)}
            >
              重新读取确认状态
            </Button>
          )}
          {error && initialOperation && (
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={() => (onRejected ? onRejected() : void onSaved())}
            >
              刷新原提案状态
            </Button>
          )}
        </>
      ) : (
        <>
          <Button type="button" disabled={disabled || busy} onClick={preview}>
            {busy ? "正在预览…" : label}
          </Button>
          {error && (
            <p className="inline-error" role="alert">
              {error}
            </p>
          )}
        </>
      )}
    </div>
  );
}
