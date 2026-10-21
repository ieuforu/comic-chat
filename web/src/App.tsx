import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import * as stylex from "@stylexjs/stylex";

interface EventMessage {
  type: "chat" | "image" | "snapshot" | "presence" | "typing" | "notice" | "profile";
  clientId?: string;
  sender?: string;
  content?: string;
  time?: string;
  history?: EventMessage[];
  online?: number;
  isTyping?: boolean;
  seq?: number;
  truncated?: boolean;
  buildId?: string;
  connections?: number;
  notice?: string;
  name?: string;
  me?: Profile;
}

interface Profile {
  sessionId: string;
  name: string;
  emoji: string;
}

interface HistoryResponse {
  messages?: EventMessage[];
  hasMore?: boolean;
}

interface EffectItem {
  id: string;
  text: string;
  rotation: number;
}


const SFX_WORDS = ["POW!", "BOOM!", "BAM!", "KAPOW!", "WHAM!", "ZAP!"];

const DRAFT_KEY = "comic:draft";

// 页面真正加载到的前端版本。旧页面会一直保留旧值，直到它被 reload。
const LOADED_BUILD_ID =
  typeof document === "undefined"
    ? ""
    : (document.querySelector<HTMLMetaElement>('meta[name="app-build"]')?.content ?? "");

// 刷新/部署后恢复未发送的草稿；sessionStorage 按标签页隔离。
const readDraft = (): string => {
  if (typeof sessionStorage === "undefined") return "";

  try {
    return sessionStorage.getItem(DRAFT_KEY) ?? "";
  } catch {
    return "";
  }
};

// 漫画拟声词爆发动画
const burstAnim = stylex.keyframes({
  "0%": { transform: "scale(0.2) rotate(var(--rot))", opacity: 0 },
  "35%": { transform: "scale(1.3) rotate(calc(var(--rot) * -0.5))", opacity: 1 },
  "65%": { transform: "scale(1.05) rotate(var(--rot))", opacity: 1 },
  "100%": { transform: "scale(0.8) translateY(-40px) rotate(calc(var(--rot) * 1.5))", opacity: 0 },
});

const pulseAnim = stylex.keyframes({
  "0%": { opacity: 0.6 },
  "50%": { opacity: 1 },
  "100%": { opacity: 0.6 },
});

const themeStyles = stylex.create({
  bubble0: { backgroundColor: "#fef08a", color: "#000", boxShadow: "4px 4px 0px #ca8a04" },
  badge0: { backgroundColor: "#eab308", color: "#000" },
  bubble1: { backgroundColor: "#f3e8ff", color: "#000", boxShadow: "4px 4px 0px #9333ea" },
  badge1: { backgroundColor: "#a855f7", color: "#fff" },
  bubble2: { backgroundColor: "#ffe4e6", color: "#000", boxShadow: "4px 4px 0px #e11d48" },
  badge2: { backgroundColor: "#f43f5e", color: "#fff" },
  bubble3: { backgroundColor: "#e0f2fe", color: "#000", boxShadow: "4px 4px 0px #0284c7" },
  badge3: { backgroundColor: "#0284c7", color: "#fff" },
  bubble4: { backgroundColor: "#dcfce7", color: "#000", boxShadow: "4px 4px 0px #16a34a" },
  badge4: { backgroundColor: "#22c55e", color: "#000" },
  bubble5: { backgroundColor: "#ffedd5", color: "#000", boxShadow: "4px 4px 0px #ea580c" },
  badge5: { backgroundColor: "#f97316", color: "#fff" },
  bubble6: { backgroundColor: "#fce7f3", color: "#000", boxShadow: "4px 4px 0px #db2777" },
  badge6: { backgroundColor: "#ec4899", color: "#fff" },
  bubble7: { backgroundColor: "#f1f5f9", color: "#000", boxShadow: "4px 4px 0px #475569" },
  badge7: { backgroundColor: "#64748b", color: "#fff" },
  bubble8: { backgroundColor: "#ccfbf1", color: "#000", boxShadow: "4px 4px 0px #0d9488" },
  badge8: { backgroundColor: "#14b8a6", color: "#000" },
  bubble9: { backgroundColor: "#e0e7ff", color: "#000", boxShadow: "4px 4px 0px #4f46e5" },
  badge9: { backgroundColor: "#6366f1", color: "#fff" },
});

