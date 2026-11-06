import { useState, type ReactNode } from "react";
import { ContextMenu } from "radix-ui";
import {
  editableTarget,
  menuAvailability,
} from "@/lib/presentation/desktop.mjs";
import { api } from "@/lib/api/client";
import { current, loadNavigation, report } from "@/lib/store/runtime";

export function DesktopMenu({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<Element | null>(null),
    [selection, setSelection] = useState("");
  if (!window.faOpenExternal) return children;
  const editable = editableTarget(target) as
    | HTMLTextAreaElement
    | HTMLInputElement
    | null;
  const available = menuAvailability(target, selection),
    chatID = target?.closest<HTMLElement>("[data-chat]")?.dataset.chat;
  const chat = current().chats.find((chat) => chat.id === chatID);
  async function action(kind: string) {
    try {
      if (kind === "pin" && chat) {
        await api(`chats/${chat.id}/pin`, { pinned: !chat.pinned });
        await loadNavigation();
        return;
      }
      if (kind === "select") {
        if (editable) {
          editable.focus();
          editable.select();
        } else {
          const root = target?.closest(
            ".user-message,.assistant-message,.markdown",
          );
          if (root) {
            const range = document.createRange();
            range.selectNodeContents(root);
            const selected = window.getSelection();
            selected?.removeAllRanges();
            selected?.addRange(range);
          }
        }
        return;
      }
      if (kind === "paste" && editable) {
        const text = await navigator.clipboard.readText();
        editable.setRangeText(
          text,
          editable.selectionStart || 0,
          editable.selectionEnd || 0,
          "end",
        );
        editable.dispatchEvent(new Event("input", { bubbles: true }));
        editable.focus();
        return;
      }
      const text =
        kind === "link"
          ? target?.closest<HTMLAnchorElement>("a[href]")?.href || ""
          : editable
            ? editable.value.slice(
                editable.selectionStart || 0,
                editable.selectionEnd || 0,
              )
            : selection;
      await navigator.clipboard.writeText(text);
      if (kind === "cut" && editable) {
        editable.setRangeText(
          "",
          editable.selectionStart || 0,
          editable.selectionEnd || 0,
          "start",
        );
        editable.dispatchEvent(new Event("input", { bubbles: true }));
        editable.focus();
      }
    } catch (error) {
      report(error);
    }
  }
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger asChild>
        <div
          className="native-shell"
          onContextMenuCapture={(event) => {
            setTarget(event.target as Element);
            setSelection(window.getSelection()?.toString() || "");
          }}
        >
          {children}
        </div>
      </ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Content className="choice-menu" aria-label="编辑菜单">
          {[
            ["cut", "剪切", available.cut],
            ["copy", "复制", available.copy],
            ["paste", "粘贴", available.paste],
            ["select", "全选", true],
            ["link", "复制链接", available.copyLink],
            ...(chat
              ? [["pin", chat.pinned ? "取消置顶会话" : "置顶会话", true]]
              : []),
          ].map(([id, label, enabled]) => (
            <ContextMenu.Item
              key={String(id)}
              className="choice-item"
              disabled={!enabled}
              onSelect={() => void action(String(id))}
            >
              {label}
            </ContextMenu.Item>
          ))}
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  );
}
