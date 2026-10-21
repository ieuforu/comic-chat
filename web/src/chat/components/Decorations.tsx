import type { CSSProperties } from "react";
import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";

const WORDS = [
  { text: "POW!", position: styles.wordOne, rotation: "-8deg" },
  { text: "BAM!", position: styles.wordTwo, rotation: "6deg" },
  { text: "ZAP!", position: styles.wordThree, rotation: "-4deg" },
  { text: "KAPOW!", position: styles.wordFour, rotation: "8deg" },
];

export function Decorations() {
  return (
    <div aria-hidden="true" {...stylex.props(styles.decorLayer)}>
      <div {...stylex.props(styles.halftone)} />
      <div {...stylex.props(styles.speedBurst)} />

      {WORDS.map((word) => (
        <span
          key={word.text}
          {...stylex.props(styles.decorWord, word.position)}
          style={{ "--rot": word.rotation } as CSSProperties}
        >
          {word.text}
        </span>
      ))}

      <span {...stylex.props(styles.spark, styles.sparkOne)}>✦</span>
      <span {...stylex.props(styles.spark, styles.sparkTwo)}>✦</span>
      <span {...stylex.props(styles.spark, styles.sparkThree)}>✦</span>
    </div>
  );
}
