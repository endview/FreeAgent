import { StrictMode, useEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import { createRoot } from "react-dom/client";
import { Select, Switch } from "radix-ui";
import { ArrowDown, ArrowUp, Check, CheckCheck, ChevronDown, ChevronRight, CircleAlert, Clock3, Copy, FileText, Folder, Globe, History, LoaderCircle, Menu, MessageSquare, Moon, MoreHorizontal, Paperclip, Pin, Plus, Search, Settings2, ShieldCheck, Square, Sun, Terminal, Trash2, WandSparkles, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import "./styles.css";

type Scenario = "completed" | "running" | "waiting_confirmation" | "unknown" | "failed" | "empty";
type Page = "chat" | "settings";
const scenarios: Record<Scenario, string> = { completed: "正常回答", running: "运行中", waiting_confirmation: "等待确认", unknown: "结果未知", failed: "失败", empty: "空对话" };
const settingsGroups = [
  { title: "工作台", entries: ["通用", "模型与 Key", "权限与执行", "上下文"] },
  { title: "能力", entries: ["内置工具", "MCP", "Skill", "扩展"] },
  { title: "连接与数据", entries: ["渠道", "公网安全", "数据库与备份", "诊断"] },
];

function Choice({ value, onChange, label, options }: { value: string; onChange: (value: string) => void; label: string; options: [string, string][] }) {
  return <Select.Root value={value} onValueChange={onChange}><Select.Trigger aria-label={label} className="choice"><Select.Value /><Select.Icon><ChevronDown size={14} /></Select.Icon></Select.Trigger><Select.Portal><Select.Content className="choice-menu" position="popper" sideOffset={5}><Select.Viewport>{options.map(([id, name]) => <Select.Item className="choice-item" key={id} value={id}><Select.ItemText>{name}</Select.ItemText><Select.ItemIndicator><Check size={14} /></Select.ItemIndicator></Select.Item>)}</Select.Viewport></Select.Content></Select.Portal></Select.Root>;
}
function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return <Switch.Root className="toggle" aria-label={label} checked={checked} onCheckedChange={onChange}><Switch.Thumb className="toggle-thumb" /></Switch.Root>;
}
function Divider({ axis, label, value, min, max, onChange, reset }: { axis: "x" | "y"; label: string; value: number; min: number; max: number; onChange: (value: number) => void; reset: number }) {
  function start(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault(); const element = event.currentTarget; element.setPointerCapture(event.pointerId);
    const initial = axis === "x" ? event.clientX : event.clientY;
    const move = (next: PointerEvent) => onChange(Math.max(min, Math.min(max, value + (axis === "x" ? next.clientX - initial : initial - next.clientY))));
    const end = () => { element.removeEventListener("pointermove", move); element.removeEventListener("pointerup", end); element.removeEventListener("pointercancel", end); };
    element.addEventListener("pointermove", move); element.addEventListener("pointerup", end); element.addEventListener("pointercancel", end);
  }
  return <div role="separator" tabIndex={0} aria-label={label} aria-orientation={axis === "x" ? "vertical" : "horizontal"} aria-valuenow={value} aria-valuemin={min} aria-valuemax={max} className={`divider divider-${axis}`} onPointerDown={start} onDoubleClick={() => onChange(reset)} onKeyDown={event => {
    const delta = ["ArrowRight", "ArrowUp"].includes(event.key) ? 16 : ["ArrowLeft", "ArrowDown"].includes(event.key) ? -16 : 0;
    if (delta) { event.preventDefault(); onChange(Math.max(min, Math.min(max, value + delta))); }
    if (event.key === "Home") { event.preventDefault(); onChange(reset); }
  }}><span /></div>;
}

function App() {
  const [page, setPage] = useState<Page>("chat"); const [scenario, setScenario] = useState<Scenario>("waiting_confirmation");
  const [theme, setTheme] = useState("light"); const [mobileNav, setMobileNav] = useState(false); const [setting, setSetting] = useState("模型与 Key"); const [mobileSettingsDetail, setMobileSettingsDetail] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(252); const [readingWidth, setReadingWidth] = useState(820); const [composerMax, setComposerMax] = useState(190);
  const [draft, setDraft] = useState(""); const [messages, setMessages] = useState<string[]>([]); const [queue, setQueue] = useState<string[]>([]); const [guided, setGuided] = useState("");
  const [providerName, setProviderName] = useState("DeepSeek"); const [providerURL, setProviderURL] = useState("https://api.deepseek.com"); const [secret, setSecret] = useState("");
  const [enabled, setEnabled] = useState(true); const [autoCompact, setAutoCompact] = useState(true); const [pinned, setPinned] = useState(true); const [connection, setConnection] = useState(false); const [model, setModel] = useState("deepseek-flash");
  const [notice, setNotice] = useState(""); const [toolsOpen, setToolsOpen] = useState(false); const [approval, setApproval] = useState<"pending" | "approved" | "rejected">("pending");
  const [permission, setPermission] = useState("workspace"); const [advanced, setAdvanced] = useState(false); const textarea = useRef<HTMLTextAreaElement>(null); const menuButton = useRef<HTMLButtonElement>(null);
  const running = scenario === "running";
  useEffect(() => { document.documentElement.classList.toggle("dark", theme === "dark"); }, [theme]);
  useEffect(() => { if (!notice) return; const timer = window.setTimeout(() => setNotice(""), 2000); return () => window.clearTimeout(timer); }, [notice]);
  useEffect(() => { if (textarea.current) { textarea.current.style.height = "auto"; textarea.current.style.height = Math.min(textarea.current.scrollHeight, composerMax) + "px"; } }, [draft, composerMax, page]);
  function openPage(value: Page) { setPage(value); setMobileNav(false); if (value === "settings") setMobileSettingsDetail(false); }
  function send() {
    const value = draft.trim(); if (!value) return;
    if (running) setQueue(items => [...items, value]); else setMessages(items => [...items, value]);
    setDraft(""); if (scenario === "empty") setScenario("completed");
  }
  const nav = <>
    <div className="brand"><div className="brand-symbol"><span /><span /><span /></div><strong>FreeAgent</strong><span className="brand-version">8.1</span></div>
    <Button variant="outline" className="new-chat" onClick={() => { setScenario("empty"); setMessages([]); openPage("chat"); }}><Plus size={17} />新对话<span className="keyboard-hint">Ctrl N</span></Button>
    <button className="nav-search" onClick={() => setNotice("样例仅包含下列合成会话")}><Search size={16} /><span>搜索对话</span><kbd>Ctrl K</kbd></button>
    <div className="space-heading"><Folder size={15} /><span>FreeAgent 开发</span><ChevronDown size={14} /><button aria-label="空间操作" onClick={() => setNotice("正式版复用现有空间设置")}><MoreHorizontal size={16} /></button></div>
    <div className="nav-section-label">置顶</div>
    <button className={`conversation ${page === "chat" && scenario !== "empty" ? "active" : ""}`} onClick={() => { setScenario("waiting_confirmation"); openPage("chat"); }}><MessageSquare size={16} /><span>修复首次启动问题</span>{pinned && <Pin size={13} />}</button>
    <div className="nav-section-label">今天</div>
    {["梳理数据库备份流程", "下一版本的开发计划", "检查工具执行权限"].map(name => <button className="conversation" key={name} onClick={() => { openPage("chat"); setScenario("completed"); setNotice("已切换到合成历史状态"); }}><MessageSquare size={16} /><span>{name}</span></button>)}
    <div className="nav-section-label">昨天</div>
    {["给项目补充使用说明", "接续服务器上的对话"].map(name => <button className="conversation" key={name} onClick={() => { openPage("chat"); setScenario("completed"); }}><MessageSquare size={16} /><span>{name}</span></button>)}
    <div className="sidebar-footer"><span className="connection-label"><span className="online-dot" />本地 Core<span>已连接</span></span><button className={`nav-settings ${page === "settings" ? "active" : ""}`} onClick={() => openPage("settings")}><Settings2 size={17} />设置<ChevronRight size={15} /></button><div className="footer-bottom"><span>工作区 / FreeAgent</span><button aria-label="切换主题" onClick={() => setTheme(theme === "light" ? "dark" : "light")}>{theme === "light" ? <Moon size={16} /> : <Sun size={16} />}</button></div></div>
  </>;
  const layout = { "--sidebar-width": sidebarWidth + "px", "--reading-width": readingWidth + "px", "--composer-max": composerMax + "px" } as CSSProperties;

  return <div className="app" style={layout}>
    <aside className="sidebar">{nav}</aside><div className="sidebar-divider"><Divider axis="x" label="调整侧栏宽度" value={sidebarWidth} min={208} max={380} onChange={setSidebarWidth} reset={252} /></div>
    <Dialog open={mobileNav} onOpenChange={setMobileNav}><DialogContent className="mobile-navigation" onCloseAutoFocus={event => { event.preventDefault(); menuButton.current?.focus(); }}><DialogHeader className="sr-only"><DialogTitle>会话导航</DialogTitle><DialogDescription>切换会话或打开设置</DialogDescription></DialogHeader>{nav}</DialogContent></Dialog>
    <main className="workspace">
      <header className="workspace-header"><div className="breadcrumb"><button ref={menuButton} className="mobile-menu" aria-label="打开导航" onClick={() => setMobileNav(true)}><Menu size={20} /></button><Folder size={16} /><span>FreeAgent 开发</span><ChevronRight size={13} /><strong>{page === "chat" ? scenario === "empty" ? "新对话" : "修复首次启动问题" : "设置"}</strong></div><div className="header-actions"><span className="preview-label">交互样例 · 合成数据</span><button aria-label={pinned ? "取消置顶" : "置顶对话"} className={pinned ? "is-pinned" : ""} onClick={() => { setPinned(!pinned); setNotice(pinned ? "已取消置顶" : "对话已置顶"); }}><Pin size={16} /></button><button aria-label="会话更多操作" onClick={() => setAdvanced(!advanced)}><MoreHorizontal size={19} /></button></div></header>
      {advanced && <div className="session-options"><button onClick={() => { setSidebarWidth(252); setReadingWidth(820); setComposerMax(190); setAdvanced(false); }}>恢复默认布局</button><button onClick={() => setNotice("从消息重新开始会在正式版创建关联分支")}><History size={14} />从消息重新开始</button></div>}
      {page === "chat" ? <>
        <div className="preview-toolbar"><span>查看状态</span><Choice label="样例状态" value={scenario} onChange={value => { setScenario(value as Scenario); setApproval("pending"); }} options={Object.entries(scenarios)} /><span className="toolbar-note">所有操作仅作用于此样例</span></div>
        <section className="messages" aria-label="对话消息"><div className="reading-frame"><div className="reading-column">
          {scenario === "empty" ? <div className="empty-conversation"><div className="empty-mark"><MessageSquare size={28} /></div><h1>从一件具体的事开始</h1><p>描述目标、附上文件，FreeAgent 会使用你选择的能力完成它。</p><div className="starter-actions">{["检查项目中的问题", "整理这份文档", "继续上次的任务"].map(text => <button key={text} onClick={() => { setDraft(text); textarea.current?.focus(); }}>{text}<ArrowUp size={14} /></button>)}</div></div> : <>
            <div className="conversation-date">今天 14:32</div>
            <article className="user-message"><p>检查这个项目的启动流程，修复首次打开时初始化失败的问题。</p></article>
            <article className="assistant-message"><div className="assistant-label"><div className="small-mark">F</div><strong>FreeAgent</strong><span>deepseek-flash</span></div>
              <p>我会先检查桌面入口和引擎启动链路，再确认首次初始化是否调用了正确的程序。</p>
              <div className="tool-group"><button className="tool-summary" onClick={() => setToolsOpen(!toolsOpen)}><ChevronRight size={15} className={toolsOpen ? "expanded" : ""} /><CheckCheck size={16} /><span>已读取 2 个文件，搜索 1 次</span><time>0.42 秒</time></button>{toolsOpen && <div className="tool-list">{[["file_read", "cmd/freeagent-desktop/main_windows.go", "24 ms"], ["file_read", "cmd/freeagent-desktop/engine_windows.go", "18 ms"], ["file_search", "runCLI · engineExecutable", "31 ms"]].map(([tool, path, ms]) => <div key={path}><Check size={13} /><code>{tool}</code><span>{path}</span><time>{ms}</time></div>)}</div>}</div>
              {running ? <><p>已经定位到初始化与配置读取的入口差异，正在核对工作区切换时的调用。</p><div className="running-action"><LoaderCircle size={15} className="spin" /><span>正在检查工作区切换流程</span><time>3.2 秒</time></div></> : <>
                <p>问题出在初始化时选择了桌面程序自身，而后续引擎启动使用了独立 CLI。首次启动的两次调用因此没有得到预期结果。</p>
                <p>修复会统一这两个入口，并保留当前的数据目录和工作区。</p>
                {scenario === "waiting_confirmation" && <div className={`approval-card ${approval !== "pending" ? "approval-resolved" : ""}`}><div className="approval-heading"><div className="approval-icon"><FileText size={18} /></div><div><strong>{approval === "pending" ? "确认本次文件修改" : approval === "approved" ? "已确认这项变更" : "已拒绝这项变更"}</strong><p>{approval === "pending" ? "只批准下方展示的具体内容" : "样例状态已更新，未修改真实文件"}</p></div><span className="risk-label">写入</span></div><div className="diff-heading"><code>cmd/freeagent-desktop/main_windows.go</code><span><b className="diff-add-count">+2</b> <b className="diff-remove-count">−2</b></span></div><pre className="diff"><span className="diff-remove">− runCLI(ctx, desktopExecutable, "init", args)</span><span className="diff-add">+ runCLI(ctx, engineExecutable(), "init", args)</span><span className="diff-remove">− runCLI(ctx, desktopExecutable, "config", "show")</span><span className="diff-add">+ runCLI(ctx, engineExecutable(), "config", "show")</span></pre>{approval === "pending" && <div className="approval-footer"><span><ShieldCheck size={14} />变更后重新确认</span><Button variant="ghost" onClick={() => setApproval("rejected")}>拒绝</Button><Button onClick={() => setApproval("approved")}>批准并继续<ArrowUp size={14} /></Button></div>}</div>}
                {scenario === "unknown" && <div className="state-notice unknown"><CircleAlert size={20} /><div><strong>命令结果未知</strong><p>连接中断前命令已派发，现有记录不能证明是否完成。可以先只读核查文件状态。</p><details><summary>查看执行证据</summary><p>command_run 已派发；未收到完整退出回执。原调用不会被自动重放。</p></details><Button variant="outline" onClick={() => setNotice("正式版将发起只读核查；本样例保留结果未知")}>只读核查</Button></div></div>}
                {scenario === "failed" && <div className="state-notice failure"><CircleAlert size={20} /><div><strong>模型认证失败</strong><p>服务返回 HTTP 401。请检查当前 Provider 的 Key，然后显式重试。</p><Button variant="outline" onClick={() => openPage("settings")}>打开模型设置</Button></div></div>}
                {scenario === "completed" && <><h2>已完成</h2><ul><li>初始化与配置读取统一使用 CLI 引擎。</li><li>工作区切换沿用同一入口选择规则。</li><li>补充干净目录首次启动的回归用例。</li></ul><p>现在可以从新目录启动便携包，检查初始化与界面打开流程。</p><div className="message-actions"><button onClick={() => { navigator.clipboard?.writeText("初始化与配置读取统一使用 CLI 引擎。"); setNotice("已复制样例原文"); }}><Copy size={14} />复制</button><button onClick={() => setNotice("正式版会预览从此消息创建的新分支")}><History size={14} />从这里重新开始</button><span>共 4.8 秒</span></div></>}
              </>}
            </article>
          </>}
          {messages.map((message, index) => <article key={index} className="user-message"><p>{message}</p><small>样例消息，未发往模型</small></article>)}
          {guided && <div className="guidance-receipt"><WandSparkles size={14} />已转为引导：{guided}<span>样例采用状态</span></div>}
        </div><Divider axis="x" label="调整阅读宽度" value={readingWidth} min={540} max={1120} onChange={setReadingWidth} reset={820} /></div></section>
        <div className="composer-area"><div className="composer-column">
          {queue.length > 0 && <div className="queue-list">{queue.map((text, index) => <div key={index}><Clock3 size={14} /><span>{text}</span><button onClick={() => { setGuided(text); setQueue(items => items.filter((_, i) => i !== index)); }}>转为引导</button><button aria-label="删除排队消息" onClick={() => setQueue(items => items.filter((_, i) => i !== index))}><X size={14} /></button></div>)}</div>}
          <div className="composer"><Divider axis="y" label="调整输入区最大高度" value={composerMax} min={88} max={380} onChange={setComposerMax} reset={190} /><textarea ref={textarea} aria-label="消息" placeholder={running ? "补充消息，先加入队列，也可转为引导…" : "说说接下来要做什么…"} value={draft} rows={2} onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); send(); } }} /><div className="composer-tools"><button aria-label="添加附件" onClick={() => setNotice("附件上传在正式 Core 联调阶段接入")}><Paperclip size={18} /></button><Choice label="当前模型" value={model} onChange={setModel} options={[["deepseek-flash", "DeepSeek Flash"], ["gpt-current", "OpenAI · 自定义模型"]]} /><Choice label="当前权限" value={permission} onChange={setPermission} options={[["workspace", "工作区"], ["ask", "每次询问"], ["full", "Full access"]]} /><button aria-label="本轮能力" className="capabilities-button" onClick={() => { openPage("settings"); setSetting("内置工具"); }}><WandSparkles size={15} /><span>本轮能力</span></button><span className="composer-spacer" />{running ? <button className="send-button stop-button" aria-label="停止运行" onClick={() => { setScenario("completed"); setNotice("已停止样例运行；不表示副作用已回滚"); }}><Square size={13} fill="currentColor" /></button> : <button className="send-button" aria-label="发送消息" disabled={!draft.trim()} onClick={send}><ArrowUp size={20} /></button>}</div></div>
          <div className="composer-meta"><span><span className="context-meter"><i /></span>上下文 32%<button onClick={() => setNotice("正式版将先估算范围并执行手动压缩")}><History size={12} />压缩</button></span><span>{permission === "full" ? "Full access · 宿主机执行" : "Enter 发送 · Shift Enter 换行"}</span></div>
        </div></div>
      </> : <div className={`settings-layout ${mobileSettingsDetail ? "mobile-detail-active" : ""}`}><nav className="settings-nav" aria-label="设置分类"><h1>设置</h1>{settingsGroups.map(group => <section key={group.title}><h2>{group.title}</h2>{group.entries.map(name => <button key={name} className={setting === name ? "selected" : ""} onClick={() => { setSetting(name); setMobileSettingsDetail(true); }}>{name}{name === "模型与 Key" && <span>1</span>}</button>)}</section>)}</nav><section className="settings-detail"><button className="settings-back" onClick={() => setMobileSettingsDetail(false)}><ChevronRight size={14} />返回设置列表</button><div className="settings-heading"><div><p>设置 / {settingsGroups.find(group => group.entries.includes(setting))?.title}</p><h1>{setting}</h1></div><span className="scope-label">全局 · 后续运行生效</span></div>
        {setting === "模型与 Key" ? <><p className="settings-intro">管理模型服务、连接凭据和可用模型。保存后需要显式开启 Provider。</p><div className="provider-heading"><div className="provider-logo">D</div><div><strong>{providerName || "未命名 Provider"}</strong><span>OpenAI 兼容协议</span></div><Toggle label="启用 Provider" checked={enabled} onChange={setEnabled} /></div><div className="form-grid"><label>名称<input value={providerName} onChange={event => setProviderName(event.target.value)} /></label><label>服务地址<input value={providerURL} onChange={event => setProviderURL(event.target.value)} spellCheck={false} /></label><label className="full-field">API Key<input type="password" value={secret} placeholder="保留已保存的 Key；输入可替换" autoComplete="off" onChange={event => setSecret(event.target.value)} /><small>草稿在切页后保留；秘密仅在此样例内存中。</small></label></div><div className="provider-actions"><Button variant="outline" onClick={() => { setConnection(true); setNotice("样例连接成功，草稿已保留"); }}><Globe size={15} />测试连接</Button><Button onClick={() => setNotice("已保存样例配置，未修改产品设置")}>保存配置</Button></div>{connection && <div className="discovered-models"><Check size={16} /><div><strong>样例连接成功，发现 2 个模型</strong><p>deepseek-flash · deepseek-v4-pro</p></div><Button size="sm" variant="outline" onClick={() => setNotice("已将样例模型填入下方列表")}>自动填充</Button></div>}<div className="section-title"><h2>可用模型</h2><button onClick={() => setNotice("正式版将显示添加模型表单")}><Plus size={15} />添加模型</button></div><div className="model-row"><span><strong>DeepSeek Flash</strong><code>deepseek-flash</code></span><span className="default-badge">默认</span><button aria-label="删除模型" onClick={() => setNotice("正式版删除默认模型前需要选择替代模型")}><Trash2 size={15} /></button></div><p className="settings-footnote">Provider 的全局开关控制后续模型请求。运行中的任务保留自己的模型与权限快照。</p></> : setting === "上下文" ? <><p className="settings-intro">先保留当前任务和约束，再压缩可替代的历史内容。</p><div className="setting-row"><div><strong>自动压缩</strong><p>接近模型窗口时，使用同一压缩流程整理历史。</p></div><Toggle label="自动压缩" checked={autoCompact} onChange={setAutoCompact} /></div><label className="single-field">摘要预算（字节）<input type="number" defaultValue={4096} min={0} max={16384} /></label><Button onClick={() => setNotice("已保存样例压缩设置")}>保存设置</Button></> : <><p className="settings-intro">{setting === "诊断" ? "查看启动、数据库、运行时与连接状态，并导出不含正文和凭据的诊断资料。" : `集中管理${setting}。当前选择仅用于预览界面层级。`}</p><div className="setting-row"><div><strong>{setting === "数据库与备份" ? "PostgreSQL" : setting === "内置工具" ? "文件工具" : setting === "扩展" ? "AffectWeave" : "当前状态"}</strong><p>{setting === "扩展" ? "可选扩展，启用后再加载；管理页使用同一界面框架。" : "正式版读取同一 Core 的授权状态与配置。"}</p></div><span className="scope-label">{setting === "扩展" ? "未启用" : "可用"}</span></div><Button variant="outline" onClick={() => setNotice("此入口将在对应实施阶段连接 Core")}>查看详情<ChevronRight size={15} /></Button></>}
      </section></div>}
      {notice && <div className="toast" role="status"><Check size={16} />{notice}</div>}
    </main>
  </div>;
}

createRoot(document.getElementById("root")!).render(<StrictMode><App /></StrictMode>);
