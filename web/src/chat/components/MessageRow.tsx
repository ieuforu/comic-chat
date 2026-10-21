import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";
import { getHeroIndex, getThemeBadge, getThemeBubble } from "../theme";
import type { EventMessage } from "../types";

interface MessageRowProps {
  message: EventMessage;
  index: number;
  isMe: boolean;
  virtualize: boolean;
  onZoom: (src: string) => void;
}

export function MessageRow({ message, index, isMe, virtualize, onZoom }: MessageRowProps) {
  const themeIdx = getHeroIndex(message.clientId || String(index));

  return (
    <li
      data-seq={message.seq}
      {...stylex.props(
        styles.bubbleRow,
        isMe ? styles.bubbleRowMe : styles.bubbleRowOther,
        virtualize && styles.bubbleRowVirtual,
      )}
    >
      <div {...stylex.props(styles.metaRow)}>
        <span
          {...stylex.props(
            styles.speakerBadge,
            isMe ? styles.badgeMe : getThemeBadge(themeIdx),
          )}
        >
          {isMe ? "YOU" : message.sender || "UNKNOWN"}
        </span>
        <span {...stylex.props(styles.timeLabel)}>{message.time}</span>
      </div>

      <div
        {...stylex.props(
          styles.bubbleBox,
          isMe ? styles.bubbleMe : getThemeBubble(themeIdx),
          message.type === "image" && styles.bubbleImage,
        )}
      >
        {message.type === "image" ? (
          <img
            src={`/media/${message.content}`}
            alt="图片消息"
            loading="lazy"
            onClick={() => onZoom(`/media/${message.content}`)}
            onError={(e) => {
              // 超过 7 天会被清理，给一个明确提示而不是破图
              const target = e.currentTarget;
              target.style.display = "none";
              target.insertAdjacentText("afterend", "（图片已过期）");
            }}
            {...stylex.props(styles.msgImage)}
          />
        ) : (
          message.content
        )}
        <div {...stylex.props(isMe ? styles.tailMe : styles.tailOther)} />
      </div>
    </li>
  );
}
