import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { defaultView, type ViewPreferences } from "@/lib/platform";
import {
  loadEnvironment,
  loadNavigation,
  useRuntime,
} from "@/lib/store/runtime";
import { Field, Result } from "./shared";
export default function GeneralSettings({
  value,
  onChange,
}: {
  value: ViewPreferences;
  onChange: (value: ViewPreferences) => void;
}) {
  const state = useRuntime();
  const [error, setError] = useState("");
  return (
    <>
      <p className="settings-intro">
        这些外观偏好只作用于当前设备的客户端，不改变任务权限，也不向其他窗口广播。
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
                  await loadEnvironment();
                  await loadNavigation();
                }
              } catch (error) {
                setError((error as Error).message);
              }
            }}
          >
            选择工作区
          </Button>
        ) : (
          <p className="form-hint">
            可通过对话提出工作区变更并确认，或使用本机 CLI 的 control 命令修改。
          </p>
        )}
      </section>
      <Result error={error} />
    </>
  );
}
