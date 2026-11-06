import { useRef, useState } from "react";
import { MessageSquare, Pin } from "lucide-react";
import type { Conversation } from "@/lib/api/types";

// Fixed-height, single-line navigation only. Message history stays native so
// browser selection, find and pagination keep their normal semantics.
export function Navigation({
  chats,
  selected,
  onOpen,
}: {
  chats: Conversation[];
  selected: string;
  onOpen: (id: string) => void;
}) {
  const root = useRef<HTMLDivElement>(null);
  const [top, setTop] = useState(0),
    [height, setHeight] = useState(600);
  const rowHeight = 46,
    start = Math.max(0, Math.floor(top / rowHeight) - 8),
    end = Math.min(chats.length, Math.ceil((top + height) / rowHeight) + 8);
  return (
    <div
      className="chat-navigation"
      ref={root}
      aria-label="会话列表"
      role="list"
      onScroll={(event) => {
        setTop(event.currentTarget.scrollTop);
        setHeight(event.currentTarget.clientHeight);
      }}
      onKeyDown={(event) => {
        if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key))
          return;
        const current = Number(
          (event.target as HTMLElement).closest<HTMLElement>("[data-index]")
            ?.dataset.index || 0,
        );
        const next =
          event.key === "Home"
            ? 0
            : event.key === "End"
              ? chats.length - 1
              : Math.max(
                  0,
                  Math.min(
                    chats.length - 1,
                    current + (event.key === "ArrowDown" ? 1 : -1),
                  ),
                );
        if (!root.current || !chats.length) return;
        event.preventDefault();
        const container = root.current;
        if (next * rowHeight < container.scrollTop)
          container.scrollTop = next * rowHeight;
        else if (
          (next + 1) * rowHeight >
          container.scrollTop + container.clientHeight
        )
          container.scrollTop = (next + 1) * rowHeight - container.clientHeight;
        setTop(container.scrollTop);
        setHeight(container.clientHeight);
        requestAnimationFrame(() =>
          container
            .querySelector<HTMLButtonElement>(`[data-index="${next}"] button`)
            ?.focus(),
        );
      }}
    >
      <div
        className="nav-virtual-spacer"
        style={{ height: chats.length * rowHeight }}
      >
        {chats.slice(start, end).map((chat, offset) => (
          <div
            role="listitem"
            aria-posinset={start + offset + 1}
            aria-setsize={chats.length}
            data-index={start + offset}
            key={chat.id}
            data-chat={chat.id}
            style={{
              position: "absolute",
              top: (start + offset) * rowHeight,
              left: 0,
              right: 0,
              height: rowHeight,
            }}
          >
            <button
              className={`conversation ${selected === chat.id ? "active" : ""}`}
              onClick={() => onOpen(chat.id)}
            >
              <MessageSquare size={16} />
              <span>{chat.title}</span>
              {chat.pinned && <Pin size={12} />}
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
