import type { ChangeEvent, ClipboardEvent, FormEvent } from "react";
import * as stylex from "@stylexjs/stylex";

import { styles } from "../styles";
import type { Profile } from "../types";

interface ComposerProps {
  input: string;
  connected: boolean;
  uploading: boolean;
  rateNotice: string;
  updateNotice: string;
  typingUsers: string[];
  user: Profile | null;
  onInputChange: (e: ChangeEvent<HTMLInputElement>) => void;
  onPaste: (e: ClipboardEvent<HTMLInputElement>) => void;
  onSubmit: (e: FormEvent) => void;
}

export function Composer({
  input,
  connected,
  uploading,
  rateNotice,
  updateNotice,
  typingUsers,
  user,
  onInputChange,
  onPaste,
  onSubmit,
}: ComposerProps) {
  const typingText =
    typingUsers.length === 1
      ? `${typingUsers[0]} 正在构思对白...`
      : typingUsers.length > 1
        ? `${typingUsers.join(", ")} 正在激烈辩论...`
        : "";

  return (
    <>
      {updateNotice && (
        <div role="status" aria-live="polite" {...stylex.props(styles.updateBanner)}>
          {updateNotice}
        </div>
      )}

      <div {...stylex.props(styles.inputAreaWrapper)}>
        <div {...stylex.props(styles.typingBar)}>
          {uploading ? (
            <span>📤 正在上传图片…</span>
          ) : rateNotice ? (
            <span>⏳ {rateNotice}</span>
          ) : (
            typingText && (
              <>
                <span>🗯️</span>
                <span>{typingText}</span>
              </>
            )
          )}
        </div>

        <form onSubmit={onSubmit} {...stylex.props(styles.inputForm)}>
          <input
            {...stylex.props(styles.comicInput)}
            value={input}
            placeholder={`Say something, ${user ? `${user.emoji} ${user.name}` : "…"}...`}
            onChange={onInputChange}
            onPaste={onPaste}
          />
          <button
            type="submit"
            disabled={!connected}
            {...stylex.props(styles.comicButton, !connected && styles.comicButtonDisabled)}
          >
            POW!
          </button>
        </form>

        <div {...stylex.props(styles.learningNote)}>
          LEARNING PROJECT · 非 IM · 随时可能关闭
        </div>
      </div>
    </>
  );
}