const getThemeBubble = (idx: number) => {
  const map = [
    themeStyles.bubble0,
    themeStyles.bubble1,
    themeStyles.bubble2,
    themeStyles.bubble3,
    themeStyles.bubble4,
    themeStyles.bubble5,
    themeStyles.bubble6,
    themeStyles.bubble7,
    themeStyles.bubble8,
    themeStyles.bubble9,
  ];
  return map[idx % 10];
};

const getThemeBadge = (idx: number) => {
  const map = [
    themeStyles.badge0,
    themeStyles.badge1,
    themeStyles.badge2,
    themeStyles.badge3,
    themeStyles.badge4,
    themeStyles.badge5,
    themeStyles.badge6,
    themeStyles.badge7,
    themeStyles.badge8,
    themeStyles.badge9,
  ];
  return map[idx % 10];
};

const getHeroIndex = (str: string = "") => {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = (hash << 5) - hash + str.charCodeAt(i);
    hash |= 0;
  }
  return Math.abs(hash) % 10;
};

const SNAPSHOT_HISTORY = 50;
const HISTORY_PAGE_SIZE = 50;
const SCROLL_NEAR_BOTTOM = 80;

// 旧消息页是升序的，直接按 seq 合并并排序，避免依赖到达顺序。
const mergeOlder = (prev: EventMessage[], older: EventMessage[]): EventMessage[] => {
  const bySeq = new Map<number, EventMessage>();

  for (const msg of [...older, ...prev]) {
    if (typeof msg.seq === "number") bySeq.set(msg.seq, msg);
  }

  return [...bySeq.values()].sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
};

