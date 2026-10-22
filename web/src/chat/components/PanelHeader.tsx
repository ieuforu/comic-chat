import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";

interface PanelHeaderProps {
  buildId: string;
  connected: boolean;
  retrying: boolean;
  onlineCount: number;
}

export function PanelHeader({ buildId, connected, retrying, onlineCount }: PanelHeaderProps) {
  return (
    <div {...stylex.props(styles.panelHeader)}>
      <div {...stylex.props(styles.titleBlock)}>
        <h1 {...stylex.props(styles.comicTitle)}>PANEL // DIALOGUE</h1>
        <div {...stylex.props(styles.titleMeta)}>
          <span {...stylex.props(styles.buildBadge)} title="当前页面加载的前端 build">
            BUILD {buildId || "dev"}
          </span>
          <a
            href="/rules.html"
            target="_blank"
            rel="noreferrer"
            {...stylex.props(styles.rulesLink)}
          >
            规则 / RULES
          </a>
        </div>
      </div>
      <div {...stylex.props(styles.presenceBadge)}>
        <span>⚡</span>
        <span>{connected ? `ONLINE: ${onlineCount}` : retrying ? "重连中…" : "OFFLINE"}</span>
      </div>
    </div>
  );
}
