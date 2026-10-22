import { useCallback, useEffect, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";

import { LOADED_BUILD_ID, RATE_NOTICE_MS } from "../constants";
import { countMissedMessages, mergeBySeq } from "../messages";
import type { EventMessage, Profile } from "../types";

interface UseChatSocketOptions {
  setMessages: Dispatch<SetStateAction<EventMessage[]>>;
  onFirstSnapshot: (historyLength: number, truncated: boolean | undefined) => void;
  onMissed: (count: number) => void;
  onIncoming: () => void;
  onBuildChange: () => void;
}

/**
 * 连接、重连和全部下行事件都在这里。消息数组仍由调用方持有，历史分页才能直接
 * 往同一个 setMessages 里前插。
 */
export function useChatSocket({
  setMessages,
  onFirstSnapshot,
  onMissed,
  onIncoming,
  onBuildChange,
}: UseChatSocketOptions) {
  const [user, setUser] = useState<Profile | null>(null);
  const [onlineCount, setOnlineCount] = useState(1);
  const [connected, setConnected] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [rateNotice, setRateNotice] = useState("");
  const [typingMap, setTypingMap] = useState<Record<string, string>>({});

  const wsRef = useRef<WebSocket | null>(null);
  const lastSeqRef = useRef(0);

  // 回调放在 ref 里，避免它们变化时触发 WebSocket 重连。
  const callbacksRef = useRef({ onFirstSnapshot, onMissed, onIncoming, onBuildChange });

  useEffect(() => {
    callbacksRef.current = { onFirstSnapshot, onMissed, onIncoming, onBuildChange };
  }, [onBuildChange, onFirstSnapshot, onIncoming, onMissed]);

  // Identity comes from the server: a httpOnly cookie holds the session, and this
  // endpoint returns the name/emoji the server will stamp on every message.
  useEffect(() => {
    let cancelled = false;

    fetch("/api/session", { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
      .then((profile: Profile) => {
        if (!cancelled) setUser(profile);
      })
      .catch((err) => console.error("获取会话失败:", err));

    return () => {
      cancelled = true;
    };
  }, []);

  const showNotice = useCallback((message: string) => {
    setRateNotice(message);
    window.setTimeout(() => setRateNotice(""), RATE_NOTICE_MS);
  }, []);

  const sessionID = user?.sessionId ?? "";

  useEffect(() => {
    if (!sessionID) return;

    let disposed = false;
    let socket: WebSocket | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let stableTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;

    const handlePayload = (raw: string) => {
      let payload: EventMessage;

      try {
        payload = JSON.parse(raw);
      } catch (err) {
        console.error("Failed to parse:", err);
        return;
      }

      if (payload.type === "snapshot") {
        const history = payload.history ?? [];
        const firstSnapshot = lastSeqRef.current === 0;

        // 快照带着服务端认定的"我是谁"，每次（重）连接都以它为准。
        // 用函数式更新比较，而不是读闭包里的 user：
        //  - 读 user 会让 effect 依赖 user，setUser 后又触发重连（无限循环）
        //  - 读旧 user 又会拿到过期值（exhaustive-deps 报的正是这个）
        // 只有身份真的变了才写 state，否则返回同一个对象，不触发渲染。
        if (payload.me) {
          const me = payload.me;
          setUser((prev) =>
            prev &&
            prev.sessionId === me.sessionId &&
            prev.name === me.name &&
            prev.emoji === me.emoji
              ? prev
              : me,
          );
        }

        // 部署后旧页面重连会看到新的 buildId：先标记，等输入区空闲再刷新。
        // 开发模式下 Vite 的 index.html 没有 meta，因此两边都为空时不会误触发。
        if (LOADED_BUILD_ID && payload.buildId && payload.buildId !== LOADED_BUILD_ID) {
          callbacksRef.current.onBuildChange();
        }

        // 首次连接用快照替换；重连时只补进缺失的部分。
        // 首次快照的 50 条是服务端内存上限，可能还有更早的库内历史。
        if (firstSnapshot) {
          setMessages(history);
          callbacksRef.current.onFirstSnapshot(history.length, payload.truncated);
        } else {
          const previousLastSeq = lastSeqRef.current;
          setMessages((prev) => mergeBySeq(prev, history));

          // 断线期间漏掉的消息也要计入“X 条新消息”，而不是只算实时消息。
          callbacksRef.current.onMissed(
            countMissedMessages(history, previousLastSeq, sessionID),
          );
        }

        if (payload.truncated) {
          console.warn("部分消息已过期，未能补发");
        }

        let newest = payload.seq ?? 0;
        for (const msg of history) {
          if (typeof msg.seq === "number" && msg.seq > newest) newest = msg.seq;
        }
        lastSeqRef.current = Math.max(lastSeqRef.current, newest);

        if (payload.online !== undefined) setOnlineCount(payload.online);
      } else if (payload.type === "presence") {
        if (payload.online !== undefined) setOnlineCount(payload.online);
      } else if (payload.type === "typing") {
        if (payload.clientId && payload.clientId !== sessionID) {
          setTypingMap((prev) => {
            const next = { ...prev };
            if (payload.isTyping && payload.sender) {
              next[payload.clientId!] = payload.sender;
            } else {
              delete next[payload.clientId!];
            }
            return next;
          });
        }
      } else if (payload.type === "profile") {
        // 运营者改了某个会话的昵称：把已经加载的消息也一起改掉，历史气泡不需要刷新。
        if (payload.clientId && payload.name) {
          const prefix = payload.clientId;
          const sender = payload.name;

          setMessages((prev) =>
            prev.map((msg) => (msg.clientId?.startsWith(prefix) ? { ...msg, sender } : msg)),
          );
        }

        // 如果是我自己，立刻重新取一次身份
        if (sessionID && payload.clientId && sessionID.startsWith(payload.clientId)) {
          fetch("/api/session", { credentials: "same-origin" })
            .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
            .then((profile: Profile) => setUser(profile))
            .catch(() => {});
        }
      } else if (payload.type === "notice") {
        // 服务端限流时的提示，2 秒后自动消失
        showNotice(payload.notice ?? "");
      } else if (payload.type === "delete") {
        // 运营者删除了某条消息：已加载的列表里也立即去掉，不需要刷新。
        if (typeof payload.seq === "number") {
          setMessages((prev) => prev.filter((message) => message.seq !== payload.seq));
        }
      } else if (payload.type === "chat" || payload.type === "image") {
        // 补发与实时消息可能重叠，按 seq 丢弃已见过的。
        if (typeof payload.seq === "number" && payload.seq <= lastSeqRef.current) return;

        if (typeof payload.seq === "number") lastSeqRef.current = payload.seq;

        setMessages((prev) => mergeBySeq(prev, [payload]));

        // 只有不在底部时才累计“新消息”；在底部时上面的 layout effect 会跟随。
        if (payload.clientId && payload.clientId !== sessionID) {
          callbacksRef.current.onIncoming();
        }

        if (payload.clientId) {
          setTypingMap((prev) => {
            const next = { ...prev };
            delete next[payload.clientId!];
            return next;
          });
        }
      }
    };

    const scheduleRetry = () => {
      if (disposed) return;

      // 指数退避 + 随机抖动：服务重启时所有客户端同时重连会打满监听队列。
      const backoff = Math.min(30_000, 500 * 2 ** attempt);
      const delay = Math.round(backoff * (0.5 + Math.random() * 0.5));
      attempt += 1;
      setRetrying(true);
      retryTimer = setTimeout(connect, delay);
    };

    const connect = () => {
      if (disposed) return;

      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const since = lastSeqRef.current;
      const query = since > 0 ? `?since=${since}` : "";
      const ws = new WebSocket(`${protocol}//${window.location.host}/ws${query}`);
      socket = ws;
      wsRef.current = ws;

      ws.onopen = () => {
        setConnected(true);
        setRetrying(false);

        // 只有稳定保持 5 秒才清零退避。否则"连上→立刻被拆→重连"的循环会一直
        // 以最快速度重试（曾把服务端打到 14 次/秒）；加了护栏后同样的循环会
        // 自动升到 30 秒一次。
        if (stableTimer) clearTimeout(stableTimer);
        stableTimer = setTimeout(() => {
          attempt = 0;
        }, 5000);
      };

      ws.onclose = () => {
        if (stableTimer) {
          clearTimeout(stableTimer);
          stableTimer = null;
        }

        setConnected(false);
        scheduleRetry();
      };

      ws.onerror = () => {
        // onclose 紧随其后，重连逻辑放在那里，避免触发两次。
        ws.close();
      };

      ws.onmessage = (event) => {
        if (disposed) return;

        // 服务端会把多条消息合并到一帧（换行分隔），逐行解析。
        for (const line of String(event.data).split("\n")) {
          if (line) handlePayload(line);
        }
      };
    };

    connect();

    return () => {
      disposed = true;
      if (retryTimer) clearTimeout(retryTimer);
      if (stableTimer) clearTimeout(stableTimer);
      if (socket) {
        socket.onclose = null;
        socket.close();
      }
    };
  }, [sessionID, setMessages, showNotice]);

  const canSend = useCallback(() => {
    const ws = wsRef.current;
    return !!ws && ws.readyState === WebSocket.OPEN;
  }, []);

  const sendTyping = useCallback((isTyping: boolean) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ type: "typing", isTyping }));
  }, []);

  const sendChat = useCallback((content: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify({ type: "chat", content }));
    return true;
  }, []);

  const sendImage = useCallback((id: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify({ type: "image", content: id }));
    return true;
  }, []);

  return {
    user,
    onlineCount,
    connected,
    retrying,
    rateNotice,
    typingMap,
    showNotice,
    canSend,
    sendTyping,
    sendChat,
    sendImage,
  };
}
