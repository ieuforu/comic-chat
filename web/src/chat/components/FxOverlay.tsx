import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";
import type { EffectItem } from "../types";

export function FxOverlay({ effects }: { effects: EffectItem[] }) {
  return (
    <>
      {effects.map((item) => (
        <div
          key={item.id}
          {...stylex.props(styles.fxBadge)}
          style={{ "--rot": `${item.rotation}deg` } as React.CSSProperties}
        >
          {item.text}
        </div>
      ))}
    </>
  );
}
