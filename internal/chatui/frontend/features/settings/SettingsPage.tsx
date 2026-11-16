import { useEffect, useRef, useState, type ReactNode } from "react";
import { ArrowLeft, ChevronDown, Moon, Search, Sun, X } from "lucide-react";
import Providers from "./Providers";
import SearchSettings from "./SearchSettings";
import RuntimeSettings from "./RuntimeSettings";
import {
  MCPSettings,
  SkillSettings,
  ExtensionSettings,
} from "./CapabilitySettings";
import CommandSessions from "./CommandSessions";
import PublicSettings from "./PublicSettings";
import ChannelSettings from "./ChannelSettings";
import SpacesSettings from "./SpacesSettings";
import MemorySettings from "./MemorySettings";
import AttachmentSettings from "./AttachmentSettings";
import DataSettings from "./DataSettings";
import GeneralSettings from "./GeneralSettings";
import AdvancedSettings from "./AdvancedSettings";
import DeliverySettings from "./DeliverySettings";
import ManagementPage from "@/features/conversations/Management";
import { settingsGroups as groups } from "./navigation";
import type { ViewPreferences } from "@/lib/platform";
const scopes: Record<string, [string, string]> = {
  general: ["当前客户端", "外观与布局即时保存，仅影响当前客户端。"],
  search: [
    "后续任务",
    "搜索方式、连接和预算保存后生效；正在执行的任务保留原快照。",
  ],
  spaces: ["所选空间", "管理空间和会话。授权与当前选中的空间分别保存。"],
  "space-content": [
    "空间与会话",
    "归类、移动或清理会话；空间信息与权限在“空间设置”中分别保存。",
  ],
  trash: ["会话回收站", "保留 7 天；恢复不会重新启动任务或主动发送。"],
  retained: [
    "保留的执行证据",
    "这些记录用于核验与防止重复执行，不是可恢复的会话。",
  ],
  memory: ["按条目范围", "每条内容有自己的可见范围；固定与会话选择分别生效。"],
  extensions: [
    "扩展与会话",
    "全局启用决定扩展是否可用；当前对话选择决定是否参与回答。",
  ],
  providers: [
    "Provider 与模型",
    "启用开关即时保存；连接配置、模型与思考参数分别保存。",
  ],
  context: [
    "Core 默认设置",
    "保存后用于后续请求。当前对话的占用与手动压缩在输入区查看。",
  ],
  sessions: ["当前 Owner", "查看正在运行的后台命令；停止操作会影响对应进程。"],
  channels: ["所选渠道", "连接、准入和空间权限分别设置。"],
  delivery: ["所选目标", "只向明确授权的目标投递，主动规则单独开启。"],
  data: ["当前数据目录", "状态来自当前 Core，备份与恢复各自单独执行。"],
  diagnosis: ["当前 Core", "检查当前运行环境与连接状态。"],
};
const keywords: Record<string, string> = {
  general: "主题 深色 浅色 外观 宽度 布局 工作区",
  providers: "provider api key 密钥 连接 思考 token 模型",
  context: "压缩 摘要 token 长度 上限 预算 恢复",
  permissions: "full 授权 沙箱",
  commands: "command_run 沙箱 宿主机 超时 输出",
  files: "路径 白名单 工作区",
  tools: "网络 搜索 开关",
  extensions: "affectweave 人物 插件",
  public: "登录 密码 cookie ip 黑名单 白名单",
  channels: "telegram tg lark qq bot",
  search: "联网 搜索 search fetch tavily brave deepseek 网页 来源",
  data: "postgresql pg 备份 恢复",
  memory: "记忆 固定 上下文",
  delivery: "主动 跟进 发送",
  "space-content": "空间 会话 分类 移动 分组 文件夹 附件 空间设置 授权",
  trash: "回收站 删除 恢复 清空 7 天",
  retained: "保留 执行 证据 未决 结果未知 unknown 投递",
};
export default function SettingsPage({
  section,
  onSection,
  preferences,
  onPreferences,
  header,
  space,
  initialSelected,
  onOpenChat,
  onReturnChat,
}: {
  section: string;
  onSection: (section: string) => void;
  preferences: ViewPreferences;
  onPreferences: (value: ViewPreferences) => void;
  header: ReactNode;
  space: string;
  initialSelected: string;
  onOpenChat: (id: string) => void;
  onReturnChat: () => void;
}) {
  // The old spaces URL remains a direct link to its existing settings form.
  const activeSection = section === "spaces" ? "space-content" : section;
  const activeGroup = groups.find((group) =>
    group.entries.some((entry) => entry.id === activeSection),
  )?.id;
  const [expanded, setExpanded] = useState(
    () => new Set(activeGroup ? [activeGroup] : []),
  );
  const [detail, setDetail] = useState(
    () =>
      new URLSearchParams(location.search).has("section") ||
      ["manage", "trash", "retained"].includes(
        new URLSearchParams(location.search).get("view") || "",
      ),
  );
  const [query, setQuery] = useState("");
  const content = useRef<HTMLElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const navigation = useRef<HTMLElement>(null);
  useEffect(() => {
    const pop = () => {
      const params = new URLSearchParams(location.search);
      setDetail(
        params.has("section") ||
          ["manage", "trash", "retained"].includes(params.get("view") || ""),
      );
    };
    window.addEventListener("popstate", pop);
    return () => window.removeEventListener("popstate", pop);
  }, []);
  useEffect(() => {
    content.current?.scrollTo({ top: 0 });
  }, [section]);
  useEffect(() => {
    if (activeGroup) setExpanded((old) => new Set([...old, activeGroup]));
  }, [activeGroup, section]);
  const [scope, scopeHint] = scopes[activeSection] || [
    "Core 默认设置",
    "保存后用于后续任务；正在执行的任务保留已采用的配置。",
  ];
  const visibleGroups = groups
    .map((group) => ({
      ...group,
      entries: group.entries.filter(({ id, name }) =>
        `${group.title} ${name} ${keywords[id] || ""}`
          .toLowerCase()
          .includes(query.trim().toLowerCase()),
      ),
    }))
    .filter((group) => group.entries.length);
  const title =
    groups
      .flatMap((item) => item.entries)
      .find((entry) => entry.id === activeSection)?.name || "设置";
  return (
    <div className={`settings-layout ${detail ? "mobile-detail-active" : ""}`}>
      <nav className="settings-nav" aria-label="设置分类" ref={navigation}>
        <div className="settings-nav-heading">
          <h1>设置</h1>
          <button className="settings-nav-return" onClick={onReturnChat}>
            <ArrowLeft size={14} aria-hidden="true" />
            返回对话
          </button>
        </div>
        <div className="settings-search">
          <Search size={15} aria-hidden="true" />
          <input
            aria-label="查找设置"
            placeholder="查找设置…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          {query && (
            <button
              type="button"
              aria-label="清除设置搜索"
              onClick={() => setQuery("")}
            >
              <X size={15} aria-hidden="true" />
            </button>
          )}
        </div>
        <div className="settings-nav-groups">
          {visibleGroups.map((group) => {
            const open = !!query.trim() || expanded.has(group.id);
            return (
              <section key={group.id} className="settings-nav-group">
                <h2>
                  <button
                    className="settings-group-toggle"
                    aria-expanded={open}
                    aria-controls={`settings-group-${group.id}`}
                    onClick={() =>
                      setExpanded((old) => {
                        const next = new Set(old);
                        if (next.has(group.id)) next.delete(group.id);
                        else next.add(group.id);
                        return next;
                      })
                    }
                  >
                    <span>{group.title}</span>
                    <ChevronDown
                      size={14}
                      aria-hidden="true"
                      className="settings-group-chevron"
                    />
                  </button>
                </h2>
                <div
                  id={`settings-group-${group.id}`}
                  className="settings-group-items"
                  hidden={!open}
                >
                  {group.entries.map(({ id, name, icon: Icon }) => (
                    <button
                      key={id}
                      className={`settings-nav-item ${activeSection === id ? "selected" : ""}`}
                      aria-current={activeSection === id ? "page" : undefined}
                      aria-controls="settings-detail"
                      onClick={() => {
                        onSection(id);
                        setDetail(true);
                        requestAnimationFrame(() =>
                          heading.current?.focus({ preventScroll: true }),
                        );
                      }}
                    >
                      <Icon size={17} aria-hidden="true" />
                      <span>{name}</span>
                    </button>
                  ))}
                </div>
              </section>
            );
          })}
          {!visibleGroups.length && (
            <p className="form-hint">没有匹配的设置。</p>
          )}
        </div>
        <div className="settings-nav-footer">
          <span>FreeAgent</span>
          <button
            aria-label="切换主题"
            onClick={() =>
              onPreferences({
                ...preferences,
                theme: preferences.theme === "light" ? "dark" : "light",
              })
            }
          >
            {preferences.theme === "light" ? (
              <Moon size={16} />
            ) : (
              <Sun size={16} />
            )}
          </button>
        </div>
      </nav>
      <div className="settings-main">
        {header}
        <section
          id="settings-detail"
          className="settings-detail"
          ref={content}
          aria-label={title}
        >
          <div className="settings-content">
            <button
              className="settings-back"
              onClick={() => {
                setDetail(false);
                const url = new URL(location.href);
                url.searchParams.delete("section");
                history.pushState({}, "", url);
                requestAnimationFrame(() =>
                  navigation.current
                    ?.querySelector<HTMLButtonElement>("[aria-current]")
                    ?.focus(),
                );
              }}
            >
              <ArrowLeft size={14} aria-hidden="true" />
              返回设置列表
            </button>
            <div className="settings-heading">
              <h1 ref={heading} tabIndex={-1}>
                {title}
              </h1>
              <span className="scope-label">{scope}</span>
            </div>
            <p className="settings-scope-hint">{scopeHint}</p>
            {section === "advanced" ? (
              <AdvancedSettings />
            ) : section === "general" ? (
              <GeneralSettings value={preferences} onChange={onPreferences} />
            ) : activeSection === "space-content" ? (
              <>
                <nav className="settings-space-tabs" aria-label="空间内容页面">
                  <button
                    aria-current={section !== "spaces" ? "page" : undefined}
                    onClick={() => onSection("space-content")}
                  >
                    会话列表
                  </button>
                  <button
                    aria-current={section === "spaces" ? "page" : undefined}
                    onClick={() => onSection("spaces")}
                  >
                    空间设置
                  </button>
                </nav>
                {section === "spaces" ? (
                  <SpacesSettings />
                ) : (
                  <ManagementPage
                    embedded
                    view="manage"
                    space={space}
                    initialSelected={initialSelected}
                    onOpenChat={onOpenChat}
                  />
                )}
              </>
            ) : section === "trash" || section === "retained" ? (
              <ManagementPage
                key={section}
                embedded
                view={section}
                space={space}
                onOpenChat={onOpenChat}
              />
            ) : section === "search" ? (
              <SearchSettings />
            ) : section === "channels" ? (
              <ChannelSettings />
            ) : section === "delivery" ? (
              <DeliverySettings />
            ) : section === "public" ? (
              <PublicSettings />
            ) : section === "memory" ? (
              <MemorySettings />
            ) : section === "attachments" ? (
              <AttachmentSettings />
            ) : section === "data" || section === "diagnosis" ? (
              <DataSettings key={section} diagnosis={section === "diagnosis"} />
            ) : section === "providers" ? (
              <Providers />
            ) : section === "mcp" ? (
              <MCPSettings />
            ) : section === "skills" ? (
              <SkillSettings />
            ) : section === "extensions" ? (
              <ExtensionSettings />
            ) : section === "sessions" ? (
              <CommandSessions />
            ) : [
                "permissions",
                "files",
                "commands",
                "tools",
                "context",
              ].includes(section) ? (
              <RuntimeSettings key={section} section={section} />
            ) : (
              <p className="inline-error">未找到此设置页，请从左侧选择。</p>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}
