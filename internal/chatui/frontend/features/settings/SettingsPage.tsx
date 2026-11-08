import { useState } from "react";
import { ChevronRight } from "lucide-react";
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
  const title =
    groups
      .flatMap((item) => item.entries)
      .find(([id]) => id === section)?.[1] || "设置";
  return (
    <div className={`settings-layout ${detail ? "mobile-detail-active" : ""}`}>
      <nav className="settings-nav" aria-label="设置分类">
        <h1>设置</h1>
        {groups.map((group) => (
          <section key={group.title}>
            <h2>{group.title}</h2>
            {group.entries.map(([id, name]) => (
              <button
                key={id}
                className={section === id ? "selected" : ""}
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
      </nav>
      <section className="settings-detail">
        <button className="settings-back" onClick={() => setDetail(false)}>
          <ChevronRight size={14} />
          返回设置列表
        </button>
        <div className="settings-heading">
          <h1>{title}</h1>
          <span className="scope-label">全局设置</span>
        </div>
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
