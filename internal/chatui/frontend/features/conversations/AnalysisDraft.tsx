import { useState } from "react";
import { api } from "@/lib/api/client";
import { loadEnvironment, notify, report } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { ControlPreview } from "@/components/ControlPreview";
import { Field, useFormDraft } from "@/features/settings/shared";

export interface Candidate extends Record<string, unknown> {
  kind: string;
  id: string;
  title: string;
  body: string;
  document: string;
  sources: string[];
  source_caveats: string[];
  limitations: string[];
  verification: string;
}
function evidenceText(value: unknown): string {
  if (typeof value === "string") return value;
  return value == null ? "" : JSON.stringify(value, null, 2);
}
function evidenceList(value: unknown): string[] {
  return (Array.isArray(value) ? value : [value])
    .map(evidenceText)
    .filter((item) => item.trim());
}
export function candidate(text: string): Candidate {
  try {
    const value = JSON.parse(
      text.trim().replace(/^```(?:json)?\s*\n([\s\S]*?)\n```$/i, "$1"),
    );
    if (value && typeof value === "object" && !Array.isArray(value))
      return {
        kind: ["skill", "memory", "draft"].includes(value.kind)
          ? value.kind
          : "draft",
        id: typeof value.id === "string" ? value.id : "",
        title: typeof value.title === "string" ? value.title : "",
        body: typeof value.body === "string" ? value.body : "",
        document: typeof value.document === "string" ? value.document : "",
        sources: evidenceList(value.sources),
        source_caveats: evidenceList(value.source_caveats),
        limitations: evidenceList(value.limitations),
        verification: Array.isArray(value.verification)
          ? value.verification.map(evidenceText).join("\n")
          : evidenceText(value.verification),
      };
  } catch {
    /* A non-JSON answer remains an editable draft. */
  }
  return {
    kind: "draft",
    id: "",
    title: "",
    body: text,
    document: "",
    sources: [],
    source_caveats: [],
    limitations: [],
    verification: "",
  };
}
// Both save paths use the exact text shown in the publication preview.
export function analysisPublication(form: Candidate): string {
  const base = form.kind === "skill" ? form.document : form.body;
  const sections = [
    ["来源", evidenceList(form.sources)],
    ["来源注意事项", evidenceList(form.source_caveats)],
    ["局限", evidenceList(form.limitations)],
  ] as const;
  return (
    base.trimEnd() +
    sections
      .filter(([, values]) => values.length > 0)
      .map(
        ([title, values]) =>
          `\n\n## ${title}\n\n${values.map((value) => `- ${value}`).join("\n")}`,
      )
      .join("") +
    (form.verification.trim() ? `\n\n## 验收方法\n\n${form.verification}` : "")
  );
}
export async function analysisMemoryRequestKey(
  run: string,
  title: string,
  body: string,
): Promise<string> {
  const hash = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(JSON.stringify([run, title, body])),
  );
  return (
    "analysis-memory/" +
    Array.from(new Uint8Array(hash), (value) =>
      value.toString(16).padStart(2, "0"),
    ).join("")
  );
}
export default function AnalysisDraft({
  chat,
  run,
  text,
}: {
  chat: string;
  run: string;
  text: string;
}) {
  const initial = candidate(text);
  const [stored, change] = useFormDraft<Candidate>(
    "analysis-draft:" + chat + ":" + run,
    initial,
  );
  // Drafts opened before caveat fields were supported keep their edits while
  // recovering the evidence from the original model response.
  const form = { ...initial, ...stored };
  const publication = analysisPublication(form);
  const [busy, setBusy] = useState(false),
    [checked, setChecked] = useState(false),
    [expected, setExpected] = useState(""),
    [old, setOld] = useState("");
  const [saved, setSaved] = useState<{
    receipt: { entry_id: string; revision: number };
    scope: Record<string, unknown>;
  } | null>(null);
  async function checkSkill() {
    setBusy(true);
    try {
      const list = await api<{
        skills: { name: string; enabled: boolean; sha256: string }[];
      }>("skills");
      const existing = list.skills.find((skill) => skill.name === form.id);
      if (existing?.enabled)
        throw new Error("先在能力管理中停用已有 Skill，再预览替换。");
      setExpected(existing?.sha256 || "");
      setOld(
        existing
          ? (
              await api<{ body: string }>(
                `skills/${encodeURIComponent(form.id)}/view`,
              )
            ).body
          : "",
      );
      setChecked(true);
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  async function memory() {
    setBusy(true);
    try {
      setSaved(
        await api("attach/analyze/memory", {
          conversation_id: chat,
          run_id: run,
          request_key: await analysisMemoryRequestKey(
            run,
            form.title,
            publication,
          ),
          title: form.title,
          body: publication,
        }),
      );
      notify("Memory 已保存，尚未固定。");
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p>
        这是待核对的分析草稿。下方来源、局限和验收方法会随正文保存；发布不会执行其中的命令。
      </p>
      <Choice
        label="草稿类型"
        value={form.kind}
        options={[
          ["draft", "仅保留草稿"],
          ["memory", "简短 Memory"],
          ["skill", "可重复 Skill"],
        ]}
        onChange={(kind) => change({ ...form, kind })}
      />
      <Field label="标题">
        <input
          value={form.title}
          onChange={(event) => change({ ...form, title: event.target.value })}
        />
      </Field>
      {form.kind === "skill" ? (
        <>
          <Field label="Skill 标识">
            <input
              value={form.id}
              onChange={(event) => {
                change({ ...form, id: event.target.value });
                setChecked(false);
              }}
            />
          </Field>
          <Field label="完整 SKILL.md">
            <textarea
              rows={12}
              value={form.document}
              onChange={(event) =>
                change({ ...form, document: event.target.value })
              }
            />
          </Field>
          <Button
            disabled={busy || !form.id}
            variant="outline"
            onClick={() => void checkSkill()}
          >
            检查保存位置与现有版本
          </Button>
          {checked && (
            <>
              <p>保存位置：skills/{form.id}/SKILL.md；保存后不会启用。</p>
              {old && (
                <details>
                  <summary>将被替换的旧正文</summary>
                  <pre>{old}</pre>
                </details>
              )}
              <ControlPreview
                kind="skills.publish"
                arguments={{
                  id: form.id,
                  document: publication,
                  expected_sha256: expected,
                }}
                onSaved={async () => {
                  setChecked(false);
                  await loadEnvironment();
                  notify("Skill 已保存，请在能力管理中明确启用。");
                }}
                label="预览并确认发布 Skill"
              />
            </>
          )}
        </>
      ) : (
        <Field label="正文与局限">
          <textarea
            rows={8}
            value={form.body}
            onChange={(event) => change({ ...form, body: event.target.value })}
          />
        </Field>
      )}
      {(
        [
          ["sources", "来源"],
          ["source_caveats", "来源注意事项"],
          ["limitations", "局限与未验证内容"],
        ] as const
      ).map(([key, label]) => (
        <Field key={key} label={label} hint="每行一项，随正文保存。">
          <textarea
            rows={3}
            value={form[key].join("\n")}
            onChange={(event) =>
              change({ ...form, [key]: event.target.value.split("\n") })
            }
          />
        </Field>
      ))}
      <Field label="验收方法">
        <textarea
          rows={3}
          value={form.verification}
          onChange={(event) =>
            change({ ...form, verification: event.target.value })
          }
        />
      </Field>
      <details>
        <summary>
          将保存的完整内容（{new TextEncoder().encode(publication).length}{" "}
          字节）
        </summary>
        <pre className="settings-output">{publication}</pre>
      </details>
      {form.kind === "memory" && !saved && (
        <>
          <p>保存到当前对话所属的私有 Memory；不会自动全局共享或固定。</p>
          <Button
            disabled={busy || !form.title || !form.body}
            onClick={() => void memory()}
          >
            确认保存上述内容到私有 Memory
          </Button>
        </>
      )}
      {saved && (
        <ControlPreview
          kind="memory.set_pin"
          arguments={{
            conversation_id: chat,
            scope: saved.scope,
            entry_id: saved.receipt.entry_id,
            expected_revision: saved.receipt.revision,
            pin: { scope: "conversation", target: chat },
          }}
          onSaved={async () => {
            notify("已固定到当前对话。");
            await loadEnvironment();
          }}
          label="预览并固定到当前对话"
        />
      )}
    </>
  );
}
