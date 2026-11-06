import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import {
  commandEntries,
  filterCommands,
} from "../../lib/presentation/commands.mjs";
import { report } from "@/lib/store/runtime";
export type SlashEntry = ReturnType<typeof commandEntries>[number];
export function SlashCommands({
  query,
  disabled,
  onChoose,
}: {
  query: string | null;
  disabled: boolean;
  onChoose: (entry: SlashEntry) => void;
}) {
  const [entries, setEntries] = useState<SlashEntry[]>([]);
  const open = query !== null;
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    Promise.all([
      api<{ skills: never[] }>(
        "skills",
        undefined,
        undefined,
        controller.signal,
      ),
      api<{ extensions: never[] }>(
        "extensions",
        undefined,
        undefined,
        controller.signal,
      ),
    ])
      .then(([skills, extensions]) =>
        setEntries(
          commandEntries({
            skills: skills.skills,
            extensions: extensions.extensions,
            builtins: true,
          }),
        ),
      )
      .catch((error) => {
        if (!controller.signal.aborted) report(error);
      });
    return () => controller.abort();
  }, [open]);
  if (!open) return null;
  const visible: SlashEntry[] = filterCommands(entries, query || "").slice(
    0,
    30,
  );
  return (
    <div className="slash-commands" role="region" aria-label="可用命令">
      <h2>可用命令</h2>
      {visible.length ? (
        visible.map((entry) => (
          <button
            type="button"
            disabled={disabled}
            key={entry.id || entry.kind + ":" + entry.name + ":" + entry.source}
            onClick={() => onChoose(entry)}
          >
            <strong>/{entry.name}</strong>
            <span>{entry.description}</span>
            <small>{entry.source}</small>
          </button>
        ))
      ) : (
        <p>没有匹配命令。可在设置中启用 Skill 或扩展。</p>
      )}
    </div>
  );
}
