import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import * as stylex from "@stylexjs/stylex";

import { Composer } from "./chat/components/Composer";
import { FxOverlay } from "./chat/components/FxOverlay";
import { JumpButton } from "./chat/components/JumpButton";
import { Lightbox } from "./chat/components/Lightbox";
import { MessageStream } from "./chat/components/MessageStream";
import { PanelHeader } from "./chat/components/PanelHeader";
import {
  DRAFT_KEY,
  FX_DURATION_MS,
  HISTORY_PAGE_SIZE,
  LOADED_BUILD_ID,
  PROGRAMMATIC_SCROLL_TIMEOUT_MS,
  RATE_NOTICE_MS,
  RELOAD_DELAY_MS,
  SCROLL_NEAR_BOTTOM,
  SFX_WORDS,
  TYPING_DEBOUNCE_MS,
  readDraft,
} from "./chat/constants";
import {
  countMissedMessages,
  mergeBySeq,
  mergeOlder,
  snapshotHasOlder,
} from "./chat/messages";
import { styles } from "./chat/styles";
import type {
  EffectItem,
  EventMessage,
  HistoryResponse,
  PrependAnchor,
  Profile,
} from "./chat/types";

export default function App() {
  // Identity comes from the server: a httpOnly cookie holds the session, and this
  // endpoint returns the name/emoji the server will stamp on every message.
  const [user, setUser] = useState<Profile | null>(null);

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

  const [messages, setMessages] = useState<EventMessage[]>([]);
  const [onlineCount, setOnlineCount] = useState<number>(1);
  const [input, setInput] = useState(readDraft);
  const [connected, setConnected] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [rateNotice, setRateNotice] = useState("");
  const [updateNotice, setUpdateNotice] = useState("");
  const [uploading, setUploading] = useState(false);
  // 点击图片后全屏查看
  const [zoomed, setZoomed] = useState<string | null>(null);
  // 历史分页 / 滚动位置
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState(false);
  const [atBottom, setAtBottom] = useState(true);
  const [newMessages, setNewMessages] = useState(0);
  // 已收到的最大消息序号：重连时带上它，服务端只补发之后的消息。
  const lastSeqRef = useRef(0);

  // 正在输入的人员清单 (clientId -> senderName)
  const [typingMap, setTypingMap] = useState<Record<string, string>>({});

  // 动效队列
  const [effects, setEffects] = useState<EffectItem[]>([]);

  const wsRef = useRef<WebSocket | null>(null);
  const scrollRef = useRef<HTMLUListElement | null>(null);
  const topSentinelRef = useRef<HTMLLIElement | null>(null);
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inputRef = useRef(input);
  const uploadingRef = useRef(false);
  const pendingReloadRef = useRef(false);
  const reloadTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const stickToBottomRef = useRef(true);
  const initialScrollDoneRef = useRef(false);
  const pendingPrependRef = useRef<PrependAnchor | null>(null);
  const loadingOlderRef = useRef(false);
  const forceBottomRef = useRef(false);
  const programmaticScrollRef = useRef(false);
  const programmaticScrollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (reloadTimerRef.current) clearTimeout(reloadTimerRef.current);
      if (programmaticScrollTimerRef.current) clearTimeout(programmaticScrollTimerRef.current);
    };
  }, []);

  // 服务端 build 变化后，尽量在不丢输入的前提下刷新：
  // 空闲时自动 reload；有草稿或正在上传时等状态结束，由对应 handler 再次调用。
  const maybeReload = useCallback(() => {
    if (!pendingReloadRef.current) return;

    if (uploadingRef.current) {
      setUpdateNotice("服务端已更新，上传完成后自动刷新…");
      return;
    }

    if (inputRef.current.trim() !== "") {
      setUpdateNotice("服务端已更新，发送后自动刷新…");
      return;
    }

    setUpdateNotice("服务端已更新，正在刷新…");

    if (reloadTimerRef.current) return;

    reloadTimerRef.current = setTimeout(() => {
      reloadTimerRef.current = null;

      try {
        sessionStorage.setItem(DRAFT_KEY, inputRef.current);
      } catch {
        // ignore
      }

      window.location.reload();
    }, RELOAD_DELAY_MS);
  }, []);

  // 触发本地拟声词爆发
  const triggerFx = () => {
    const text = SFX_WORDS[Math.floor(Math.random() * SFX_WORDS.length)];
    const rotation = Math.floor(Math.random() * 26) - 13;
    const id = crypto.randomUUID();
    setEffects((prev) => [...prev, { id, text, rotation }]);

    setTimeout(() => {
      setEffects((prev) => prev.filter((item) => item.id !== id));
    }, FX_DURATION_MS);
  };

  const broadcastTyping = (isTyping: boolean) => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
    const payload: EventMessage = {
      type: "typing",
      isTyping,
    };
    wsRef.current.send(JSON.stringify(payload));
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setInput(value);
    inputRef.current = value;

    try {
      if (value) sessionStorage.setItem(DRAFT_KEY, value);
      else sessionStorage.removeItem(DRAFT_KEY);
    } catch {
      // ignore
    }

    // 用户开始输入时取消已经排队的刷新；否则会在他打字时 reload。
    if (reloadTimerRef.current) {
      clearTimeout(reloadTimerRef.current);
      reloadTimerRef.current = null;
    }

    // 广播正在输入
    broadcastTyping(true);

    // 防抖：1.8秒没有新键入，广播停止输入
    if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);
    typingTimeoutRef.current = setTimeout(() => {
      broadcastTyping(false);
    }, TYPING_DEBOUNCE_MS);

    // 输入被清空（例如草稿删掉）时，如果服务端有新版本就立即刷新。
    maybeReload();
  };

  const cancelProgrammaticScroll = useCallback(() => {
    programmaticScrollRef.current = false;

    if (programmaticScrollTimerRef.current) {
      clearTimeout(programmaticScrollTimerRef.current);
      programmaticScrollTimerRef.current = null;
    }
  }, []);

  const beginProgrammaticScroll = useCallback(() => {
    programmaticScrollRef.current = true;

    if (programmaticScrollTimerRef.current) {
      clearTimeout(programmaticScrollTimerRef.current);
    }

    // Fallback: if the browser does not emit a final scroll event (already at
    // bottom, animation cancelled), do not leave the guard stuck forever.
    programmaticScrollTimerRef.current = setTimeout(() => {
      programmaticScrollTimerRef.current = null;
      programmaticScrollRef.current = false;
    }, PROGRAMMATIC_SCROLL_TIMEOUT_MS);
  }, []);

  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;

    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    const bottom = distance < SCROLL_NEAR_BOTTOM;

    // 平滑滚动的中间位置离底部很远，不能因此把 stick 改成 false。
    if (programmaticScrollRef.current) {
      if (bottom) {
        cancelProgrammaticScroll();
        stickToBottomRef.current = true;
        setAtBottom(true);
        setNewMessages(0);
      }

      return;
    }

    stickToBottomRef.current = bottom;
    setAtBottom(bottom);

    if (bottom) setNewMessages(0);
  }, [cancelProgrammaticScroll]);

  const scrollToBottom = useCallback(
    (behavior: ScrollBehavior = "smooth") => {
      const el = scrollRef.current;
      if (!el) return;

      beginProgrammaticScroll();
      el.scrollTo({ top: el.scrollHeight, behavior });
      stickToBottomRef.current = true;
      setAtBottom(true);
      setNewMessages(0);
    },
    [beginProgrammaticScroll],
  );

  // 只在第一次拿到历史时跳到底部；之后是否跟随由滚动位置决定。
  // 旧消息前插时用 pendingPrependRef 保持视口不动，避免“加载更早”把页面顶飞。
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;

    const pending = pendingPrependRef.current;
    if (pending) {
      pendingPrependRef.current = null;

      // 用户刚发送消息或点了“回到底部”时，发送优先于旧消息前插的锚定；
      // 否则保持当前视口，避免加载历史把页面顶飞。
      if (!stickToBottomRef.current) {
        const keepViewport = () => {
          if (scrollRef.current !== el) return;

          if (pending.anchorSeq) {
            const anchor = el.querySelector<HTMLElement>(`[data-seq="${pending.anchorSeq}"]`);
            if (anchor) {
              el.scrollTop += anchor.getBoundingClientRect().top - pending.anchorTop;
              return;
            }
          }

          // 锚点不可用时退回滚动高度差；比不调整好。
          el.scrollTop = el.scrollHeight - pending.scrollHeight + pending.scrollTop;
        };

        keepViewport();
        requestAnimationFrame(keepViewport);
        return;
      }
    }

    const scrollToEnd = () => {
      if (scrollRef.current === el) el.scrollTop = el.scrollHeight;
    };

    // 发送消息后等回显真正进入列表，再按真实高度落到底部。这里用瞬时定位
    // 而不是 smooth：平滑滚动的目标在执行时就固定了，之后气泡真实高度变化
    // 会让它停在“元信息可见、气泡被截一半”的位置。
    if (forceBottomRef.current) {
      forceBottomRef.current = false;
      stickToBottomRef.current = true;
      setNewMessages(0);
      setAtBottom(true);

      const settle = () => {
        if (scrollRef.current === el) el.scrollTop = el.scrollHeight;
      };

      settle();
      requestAnimationFrame(settle);
      return;
    }

    if (!initialScrollDoneRef.current) {
      if (messages.length === 0) return;

      initialScrollDoneRef.current = true;
      stickToBottomRef.current = true;
      scrollToEnd();
      requestAnimationFrame(scrollToEnd);
      return;
    }

    if (stickToBottomRef.current) {
      scrollToEnd();
      requestAnimationFrame(scrollToEnd);
    }
  }, [beginProgrammaticScroll, messages]);

  const loadOlder = useCallback(async () => {
    if (loadingOlderRef.current || !hasMore) return;

    const oldest = messages.find((message) => typeof message.seq === "number")?.seq;
    if (!oldest || oldest <= 1) {
      setHasMore(false);
      return;
    }

    loadingOlderRef.current = true;
    setLoadingOlder(true);
    setOlderError(false);

    try {
      const res = await fetch(`/api/history?before=${oldest}&limit=${HISTORY_PAGE_SIZE}`, {
        credentials: "same-origin",
      });
      if (!res.ok) throw new Error(`history ${res.status}`);

      const page = (await res.json()) as HistoryResponse;
      const older = page.messages ?? [];

      if (older.length > 0) {
        const el = scrollRef.current;
        if (el) {
          const anchor = el.querySelector<HTMLElement>("[data-seq]");

          pendingPrependRef.current = {
            anchorSeq: anchor?.dataset.seq ?? "",
            anchorTop: anchor?.getBoundingClientRect().top ?? 0,
            scrollHeight: el.scrollHeight,
            scrollTop: el.scrollTop,
          };
        }

        setMessages((prev) => mergeOlder(prev, older));
      }

      setHasMore(Boolean(page.hasMore));
    } catch {
      setOlderError(true);
    } finally {
      loadingOlderRef.current = false;
      setLoadingOlder(false);
    }
  }, [hasMore, messages]);

  // 滚到顶部附近时自动加载上一页；按钮仍然保留，加载失败也可以手动重试。
  useEffect(() => {
    if (!hasMore) return;

    const root = scrollRef.current;
    const target = topSentinelRef.current;
    if (!root || !target) return;

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) void loadOlder();
      },
      { root, rootMargin: "240px 0px 0px 0px" },
    );

    observer.observe(target);

    return () => observer.disconnect();
  }, [hasMore, loadOlder]);

  // 连接只跟"会话"绑定：改名不应导致重连。
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
          pendingReloadRef.current = true;
          setUpdateNotice("服务端已更新，正在检查…");
          maybeReload();
        }

        // 首次连接用快照替换；重连时只补进缺失的部分。
        // 首次快照的 50 条是服务端内存上限，可能还有更早的库内历史。
        if (firstSnapshot) {
          setMessages(history);
          setHasMore(snapshotHasOlder(history.length, payload.truncated));
        } else {
          const previousLastSeq = lastSeqRef.current;
          setMessages((prev) => mergeBySeq(prev, history));

          // 断线期间漏掉的消息也要计入“X 条新消息”，而不是只算实时消息。
          if (!stickToBottomRef.current) {
            const missed = countMissedMessages(history, previousLastSeq, sessionID);

            if (missed > 0) setNewMessages((count) => count + missed);
          }
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
        setRateNotice(payload.notice ?? "");
        window.setTimeout(() => setRateNotice(""), RATE_NOTICE_MS);
      } else if (payload.type === "chat" || payload.type === "image") {
        // 补发与实时消息可能重叠，按 seq 丢弃已见过的。
        if (typeof payload.seq === "number" && payload.seq <= lastSeqRef.current) return;

        if (typeof payload.seq === "number") lastSeqRef.current = payload.seq;

        setMessages((prev) => mergeBySeq(prev, [payload]));

        // 只有不在底部时才累计“新消息”；在底部时上面的 layout effect 会跟随。
        if (payload.clientId && payload.clientId !== sessionID && !stickToBottomRef.current) {
          setNewMessages((count) => count + 1);
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
  }, [maybeReload, sessionID]);

  // 粘贴图片：读取剪贴板里的文件 → 上传 → 再发一条 image 消息
  const uploadAndSend = async (file: File) => {
    const socket = wsRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) return;

    if (reloadTimerRef.current) {
      clearTimeout(reloadTimerRef.current);
      reloadTimerRef.current = null;
    }

    uploadingRef.current = true;
    setUploading(true);

    if (pendingReloadRef.current) {
      setUpdateNotice("服务端已更新，上传完成后自动刷新…");
    }

    try {
      const form = new FormData();
      form.append("file", file);

      const res = await fetch("/api/upload", {
        method: "POST",
        credentials: "same-origin",
        body: form,
      });

      if (!res.ok) {
        const detail = await res.json().catch(() => ({}));
        setRateNotice(detail.error ?? `上传失败（${res.status}）`);
        window.setTimeout(() => setRateNotice(""), RATE_NOTICE_MS);

        return;
      }

      const media = (await res.json()) as { id: string };

      // 图片占一条独立消息，content 只放 media id
      socket.send(JSON.stringify({ type: "image", content: media.id }));
    } catch {
      setRateNotice("上传失败，请重试");
      window.setTimeout(() => setRateNotice(""), RATE_NOTICE_MS);
    } finally {
      uploadingRef.current = false;
      setUploading(false);
      maybeReload();
    }
  };

  const handlePaste = (e: React.ClipboardEvent<HTMLInputElement>) => {
    const files = Array.from(e.clipboardData?.files ?? []);
    const image = files.find((f) => f.type.startsWith("image/"));

    if (!image) return; // 普通文本粘贴走默认行为

    e.preventDefault();
    void uploadAndSend(image);
  };

  const sendMessage = (e: React.FormEvent) => {
    e.preventDefault();
    if (!input.trim() || !wsRef.current) return;

    // 立即解除输入状态
    if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);

    if (reloadTimerRef.current) {
      clearTimeout(reloadTimerRef.current);
      reloadTimerRef.current = null;
    }

    broadcastTyping(false);

    // sender/clientId/time are stamped by the server from the session cookie.
    const payload: EventMessage = {
      type: "chat",
      content: input.trim(),
    };

    wsRef.current.send(JSON.stringify(payload));
    setInput("");
    inputRef.current = "";

    try {
      sessionStorage.removeItem(DRAFT_KEY);
    } catch {
      // ignore
    }

    // 自己发消息时回到底部：先即时跳到当前底部，回显到达后再按真实高度校正。
    // 不用 smooth，避免发送时的滚动目标和回显后的新高度打架。
    forceBottomRef.current = true;
    scrollToBottom("auto");

    // 如果服务端已经发过新版本，发完这条就刷新；消息已经进入 socket 队列。
    maybeReload();

    triggerFx(); // 触发波普拟声词动效
  };

  // 格式化输入中提示文字
  const typingUsers = Object.values(typingMap);

  return (
    <div {...stylex.props(styles.comicPage)}>
      <div {...stylex.props(styles.comicPanel)}>
        <PanelHeader
          buildId={LOADED_BUILD_ID}
          connected={connected}
          retrying={retrying}
          onlineCount={onlineCount}
        />

        <MessageStream
          messages={messages}
          selfSessionId={sessionID}
          scrollRef={scrollRef}
          sentinelRef={topSentinelRef}
          hasMore={hasMore}
          loadingOlder={loadingOlder}
          olderError={olderError}
          onLoadOlder={() => void loadOlder()}
          onScroll={handleScroll}
          onWheel={cancelProgrammaticScroll}
          onTouchStart={cancelProgrammaticScroll}
          onZoom={(src) => setZoomed(src)}
        />

        <JumpButton
          visible={!atBottom}
          count={newMessages}
          onClick={() => scrollToBottom("smooth")}
        />

        <FxOverlay effects={effects} />
        <Lightbox src={zoomed} onClose={() => setZoomed(null)} />

        <Composer
          input={input}
          connected={connected}
          uploading={uploading}
          rateNotice={rateNotice}
          updateNotice={updateNotice}
          typingUsers={typingUsers}
          user={user}
          onInputChange={handleInputChange}
          onPaste={handlePaste}
          onSubmit={sendMessage}
        />
      </div>
    </div>
  );
}
