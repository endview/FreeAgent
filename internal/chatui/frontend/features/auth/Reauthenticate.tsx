import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api } from "@/lib/api/client";
import { Field, Result } from "@/features/settings/shared";

export function Reauthenticate() {
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [verified, setVerified] = useState(false);
  useEffect(() => {
    const show = () => {
      setError("");
      setPassword("");
      setVerified(false);
      setOpen(true);
    };
    window.addEventListener("freeagent-reauthentication-required", show);
    return () =>
      window.removeEventListener("freeagent-reauthentication-required", show);
  }, []);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (busy) return;
        setPassword("");
        setOpen(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {verified ? "已验证密码" : "再次验证管理员密码"}
          </DialogTitle>
          <DialogDescription>
            {verified
              ? "尚未保存。请返回表单核对并重新提交。"
              : "当前设置尚未保存。"}
          </DialogDescription>
        </DialogHeader>
        {verified ? (
          <DialogFooter>
            <Button onClick={() => setOpen(false)}>返回设置</Button>
          </DialogFooter>
        ) : (
          <form
            onSubmit={async (event) => {
              event.preventDefault();
              const secret = password;
              setPassword("");
              setError("");
              setBusy(true);
              try {
                await api("public/reauthenticate", { password: secret });
                setVerified(true);
              } catch (err) {
                setError((err as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <Field label="当前密码">
              <input
                type="password"
                autoComplete="current-password"
                value={password}
                required
                onChange={(event) => setPassword(event.target.value)}
                disabled={busy}
              />
            </Field>
            <Result error={error} />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => {
                  setPassword("");
                  setOpen(false);
                }}
              >
                取消
              </Button>
              <Button type="submit" disabled={busy || !password}>
                {busy ? "正在验证…" : "验证"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
