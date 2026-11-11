import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { ControlPreview } from "@/components/ControlPreview";
import { defaultView, type ViewPreferences } from "@/lib/platform";
import {
  loadEnvironment,
  loadNavigation,
  useRuntime,
  selectConversation,
  current,
  notify,
} from "@/lib/store/runtime";
import { clearFormDraft } from "@/lib/drafts";
import { Field, Result, useFormDraft } from "./shared";
export default function GeneralSettings({
  value,
  onChange,
}: {
  value: ViewPreferences;
  onChange: (value: ViewPreferences) => void;
}) {
  const state = useRuntime();
  const [error, setError] = useState("");
  const workspaceDirty = useRef(false);
  const [workspace, setWorkspace] = useFormDraft("workspace", {
    path: state.environment?.workspace || "",
    expected_revisions: {
      "config.workspace":
        (
          state.environment?.control_revisions as
            | Record<string, number>
            | undefined
        )?.["config.workspace"] || 1,
    },
  });
  function savedWorkspace() {
    const environment = current().environment;
    return {
      path: environment?.workspace || "",
      expected_revisions: {
        "config.workspace":
          (
            environment?.control_revisions as Record<string, number> | undefined
          )?.["config.workspace"] || 1,
      },
    };
  }
  useEffect(() => {
    if (!workspaceDirty.current && !workspace.path && state.environment)
      setWorkspace(savedWorkspace());
  }, [state.environment]);
  async function workspaceSaved() {
    await selectConversation("");
    const url = new URL(location.href);
    url.searchParams.delete("chat");
    url.searchParams.delete("run");
    history.replaceState(null, "", url);
    await loadEnvironment();
    await loadNavigation();
    setWorkspace(savedWorkspace());
    workspaceDirty.current = false;
    clearFormDraft("workspace");
    notify("工作区已切换，后续新任务使用当前路径。");
  }
  return (
    <>
      <p className="settings-intro">
        外观修改自动保存，只作用于当前客户端。也可在此窗口的对话框输入 /view
        theme dark、/view reading 960 或 /view reset，不会发送给模型或其他窗口。
      </p>
      <Field label="主题">
        <Choice
          label="主题"
          value={value.theme}
          options={[
            ["light", "浅色"],
            ["dark", "深色"],
            ["system", "跟随系统"],
          ]}
          onChange={(theme) =>
            onChange({ ...value, theme: theme as ViewPreferences["theme"] })
          }
        />
      </Field>
      <div className="form-grid">
        {(
          [
            ["sidebar", "侧栏宽度（px）", 208, 380],
            ["reading", "阅读宽度（px）", 540, 1120],
            ["composer", "输入区最大高度（px）", 88, 380],
          ] as const
        ).map(([id, label, min, max]) => (
          <Field key={id} label={label}>
            <input
              type="number"
              min={min}
              max={max}
              step={1}
              value={value[id]}
              onChange={(event) => {
                const number = Number(event.target.value);
                if (number >= min && number <= max)
                  onChange({ ...value, [id]: number });
              }}
            />
          </Field>
        ))}
      </div>
      <Button variant="outline" onClick={() => onChange(defaultView)}>
        恢复默认外观
      </Button>
      <section className="settings-section">
        <h2>工作区</h2>
        <p className="form-hint">{state.environment?.workspace}</p>
        {window.faChangeWorkspace ? (
          <Button
            variant="outline"
            onClick={async () => {
              setError("");
              try {
                const value = await window.faChangeWorkspace!();
                if (value.changed) {
                  await workspaceSaved();
                }
              } catch (error) {
                setError((error as Error).message);
              }
            }}
          >
            选择工作区
          </Button>
        ) : (
          <>
            <Field
              label="工作区绝对路径（Core 所在机器）"
              hint="输入已存在的文件夹。预览确认后生效；若有执行或未决操作，先完成或结束它。"
            >
              <input
                value={workspace.path}
                onChange={(event) => {
                  workspaceDirty.current = true;
                  setWorkspace({ ...workspace, path: event.target.value });
                }}
              />
            </Field>
            <ControlPreview
              kind="config.set_workspace"
              arguments={{ workspace: workspace.path }}
              expectedRevisions={workspace.expected_revisions}
              onSaved={workspaceSaved}
            />
            <Button
              variant="ghost"
              onClick={async () => {
                if (
                  !window.confirm("放弃工作区路径草稿，读取当前 Core 工作区？")
                )
                  return;
                try {
                  await loadEnvironment();
                  setWorkspace(savedWorkspace());
                  workspaceDirty.current = false;
                  clearFormDraft("workspace");
                } catch (error) {
                  setError((error as Error).message);
                }
              }}
            >
              读取当前工作区
            </Button>
          </>
        )}
      </section>
      <Result error={error} />
    </>
  );
}
