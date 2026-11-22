import { useEffect, useState, type FormEvent } from "react";
import {
  ArrowRight,
  ArrowUpRight,
  ChevronDown,
  Eye,
  EyeOff,
  FileText,
  Globe,
  LoaderCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import "@/styles/login.css";

function LoginMark({ small = false }: { small?: boolean }) {
  return (
    <span
      className={`login-mark ${small ? "login-mark-small" : ""}`}
      aria-hidden="true"
    >
      <i />
      <i />
      <i />
    </span>
  );
}

export function LoginPage({
  username,
  onUsernameChange,
  password,
  onPasswordChange,
  error,
  busy,
  onSubmit,
}: {
  username: string;
  onUsernameChange: (value: string) => void;
  password: string;
  onPasswordChange: (value: string) => void;
  error: string;
  busy: boolean;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    if (busy) setVisible(false);
  }, [busy]);
  return (
    <main className="login-page">
      <div className="login-layout">
        <section className="login-welcome" aria-label="FreeAgent 工作台">
          <div className="login-brand">
            <LoginMark />
            <strong translate="no">FreeAgent</strong>
          </div>
          <div className="login-welcome-copy">
            <span className="login-eyebrow">你的 AI 工作台</span>
            <h2>
              回到对话，
              <br />
              继续手上的事。
            </h2>
            <p>
              从一个想法，到一份交付。
              <br />
              在这里接续你的对话和任务。
            </p>
          </div>
          <div className="login-preview" aria-hidden="true">
            <div className="login-sample-heading">
              <span className="login-sample-window">
                <i />
                <i />
                <i />
              </span>
              <span>部署说明整理</span>
            </div>
            <div className="login-sample-body">
              <div className="login-sample-prompt">
                把启动、升级和排障整理成一份说明。
              </div>
              <div className="login-sample-answer">
                <LoginMark small />
                <span>说明已整理，可以继续核对。</span>
              </div>
              <div className="login-sample-document">
                <FileText size={18} />
                <span>部署说明.md</span>
                <span>
                  查看文件
                  <ArrowUpRight size={12} />
                </span>
              </div>
            </div>
          </div>
          <span className="login-welcome-footer">对话 · 执行 · 交付</span>
        </section>
        <section className="login-entry" aria-labelledby="login-title">
          <div className="login-entry-content">
            <div className="login-host" title={window.location.host}>
              <Globe size={14} aria-hidden="true" />
              <span translate="no">{window.location.host}</span>
            </div>
            <h1 id="login-title">登录工作台</h1>
            <p className="login-entry-intro">使用此实例的管理账号登录。</p>
            <form onSubmit={onSubmit} aria-busy={busy}>
              <label htmlFor="login-username">用户名</label>
              <input
                id="login-username"
                className="login-username"
                name="username"
                autoComplete="username"
                autoCapitalize="none"
                maxLength={64}
                spellCheck={false}
                required
                value={username}
                onChange={(event) => onUsernameChange(event.target.value)}
                readOnly={busy}
              />
              <label htmlFor="login-password">管理员密码</label>
              <div className="login-password-wrap">
                <input
                  id="login-password"
                  name="password"
                  type={visible ? "text" : "password"}
                  autoComplete="current-password"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  value={password}
                  onChange={(event) => onPasswordChange(event.target.value)}
                  placeholder="输入管理员密码"
                  aria-describedby={`login-help${error ? " login-feedback" : ""}`}
                  readOnly={busy}
                />
                <button
                  type="button"
                  className="login-password-toggle"
                  disabled={busy}
                  aria-label={visible ? "隐藏密码" : "显示密码"}
                  aria-pressed={visible}
                  onClick={() => setVisible((value) => !value)}
                >
                  {visible ? (
                    <EyeOff size={19} aria-hidden="true" />
                  ) : (
                    <Eye size={19} aria-hidden="true" />
                  )}
                </button>
              </div>
              {error && (
                <p id="login-feedback" className="login-feedback" role="alert">
                  {error}
                </p>
              )}
              <Button type="submit" className="login-enter" disabled={busy}>
                {busy ? (
                  <>
                    <LoaderCircle
                      size={17}
                      className="spin"
                      aria-hidden="true"
                    />
                    正在登录…
                  </>
                ) : (
                  <>
                    进入工作台
                    <ArrowRight size={18} aria-hidden="true" />
                  </>
                )}
              </Button>
              <p id="login-help" className="login-help">
                登录后继续此实例中的对话和任务。
              </p>
            </form>
            <details className="login-assistance">
              <summary>
                无法登录？
                <ChevronDown size={14} aria-hidden="true" />
              </summary>
              <p>
                请核对用户名和密码，并允许此站点使用
                Cookie。若多次失败触发访问限制，请稍后重试或联系此实例管理员。
              </p>
              <p>忘记密码，请联系此实例管理员。</p>
            </details>
          </div>
          <div className="login-entry-footer">
            <span translate="no">FreeAgent</span>
            <span>你的工作，随时接续。</span>
          </div>
        </section>
      </div>
    </main>
  );
}
