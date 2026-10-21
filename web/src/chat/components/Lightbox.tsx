import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";

interface LightboxProps {
  src: string | null;
  onClose: () => void;
}

export function Lightbox({ src, onClose }: LightboxProps) {
  if (!src) return null;

  return (
    <div
      onClick={onClose}
      onKeyDown={(e) => e.key === "Escape" && onClose()}
      role="presentation"
      {...stylex.props(styles.lightbox)}
    >
      <img src={src} alt="放大查看" {...stylex.props(styles.lightboxImage)} />
    </div>
  );
}
