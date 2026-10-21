import { useCallback, useEffect, useRef, useState } from "react";
import type { Dispatch, RefObject, SetStateAction } from "react";

import { HISTORY_PAGE_SIZE } from "../constants";
import { mergeOlder, snapshotHasOlder } from "../messages";
import type { EventMessage, HistoryResponse } from "../types";

interface UseHistoryPaginationOptions {
  messages: EventMessage[];
  setMessages: Dispatch<SetStateAction<EventMessage[]>>;
  scrollRef: RefObject<HTMLUListElement | null>;
  sentinelRef: RefObject<HTMLLIElement | null>;
  onBeforePrepend: () => void;
}

export function useHistoryPagination({
  messages,
  setMessages,
  scrollRef,
  sentinelRef,
  onBeforePrepend,
}: UseHistoryPaginationOptions) {
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState(false);
  const loadingOlderRef = useRef(false);

  const markSnapshot = useCallback((historyLength: number, truncated: boolean | undefined) => {
    setHasMore(snapshotHasOlder(historyLength, truncated));
  }, []);

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
        onBeforePrepend();
        setMessages((prev) => mergeOlder(prev, older));
      }

      setHasMore(Boolean(page.hasMore));
    } catch {
      setOlderError(true);
    } finally {
      loadingOlderRef.current = false;
      setLoadingOlder(false);
    }
  }, [hasMore, messages, onBeforePrepend, setMessages]);

  // 滚到顶部附近时自动加载上一页；按钮仍然保留，加载失败也可以手动重试。
  useEffect(() => {
    if (!hasMore) return;

    const root = scrollRef.current;
    const target = sentinelRef.current;
    if (!root || !target) return;

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) void loadOlder();
      },
      { root, rootMargin: "240px 0px 0px 0px" },
    );

    observer.observe(target);

    return () => observer.disconnect();
  }, [hasMore, loadOlder, scrollRef, sentinelRef]);

  return {
    hasMore,
    loadingOlder,
    olderError,
    loadOlder,
    markSnapshot,
  };
}
