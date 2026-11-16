import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { ContextMenu, DropdownMenu } from "radix-ui";
import {
  MoreHorizontal,
  Pencil,
  Pin,
  FolderInput,
  ListChecks,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogHeader,
} from "@/components/ui/dialog";
import { Choice } from "@/components/Choice";
import { api } from "@/lib/api/client";
import type { Conversation, ChatView, Space } from "@/lib/api/types";
import { downloadAttachment } from "@/lib/api/download";
import { loadNavigation, useRuntime, report } from "@/lib/store/runtime";
import { clearConversationDraft } from "@/lib/drafts";
import "@/styles/management.css";

export interface ManagedChat extends Conversation {
  lifecycle?: {
    state: string;
    trashed_at?: string;
    purge_after?: string;
    original_space?: string;
    original_title?: string;
  };
  runs: number;
  messages: number;
  attachment_bytes: number;
  attachments: number;
  cleanup_bytes: number;
  cleanup_files: number;
  retained_attempts: number;
  retained_bytes: number;
  delivery_records: number;
  unresolved: number;
  latest_entry: string;
  activity_at: string;
  blockers: string[];
  revision: string;
}
interface Page {
  items: ManagedChat[];
  total: number;
  trash_total: number;
  next_cursor?: string;
  original_spaces?: { id: string; title: string }[];
  cleanup_status?: { failed: boolean; message?: string; at: string };
}
export interface ManagementCommand {
  action: string;
  ids?: string[];
  space_id?: string;
  title?: string;
  expected_title?: string;
  replacement?: string;
  cancel_pending?: boolean;
  trash_conversations?: boolean;
}
interface Preview {
  id: string;
  command: ManagementCommand;
  items: ManagedChat[];
  warning: string;
  expires_at: string;
}
interface Result {
  id: string;
  items: {
    id: string;
    status: string;
    message: string;
    replacement?: string;
  }[];
}
interface RetainedItem {
  id: string;
  number: number;
  tool: string;
  target?: string;
  revision: string;
  resolution?: string;
  note?: string;
}
interface RetainedDelivery {
  id: string;
  cursor: string;
  status: string;
  created_at: string;
  parts: { id: string; sequence: number; kind: string; state: string }[];
}
const names: Record<string, string> = {
  trash: "移到回收站",
  restore: "恢复会话",
  purge: "永久删除",
  move: "移到空间",
  rename: "重命名",
  pin: "置顶",
  unpin: "取消置顶",
  delete_space: "删除空间",
};
const blockers: Record<string, string> = {
  unfinished_run: "本轮尚未结束",
  background_command: "后台命令需要先停止",
  queued_input: "有排队输入",
  pending_delivery: "有未发完的消息",
  dispatching: "消息正在派发，请等待回执",
  channel_route: "渠道正在使用此会话，需要选择替代会话",
};
export const size = (n: number) =>
  n >= 1048576
    ? (n / 1048576).toFixed(1) + " MB"
    : n >= 1024
      ? (n / 1024).toFixed(1) + " KB"
      : n + " B";
