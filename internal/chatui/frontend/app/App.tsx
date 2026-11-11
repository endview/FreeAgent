import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from "react";
import {
  Folder,
  ChevronRight,
  Search,
  Pin,
  Pencil,
  Plus,
  Moon,
  Sun,
  Settings2,
  Menu,
  X,
  RefreshCw,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogHeader,
} from "@/components/ui/dialog";
import { Divider } from "@/components/Divider";
import { api, login, publicSession, remote, setCSRF } from "@/lib/api/client";
import {
  loadMoreNavigation,
  loadNavigation,
  loadSnapshot,
  report,
  notify,
  searchNavigation,
  selectConversation,
  startRuntime,
  resetRuntime,
  update,
  useRuntime,
} from "@/lib/store/runtime";
import {
  applyTheme,
  loadView,
  saveView,
  viewCommand,
  type ViewPreferences,
} from "@/lib/platform";
import { clearDrafts } from "@/lib/drafts";
import ConversationPage from "@/features/conversations/ConversationPage";
import { Choice } from "@/components/Choice";
import { Navigation } from "@/features/conversations/Navigation";
const SettingsPage = lazy(() => import("@/features/settings/SettingsPage"));

export default function App() {
  const state = useRuntime();
  const [authenticated, setAuthenticated] = useState(!remote);
  const [password, setPassword] = useState("");
  const [loginError, setLoginError] = useState("");
  const [loggingIn, setLoggingIn] = useState(false);
  const [view, setView] = useState(
    new URLSearchParams(location.search).get("view") || "chat",
  );
  const [section, setSection] = useState(
    new URLSearchParams(location.search).get("section") || "providers",
  );
  const [mobile, setMobile] = useState(false);
  const menuButton = useRef<HTMLButtonElement>(null);
  const [theme, setTheme] = useState<ViewPreferences["theme"]>(() => {
    try {
      const saved =
        (window as Window & { __freeagentInitialView?: { theme?: string } })
          .__freeagentInitialView?.theme ||
        localStorage.getItem("freeagent.theme");
      return saved === "light" || saved === "dark" ? saved : "system";
    } catch {
      return "system";
    }
  });
  const loadedView = useRef("");
  const [side, setSide] = useState(252);
  const [reading, setReading] = useState(820);
  const [composerMax, setComposerMax] = useState(190);
  const [query, setQuery] = useState("");
  function route(
    nextView: string,
    nextSection = section,
    chat = state.selected,
  ) {
    const params = new URLSearchParams(location.search);
    if (nextView === "chat") params.delete("view");
    else params.set("view", nextView);
    if (nextView === "settings") params.set("section", nextSection);
    else params.delete("section");
    if (chat) params.set("chat", chat);
    else params.delete("chat");
    history.pushState(
      {},
      "",
      location.pathname + (params.size ? "?" + params : "") + location.hash,
    );
    setView(nextView);
    setSection(nextSection);
    setMobile(false);
  }
  const openChat = useCallback(async (chat: string) => {
    const params = new URLSearchParams(location.search);
    params.delete("view");
    params.delete("section");
    if (chat) params.set("chat", chat);
    else params.delete("chat");
    history.pushState(
      {},
      "",
      location.pathname + (params.size ? "?" + params : "") + location.hash,
    );
    setView("chat");
    setMobile(false);
    await selectConversation(chat);
  }, []);
  useEffect(() => {
    const pop = () => {
      const params = new URLSearchParams(location.search);
      setView(params.get("view") || "chat");
      setSection(params.get("section") || "providers");
      void selectConversation(params.get("chat") || "").catch(report);
    };
    const auth = () => {
      resetRuntime();
      clearDrafts();
      setAuthenticated(false);
    };
    window.addEventListener("popstate", pop);
    window.addEventListener("freeagent-auth-required", auth);
    return () => {
      window.removeEventListener("popstate", pop);
      window.removeEventListener("freeagent-auth-required", auth);
    };
  }, []);
  useEffect(() => {
    if (remote)
      publicSession()
        .then((info) => {
          if (info.authenticated) {
            setCSRF(info.csrf || "");
            setAuthenticated(true);
          }
        })
        .catch((error) => setLoginError(error.message));
  }, []);
  useEffect(() => {
    if (authenticated) startRuntime().catch(report);
  }, [authenticated]);
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme:dark)");
    const sync = () => applyTheme(theme);
    sync();
    media.addEventListener("change", sync);
    return () => media.removeEventListener("change", sync);
  }, [theme]);
  useEffect(() => {
    const id = state.environment?.environment;
    if (!id) return;
    let disposed = false;
    loadView(id).then((value) => {
      if (disposed) return;
      setTheme(value.theme);
      setSide(value.sidebar);
      setReading(value.reading);
      setComposerMax(value.composer);
      loadedView.current = id;
    });
    return () => {
      disposed = true;
    };
  }, [state.environment?.environment]);
  useEffect(() => {
    const id = state.environment?.environment;
    if (!id || loadedView.current !== id) return;
    const timer = setTimeout(
      () =>
        void saveView(id, {
          theme,
          sidebar: side,
          reading,
          composer: composerMax,
        }),
      180,
    );
    return () => clearTimeout(timer);
  }, [state.environment?.environment, theme, side, reading, composerMax]);
  useEffect(() => {
    const refresh = () => update({ refreshRequired: true });
    window.addEventListener("freeagent-refresh-required", refresh);
    return () =>
      window.removeEventListener("freeagent-refresh-required", refresh);
  }, []);
  const desktopBusy = Boolean(
    state.snapshot?.busy || state.snapshot?.active_chats?.length,
  );
  useEffect(() => {
    void window.faSetBusy?.(desktopBusy).catch(() => {});
  }, [desktopBusy]);
  const attentionKey =
    state.snapshot?.view.turns
      .filter((turn) => turn.state === "awaiting_approval")
      .map((turn) => turn.key)
      .join(",") || "";
  useEffect(() => {
    document.title = attentionKey
      ? "（等待确认）FreeAgent Chat"
      : "FreeAgent Chat";
    if (attentionKey) void window.faAttention?.().catch(() => {});
  }, [attentionKey]);
  function setPreferences(value: ViewPreferences) {
    setTheme(value.theme);
    setSide(value.sidebar);
    setReading(value.reading);
    setComposerMax(value.composer);
  }
  async function submitLogin(event: React.FormEvent) {
    event.preventDefault();
    setLoggingIn(true);
    setLoginError("");
    const value = password;
    setPassword("");
    try {
      const info = await login(value);
      if (!info.authenticated) throw new Error("登录未完成。");
      setAuthenticated(true);
    } catch (error) {
      setLoginError(error instanceof Error ? error.message : "登录失败。");
    } finally {
      setLoggingIn(false);
    }
  }
  const searchTimer = useRef<number | undefined>(undefined);
  const selected =
    state.chats.find((item) => item.id === state.selected) ||
    state.snapshot?.view.conversation;
  const chats = state.chats;
  const nav = (
    <>
      <div className="brand">
        <div className="brand-symbol">
          <span />
          <span />
          <span />
        </div>
        <strong>FreeAgent</strong>
        <span className="brand-version">
          {state.snapshot?.server_version?.replace(/^v/, "") || "8.3"}
        </span>
      </div>
      <Button
        variant="outline"
        className="new-chat"
        onClick={() => openChat("").catch(report)}
      >
        <Plus size={17} />
        新对话
      </Button>
      <label className="nav-search">
        <Search size={16} />
        <input
          aria-label="搜索对话"
          placeholder="搜索对话"
          value={query}
          onChange={(event) => {
            const value = event.target.value;
            setQuery(value);
            clearTimeout(searchTimer.current);
            searchTimer.current = window.setTimeout(
              () => searchNavigation(value).catch(report),
              220,
            );
          }}
        />
      </label>
      <div className="space-heading">
        <Folder size={15} />
        <Choice
          label="当前空间"
          value={state.navSpace || "__all"}
          options={[
            ["__all", "所有空间"],
            ...state.spaces.map(
              (space) => [space.id, space.title] as [string, string],
            ),
          ]}
          onChange={(id) =>
            searchNavigation(query, id === "__all" ? "" : id).catch(report)
          }
        />
      </div>
      <Navigation
        chats={chats}
        selected={view === "chat" ? state.selected : ""}
        onOpen={(id) => openChat(id).catch(report)}
      />
      {!chats.length && (
        <p className="navigation-empty">
          {query ? "没有匹配标题的会话" : "还没有对话"}
        </p>
      )}
      {query && <p className="nav-search-scope">搜索当前空间内的会话标题</p>}
      {state.navMore && (
        <button
          className="nav-load-more"
          disabled={state.navLoading}
          onClick={() => loadMoreNavigation().catch(report)}
        >
          {state.navLoading ? "正在读取…" : "更多会话"}
        </button>
      )}
      <div className="sidebar-footer">
        <span className="connection-label">
          <span className={state.connected ? "online-dot" : "offline-dot"} />
          {remote ? "远程 Core" : "本地 Core"}
          <span>{state.connected ? "已连接" : "重新连接中"}</span>
        </span>
        <button
          className={`nav-settings ${view === "settings" ? "active" : ""}`}
          onClick={() => route("settings")}
        >
          <Settings2 size={17} />
          设置
          <ChevronRight size={15} />
        </button>
        <div className="footer-bottom">
          <span title={state.environment?.workspace}>
            工作区 /{" "}
            {state.environment?.workspace?.split(/[\\/]/).pop() || "FreeAgent"}
          </span>
          <button
            aria-label="切换主题"
            onClick={() => setTheme(theme === "light" ? "dark" : "light")}
          >
            {theme === "light" ? <Moon size={16} /> : <Sun size={16} />}
          </button>
        </div>
      </div>
    </>
  );
  if (!authenticated)
    return (
      <main className="login-page">
        <div className="login-brand">FreeAgent</div>
        <form onSubmit={submitLogin}>
          <h1>登录工作台</h1>
          <p>接续此实例中的对话和任务。</p>
          <label>
            管理员密码
            <input
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          {loginError && <p role="alert">{loginError}</p>}
          <Button disabled={loggingIn}>
            {loggingIn ? "正在登录…" : "登录"}
          </Button>
        </form>
      </main>
    );
  return (
    <div
      className="app"
      style={
        {
          "--sidebar-width": side + "px",
          "--reading-width": reading + "px",
          "--composer-max": composerMax + "px",
        } as CSSProperties
      }
    >
      <aside className="sidebar">{nav}</aside>
      <div className="sidebar-divider">
        <Divider
          axis="x"
          label="调整侧栏宽度"
          value={side}
          min={208}
          max={380}
          reset={252}
          onChange={setSide}
        />
      </div>
      <Dialog open={mobile} onOpenChange={setMobile}>
        <DialogContent
          className="mobile-navigation"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            menuButton.current?.focus();
          }}
        >
          <DialogHeader className="sr-only">
            <DialogTitle>会话导航</DialogTitle>
            <DialogDescription>切换对话和设置</DialogDescription>
          </DialogHeader>
          {nav}
        </DialogContent>
      </Dialog>
      <main className="workspace">
        <header className="workspace-header">
          <div className="breadcrumb">
            <button
              ref={menuButton}
              className="mobile-menu"
              aria-label="打开导航"
              onClick={() => setMobile(true)}
            >
              <Menu size={20} />
            </button>
            <Folder size={16} />
            <span>
              {(selected?.space_id &&
                state.spaces.find((space) => space.id === selected.space_id)
                  ?.title) ||
                "本地空间"}
            </span>
            <ChevronRight size={13} />
            <strong>
              {view === "settings" ? "设置" : selected?.title || "新对话"}
            </strong>
          </div>
          <div className="header-actions">
            {remote && (
              <button
                aria-label="退出登录"
                onClick={async () => {
                  try {
                    await api("public/logout", {});
                    setCSRF("");
                    clearDrafts();
                    resetRuntime();
                    setAuthenticated(false);
                  } catch (error) {
                    report(error);
                  }
                }}
              >
                退出
              </button>
            )}
            {selected && view === "chat" && (
              <button
                aria-label="重命名会话"
                onClick={async () => {
                  const title = window.prompt("会话名称", selected.title || "");
                  if (title === null || !title.trim()) return;
                  try {
                    await api(`chats/${selected.id}/title`, {
                      title: title.trim(),
                    });
                    await loadNavigation();
                    await loadSnapshot();
                  } catch (error) {
                    report(error);
                  }
                }}
              >
                <Pencil size={16} />
              </button>
            )}
            {selected && (
              <button
                aria-label={selected.pinned ? "取消置顶" : "置顶对话"}
                onClick={() =>
                  api(`chats/${selected.id}/pin`, { pinned: !selected.pinned })
                    .then(loadNavigation)
                    .catch(report)
                }
              >
                <Pin size={16} />
              </button>
            )}
            <button
              aria-label="刷新记录"
              onClick={() =>
                Promise.all([loadNavigation(), loadSnapshot()]).catch(report)
              }
            >
              <RefreshCw size={16} />
            </button>
          </div>
        </header>
        {state.refreshRequired && (
          <div className="update-notice">
            服务端已更新，请保存草稿后刷新页面。
            <button
              onClick={() => {
                if (
                  window.confirm(
                    "Provider 秘密草稿刷新后会丢失。确认已保存或放弃这些草稿后刷新？",
                  )
                )
                  location.reload();
              }}
            >
              刷新页面
            </button>
          </div>
        )}
        {state.error && (
          <div className="app-error" role="alert">
            <span>{state.error}</span>
            <button
              aria-label="关闭错误提示"
              onClick={() => update({ error: "" })}
            >
              <X size={16} />
            </button>
          </div>
        )}
        {view === "settings" ? (
          <Suspense fallback={<p className="loading-label">正在读取设置…</p>}>
            <SettingsPage
              section={section}
              onSection={(next) => route("settings", next)}
              preferences={{
                theme,
                sidebar: side,
                reading,
                composer: composerMax,
              }}
              onPreferences={setPreferences}
            />
          </Suspense>
        ) : (
          <ConversationPage
            reading={reading}
            onReading={setReading}
            composerMax={composerMax}
            onComposerMax={setComposerMax}
            onOpenChat={openChat}
            onOpenModelSettings={() => route("settings", "providers")}
            onViewCommand={(text) => {
              const next = viewCommand(text, {
                theme,
                sidebar: side,
                reading,
                composer: composerMax,
              });
              if (!next) return false;
              setPreferences(next);
              notify("当前窗口外观已保存，其他客户端不变。");
              return true;
            }}
          />
        )}
        {state.notice && (
          <div className="toast" role="status">
            {state.notice}
          </div>
        )}
      </main>
    </div>
  );
}
