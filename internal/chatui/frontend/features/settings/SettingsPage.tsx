import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Search } from "lucide-react";
import Providers from "./Providers";
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
import type { ViewPreferences } from "@/lib/platform";
const scopes: Record<string, [string, string]> = {
  general: ["当前客户端", "外观与布局即时保存，仅影响当前客户端。"],
  spaces: ["所选空间", "管理空间和会话。授权与当前选中的空间分别保存。"],
  memory: ["按条目范围", "每条内容有自己的可见范围；固定与会话选择分别生效。"],
  extensions: [
    "扩展与会话",
    "全局启用决定扩展是否可用；当前对话选择决定是否参与回答。",
  ],
  sessions: ["当前 Owner", "查看正在运行的后台命令；停止操作会影响对应进程。"],
  channels: ["所选渠道", "连接、准入和空间权限分别设置。"],
  delivery: ["所选目标", "只向明确授权的目标投递，主动规则单独开启。"],
  data: ["当前数据目录", "状态来自当前 Core，备份与恢复各自单独执行。"],
  diagnosis: ["当前 Core", "检查当前运行环境与连接状态。"],
};
const groups = [
  {
    title: "工作台",
    entries: [
      ["general", "通用"],
      ["spaces", "空间与会话"],
      ["providers", "模型与 Key"],
      ["permissions", "权限与执行"],
      ["files", "文件范围"],
      ["commands", "命令执行"],
      ["sessions", "后台命令"],
      ["context", "上下文"],
    ],
  },
  {
    title: "能力",
    entries: [
      ["tools", "内置工具"],
      ["mcp", "MCP"],
      ["skills", "Skill"],
      ["memory", "Memory 与固定内容"],
      ["extensions", "扩展"],
    ],
  },
  {
    title: "连接与数据",
    entries: [
      ["channels", "渠道"],
      ["delivery", "投递授权"],
      ["public", "公网安全"],
      ["data", "数据库与备份"],
      ["attachments", "附件"],
      ["diagnosis", "诊断"],
      ["advanced", "高级策略"],
    ],
  },
];
export default function SettingsPage({
  section,
  onSection,
  preferences,
  onPreferences,
}: {
  section: string;
  onSection: (section: string) => void;
  preferences: ViewPreferences;
  onPreferences: (value: ViewPreferences) => void;
}) {
  const [detail, setDetail] = useState(false);
  const [query, setQuery] = useState("");
  const content = useRef<HTMLElement>(null);
  useEffect(() => {
    content.current?.scrollTo({ top: 0 });
  }, [section]);
  const [scope, scopeHint] = scopes[section] || [
    "Core 默认设置",
    "保存后用于后续任务；正在执行的任务保留已采用的配置。",
  ];
  const visibleGroups = groups
    .map((group) => ({
      ...group,
      entries: group.entries.filter(([, name]) =>
        name.toLowerCase().includes(query.trim().toLowerCase()),
      ),
    }))
    .filter((group) => group.entries.length);
  const title =
    groups
      .flatMap((item) => item.entries)
      .find(([id]) => id === section)?.[1] || "设置";
  return (
    <div className={`settings-layout ${detail ? "mobile-detail-active" : ""}`}>
      <nav className="settings-nav" aria-label="设置分类">
        <h1>设置</h1>
        <label className="settings-search">
          <Search size={15} aria-hidden="true" />
          <input
            aria-label="查找设置"
            placeholder="查找设置…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        {visibleGroups.map((group) => (
          <section key={group.title}>
            <h2>{group.title}</h2>
            {group.entries.map(([id, name]) => (
              <button
                key={id}
                className={section === id ? "selected" : ""}
                aria-current={section === id ? "page" : undefined}
                onClick={() => {
                  onSection(id);
                  setDetail(true);
                }}
              >
                {name}
              </button>
            ))}
          </section>
        ))}
        {!visibleGroups.length && <p className="form-hint">没有匹配的设置。</p>}
      </nav>
      <section className="settings-detail" ref={content} aria-label={title}>
        <button className="settings-back" onClick={() => setDetail(false)}>
          <ArrowLeft size={14} aria-hidden="true" />
          返回设置列表
        </button>
        <div className="settings-heading">
          <h1>{title}</h1>
          <span className="scope-label">{scope}</span>
        </div>
        <p className="settings-scope-hint">{scopeHint}</p>
        {section === "advanced" ? (
          <AdvancedSettings />
        ) : section === "general" ? (
          <GeneralSettings value={preferences} onChange={onPreferences} />
        ) : section === "spaces" ? (
          <SpacesSettings />
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
        ) : ["permissions", "files", "commands", "tools", "context"].includes(
            section,
          ) ? (
          <RuntimeSettings key={section} section={section} />
        ) : (
          <p className="inline-error">未找到此设置页，请从左侧选择。</p>
        )}
      </section>
    </div>
  );
}