const when = (v?: string) =>
  v
    ? new Date(v).toLocaleString("zh-CN", {
        month: "numeric",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "—";
const changed = () => {
  window.dispatchEvent(new Event("freeagent-management-changed"));
  void loadNavigation().catch(report);
};
export async function saveConversationMetadata(command: ManagementCommand) {
  const p = await api<Preview>("conversation-management", {
    operation: "preview",
    command,
  });
  const r = await api<Result>("conversation-management", {
    operation: "execute",
    id: p.id,
  });
  if (r.items.some((i) => i.status !== "completed"))
    throw new Error(
      r.items.find((i) => i.status !== "completed")?.message || "未保存",
    );
  changed();
}
const ManagementContext = createContext<{
  open: (c: ManagementCommand) => void;
  navigate: (view: string, space?: string, selected?: string) => void;
}>({ open: () => {}, navigate: () => {} });
export const useManagement = () => useContext(ManagementContext);

export function ManagementProvider({ children }: { children: ReactNode }) {
  const state = useRuntime();
  const [command, setCommand] = useState<ManagementCommand | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null),
    [result, setResult] = useState<Result | null>(null);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const cancel = useRef<HTMLButtonElement>(null);
  const batchPreviews = useRef<Preview[]>([]);
  const stopRemaining = useRef(false);
  const [progress, setProgress] = useState("");
  const [spaceSearch, setSpaceSearch] = useState("");
  const [newSpace, setNewSpace] = useState("");
  const singleID = command?.ids?.length === 1 ? command.ids[0] : "";
  const sourceSpace =
    command?.action === "move" && singleID
      ? state.chats.find((c) => c.id === singleID)?.space_id ||
        (state.snapshot?.view.conversation.id === singleID
          ? state.snapshot.view.conversation.space_id
          : "")
      : "";
  const open = useCallback((c: ManagementCommand) => {
    setCommand(c);
    setPreview(null);
    setResult(null);
    setError("");
    batchPreviews.current = [];
    setProgress("");
    setSpaceSearch("");
    setNewSpace("");
  }, []);
  const navigate = useCallback((view: string, space = "", selected = "") => {
    window.dispatchEvent(
      new CustomEvent("freeagent-management-navigate", {
        detail: { view, space, selected },
      }),
    );
  }, []);
  const review = async () => {
    if (!command) return;
    setBusy(true);
    setError("");
    try {
      if (command.action === "rename") {
        await saveConversationMetadata(command);
        setCommand(null);
        return;
      }
      const batches: Preview[] = [];
      const ids = command.ids || [];
      for (let offset = 0; offset < Math.max(1, ids.length); offset += 500) {
        batches.push(
          await api<Preview>("conversation-management", {
            operation: "preview",
            command: ids.length
              ? { ...command, ids: ids.slice(offset, offset + 500) }
              : command,
          }),
        );
      }
      batchPreviews.current = batches;
      setPreview({ ...batches[0], items: batches.flatMap((b) => b.items) });
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };
  const execute = async () => {
    if (!preview) return;
    setBusy(true);
    setError("");
    stopRemaining.current = false;
    const completed: Result["items"] = [];
    try {
      for (const batch of batchPreviews.current.length
        ? batchPreviews.current
        : [preview]) {
        if (stopRemaining.current) break;
        const r = await api<Result>("conversation-management", {
          operation: "execute",
          id: batch.id,
        });
        completed.push(...r.items);
        setProgress(`已处理 ${completed.length} / ${preview.items.length}`);
        for (const item of r.items)
          if (preview.command.action === "purge" && item.status === "completed")
            clearConversationDraft(item.id);
        changed();
      }
      setResult({ id: preview.id, items: completed });
    } catch (e) {
      if (completed.length) setResult({ id: preview.id, items: completed });
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };
  const finish = async () => {
    if (!preview) return;
    setBusy(true);
    setError("");
    try {
      for (const batch of batchPreviews.current.length
        ? batchPreviews.current
        : [preview])
        await api("conversation-management", {
          operation: "finish",
          id: batch.id,
        });
      setPreview(null);
      changed();
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };
  const edit = (values: Partial<ManagementCommand>) => {
    setCommand((c) => c && { ...c, ...values });
    setPreview(null);
    setError("");
  };
  const createTarget = async () => {
    if (!newSpace.trim()) return;
    setBusy(true);
    setError("");
    try {
      const created = await api<{ data: Space }>("spaces", {
        kind: "space.create",
        arguments: { title: newSpace.trim() },
        confirmed: true,
      });
      await loadNavigation();
      edit({ space_id: created.data.id });
      setNewSpace("");
      setSpaceSearch("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <ManagementContext.Provider value={{ open, navigate }}>
      {children}
      <Dialog
        open={!!command}
        onOpenChange={(v) => {
          if (!v && !busy) setCommand(null);
        }}
      >
        <DialogContent
          className="management-dialog"
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            cancel.current?.focus();
          }}
          onEscapeKeyDown={(e) => {
            if (busy) e.preventDefault();
          }}
          onPointerDownOutside={(e) => {
            if (busy) e.preventDefault();
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {command ? names[command.action] : "会话管理"}
            </DialogTitle>
            <DialogDescription>
              确认具体内容后执行。移入回收站后保留 7
              天；恢复不会自动重启任务或主动发送。
            </DialogDescription>
          </DialogHeader>
          {command && !result && (
            <>
              {command.action === "rename" && (
                <label>
                  会话名称
                  <input
                    autoComplete="off"
                    maxLength={80}
                    value={command.title || ""}
                    onChange={(e) => edit({ title: e.target.value })}
                  />
                </label>
              )}
              {(command.action === "move" || command.action === "restore") && (
                <div className="management-target">
                  <label>
                    搜索目标空间
                    <input
                      value={spaceSearch}
                      onChange={(e) => setSpaceSearch(e.target.value)}
                      placeholder="按名称查找"
                    />
                  </label>
                  <label>
                    目标空间
                    <Choice
                      label="目标空间"
                      value={command.space_id || "__original"}
                      options={(
                        [
                          [
                            "__original",
                            command.action === "restore"
                              ? "原空间（已删除则回本地）"
                              : "选择目标空间",
                          ],
                          ...state.spaces
                            .filter((s) => s.id !== sourceSpace)
                            .map(
                              (s) =>
                                [
                                  s.id,
                                  s.title +
                                    (s.source === "local"
                                      ? " · 普通空间"
                                      : " · " + s.source),
                                ] as [string, string],
                            ),
                        ] as [string, string][]
                      ).filter(
                        ([id, label]) =>
                          id === "__original" ||
                          id === command.space_id ||
                          label
                            .toLocaleLowerCase()
                            .includes(spaceSearch.toLocaleLowerCase()),
                      )}
                      onChange={(v) =>
                        edit({ space_id: v === "__original" ? "" : v })
                      }
                    />
                  </label>
                  {command.action === "move" && (
                    <div className="management-new-space">
                      <label>
                        新建普通空间并移入
                        <input
                          maxLength={80}
                          value={newSpace}
                          onChange={(e) => setNewSpace(e.target.value)}
                          placeholder="新空间名称"
                        />
                      </label>
                      <Button
                        variant="outline"
                        disabled={busy || !newSpace.trim()}
                        onClick={() => void createTarget()}
                      >
                        创建并选中
                      </Button>
                    </div>
                  )}
                </div>
              )}
              {command.action === "delete_space" && (
                <label className="management-check">
                  <input
                    type="checkbox"
                    checked={command.trash_conversations !== false}
                    onChange={(e) =>
                      edit({ trash_conversations: e.target.checked })
                    }
                  />
                  同时将其中的会话移到回收站
                </label>
              )}
              {["trash", "move", "delete_space"].includes(command.action) && (
                <label className="management-check">
                  <input
                    type="checkbox"
                    checked={!!command.cancel_pending}
                    onChange={(e) => edit({ cancel_pending: e.target.checked })}
                  />
                  取消所选会话的排队输入和未派发消息
                </label>
              )}
              {["trash", "move"].includes(command.action) && (
                <label>
                  渠道替代会话
                  <Choice
                    label="渠道替代会话"
                    value={command.replacement || "__none"}
                    options={[
                      ["__none", "未选择（普通会话无需选择）"],
                      ["new", "新建并切换，保留当前能力排除"],
                      ...state.chats
                        .filter((c) => !command.ids?.includes(c.id))
                        .map((c) => [c.id, c.title] as [string, string]),
                    ]}
                    onChange={(v) =>
                      edit({ replacement: v === "__none" ? "" : v })
                    }
                  />
                </label>
              )}
              {preview && (
                <div className="management-review">
                  <p>{preview.warning}</p>
                  <p>
                    {preview.items.length} 个会话 · 托管附件{" "}
                    {size(
                      preview.items.reduce((n, c) => n + c.attachment_bytes, 0),
                    )}
                  </p>
                  <ul>
                    {preview.items.map((c) => (
                      <li key={c.id}>
                        <strong>{c.title}</strong>
                        <span>
                          {c.runs} 轮 · {c.attachments} 个附件
                          {c.unresolved
                            ? ` · ${c.unresolved} 项未确认，证据保留`
                            : ""}
                          {c.retained_attempts > 0
                            ? ` · 保留 ${c.retained_attempts} 项执行证据（约 ${size(c.retained_bytes)}）`
                            : ""}
                          {c.delivery_records > 0
                            ? ` · ${c.delivery_records} 项投递记录保留去重和回执`
                            : ""}
                        </span>
                        {c.blockers.map((b) => (
                          <small key={b}>{blockers[b] || b}</small>
                        ))}
                      </li>
                    ))}
                  </ul>
                  {preview.command.action === "purge" && (
                    <p className="destructive-text">
                      永久删除无法撤销。必要执行证据、投递去重和备份可能继续保留，磁盘文件删除失败会重试。
                    </p>
                  )}
                </div>
              )}
            </>
          )}
          {result && (
            <div className="management-result" role="status">
              <p>
                已完成{" "}
                {result.items.filter((i) => i.status === "completed").length} /{" "}
                {result.items.length}
              </p>
              <ul>
                {result.items.map((i) => (
                  <li key={i.id}>
                    {preview?.items.find((c) => c.id === i.id)?.title || "会话"}
                    ：{i.status === "completed" ? "已完成" : "未处理"}
                    <small>{i.message}</small>
                  </li>
                ))}
              </ul>
              {preview?.command.action === "trash" &&
                result.items.some((i) => i.status === "completed") && (
                  <Button
                    variant="outline"
                    onClick={() =>
                      open({
                        action: "restore",
                        ids: result.items
                          .filter((i) => i.status === "completed")
                          .map((i) => i.id),
                      })
                    }
                  >
                    撤销移入回收站
                  </Button>
                )}
            </div>
          )}
          {error && (
            <p className="management-error" role="alert">
              {error}
            </p>
          )}
          {progress && (
            <p role="status">
              {progress}
              {stopRemaining.current ? "，已停止剩余批次" : ""}
            </p>
          )}
          <div className="management-dialog-actions">
            {busy && preview && (
              <Button
                variant="outline"
                onClick={() => {
                  stopRemaining.current = true;
                  setProgress((p) => p + " · 请求停止剩余批次");
                }}
              >
                取消剩余操作
              </Button>
            )}
            <Button
              ref={cancel}
              variant="outline"
              disabled={busy}
              onClick={() => setCommand(null)}
            >
              {result ? "关闭" : "取消"}
            </Button>
            {!result && (
              <>
                {preview?.items.some(
                  (i) =>
                    i.blockers.includes("unfinished_run") ||
                    i.blockers.includes("background_command") ||
                    i.blockers.includes("queued_input") ||
                    i.blockers.includes("pending_delivery"),
                ) && (
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() => void finish()}
                  >
                    停止并结束这些任务
                  </Button>
                )}
                <Button
                  disabled={
                    busy || (command?.action === "move" && !command.space_id)
                  }
                  variant={
                    command &&
                    ["purge", "delete_space", "trash"].includes(command.action)
                      ? "destructive"
                      : "default"
                  }
                  onClick={() => void (preview ? execute() : review())}
                >
                  {busy
                    ? "正在处理…"
                    : preview
                      ? "确认" + (names[command?.action || ""] || "操作")
                      : command?.action === "rename"
                        ? "保存"
                        : "预览影响"}
                </Button>
              </>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </ManagementContext.Provider>
  );
}

export function ConversationMenu({
  chat,
  children,
  onRename,
}: {
  chat: Conversation;
  children?: ReactNode;
  onRename?: () => void;
}) {
  const { open, navigate } = useManagement();
  const actions = [
    {
      name: "重命名",
      icon: Pencil,
      run: () =>
        onRename
          ? onRename()
          : open({
              action: "rename",
              ids: [chat.id],
              title: chat.title,
              expected_title: chat.title,
            }),
    },
    {
      name: chat.pinned ? "取消置顶" : "置顶",
      icon: Pin,
      run: () =>
        void saveConversationMetadata({
          action: chat.pinned ? "unpin" : "pin",
          ids: [chat.id],
        }).catch(report),
    },
    {
      name: "移到空间…",
      icon: FolderInput,
      run: () => open({ action: "move", ids: [chat.id] }),
    },
    {
      name: "选择多个会话…",
      icon: ListChecks,
      run: () => navigate("manage", chat.space_id, chat.id),
    },
    {
      name: "移到回收站…",
      icon: Trash2,
      run: () => open({ action: "trash", ids: [chat.id] }),
    },
  ];
  const items = (context: boolean) =>
    actions.map((a) => {
      const Item = context ? ContextMenu.Item : DropdownMenu.Item;
      return (
        <Item
          key={a.name}
          className={
            "choice-item " + (a.icon === Trash2 ? "destructive-text" : "")
          }
          onSelect={a.run}
        >
          <a.icon size={16} />
          {a.name}
        </Item>
      );
    });
  const dropdown = (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="conversation-more"
          aria-label={`管理会话：${chat.title}`}
        >
          <MoreHorizontal size={18} />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="choice-menu conversation-actions-menu"
          sideOffset={4}
          collisionPadding={12}
        >
          {items(false)}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
  return children ? (
    <ContextMenu.Root>
      <ContextMenu.Trigger asChild>
        <div
          className="conversation-row"
          onContextMenu={(e) => e.stopPropagation()}
        >
          {children}
          {dropdown}
        </div>
      </ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Content className="choice-menu conversation-actions-menu">
          {items(true)}
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  ) : (
    dropdown
  );
}

export function SpaceMenu({ spaceID }: { spaceID: string }) {
  const state = useRuntime();
  const { open, navigate } = useManagement();
  const [title, setTitle] = useState("");
  const [form, setForm] = useState<"new" | "rename" | "">("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const space = state.spaces.find((s) => s.id === spaceID);
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await api("spaces", {
        kind: form === "new" ? "space.create" : "space.rename",
        arguments: {
          ...(form === "rename" ? { space_id: spaceID } : {}),
          title: title.trim(),
        },
        confirmed: true,
      });
      setForm("");
      changed();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button aria-label="管理空间" className="conversation-more">
            <MoreHorizontal size={18} />
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content className="choice-menu conversation-actions-menu">
            {space && (
              <DropdownMenu.Item
                className="choice-item"
                onSelect={() =>
                  void api<Conversation>("chats", {
                    title: "新对话",
                    space_id: spaceID,
                  })
                    .then((c) => {
                      changed();
                      navigate("chat", spaceID, c.id);
                    })
                    .catch(report)
                }
              >
                新建会话
              </DropdownMenu.Item>
            )}
            <DropdownMenu.Item
              className="choice-item"
              onSelect={() => {
                setForm("new");
                setTitle("");
              }}
            >
              新建普通空间
            </DropdownMenu.Item>
            {space && (
              <>
                <DropdownMenu.Item
                  className="choice-item"
                  onSelect={() => {
                    setForm("rename");
                    setTitle(space.title);
                  }}
                >
                  重命名空间
                </DropdownMenu.Item>
                <DropdownMenu.Item
                  className="choice-item"
                  onSelect={() => navigate("manage", spaceID)}
                >
                  管理内容…
                </DropdownMenu.Item>
                <DropdownMenu.Item
                  className="choice-item"
                  onSelect={() => navigate("settings", "spaces")}
                >
                  空间设置
                </DropdownMenu.Item>
                {spaceID !== "local" && (
                  <DropdownMenu.Item
                    className="choice-item destructive-text"
                    onSelect={() =>
                      open({
                        action: "delete_space",
                        space_id: spaceID,
                        trash_conversations: true,
                      })
                    }
                  >
                    删除空间…
                  </DropdownMenu.Item>
                )}
              </>
            )}
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
      <Dialog
        open={!!form}
        onOpenChange={(v) => {
          if (!v && !busy) setForm("");
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {form === "new" ? "新建普通空间" : "重命名空间"}
            </DialogTitle>
            <DialogDescription>
              普通空间用于归类会话，渠道绑定在渠道设置中管理。
            </DialogDescription>
          </DialogHeader>
          <label>
            空间名称
            <input
              value={title}
              maxLength={80}
              onChange={(e) => setTitle(e.target.value)}
            />
          </label>
          {error && <p role="alert">{error}</p>}
          <Button disabled={busy || !title.trim()} onClick={() => void save()}>
            保存
          </Button>
        </DialogContent>
      </Dialog>
    </>
  );
}

export default function ManagementPage({
  view,
  space,
  initialSelected = "",
  onOpenChat,
}: {
  view: string;
  space: string;
  initialSelected?: string;
  onOpenChat: (id: string) => void;
}) {
  const { open, navigate } = useManagement();
  const state = useRuntime();
  const [tab, setTab] = useState("conversations");
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [page, setPage] = useState<Page>({
    items: [],
    total: 0,
    trash_total: 0,
  });
  const [selected, setSelected] = useState<Set<string>>(
    () => new Set(initialSelected ? [initialSelected] : []),
  );
  const [query, setQuery] = useState(""),
    [age, setAge] = useState(""),
    [before, setBefore] = useState(""),
    [entry, setEntry] = useState(""),
    [includePinned, setIncludePinned] = useState(false),
    [filter, setFilter] = useState(""),
    [sort, setSort] = useState(""),
    [cursor, setCursor] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [read, setRead] = useState<ChatView | null>(null);
  const [readOpen, setReadOpen] = useState(false);
  const loadGeneration = useRef(0);
  const [evidence, setEvidence] = useState<RetainedItem[]>([]);
  const [evidenceChat, setEvidenceChat] = useState("");
  const [retainedDeliveries, setRetainedDeliveries] = useState<
    RetainedDelivery[]
  >([]);
  const [deliveryMore, setDeliveryMore] = useState(false);
  const loadRetainedDeliveries = async (id: string, append = false) => {
    const q = new URLSearchParams({ deliveries: id });
    if (append && retainedDeliveries.length)
      q.set("cursor", retainedDeliveries[retainedDeliveries.length - 1].cursor);
    const groups = await api<RetainedDelivery[]>(
      "conversation-management?" + q,
    );
    setRetainedDeliveries((old) => (append ? [...old, ...groups] : groups));
    setDeliveryMore(groups.length === 50);
  };
  const [decision, setDecision] = useState<{
    item: RetainedItem;
    action: string;
  } | null>(null);
  const loadEvidence = async (id: string, append = false) => {
    const after =
      append && evidence.length ? evidence[evidence.length - 1].number : 0;
    const list = await api<RetainedItem[]>(
      "conversation-management?evidence=" +
        encodeURIComponent(id) +
        "&after=" +
        after,
    );
    setEvidence((old) => (append ? [...old, ...list] : list));
    setEvidenceChat(id);
  };
  const resolveEvidence = async () => {
    if (!decision) return;
    setBusy(true);
    setError("");
    try {
      await api("conversation-management", {
        operation: "resolve",
        conversation_id: evidenceChat,
        item: decision.item.id,
        revision: decision.item.revision,
        resolution: decision.action,
      });
      setDecision(null);
      await loadEvidence(evidenceChat);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const load = useCallback(
    async (append = false) => {
      const generation = ++loadGeneration.current;
      setBusy(true);
      setError("");
      try {
        const q = new URLSearchParams({
          view:
            view === "trash"
              ? "trash"
              : view === "retained"
                ? "retained"
                : "active",
          space_id: space,
          q: query,
          filter,
          sort,
          entry,
          before:
            age === "custom"
              ? before
                ? new Date(before).toISOString()
                : ""
              : age
                ? new Date(Date.now() - Number(age) * 86400000).toISOString()
                : "",
          limit: "60",
        });
        if (append && cursor) q.set("cursor", cursor);
        const next = await api<Page>("conversation-management?" + q);
        if (generation !== loadGeneration.current) return;
        setPage((old) => ({
          ...next,
          items: append
            ? [
                ...new Map(
                  [...old.items, ...next.items].map((i) => [i.id, i]),
                ).values(),
              ]
            : next.items,
        }));
        setCursor(next.next_cursor || "");
      } catch (e) {
        if (generation !== loadGeneration.current) return;
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (generation === loadGeneration.current) setBusy(false);
      }
    },
    [view, space, query, filter, sort, cursor, age, before, entry],
  );
  useEffect(() => {
    void load();
    const fn = () => void load();
    window.addEventListener("freeagent-management-changed", fn);
    return () => window.removeEventListener("freeagent-management-changed", fn);
  }, [view, space, query, filter, sort, initialSelected, age, before, entry]);
  useEffect(() => {
    setSelected(new Set(initialSelected ? [initialSelected] : []));
  }, [view, space, query, filter, initialSelected, age, before, entry]);
  const toggle = (id: string) =>
    setSelected((old) => {
      const next = new Set(old);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const selectAll = async (all = false) => {
    setBusy(true);
    setError("");
    try {
      const chosen = new Set<string>();
      const visited = new Set<string>();
      const cutoff =
        age === "custom"
          ? before
            ? new Date(before).toISOString()
            : ""
          : age
            ? new Date(Date.now() - Number(age) * 86400000).toISOString()
            : "";
      let next = "";
      do {
        const q = new URLSearchParams({
          view: view === "trash" ? "trash" : "active",
          space_id: space,
          q: query,
          filter,
          sort,
          entry,
          limit: "500",
          before: cutoff,
        });
        if (next) q.set("cursor", next);
        const p = await api<Page>("conversation-management?" + q);
        p.items
          .filter((i) => view === "trash" || includePinned || !i.pinned)
          .forEach((i) => chosen.add(i.id));
        next = p.next_cursor || "";
        if (next && visited.has(next))
          throw new Error("列表在整理期间发生了变化，请刷新后重新选择。");
        if (next) visited.add(next);
        if (chosen.size > 5000)
          throw new Error(
            "一次最多整理 5000 个会话，请缩小筛选范围后分批处理。",
          );
      } while (next);
      setSelected(chosen);
      if (all) open({ action: "purge", ids: [...chosen] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const title =
    view === "trash"
      ? "回收站"
      : view === "retained"
        ? "保留的执行记录"
        : "空间内容";
  const inspect = async (chat: ManagedChat) => {
    setError("");
    if (view === "retained") {
      try {
        await Promise.all([
          loadEvidence(chat.id),
          loadRetainedDeliveries(chat.id),
        ]);
        setRead(null);
        setReadOpen(true);
      } catch (e) {
        setError(String(e));
      }
      return;
    }
    if (view !== "trash") {
      onOpenChat(chat.id);
      return;
    }
    try {
      setRead(
        await api<ChatView>(
          "conversation-management?inspect=" + encodeURIComponent(chat.id),
        ),
      );
      setEvidence([]);
      setReadOpen(true);
    } catch (e) {
      setError(String(e));
    }
  };
  return (
    <section className="management-page">
      <div className="management-page-heading">
        <div>
          <h1>{title}</h1>
          <p>
            {view === "trash"
              ? "保留 7 天；恢复不会重新启动任务或主动发送。"
              : view === "retained"
                ? "这些记录用于核验与防止重复执行，不是可恢复的会话。"
                : "按空间整理会话，勾选后移动或移到回收站。"}
          </p>
        </div>
        <SpaceMenu spaceID={space} />
      </div>
      {page.cleanup_status?.failed && (
        <p className="management-error" role="status">
          {page.cleanup_status.message ||
            "最近一次自动清理未完成，原记录保留；Owner 下次扫描会继续检查。"}
        </p>
      )}
      {view === "manage" && (
        <>
          <nav className="management-tabs" aria-label="内容分类">
            <Button
              variant={tab === "conversations" ? "default" : "outline"}
              onClick={() => setTab("conversations")}
            >
              会话
            </Button>
            <Button
              variant={tab === "attachments" ? "default" : "outline"}
              onClick={() => setTab("attachments")}
            >
              附件与结果文件
            </Button>
          </nav>
          <details className="management-location">
            <summary>工作目录与独立数据</summary>
            <p>
              工作目录：{state.environment?.workspace || "未读取"}。独立
              Memory、Skill 和扩展数据在各自设置页管理。
            </p>
            <div className="provider-actions">
              <Button
                variant="outline"
                onClick={() =>
                  void navigator.clipboard
                    .writeText(state.environment?.workspace || "")
                    .catch(report)
                }
              >
                复制工作目录路径
              </Button>
              <Button
                variant="outline"
                onClick={() => navigate("settings", "memory")}
              >
                管理 Memory
              </Button>
              <Button
                variant="outline"
                onClick={() => navigate("settings", "skills")}
              >
                管理 Skill
              </Button>
              {!!state.environment?.capabilities?.extensions.length && (
                <Button
                  variant="outline"
                  onClick={() => navigate("settings", "extensions")}
                >
                  管理扩展数据
                </Button>
              )}
            </div>
          </details>
        </>
      )}
      {tab === "attachments" && view === "manage" ? (
        <AttachmentManager space={space} />
      ) : (
        <>
          <div className="management-toolbar" data-expanded={filtersOpen}>
            {view === "manage" && (
              <Choice
                label="所在空间"
                value={space || "__all"}
                options={[
                  ["__all", "所有空间"],
                  ...state.spaces.map(
                    (s) => [s.id, s.title] as [string, string],
                  ),
                ]}
                onChange={(v) =>
                  window.dispatchEvent(
                    new CustomEvent("freeagent-management-navigate", {
                      detail: {
                        view: "manage",
                        space: v === "__all" ? "" : v,
                        selected: "",
                      },
                    }),
                  )
                }
              />
            )}
            {view === "trash" && (
              <Choice
                label="原空间"
                value={space || "__all"}
                options={[
                  ["__all", "所有原空间"],
                  ...state.spaces.map(
                    (s) => [s.id, s.title] as [string, string],
                  ),
                  ...(page.original_spaces || [])
                    .filter(
                      (s, index, all) =>
                        !state.spaces.some((current) => current.id === s.id) &&
                        all.findIndex((v) => v.id === s.id) === index,
                    )
                    .map(
                      (s) =>
                        [s.id, s.title + "（空间已删除）"] as [string, string],
                    ),
                ]}
                onChange={(v) =>
                  window.dispatchEvent(
                    new CustomEvent("freeagent-management-navigate", {
                      detail: {
                        view: "trash",
                        space: v === "__all" ? "" : v,
                        selected: "",
                      },
                    }),
                  )
                }
              />
            )}
            {view === "manage" && (
              <div className="management-extra">
                <Choice
                  label="最近活动"
                  value={age || "all"}
                  options={[
                    ["all", "所有时间"],
                    ["7", "7 天以前"],
                    ["30", "30 天以前"],
                    ["90", "90 天以前"],
                    ["custom", "指定时间以前"],
                  ]}
                  onChange={(v) => {
                    setAge(v === "all" ? "" : v);
                    setSelected(new Set());
                  }}
                />
              </div>
            )}
            {view === "manage" && age === "custom" && (
              <label className="management-extra">
                活动早于
                <input
                  type="datetime-local"
                  value={before}
                  onChange={(e) => setBefore(e.target.value)}
                />
              </label>
            )}
            {view === "manage" && (
              <div className="management-extra">
                <Choice
                  label="最近入口"
                  value={entry || "all"}
                  options={[
                    ["all", "所有入口"],
                    ["local", "本机入口（未细分）"],
                    ["telegram", "Telegram"],
                    ["lark", "飞书"],
                    ["qq", "QQ"],
                    ["unrecorded", "未记录入口"],
                  ]}
                  onChange={(v) => setEntry(v === "all" ? "" : v)}
                />
              </div>
            )}
            <label className="management-search">
              <span className="sr-only">搜索会话标题</span>
              <input
                placeholder="搜索会话标题"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setSelected(new Set());
                }}
              />
            </label>
            <div className="management-extra">
              <Choice
                label="筛选会话"
                value={filter || "all"}
                options={[
                  ["all", "全部会话"],
                  ["unpinned", "未置顶"],
                  ["pinned", "置顶"],
                  ["empty", "空会话"],
                  ["attachments", "有附件"],
                  ["branch", "分支"],
                  ["blocked", "需先处理任务"],
                ]}
                onChange={(v) => {
                  setFilter(v === "all" ? "" : v);
                  setSelected(new Set());
                }}
              />
            </div>
            <div className="management-extra">
              <Choice
                label="排序"
                value={sort || "recent"}
                options={[
                  ["recent", "最近活动"],
                  ["bytes", "附件大小"],
                  ["runs", "轮次"],
                ]}
                onChange={(v) => setSort(v === "recent" ? "" : v)}
              />
            </div>
            <Button
              className="management-filter-toggle"
              variant="outline"
              aria-expanded={filtersOpen}
              onClick={() => setFiltersOpen((v) => !v)}
            >
              筛选与排序
              {[filter, age, entry, sort].filter(Boolean).length
                ? " · " + [filter, age, entry, sort].filter(Boolean).length
                : ""}
            </Button>
          </div>
          <div className="management-stats">
            <span>
              {page.total} 个会话
              {selected.size ? ` · 已选 ${selected.size}` : ""}
            </span>
            {view !== "retained" && (
              <>
                <button
                  onClick={() =>
                    setSelected(
                      new Set(
                        page.items
                          .filter(
                            (c) =>
                              view === "trash" || includePinned || !c.pinned,
                          )
                          .map((c) => c.id),
                      ),
                    )
                  }
                >
                  选择本页
                </button>
                <button disabled={busy} onClick={() => void selectAll()}>
                  选择全部筛选结果
                </button>
                <button onClick={() => setSelected(new Set())}>取消选择</button>
                {view === "manage" && (
                  <label className="management-check">
                    <input
                      type="checkbox"
                      checked={includePinned}
                      onChange={(e) => setIncludePinned(e.target.checked)}
                    />
                    全选时包含置顶
                  </label>
                )}
              </>
            )}
            {view === "trash" && (
              <Button
                variant="outline"
                disabled={busy || !page.total}
                onClick={() => void selectAll(true)}
              >
                {space || query || filter ? "永久删除筛选结果" : "清空回收站"}
              </Button>
            )}
          </div>
          {error && (
            <p className="management-error" role="alert">
              {error}
            </p>
          )}
          <div className="management-list" aria-label={title} aria-busy={busy}>
            {!page.items.length && (
              <p className="management-empty">
                {busy ? "正在读取…" : "没有符合条件的会话"}
              </p>
            )}
            {page.items.map((chat) => (
              <div className="management-row" key={chat.id}>
                {view !== "retained" && (
                  <label className="management-select">
                    <input
                      type="checkbox"
                      checked={selected.has(chat.id)}
                      aria-label={`选择 ${chat.title}`}
                      onChange={() => toggle(chat.id)}
                    />
                  </label>
                )}
                <button
                  className="management-title"
                  onClick={() => void inspect(chat)}
                >
                  <strong>{chat.title}</strong>
                  <span>
                    {chat.lifecycle?.original_title ||
                      state.spaces.find((s) => s.id === chat.space_id)?.title ||
                      "本地空间"}{" "}
                    · {chat.runs} 轮 · {size(chat.attachment_bytes)}
                    {chat.pinned ? " · 置顶" : ""}
                    {" · " +
                      ((
                        {
                          local: "本机入口",
                          telegram: "Telegram",
                          lark: "飞书",
                          qq: "QQ",
                        } as Record<string, string>
                      )[chat.latest_entry] || "未记录入口")}
                  </span>
                </button>
                <div className="management-meta">
                  <span>
                    {view === "trash"
                      ? "自动清理 " + when(chat.lifecycle?.purge_after)
                      : when(chat.activity_at)}
                  </span>
                  {chat.blockers
                    .filter((b) => b !== "channel_route")
                    .map((b) => (
                      <small key={b}>{blockers[b] || b}</small>
                    ))}
                  {chat.unresolved > 0 && (
                    <small>{chat.unresolved} 项未确认，证据保留</small>
                  )}
                  {chat.cleanup_files > 0 && (
                    <small>
                      {chat.cleanup_files} 个文件待清理，仍占用{" "}
                      {size(chat.cleanup_bytes)}
                    </small>
                  )}
                  {view === "retained" && chat.retained_attempts > 0 && (
                    <small>
                      {chat.retained_attempts} 项执行证据 · 约{" "}
                      {size(chat.retained_bytes)}
                    </small>
                  )}
                  {view === "retained" && chat.delivery_records > 0 && (
                    <small>{chat.delivery_records} 项投递记录保留</small>
                  )}
                </div>
                {view === "trash" ? (
                  <Button
                    variant="outline"
                    onClick={() => open({ action: "restore", ids: [chat.id] })}
                  >
                    恢复
                  </Button>
                ) : view !== "retained" ? (
                  <ConversationMenu chat={chat} />
                ) : null}
              </div>
            ))}
          </div>
          {cursor && (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => void load(true)}
            >
              加载更多
            </Button>
          )}
          {view !== "retained" && selected.size > 0 && (
            <div className="management-batch">
              <span>已选 {selected.size}</span>
              {view === "trash" ? (
                <>
                  <Button
                    variant="outline"
                    disabled={!selected.size || busy}
                    onClick={() =>
                      open({ action: "restore", ids: [...selected] })
                    }
                  >
                    恢复所选
                  </Button>
                  <Button
                    variant="destructive"
                    disabled={!selected.size || busy}
                    onClick={() =>
                      open({ action: "purge", ids: [...selected] })
                    }
                  >
                    永久删除所选
                  </Button>
                </>
              ) : (
                <>
                  <Button
                    variant="outline"
                    disabled={!selected.size}
                    onClick={() => open({ action: "move", ids: [...selected] })}
                  >
                    移到空间
                  </Button>
                  <Button
                    variant="outline"
                    disabled={!selected.size}
                    onClick={() => open({ action: "pin", ids: [...selected] })}
                  >
                    置顶
                  </Button>
                  <Button
                    variant="destructive"
                    disabled={!selected.size}
                    onClick={() =>
                      open({ action: "trash", ids: [...selected] })
                    }
                  >
                    移到回收站
                  </Button>
                </>
              )}
            </div>
          )}
        </>
      )}
      <Dialog open={readOpen} onOpenChange={setReadOpen}>
        <DialogContent className="management-dialog">
          <DialogHeader>
            <DialogTitle>{read ? "只读会话" : "保留的执行记录"}</DialogTitle>
            <DialogDescription>查看不会启动模型或扩展。</DialogDescription>
          </DialogHeader>
          {read ? (
            <div className="management-readonly">
              {read.has_earlier && (
                <Button
                  variant="outline"
                  onClick={() =>
                    void api<ChatView>(
                      "conversation-management?" +
                        new URLSearchParams({
                          inspect: read.conversation.id,
                          before: read.before || "",
                        }),
                    )
                      .then(setRead)
                      .catch(report)
                  }
                >
                  查看更早轮次
                </Button>
              )}
              {read.turns?.map((turn) => (
                <div key={turn.key}>
                  {turn.messages.map((m) => (
                    <article key={m.key}>
                      <strong>{m.role === "user" ? "你" : "助手"}</strong>
                      <p>{m.raw}</p>
                      {m.attachments?.map((ref) => (
                        <Button
                          key={ref.id}
                          variant="outline"
                          onClick={() =>
                            void downloadAttachment(
                              read.conversation.id,
                              ref,
                              true,
                            ).catch(report)
                          }
                        >
                          下载回收站附件：{ref.name}
                        </Button>
                      ))}
                    </article>
                  ))}
                </div>
              ))}
            </div>
          ) : (
            <div className="management-readonly">
              {!evidence.length && (
                <p>没有未确认项；可能仅保留投递回执或去重标识。</p>
              )}
              <Button
                variant="outline"
                onClick={() =>
                  void Promise.all([
                    loadEvidence(evidenceChat),
                    loadRetainedDeliveries(evidenceChat),
                  ]).catch(report)
                }
              >
                刷新保留记录
              </Button>
              {evidence.map((item) => (
                <article key={item.id}>
                  <strong>
                    #{item.number} {item.tool}
                  </strong>
                  <p>
                    {item.target || "原操作"} ·{" "}
                    {item.resolution || "结果未确认"}
                  </p>
                  <div className="management-dialog-actions">
                    <Button
                      variant="outline"
                      onClick={() =>
                        setDecision({ item, action: "user_confirmed_applied" })
                      }
                    >
                      确认已生效
                    </Button>
                    <Button
                      variant="outline"
                      onClick={() =>
                        setDecision({
                          item,
                          action: "user_confirmed_not_applied",
                        })
                      }
                    >
                      确认未生效
                    </Button>
                    <Button
                      variant="outline"
                      onClick={() => setDecision({ item, action: "ignored" })}
                    >
                      结束提醒
                    </Button>
                  </div>
                </article>
              ))}
              {evidence.length > 0 && evidence.length % 50 === 0 && (
                <Button
                  variant="outline"
                  onClick={() =>
                    void loadEvidence(evidenceChat, true).catch(report)
                  }
                >
                  读取更多记录
                </Button>
              )}
              {retainedDeliveries.map((group) => (
                <article key={group.id}>
                  <strong>投递记录 · {when(group.created_at)}</strong>
                  <p>
                    {group.parts
                      .map(
                        (part) =>
                          `第 ${part.sequence} 条：${({ sent: "已确认送达", unknown: "结果未知", cancelled: "已取消", failed: "失败", dispatching: "正在派发" } as Record<string, string>)[part.state] || part.state}`,
                      )
                      .join("；")}
                  </p>
                  <small>
                    只保留回执与防重复派发事实，不从已删除会话重新发送。
                  </small>
                </article>
              ))}
              {deliveryMore && (
                <Button
                  variant="outline"
                  onClick={() =>
                    void loadRetainedDeliveries(evidenceChat, true).catch(
                      report,
                    )
                  }
                >
                  读取更早投递记录
                </Button>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!decision}
        onOpenChange={(v) => {
          if (!v && !busy) setDecision(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认处理 #{decision?.item.number}</DialogTitle>
            <DialogDescription>
              只记录你的确认或结束提醒，不重做原操作。确认未生效可解除此项额外防重放限制，其他权限规则继续适用。
            </DialogDescription>
          </DialogHeader>
          <p>
            {decision?.action === "ignored"
              ? "结束提醒，保留未知事实与防重放。"
              : decision?.action === "user_confirmed_applied"
                ? "记录为用户确认已生效。"
                : "记录为用户确认未生效。"}
          </p>
          {error && <p role="alert">{error}</p>}
          <div className="management-dialog-actions">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setDecision(null)}
            >
              取消
            </Button>
            <Button disabled={busy} onClick={() => void resolveEvidence()}>
              确认保存
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </section>
  );
}

interface ManagedFile {
  ref: { id: string; name: string; bytes: number; sha256: string };
  conversation_id: string;
  title: string;
  references: number;
  deleted: boolean;
}
function AttachmentManager({ space }: { space: string }) {
  const [items, setItems] = useState<ManagedFile[]>([]),
    [selected, setSelected] = useState<Set<string>>(new Set());
  const [cursor, setCursor] = useState(""),
    [review, setReview] = useState<ManagedFile[]>([]);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const load = async (append = false) => {
    setBusy(true);
    setError("");
    try {
      const q = new URLSearchParams({ attachments: "1", space_id: space });
      if (append && cursor) q.set("cursor", cursor);
      const page = await api<{ items: ManagedFile[]; next_cursor?: string }>(
        "conversation-management?" + q,
      );
      setItems((old) => (append ? [...old, ...page.items] : page.items));
      setCursor(page.next_cursor || "");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    setSelected(new Set());
    void load();
  }, [space]);
  const remove = async () => {
    setBusy(true);
    setError("");
    let done = 0;
    try {
      for (const file of review) {
        await api("attachments/delete", {
          conversation_id: file.conversation_id,
          id: file.ref.id,
          sha256: file.ref.sha256,
        });
        done++;
      }
      setReview([]);
      setSelected(new Set());
      setResult(`已永久删除 ${done} 个托管文件。`);
      await load();
    } catch (e) {
      setError(
        `已处理 ${done} 个，剩余未处理。` +
          (e instanceof Error ? e.message : String(e)),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="management-files">
      <p>
        单独删除附件会立即撤销文件引用，无法从回收站恢复。已复制的分支附件和已发送给外部平台的副本不受影响。
      </p>
      {error && (
        <p role="alert" className="management-error">
          {error}
        </p>
      )}
      {result && <p role="status">{result}</p>}
      <div className="management-list">
        {!items.length && (
          <p className="management-empty">
            {busy ? "正在读取…" : "没有托管附件"}
          </p>
        )}
        {items.map((f) => (
          <div className="management-row" key={f.ref.id}>
            <label className="management-select">
              <input
                type="checkbox"
                aria-label={`选择附件 ${f.ref.name}`}
                disabled={f.deleted}
                checked={selected.has(f.ref.id)}
                onChange={() =>
                  setSelected((old) => {
                    const next = new Set(old);
                    if (next.has(f.ref.id)) next.delete(f.ref.id);
                    else next.add(f.ref.id);
                    return next;
                  })
                }
              />
            </label>
            <div className="management-title">
              <strong>{f.ref.name}</strong>
              <span>
                {f.title} · {size(f.ref.bytes)} · {f.references} 条消息引用
              </span>
            </div>
            <span>{f.deleted ? "已删除或等待文件清理" : "可管理"}</span>
          </div>
        ))}
      </div>
      {cursor && (
        <Button
          variant="outline"
          disabled={busy}
          onClick={() => void load(true)}
        >
          加载更多附件
        </Button>
      )}
      <div className="management-batch">
        <span>已选 {selected.size}</span>
        <Button
          variant="destructive"
          disabled={!selected.size || busy}
          onClick={() => setReview(items.filter((f) => selected.has(f.ref.id)))}
        >
          永久删除所选附件
        </Button>
      </div>
      <Dialog
        open={!!review.length}
        onOpenChange={(v) => {
          if (!v && !busy) setReview([]);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>永久删除 {review.length} 个附件？</DialogTitle>
            <DialogDescription>
              只删除 Core
              托管文件，消息文字仍保留。删除不能撤销；必要执行记录可能继续保留。
            </DialogDescription>
          </DialogHeader>
          <p>
            {size(review.reduce((n, f) => n + f.ref.bytes, 0))} ·{" "}
            {review.reduce((n, f) => n + f.references, 0)} 条消息引用将不可用
          </p>
          <div className="management-dialog-actions">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setReview([])}
            >
              取消
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void remove()}
            >
              {busy ? "正在删除…" : "确认永久删除"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
