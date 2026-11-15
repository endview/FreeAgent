import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useId,
  useSyncExternalStore,
  isValidElement,
  cloneElement,
  Fragment,
  type ReactElement,
  type ReactNode,
} from "react";
import { api } from "@/lib/api/client";
import {
  formDraft,
  saveFormDraft,
  hasFormDraft,
  subscribeFormDrafts,
} from "@/lib/drafts";
import { Check, Circle, LoaderCircle } from "lucide-react";

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
  guard = false,
) {
  const [value, setValue] = useState<T>(() => formDraft(id, initial, guard));
  const dirty = useSyncExternalStore(
    subscribeFormDrafts,
    () => hasFormDraft(id),
    () => false,
  );
  function change(next: T) {
    saveFormDraft(id, next);
    setValue(next);
  }
  return [value, change, dirty] as const;
}

export function DraftStatus({
  dirty,
  busy = false,
  hint,
  cleanLabel = "与已保存设置一致",
}: {
  dirty: boolean;
  busy?: boolean;
  hint?: string;
  cleanLabel?: string;
}) {
  const Icon = busy ? LoaderCircle : dirty ? Circle : Check;
  return (
    <div
      className={`draft-status ${dirty ? "draft-status-pending" : ""}`}
      role="status"
      aria-live="polite"
    >
      <span>
        <Icon size={15} aria-hidden="true" />
        {busy ? "正在处理…" : dirty ? "有未保存的修改" : cleanLabel}
      </span>
      {hint && <small>{hint}</small>}
    </div>
  );
}

export function SettingsGroup({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <section className="settings-group" aria-label={title}>
      <div className="settings-group-heading">
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
      <div className="settings-group-fields">{children}</div>
    </section>
  );
}

export function Field({
  label,
  children,
  hint,
  error,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
  error?: string;
}) {
  const id = useId();
  const [validation, setValidation] = useState("");
  const fieldError = error || validation;
  const describedBy =
    [hint && `${id}-hint`, fieldError && `${id}-error`]
      .filter(Boolean)
      .join(" ") || undefined;
  // Associate both native fields and our Radix Choice with their visible help.
  const control =
    isValidElement(children) && children.type !== Fragment
      ? cloneElement(children as ReactElement<Record<string, unknown>>, {
          ...(describedBy
            ? {
                "aria-describedby": [
                  (children.props as Record<string, unknown>)[
                    "aria-describedby"
                  ],
                  describedBy,
                ]
                  .filter(Boolean)
                  .join(" "),
              }
            : {}),
          ...(fieldError ? { "aria-invalid": true } : {}),
        })
      : children;
  return (
    <div
      className="setting-field"
      onInvalidCapture={(event) => {
        const target = event.target as HTMLInputElement;
        setValidation(target.validationMessage || "请检查此项。");
      }}
      onChangeCapture={() => setValidation("")}
    >
      <label className="setting-control">
        <span>{label}</span>
        {control}
      </label>
      {hint && <small id={`${id}-hint`}>{hint}</small>}
      {fieldError && (
        <small id={`${id}-error`} className="field-error" role="alert">
          {fieldError}
        </small>
      )}
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
    <p className="loading-label" role="status">
      正在读取设置…
    </p>
  );
}
export const lines = (text: string) =>
  text
    .split(/\r?\n/)
    .map((value) => value.trim())
    .filter(Boolean);
