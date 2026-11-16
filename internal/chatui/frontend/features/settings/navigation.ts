import {
  SlidersHorizontal,
  Cpu,
  ShieldCheck,
  Folder,
  Terminal,
  SquareTerminal,
  Layers,
  Wrench,
  Plug,
  BookOpen,
  Notebook,
  Blocks,
  Radio,
  Send,
  Database,
  Paperclip,
  Activity,
  Settings2,
  ListChecks,
  Trash2,
  History,
  Globe,
  type LucideIcon,
} from "lucide-react";

interface SettingsEntry {
  id: string;
  name: string;
  icon: LucideIcon;
}

export const settingsGroups: {
  id: string;
  title: string;
  entries: SettingsEntry[];
}[] = [
  {
    id: "workbench",
    title: "工作台设置",
    entries: [
      { id: "general", name: "通用", icon: SlidersHorizontal },
      { id: "providers", name: "模型与 Key", icon: Cpu },
      { id: "permissions", name: "权限与执行", icon: ShieldCheck },
      { id: "files", name: "文件范围", icon: Folder },
      { id: "commands", name: "命令执行", icon: Terminal },
      { id: "sessions", name: "后台命令", icon: SquareTerminal },
      { id: "context", name: "上下文", icon: Layers },
    ],
  },
  {
    id: "capabilities",
    title: "能力管理",
    entries: [
      { id: "tools", name: "内置工具", icon: Wrench },
      { id: "mcp", name: "MCP", icon: Plug },
      { id: "skills", name: "Skill", icon: BookOpen },
      { id: "memory", name: "Memory 与固定内容", icon: Notebook },
      { id: "extensions", name: "扩展", icon: Blocks },
    ],
  },
  {
    id: "connections",
    title: "连接与数据",
    entries: [
      { id: "channels", name: "渠道", icon: Radio },
      { id: "search", name: "联网搜索", icon: Globe },
      { id: "delivery", name: "投递授权", icon: Send },
      { id: "public", name: "公网安全", icon: ShieldCheck },
      { id: "data", name: "数据库与备份", icon: Database },
      { id: "attachments", name: "附件", icon: Paperclip },
      { id: "diagnosis", name: "诊断", icon: Activity },
      { id: "advanced", name: "高级策略", icon: Settings2 },
    ],
  },
  {
    id: "spaces",
    title: "空间管理",
    entries: [
      { id: "space-content", name: "空间内容", icon: ListChecks },
      { id: "trash", name: "回收站", icon: Trash2 },
      { id: "retained", name: "保留的执行记录", icon: History },
    ],
  },
];
