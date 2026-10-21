import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";

interface JumpButtonProps {
  visible: boolean;
  count: number;
  onClick: () => void;
}

export function JumpButton({ visible, count, onClick }: JumpButtonProps) {
  if (!visible) return null;

  return (
    <button
      type="button"
      onClick={onClick}
      aria-label="回到底部"
      {...stylex.props(styles.jumpButton)}
    >
      {count > 0 ? `${count} 条新消息 ↓` : "回到底部 ↓"}
    </button>
  );
}
