import { useState, type FormEvent } from "react";
import { Link2, Pause, RefreshCw, Unlink2 } from "lucide-react";
import { api, APIError } from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { Field, Loading, Result, useResource } from "./shared";

type Binding = {
  id: string;
  name: string;
  state: "unbound" | "active" | "paused";
  user?: string;
  choices: string[];
  revision: number;
};

const route = "channels/telegram/admin-binding";

export function TelegramAdminBinding() {
  const resource = useResource<Binding[]>(route);
  return (
    <section className="settings-section" aria-labelledby="tg-admin-binding-heading">
      <h2 id="tg-admin-binding-heading">Telegram 私聊管理绑定</h2>
      {!resource.value ? (
        <Loading error={resource.error} />
      ) : resource.value.length ? (
        resource.value.map((bot) => (
          <BotBinding key={bot.id} bot={bot} reload={resource.reload} />
        ))
      ) : (
        <Result text="没有已启用的 Telegram 连接。" />
      )}
    </section>
  );
}

function BotBinding({
  bot,
  reload,
}: {
  bot: Binding;
  reload: () => Promise<Binding[]>;
}) {
  const [user, setUser] = useState(bot.user || "");
  const [base, setBase] = useState(bot.revision);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState("");
  const stale = base !== bot.revision;
  const canBind =
    bot.choices.includes(user) && (bot.state !== "active" || user !== bot.user);
  async function change(action: "bind" | "pause" | "revoke") {
    if (busy || stale) return;
    if (
      action === "revoke" &&
      !window.confirm("解除此 Bot 的管理绑定？未决定的旧卡会失效。")
    )
      return;
    setBusy(true);
    setError("");
    setResult("");
    try {
      await api(route, {
        bot: bot.id,
        action,
        user: action === "bind" ? user : "",
        expected_revision: base,
      });
      const next = (await reload()).find((item) => item.id === bot.id);
      if (next) {
        setBase(next.revision);
        setUser(next.user || "");
      }
      setResult(
        action === "bind"
          ? "绑定已保存。"
          : action === "pause"
            ? "绑定已暂停。"
            : "绑定已解除。",
      );
    } catch (err) {
      setError((err as Error).message);
      if (err instanceof APIError && err.code === "control_stale")
        await reload().catch(() => {});
    } finally {
      setBusy(false);
    }
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (canBind) void change("bind");
  }
  return (
    <form onSubmit={submit}>
      <div className="setting-line">
        <strong>{bot.name}</strong>
        <span role="status">
          {bot.state === "active"
            ? "已绑定"
            : bot.state === "paused"
              ? "已暂停"
              : "未绑定"}
          {bot.user ? " · " + bot.user : ""}
        </span>
      </div>
      <fieldset disabled={busy}>
        <Field label="已核实的私聊用户 ID">
          <Choice
            label="已核实的私聊用户 ID"
            value={bot.choices.includes(user) ? user : ""}
            options={[
              ["", "请选择"],
              ...bot.choices.map((id): [string, string] => [id, id]),
            ]}
            onChange={setUser}
          />
        </Field>
        <div className="provider-actions">
          <Button type="submit" disabled={!canBind || stale}>
            <Link2 size={16} aria-hidden="true" />
            {bot.state === "paused" && user === bot.user
              ? "重新绑定"
              : "保存绑定"}
          </Button>
          {bot.state === "active" && (
            <Button
              type="button"
              variant="outline"
              disabled={stale}
              onClick={() => void change("pause")}
            >
              <Pause size={16} aria-hidden="true" />
              暂停
            </Button>
          )}
          {bot.state !== "unbound" && (
            <Button
              type="button"
              variant="outline"
              disabled={stale}
              onClick={() => void change("revoke")}
            >
              <Unlink2 size={16} aria-hidden="true" />
              解除绑定
            </Button>
          )}
          <Button
            type="button"
            variant="ghost"
            onClick={async () => {
              setError("");
              try {
                const next = (await reload()).find(
                  (item) => item.id === bot.id,
                );
                if (next) setBase(next.revision);
              } catch (err) {
                setError((err as Error).message);
              }
            }}
          >
            <RefreshCw size={16} aria-hidden="true" />
            核对最新状态
          </Button>
        </div>
      </fieldset>
      <Result
        error={
          stale ? "绑定已在其他入口变更，请核对最新状态。草稿未清除。" : error
        }
        text={
          busy
            ? "正在保存…"
            : result ||
              (!bot.choices.length ? "暂无已核实的私聊身份。" : undefined)
        }
      />
    </form>
  );
}
