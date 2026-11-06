import type { ChatSnapshot, CoreEvent, ControlOperation } from "./types.ts";
function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
const fail = () => {
  throw new Error("Core 返回的数据结构无法识别，请保留记录并刷新客户端。");
};
export function decodeSnapshot(value: unknown): ChatSnapshot {
  if (
    !object(value) ||
    !object(value.view) ||
    !object(value.view.conversation) ||
    typeof value.view.conversation.id !== "string" ||
    typeof value.view.conversation.space_id !== "string" ||
    !object(value.view.permission) ||
    typeof value.view.permission.mode !== "string" ||
    !object(value.view.capabilities) ||
    !Array.isArray(value.view.turns) ||
    !Array.isArray(value.streams) ||
    typeof value.busy !== "boolean"
  )
    return fail();
  for (const turn of value.view.turns) {
    if (
      !object(turn) ||
      typeof turn.key !== "string" ||
      typeof turn.state !== "string" ||
      !Array.isArray(turn.messages) ||
      !Array.isArray(turn.activity)
    )
      return fail();
    for (const message of turn.messages)
      if (
        !object(message) ||
        typeof message.key !== "string" ||
        typeof message.role !== "string" ||
        typeof message.raw !== "string" ||
        (message.attachments !== undefined &&
          !Array.isArray(message.attachments))
      )
        return fail();
    for (const item of turn.activity)
      if (
        !object(item) ||
        typeof item.key !== "string" ||
        typeof item.kind !== "string" ||
        typeof item.status !== "string"
      )
        return fail();
  }
  for (const stream of value.streams)
    if (
      !object(stream) ||
      typeof stream.attempt !== "string" ||
      typeof stream.run !== "string" ||
      typeof stream.raw !== "string"
    )
      return fail();
  if (
    value.snapshot_seq !== undefined &&
    (!Number.isSafeInteger(value.snapshot_seq) ||
      Number(value.snapshot_seq) < 0)
  )
    return fail();
  return value as unknown as ChatSnapshot;
}
export function decodeEvent(value: unknown): CoreEvent {
  if (
    !object(value) ||
    typeof value.kind !== "string" ||
    (value.seq !== undefined &&
      (!Number.isSafeInteger(value.seq) || Number(value.seq) < 0)) ||
    (value.message !== undefined && typeof value.message !== "string") ||
    (value.generation !== undefined && typeof value.generation !== "string")
  )
    return fail();
  return value as unknown as CoreEvent;
}

export function decodeControlOperation(value: unknown): ControlOperation {
  if (
    !object(value) ||
    typeof value.operation_id !== "string" ||
    !value.operation_id ||
    typeof value.kind !== "string" ||
    typeof value.preview !== "string" ||
    typeof value.status !== "string"
  )
    return fail();
  return value as unknown as ControlOperation;
}
