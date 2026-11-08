import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api/client";
import { ApprovalView } from "./ApprovalView";
import { Button } from "./ui/button";
import type { Confirmation } from "@/lib/api/types";

import type { ControlOperation as Operation } from "@/lib/api/types";
import { decodeControlOperation } from "@/lib/api/decode";
export function ControlPreview({
  kind,
  arguments: args,
  disabled = false,
  onSaved,
  label = "预览并保存",
  initialOperation = null,
  onRejected,
}: {
  kind: string;
  arguments: Record<string, unknown>;
  disabled?: boolean;
  onSaved: () => Promise<void>;
  label?: string;
  initialOperation?: Operation | null;
  onRejected?: () => void;
}) {
  const [operation, setOperation] = useState<Operation | null>(
      initialOperation,
    ),
    [code, setCode] = useState("");
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const signature = JSON.stringify([kind, args]);
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
    if (operation)
      api<{ code: string; operation_id: string }>(
        `control/confirmation?operation_id=${encodeURIComponent(operation.operation_id)}&conversation_id=${encodeURIComponent(operation.conversation_id || "")}`,
      )
        .then((value) => {
          if (!disposed && value.operation_id === operation.operation_id)
            setCode(value.code);
        })
        .catch((error) => {
          if (!disposed) setError(error.message);
        });
    return () => {
      disposed = true;
    };
  }, [operation?.operation_id]);
  async function preview() {
    setBusy(true);
    setError("");
    const requestedSignature = currentSignature.current;
    try {
      const next = decodeControlOperation(
        await api<unknown>("control/plan", { kind, arguments: args }),
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
    try {
      await api(approved ? "control/confirm" : "control/reject", {
        operation_id: operation.operation_id,
        conversation_id: operation.conversation_id || "",
        ...(approved ? { code } : {}),
      });
      setOperation(null);
      if (approved) await onSaved();
      else onRejected?.();
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
        <ApprovalView
          preview={confirmation}
          busy={busy}
          error={error}
          ready={Boolean(code)}
          onDecide={(approved) => void decide(approved)}
        />
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
