import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

import { PROGRAMMATIC_SCROLL_TIMEOUT_MS, SCROLL_NEAR_BOTTOM } from "../constants";
import type { EventMessage, PrependAnchor } from "../types";

interface UseChatScrollOptions {
  messages: EventMessage[];
}

export function useChatScroll({ messages }: UseChatScrollOptions) {
  const scrollRef = useRef<HTMLUListElement | null>(null);
  const topSentinelRef = useRef<HTMLLIElement | null>(null);
  const stickToBottomRef = useRef(true);
  const initialScrollDoneRef = useRef(false);
  const pendingPrependRef = useRef<PrependAnchor | null>(null);
  const forceBottomRef = useRef(false);
  const programmaticScrollRef = useRef(false);
  const programmaticScrollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [newMessages, setNewMessages] = useState(0);

  useEffect(() => {
    return () => {
      if (programmaticScrollTimerRef.current) clearTimeout(programmaticScrollTimerRef.current);
    };
  }, []);

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
  }, [messages]);

  // loadOlder 在 setMessages 之前调用，记录当前视口锚点。
  const capturePrependAnchor = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;

    const anchor = el.querySelector<HTMLElement>("[data-seq]");

    pendingPrependRef.current = {
      anchorSeq: anchor?.dataset.seq ?? "",
      anchorTop: anchor?.getBoundingClientRect().top ?? 0,
      scrollHeight: el.scrollHeight,
      scrollTop: el.scrollTop,
    };
  }, []);

  // 自己发消息时立即回到底部；回显到达后 layout effect 会按真实高度再校正。
  const markSent = useCallback(() => {
    forceBottomRef.current = true;
    scrollToBottom("auto");
  }, [scrollToBottom]);

  const markIncoming = useCallback(() => {
    if (!stickToBottomRef.current) {
      setNewMessages((count) => count + 1);
    }
  }, []);

  const markMissed = useCallback((count: number) => {
    if (!stickToBottomRef.current && count > 0) {
      setNewMessages((current) => current + count);
    }
  }, []);

  return {
    scrollRef,
    topSentinelRef,
    atBottom,
    newMessages,
    handleScroll,
    cancelProgrammaticScroll,
    scrollToBottom,
    capturePrependAnchor,
    markSent,
    markIncoming,
    markMissed,
  };
}
