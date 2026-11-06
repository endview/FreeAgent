import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { api } from "@/lib/api/client";
import { formDraft, saveFormDraft } from "@/lib/drafts";

// Page-local reads; the Runtime store remains the only shared Core cache.
export function useResource<T>(path: string, enabled = true) {
  const [value, setValue] = useState<T | null>(null);
  const [error, setError] = useState("");
  const current = useRef({ path, sequence: 0 });
  current.current.path = path;
  const reload = useCallback(async () => {
    const sequence = ++current.current.sequence;
    const result = await api<T>(path);
    if (
      current.current.path === path &&
      current.current.sequence === sequence
    ) {
      setValue(result);
      setError("");
    }
    return result;
  }, [path]);
  useEffect(() => {
    const controller = new AbortController();
    const sequence = ++current.current.sequence;
    setValue(null);
    setError("");
    if (!enabled) return () => controller.abort();
    api<T>(path, undefined, undefined, controller.signal)
      .then((value) => {
        if (
          current.current.path === path &&
          current.current.sequence === sequence &&
          !controller.signal.aborted
        )
          setValue(value);
      })
      .catch((error) => {
        if (
          !controller.signal.aborted &&
          current.current.path === path &&
          current.current.sequence === sequence
        )
          setError(error.message);
      });
    return () => controller.abort();
  }, [path, enabled]);
  return { value, setValue, error, reload };
}

export function useFormDraft<T extends Record<string, unknown>>(
  id: string,
  initial: T,
) {
  const [value, setValue] = useState<T>(() => formDraft(id, initial));
  function change(next: T) {
    saveFormDraft(id, next);
    setValue(next);
  }
  return [value, change] as const;
}

export function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <div className="setting-field">
      <label className="setting-control">
        <span>{label}</span>
        {children}
      </label>
      {hint && <small>{hint}</small>}
    </div>
  );
}
export function Result({ error, text }: { error?: string; text?: string }) {
  return error ? (
    <p className="inline-error" role="alert">
      {error}
    </p>
  ) : text ? (
    <p className="form-result" role="status">
      {text}
    </p>
  ) : null;
}
export function Loading({ error }: { error?: string }) {
  return error ? (
    <p className="inline-error" role="alert">
      {error}
    </p>
  ) : (
    <p className="loading-label">正在读取设置…</p>
  );
}
export const lines = (text: string) =>
  text
    .split(/\r?\n/)
    .map((value) => value.trim())
    .filter(Boolean);
