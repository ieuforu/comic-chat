import type { RefObject } from "react";
import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";

interface LoadOlderRowProps {
  sentinelRef: RefObject<HTMLLIElement | null>;
  loading: boolean;
  error: boolean;
  hasMore: boolean;
  onLoad: () => void;
}

export function LoadOlderRow({ sentinelRef, loading, error, hasMore, onLoad }: LoadOlderRowProps) {
  return (
    <li ref={sentinelRef} {...stylex.props(styles.loadOlderRow)}>
      {loading ? (
        <span {...stylex.props(styles.loadOlderText)}>加载更早的消息…</span>
      ) : error ? (
        <button type="button" onClick={onLoad} {...stylex.props(styles.loadOlderButton)}>
          加载失败，点击重试
        </button>
      ) : hasMore ? (
        <button type="button" onClick={onLoad} {...stylex.props(styles.loadOlderButton)}>
          加载更早的消息
        </button>
      ) : (
        <span {...stylex.props(styles.loadOlderText)}>已经是最早的消息</span>
      )}
    </li>
  );
}
