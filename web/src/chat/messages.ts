import { SNAPSHOT_HISTORY } from "./constants";
import type { EventMessage } from "./types";

// 旧消息页是升序的，直接按 seq 合并并排序，避免依赖到达顺序。
export const mergeOlder = (prev: EventMessage[], older: EventMessage[]): EventMessage[] => {
  const bySeq = new Map<number, EventMessage>();

  for (const msg of [...older, ...prev]) {
    if (typeof msg.seq === "number") bySeq.set(msg.seq, msg);
  }

  return [...bySeq.values()].sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
};

// 实时消息与补发可能重叠，按 seq 去重；非 chat/image 事件没有 seq，原样保留。
export const mergeBySeq = (prev: EventMessage[], incoming: EventMessage[]): EventMessage[] => {
  const seen = new Set<number>();
  const merged: EventMessage[] = [];

  for (const msg of [...prev, ...incoming]) {
    if ((msg.type === "chat" || msg.type === "image") && typeof msg.seq === "number") {
      if (seen.has(msg.seq)) continue;
      seen.add(msg.seq);
    }
    merged.push(msg);
  }

  return merged;
};

export const snapshotHasOlder = (historyLength: number, truncated: boolean | undefined): boolean =>
  historyLength >= SNAPSHOT_HISTORY || truncated === true;

// 断线期间漏掉的消息也要计入“X 条新消息”，而不是只算实时消息。
export const countMissedMessages = (
  history: EventMessage[],
  afterSeq: number,
  selfID: string,
): number =>
  history.filter(
    (message) =>
      (message.type === "chat" || message.type === "image") &&
      typeof message.seq === "number" &&
      message.seq > afterSeq &&
      message.clientId !== selfID,
  ).length;
