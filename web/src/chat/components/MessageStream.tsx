import type { RefObject } from "react";
import * as stylex from "@stylexjs/stylex";

import { ALWAYS_RENDER_RECENT, SNAPSHOT_HISTORY } from "../constants";
import { styles } from "../styles";
import type { EventMessage } from "../types";
import { LoadOlderRow } from "./LoadOlderRow";
import { MessageRow } from "./MessageRow";

interface MessageStreamProps {
  messages: EventMessage[];
  selfSessionId: string;
  scrollRef: RefObject<HTMLUListElement | null>;
  sentinelRef: RefObject<HTMLLIElement | null>;
  hasMore: boolean;
  loadingOlder: boolean;
  olderError: boolean;
  onLoadOlder: () => void;
  onScroll: () => void;
  onWheel: () => void;
  onTouchStart: () => void;
  onZoom: (src: string) => void;
}

export function MessageStream({
  messages,
  selfSessionId,
  scrollRef,
  sentinelRef,
  hasMore,
  loadingOlder,
  olderError,
  onLoadOlder,
  onScroll,
  onWheel,
  onTouchStart,
  onZoom,
}: MessageStreamProps) {
  return (
    <ul
      ref={scrollRef}
      onScroll={onScroll}
      onWheel={onWheel}
      onTouchStart={onTouchStart}
      {...stylex.props(styles.chatStream)}
    >
      {messages.length > 0 &&
        (hasMore || loadingOlder || olderError || messages.length >= SNAPSHOT_HISTORY) && (
          <LoadOlderRow
            sentinelRef={sentinelRef}
            loading={loadingOlder}
            error={olderError}
            hasMore={hasMore}
            onLoad={onLoadOlder}
          />
        )}

      {messages.length === 0 ? (
        <li {...stylex.props(styles.emptyNotice)}>NO DIALOGUE YET...</li>
      ) : (
        messages.map((item, idx) => (
          <MessageRow
            key={item.seq ?? idx}
            message={item}
            index={idx}
            isMe={item.clientId === selfSessionId}
            virtualize={idx < messages.length - ALWAYS_RENDER_RECENT}
            onZoom={onZoom}
          />
        ))
      )}
    </ul>
  );
}
