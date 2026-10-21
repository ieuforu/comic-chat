import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";

import { DRAFT_KEY, RELOAD_DELAY_MS } from "../constants";

interface UseAutoReloadOptions {
  inputRef: RefObject<string>;
  uploadingRef: RefObject<boolean>;
}

/**
 * 服务端 build 变化后的安全刷新：空闲时自动 reload；有草稿或正在上传时等状态结束。
 * 返回值里的 isPendingReload 给上传流程判断是否需要提示。
 */
export function useAutoReload({ inputRef, uploadingRef }: UseAutoReloadOptions) {
  const [updateNotice, setUpdateNotice] = useState("");
  const pendingReloadRef = useRef(false);
  const reloadTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (reloadTimerRef.current) clearTimeout(reloadTimerRef.current);
    };
  }, []);

  const cancelScheduledReload = useCallback(() => {
    if (reloadTimerRef.current) {
      clearTimeout(reloadTimerRef.current);
      reloadTimerRef.current = null;
    }
  }, []);

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
  }, [inputRef, uploadingRef]);

  const requestReload = useCallback(() => {
    pendingReloadRef.current = true;
    setUpdateNotice("服务端已更新，正在检查…");
    maybeReload();
  }, [maybeReload]);

  const isPendingReload = useCallback(() => pendingReloadRef.current, []);

  return {
    updateNotice,
    maybeReload,
    requestReload,
    cancelScheduledReload,
    isPendingReload,
  };
}