const styles = stylex.create({
  comicPage: {
    maxWidth: 680,
    margin: "32px auto",
    padding: "0 16px",
    fontFamily:
      '"Impact", "Arial Black", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
  },
  comicPanel: {
    border: "3px solid #000000",
    backgroundColor: "#ffffff",
    boxShadow: "6px 6px 0px #000000",
    padding: 24,
    borderRadius: 2,
    display: "flex",
    flexDirection: "column",
    height: "84vh",
    boxSizing: "border-box",
    position: "relative",
    overflow: "hidden",
  },
  panelHeader: {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    borderBottom: "3px solid #000000",
    paddingBottom: 14,
    marginBottom: 16,
    flexShrink: 0,
  },
  comicTitle: {
    margin: 0,
    fontSize: 22,
    textTransform: "uppercase",
    letterSpacing: "0.05em",
  },
  presenceBadge: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    border: "2px solid #000000",
    padding: "4px 10px",
    boxShadow: "3px 3px 0px #000000",
    fontSize: 12,
    backgroundColor: "#fef08a",
  },
  chatStream: {
    listStyle: "none",
    padding: "0 8px 12px 0",
    margin: 0,
    flex: 1,
    overflowY: "auto",
    display: "flex",
    flexDirection: "column",
    gap: 16,
  },
  bubbleRow: {
    display: "flex",
    flexDirection: "column",
    position: "relative",
    // 浏览器原生虚拟化：离屏消息跳过渲染，但仍保留在 DOM 中供 Ctrl+F 等使用。
    contentVisibility: "auto",
    containIntrinsicSize: "auto 96px",
  },
  bubbleRowMe: {
    alignItems: "flex-end",
  },
  bubbleRowOther: {
    alignItems: "flex-start",
  },
  metaRow: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    marginBottom: 6,
  },
  speakerBadge: {
    fontSize: 11,
    fontFamily: "monospace",
    fontWeight: "bold",
    padding: "2px 8px",
    border: "2px solid #000000",
    borderRadius: 2,
    boxShadow: "2px 2px 0px #000000",
  },
  timeLabel: {
    fontSize: 10,
    color: "#71717a",
    fontFamily: "monospace",
  },
  bubbleBox: {
    position: "relative",
    maxWidth: "75%",
    width: "fit-content",
    padding: "10px 18px",
    border: "3px solid #000000",
    borderRadius: 16,
    fontSize: 15,
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    fontWeight: 600,
    lineHeight: 1.4,
    wordBreak: "break-word",
  },
  bubbleMe: {
    backgroundColor: "#000000",
    color: "#ffffff",
    boxShadow: "4px 4px 0px #eab308",
  },
  badgeMe: {
    backgroundColor: "#000000",
    color: "#ffffff",
  },
  tailMe: {
    position: "absolute",
    right: 14,
    bottom: -9,
    width: 0,
    height: 0,
    borderLeft: "9px solid transparent",
    borderRight: "0px solid transparent",
    borderTop: "9px solid #000000",
  },
  tailOther: {
    position: "absolute",
    left: 14,
    bottom: -9,
    width: 0,
    height: 0,
    borderRight: "9px solid transparent",
    borderLeft: "0px solid transparent",
    borderTop: "9px solid #000000",
  },
  // 输入状态条
  typingBar: {
    minHeight: 24,
    display: "flex",
    alignItems: "center",
    gap: 6,
    fontSize: 12,
    fontFamily: "monospace",
    fontWeight: "bold",
    color: "#52525b",
    padding: "0 4px",
    marginTop: 4,
    animationName: pulseAnim,
    animationDuration: "1.2s",
    animationIterationCount: "infinite",
  },
  updateBanner: {
    alignSelf: "center",
    backgroundColor: "#fef08a",
    color: "#000000",
    border: "3px solid #000000",
    boxShadow: "3px 3px 0px #000000",
    padding: "4px 12px",
    marginBottom: 4,
    fontSize: 12,
    fontFamily: "monospace",
    fontWeight: "bold",
    flexShrink: 0,
  },
  loadOlderRow: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    minHeight: 28,
    flexShrink: 0,
  },
  loadOlderButton: {
    border: "2px dashed #000000",
    backgroundColor: "#fef08a",
    color: "#000000",
    padding: "3px 10px",
    fontFamily: "monospace",
    fontSize: 11,
    fontWeight: "bold",
    cursor: "pointer",
  },
  loadOlderText: {
    fontFamily: "monospace",
    fontSize: 11,
    color: "#71717a",
  },
  jumpButton: {
    position: "absolute",
    right: 18,
    bottom: 96,
    zIndex: 20,
    border: "3px solid #000000",
    borderRadius: 999,
    backgroundColor: "#000000",
    color: "#ffffff",
    padding: "6px 14px",
    fontFamily: "monospace",
    fontSize: 12,
    fontWeight: "bold",
    cursor: "pointer",
    boxShadow: "3px 3px 0px #eab308",
  },
  inputAreaWrapper: {
    position: "relative",
    display: "flex",
    flexDirection: "column",
    marginTop: 8,
    flexShrink: 0,
  },
  inputForm: {
    display: "flex",
    gap: 8,
  },
  bubbleImage: {
    padding: 4,
  },
  msgImage: {
    display: "block",
    maxWidth: 260,
    maxHeight: 260,
    borderRadius: 6,
    cursor: "zoom-in",
  },
  lightbox: {
    position: "fixed",
    inset: 0,
    zIndex: 50,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(0, 0, 0, 0.82)",
    cursor: "zoom-out",
  },
  lightboxImage: {
    maxWidth: "92vw",
    maxHeight: "92vh",
    border: "4px solid #000000",
    boxShadow: "8px 8px 0px #000000",
    backgroundColor: "#ffffff",
  },
  comicInput: {
    flex: 1,
    height: 42,
    padding: "0 12px",
    border: "3px solid #000000",
    borderRadius: 2,
    fontSize: 14,
    fontFamily: "-apple-system, BlinkMacSystemFont, sans-serif",
    fontWeight: "bold",
    outline: "none",
    boxShadow: "3px 3px 0px #000000",
  },
  comicButton: {
    height: 42,
    padding: "0 24px",
    border: "3px solid #000000",
    backgroundColor: "#ef4444",
    color: "#ffffff",
    fontSize: 15,
    fontWeight: "bold",
    cursor: "pointer",
    boxShadow: "3px 3px 0px #000000",
    flexShrink: 0,
  },
  comicButtonDisabled: {
    backgroundColor: "#d4d4d8",
    color: "#71717a",
    cursor: "not-allowed",
  },
  emptyNotice: {
    textAlign: "center",
    fontSize: 13,
    color: "#a1a1aa",
    margin: "auto",
    border: "2px dashed #000000",
    padding: "24px 32px",
    borderRadius: 4,
  },
  // 爆发拟声词容器
  fxBadge: {
    position: "absolute",
    right: 28,
    bottom: 50,
    backgroundColor: "#fef08a",
    color: "#ef4444",
    border: "3px solid #000000",
    padding: "6px 14px",
    fontSize: 26,
    fontFamily: '"Impact", "Arial Black", sans-serif',
    letterSpacing: "0.08em",
    boxShadow: "4px 4px 0px #000000",
    pointerEvents: "none",
    zIndex: 10,
    animationName: burstAnim,
    animationDuration: "0.85s",
    animationFillMode: "forwards",
  },
});

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
  const pendingPrependRef = useRef<{
    anchorSeq: string;
    anchorTop: number;
    scrollHeight: number;
    scrollTop: number;
  } | null>(null);
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
    }, 900);
  }, []);

  // 触发本地拟声词爆发
  const triggerFx = () => {
    const text = SFX_WORDS[Math.floor(Math.random() * SFX_WORDS.length)];
    const rotation = Math.floor(Math.random() * 26) - 13;
    const id = crypto.randomUUID();
    setEffects((prev) => [...prev, { id, text, rotation }]);

    setTimeout(() => {
      setEffects((prev) => prev.filter((item) => item.id !== id));
    }, 850);
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
    }, 1800);

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
    }, 800);
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

    // 发送消息后等回显真正进入列表，再滚到包含这条消息的新底部。
    if (forceBottomRef.current) {
      forceBottomRef.current = false;
      stickToBottomRef.current = true;
      setNewMessages(0);
      setAtBottom(true);
      beginProgrammaticScroll();
      el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
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

    const mergeBySeq = (prev: EventMessage[], incoming: EventMessage[]): EventMessage[] => {
      const seen = new Set<number>();
      const merged: EventMessage[] = [];

      for (const msg of [...prev, ...incoming]) {
        // typing 事件没有 seq，按原样保留；聊天消息按 seq 去重。
        if ((msg.type === "chat" || msg.type === "image") && typeof msg.seq === "number") {
          if (seen.has(msg.seq)) continue;
          seen.add(msg.seq);
        }
        merged.push(msg);
      }

      return merged;
    };

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
          setHasMore(history.length >= SNAPSHOT_HISTORY || payload.truncated === true);
        } else {
          const previousLastSeq = lastSeqRef.current;
          setMessages((prev) => mergeBySeq(prev, history));

          // 断线期间漏掉的消息也要计入“X 条新消息”，而不是只算实时消息。
          if (!stickToBottomRef.current) {
            const missed = history.filter(
              (message) =>
                (message.type === "chat" || message.type === "image") &&
                typeof message.seq === "number" &&
                message.seq > previousLastSeq &&
                message.clientId !== sessionID,
            ).length;

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
        window.setTimeout(() => setRateNotice(""), 2500);
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
        window.setTimeout(() => setRateNotice(""), 2500);

        return;
      }

      const media = (await res.json()) as { id: string };

      // 图片占一条独立消息，content 只放 media id
      socket.send(JSON.stringify({ type: "image", content: media.id }));
    } catch {
      setRateNotice("上传失败，请重试");
      window.setTimeout(() => setRateNotice(""), 2500);
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

    // 自己发消息时回到底部：先给出即时反馈，回显到达后再按新高度校正一次。
    forceBottomRef.current = true;
    scrollToBottom("smooth");

    // 如果服务端已经发过新版本，发完这条就刷新；消息已经进入 socket 队列。
    maybeReload();

    triggerFx(); // 触发波普拟声词动效
  };

  // 格式化输入中提示文字
  const typingUsers = Object.values(typingMap);
  const typingText =
    typingUsers.length === 1
      ? `${typingUsers[0]} 正在构思对白...`
      : typingUsers.length > 1
        ? `${typingUsers.join(", ")} 正在激烈辩论...`
        : "";

  return (
    <div {...stylex.props(styles.comicPage)}>
      <div {...stylex.props(styles.comicPanel)}>
        {/* 标题栏 */}
        <div {...stylex.props(styles.panelHeader)}>
          <h1 {...stylex.props(styles.comicTitle)}>PANEL // DIALOGUE</h1>
          <div {...stylex.props(styles.presenceBadge)}>
            <span>⚡</span>
            <span>{connected ? `ONLINE: ${onlineCount}` : retrying ? "重连中…" : "OFFLINE"}</span>
          </div>
        </div>

        {/* 消息流 */}
        <ul
          ref={scrollRef}
          onScroll={handleScroll}
          onWheel={cancelProgrammaticScroll}
          onTouchStart={cancelProgrammaticScroll}
          {...stylex.props(styles.chatStream)}
        >
          {messages.length > 0 &&
            (hasMore || loadingOlder || olderError || messages.length >= SNAPSHOT_HISTORY) && (
              <li ref={topSentinelRef} {...stylex.props(styles.loadOlderRow)}>
                {loadingOlder ? (
                  <span {...stylex.props(styles.loadOlderText)}>加载更早的消息…</span>
                ) : olderError ? (
                  <button
                    type="button"
                    onClick={() => void loadOlder()}
                    {...stylex.props(styles.loadOlderButton)}
                  >
                    加载失败，点击重试
                  </button>
                ) : hasMore ? (
                  <button
                    type="button"
                    onClick={() => void loadOlder()}
                    {...stylex.props(styles.loadOlderButton)}
                  >
                    加载更早的消息
                  </button>
                ) : (
                  <span {...stylex.props(styles.loadOlderText)}>已经是最早的消息</span>
                )}
              </li>
            )}

          {messages.length === 0 ? (
            <li {...stylex.props(styles.emptyNotice)}>NO DIALOGUE YET...</li>
          ) : (
            messages.map((item, idx) => {
              const isMe = item.clientId === user?.sessionId;
              const themeIdx = getHeroIndex(item.clientId || String(idx));

              return (
                <li
                  key={item.seq ?? idx}
                  data-seq={item.seq}
                  {...stylex.props(
                    styles.bubbleRow,
                    isMe ? styles.bubbleRowMe : styles.bubbleRowOther,
                  )}
                >
                  <div {...stylex.props(styles.metaRow)}>
                    <span
                      {...stylex.props(
                        styles.speakerBadge,
                        isMe ? styles.badgeMe : getThemeBadge(themeIdx),
                      )}
                    >
                      {isMe ? "YOU" : item.sender || "UNKNOWN"}
                    </span>
                    <span {...stylex.props(styles.timeLabel)}>{item.time}</span>
                  </div>

                  <div
                    {...stylex.props(
                      styles.bubbleBox,
                      isMe ? styles.bubbleMe : getThemeBubble(themeIdx),
                      item.type === "image" && styles.bubbleImage,
                    )}
                  >
                    {item.type === "image" ? (
                      <img
                        src={`/media/${item.content}`}
                        alt="图片消息"
                        loading="lazy"
                        onClick={() => setZoomed(`/media/${item.content}`)}
                        onError={(e) => {
                          // 超过 7 天会被清理，给一个明确提示而不是破图
                          const target = e.currentTarget;
                          target.style.display = "none";
                          target.insertAdjacentText("afterend", "（图片已过期）");
                        }}
                        {...stylex.props(styles.msgImage)}
                      />
                    ) : (
                      item.content
                    )}
                    <div {...stylex.props(isMe ? styles.tailMe : styles.tailOther)} />
                  </div>
                </li>
              );
            })
          )}
        </ul>

        {!atBottom && (
          <button
            type="button"
            onClick={() => scrollToBottom("smooth")}
            aria-label="回到底部"
            {...stylex.props(styles.jumpButton)}
          >
            {newMessages > 0 ? `${newMessages} 条新消息 ↓` : "回到底部 ↓"}
          </button>
        )}

        {/* 拟声词爆炸动效挂载 */}
        {effects.map((item) => (
          <div
            key={item.id}
            {...stylex.props(styles.fxBadge)}
            style={{ "--rot": `${item.rotation}deg` } as React.CSSProperties}
          >
            {item.text}
          </div>
        ))}

        {/* 点击图片放大查看：点任意处或按 Esc 关闭 */}
        {zoomed && (
          <div
            onClick={() => setZoomed(null)}
            onKeyDown={(e) => e.key === "Escape" && setZoomed(null)}
            role="presentation"
            {...stylex.props(styles.lightbox)}
          >
            <img src={zoomed} alt="放大查看" {...stylex.props(styles.lightboxImage)} />
          </div>
        )}

        {/* 服务端新版本提示：空闲时自动刷新 */}
        {updateNotice && (
          <div role="status" aria-live="polite" {...stylex.props(styles.updateBanner)}>
            {updateNotice}
          </div>
        )}

        {/* 底部输入区 */}
        <div {...stylex.props(styles.inputAreaWrapper)}>
          {/* 实时构思状态条 */}
          <div {...stylex.props(styles.typingBar)}>
            {uploading ? (
              <span>📤 正在上传图片…</span>
            ) : rateNotice ? (
              <span>⏳ {rateNotice}</span>
            ) : (
              typingText && (
                <>
                  <span>🗯️</span>
                  <span>{typingText}</span>
                </>
              )
            )}
          </div>

          <form onSubmit={sendMessage} {...stylex.props(styles.inputForm)}>
            <input
              {...stylex.props(styles.comicInput)}
              value={input}
              placeholder={`Say something, ${user ? `${user.emoji} ${user.name}` : "…"}...`}
              onChange={handleInputChange}
              onPaste={handlePaste}
            />
            <button
              type="submit"
              disabled={!connected}
              {...stylex.props(styles.comicButton, !connected && styles.comicButtonDisabled)}
            >
              POW!
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}
