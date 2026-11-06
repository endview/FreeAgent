import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { loadSnapshot, useRuntime } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { Field, Loading, Result, useFormDraft, useResource } from "./shared";

interface Command {
  id: string;
  conversation_id: string;
  mode: string;
  state: string;
  revision: number;
  expires_at: string;
  max_expires_at?: string;
  stop_reason?: string;
  ready: boolean;
  receipt: { path?: string; detail?: string; host_escape_hatch?: boolean };
  output?: { stream: string; text: string }[];
  next_cursor: number;
  output_gap: boolean;
  encoding_loss?: boolean;
}
export default function CommandSessions() {
  const state = useRuntime();
  const resource = useResource<Command[]>("attach/commands");
  const [form, change] = useFormDraft("command-session", {
    program: "",
    args: "",
    cwd: ".",
    mode: "process",
    seconds: 1800,
    ready: "",
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const [output, setOutput] = useState<Command | null>(null),
    [extendSeconds, setExtendSeconds] = useState(600);
  const running = Boolean(
    resource.value?.some((item) =>
      ["running", "stopping"].includes(item.state),
    ),
  );
  useEffect(() => {
    if (!running) return;
    let loading = false;
    const timer = setInterval(() => {
      if (loading) return;
      loading = true;
      resource
        .reload()
        .catch((error) => setError(error.message))
        .finally(() => {
          loading = false;
        });
    }, 1500);
    return () => clearInterval(timer);
  }, [running, resource.reload]);
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      await action();
      await resource.reload();
      await loadSnapshot();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p className="settings-intro">
        查看当前 Owner 管理的命令。停止只表示已请求终止，已有副作用和 UNKNOWN
        记录保留。
      </p>
      {resource.value ? (
        <div className="command-session-list">
          {resource.value.length ? (
            resource.value.map((item) => (
              <article className="settings-section" key={item.id}>
                <div className="section-title">
                  <h2>{item.receipt.path || item.mode}</h2>
                  <span>{item.state}</span>
                </div>
                <p className="form-hint">
                  {item.mode === "service"
                    ? `服务到期：${new Date(item.expires_at).toLocaleString()}`
                    : item.stop_reason || "后台进程"}
                  {item.receipt.host_escape_hatch ? " · 宿主机执行" : ""}
                  {item.mode === "service"
                    ? item.ready
                      ? " · 已就绪"
                      : " · 尚未就绪"
                    : ""}
                </p>
                <div className="provider-actions">
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() =>
                      void perform(async () =>
                        setOutput(
                          await api<Command>(
                            `attach/commands?id=${encodeURIComponent(item.id)}&cursor=0`,
                          ),
                        ),
                      )
                    }
                  >
                    查看输出
                  </Button>
                  {["running", "stopping"].includes(item.state) && (
                    <Button
                      variant="ghost"
                      disabled={busy}
                      onClick={() => {
                        if (
                          window.confirm(
                            "停止这个命令？终止不代表撤销已有副作用。",
                          )
                        )
                          void perform(async () =>
                            setOutput(
                              await api<Command>("attach/commands", {
                                action: "stop",
                                id: item.id,
                              }),
                            ),
                          );
                      }}
                    >
                      停止命令
                    </Button>
                  )}
                  {item.mode === "service" && item.state === "running" && (
                    <Button
                      variant="ghost"
                      disabled={busy}
                      onClick={() => {
                        if (
                          window.confirm(
                            `延长此服务 ${extendSeconds} 秒？累计时长仍受已配置上限约束。`,
                          )
                        )
                          void perform(async () => {
                            await api("attach/commands", {
                              action: "extend",
                              extension: {
                                id: item.id,
                                revision: item.revision,
                                expires_at: new Date(
                                  Date.parse(item.expires_at) +
                                    extendSeconds * 1000,
                                ).toISOString(),
                                request_key: crypto.randomUUID(),
                              },
                            });
                          });
                      }}
                    >
                      延长服务
                    </Button>
                  )}
                </div>
              </article>
            ))
          ) : (
            <p className="form-hint">还没有后台命令。</p>
          )}
        </div>
      ) : (
        <Loading error={resource.error} />
      )}
      <Field label="服务延长秒数">
        <input
          type="number"
          min={1}
          max={28800}
          value={extendSeconds}
          onChange={(event) => setExtendSeconds(Number(event.target.value))}
        />
      </Field>
      {output && (
        <section className="settings-section">
          <h2>命令输出</h2>
          {output.output_gap && (
            <p className="inline-warning">部分较早输出已超出保留范围。</p>
          )}
          {output.encoding_loss && (
            <p className="inline-warning">部分字符无法按原编码显示。</p>
          )}
          <pre className="settings-output">
            {output.output?.map((part) => part.text).join("") || "暂无输出"}
          </pre>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() =>
              void perform(async () =>
                setOutput(
                  await api<Command>(
                    `attach/commands?id=${encodeURIComponent(output.id)}&cursor=${output.next_cursor}`,
                  ),
                ),
              )
            }
          >
            读取下一页输出
          </Button>
        </section>
      )}
      <form
        className="settings-section"
        onSubmit={(event) => {
          event.preventDefault();
          if (!state.selected) {
            setError("请先打开一个对话，命令会绑定到该对话的权限。");
            return;
          }
          if (
            !window.confirm(
              "按以下程序、参数和工作目录启动命令？将使用当前对话权限。",
            )
          )
            return;
          void perform(async () => {
            await api("attach/commands", {
              action: "start",
              conversation_id: state.selected,
              request_key: crypto.randomUUID(),
              arguments: {
                command: form.program,
                args: form.args === "" ? [] : form.args.split("\n"),
                cwd: form.cwd,
                background: true,
                ...(form.mode === "service"
                  ? {
                      mode: "service",
                      duration_seconds: form.seconds,
                      ready_text: form.ready,
                    }
                  : {}),
              },
            });
            setResult("命令请求已提交，实际状态见记录。");
          });
        }}
      >
        <h2>手动启动命令</h2>
        <fieldset disabled={busy}>
          <Field label="程序">
            <input
              required
              value={form.program}
              onChange={(event) =>
                change({ ...form, program: event.target.value })
              }
            />
          </Field>
          <Field label="参数（每行一个，保留空参数）">
            <textarea
              rows={3}
              value={form.args}
              onChange={(event) =>
                change({ ...form, args: event.target.value })
              }
            />
          </Field>
          <Field label="工作目录">
            <input
              required
              value={form.cwd}
              onChange={(event) => change({ ...form, cwd: event.target.value })}
            />
          </Field>
          <Field label="运行方式">
            <Choice
              label="运行方式"
              value={form.mode}
              options={[
                ["process", "后台进程"],
                ["service", "有限期服务"],
              ]}
              onChange={(mode) => change({ ...form, mode })}
            />
          </Field>
          {form.mode === "service" && (
            <>
              <Field label="服务秒数">
                <input
                  type="number"
                  min={1}
                  max={28800}
                  required
                  value={form.seconds}
                  onChange={(event) =>
                    change({ ...form, seconds: Number(event.target.value) })
                  }
                />
              </Field>
              <Field label="就绪文本">
                <input
                  value={form.ready}
                  onChange={(event) =>
                    change({ ...form, ready: event.target.value })
                  }
                />
              </Field>
            </>
          )}
          <Button type="submit" disabled={!state.selected}>
            启动命令
          </Button>
        </fieldset>
      </form>
      <Result error={error} text={result} />
    </>
  );
}
