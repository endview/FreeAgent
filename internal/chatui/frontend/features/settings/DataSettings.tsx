import { useState } from "react";
import { api, remote } from "@/lib/api/client";
import { saveBlob } from "@/lib/api/download";
import { Button } from "@/components/ui/button";
import { Field, Loading, Result, useResource } from "./shared";
import OperationHistory from "./OperationHistory";

interface Database {
  version: string;
  managed: boolean;
  bytes: number;
  pool_open: number;
  pool_limit: number;
  pool_in_use: number;
  pool_waits: number;
  pool_wait_ms: number;
  owner_lost: boolean;
  core_id: string;
  schema: number;
}
interface ServerInfo {
  policy: { port: number };
  mode: string;
  root_host: boolean;
  sandbox: { backend: string; status: string; detail: string };
  restart_required?: boolean;
}
export default function DataSettings({
  diagnosis = false,
}: {
  diagnosis?: boolean;
}) {
  const database = useResource<Database>("settings/database"),
    server = useResource<ServerInfo>("server");
  const [error, setError] = useState(""),
    [result, setResult] = useState(""),
    [port, setPort] = useState<number | null>(null);
  function exportStatus() {
    if (!database.value || !server.value) return;
    const {
      version,
      managed,
      bytes,
      pool_open,
      pool_limit,
      pool_in_use,
      pool_waits,
      pool_wait_ms,
      owner_lost,
      schema,
    } = database.value;
    const snapshot = {
      format: "freeagent-ui-status/1",
      observed_at: new Date().toISOString(),
      postgres: {
        version,
        managed,
        bytes,
        pool_open,
        pool_limit,
        pool_in_use,
        pool_waits,
        pool_wait_ms,
        owner_lost,
        schema,
      },
      server: {
        mode: server.value.mode,
        root_host: server.value.root_host,
        sandbox: {
          backend: server.value.sandbox.backend,
          status: server.value.sandbox.status,
        },
      },
    };
    saveBlob(
      new Blob([JSON.stringify(snapshot, null, 2)], {
        type: "application/json",
      }),
      "freeagent-status.json",
    );
    setResult("状态摘要已导出；不含配置、路径、Key、日志、对话或附件。");
  }
  return (
    <>
      {database.value ? (
        <section>
          <h2>PostgreSQL</h2>
          <p className="settings-intro">
            {database.value.version} ·{" "}
            {database.value.managed
              ? "随包本机实例，随 Owner 启停"
              : "外部服务，由系统管理"}
          </p>
          <dl className="context-metrics">
            <div>
              <dt>数据库大小</dt>
              <dd>{(database.value.bytes / 1048576).toFixed(1)} MiB</dd>
            </div>
            <div>
              <dt>连接</dt>
              <dd>
                {database.value.pool_in_use} 使用中 / {database.value.pool_open}{" "}
                打开 / {database.value.pool_limit} 上限
              </dd>
            </div>
            <div>
              <dt>累计池等待</dt>
              <dd>
                {database.value.pool_waits} 次 / {database.value.pool_wait_ms}{" "}
                ms
              </dd>
            </div>
            <div>
              <dt>Owner</dt>
              <dd>
                {database.value.owner_lost ? "连接已失效，需要排查" : "正常"}
              </dd>
            </div>
          </dl>
          <details>
            <summary>数据身份与版本</summary>
            <p className="form-hint">
              Core {database.value.core_id} · schema {database.value.schema}
            </p>
          </details>
        </section>
      ) : (
        <Loading error={database.error} />
      )}
      {server.value && (
        <section className="settings-section">
          <h2>运行环境</h2>
          <p className="settings-intro">
            {server.value.mode || "interactive"} ·{" "}
            {server.value.sandbox.backend} · {server.value.sandbox.status}
          </p>
          <p className="form-hint">{server.value.sandbox.detail}</p>
          {server.value.root_host && (
            <p className="inline-warning">
              当前服务以 root 运行；Full 宿主命令具有 root 权限。
            </p>
          )}
          <form
            onSubmit={(event) => {
              event.preventDefault();
              setError("");
              api<ServerInfo>(
                "server",
                { port: port ?? server.value!.policy.port },
                "PUT",
              )
                .then((next) => {
                  server.setValue(next);
                  setResult("服务器设置已保存，下次重启 Owner 生效。");
                })
                .catch((error) => setError(error.message));
            }}
          >
            <Field label="下次启动的回环端口（0 为自动）">
              <input
                type="number"
                min={0}
                max={65535}
                required
                value={port ?? server.value.policy.port}
                onChange={(event) => setPort(Number(event.target.value))}
              />
            </Field>
            <Button type="submit" variant="outline">
              保存服务器端口
            </Button>
          </form>
        </section>
      )}
      <div className="provider-actions">
        <Button
          variant="outline"
          onClick={() =>
            Promise.all([database.reload(), server.reload()]).catch((error) =>
              setError(error.message),
            )
          }
        >
          刷新状态
        </Button>
        <Button
          variant="outline"
          disabled={!database.value || !server.value}
          onClick={exportStatus}
        >
          导出状态摘要
        </Button>
      </div>
      <section className="settings-section">
        <h2>{diagnosis ? "诊断与更新检查" : "备份与恢复"}</h2>
        <p className="form-hint">
          在{remote ? "服务器 SSH" : "本机终端"}
          执行。备份可在运行中取得一致快照；附件按快照引用复制。凭据和工作区分开保管，恢复使用新目录。
        </p>
        <pre className="settings-output">
          {diagnosis
            ? "freeagent doctor\nfreeagent doctor --export <新诊断包.zip>\nfreeagent doctor --preflight --backup <已验证的备份目录>"
            : "freeagent backup --out <新备份目录>\nfreeagent backup-verify --source <备份目录>\nfreeagent restore --source <备份目录> --data-dir <新数据目录>"}
        </pre>
        {diagnosis && (
          <p className="form-hint">
            更新前检查是观察结果。实际替换文件前仍需立即核对任务、待确认操作和备份，UNKNOWN
            不会被自动重做。
          </p>
        )}
      </section>
      <Result error={error} text={result} />
      {diagnosis && <OperationHistory />}
    </>
  );
}
