import { useRef, useState } from "react";
import * as stylex from "@stylexjs/stylex";

import { Composer } from "./chat/components/Composer";
import { FxOverlay } from "./chat/components/FxOverlay";
import { JumpButton } from "./chat/components/JumpButton";
import { Lightbox } from "./chat/components/Lightbox";
import { MessageStream } from "./chat/components/MessageStream";
import { PanelHeader } from "./chat/components/PanelHeader";
import { useAutoReload } from "./chat/hooks/useAutoReload";
import { useChatScroll } from "./chat/hooks/useChatScroll";
import { useComicFx } from "./chat/hooks/useComicFx";
import { useChatSocket } from "./chat/hooks/useChatSocket";
import { useHistoryPagination } from "./chat/hooks/useHistoryPagination";
import { DRAFT_KEY, LOADED_BUILD_ID, TYPING_DEBOUNCE_MS, readDraft } from "./chat/constants";
import { styles } from "./chat/styles";
import type { EventMessage } from "./chat/types";

export default function App() {
  const [messages, setMessages] = useState<EventMessage[]>([]);
  const [input, setInput] = useState(readDraft);
  const [uploading, setUploading] = useState(false);
  // 点击图片后全屏查看
  const [zoomed, setZoomed] = useState<string | null>(null);
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inputRef = useRef(input);
  const uploadingRef = useRef(false);

  const {
    scrollRef,
    topSentinelRef,
    atBottom,
    newMessages,
    handleScroll,
    cancelProgrammaticScroll,
    scrollToBottom,
    capturePrependAnchor,
    markSent,
    markIncoming,
    markMissed,
  } = useChatScroll({ messages });

  const { hasMore, loadingOlder, olderError, loadOlder, markSnapshot } = useHistoryPagination({
    messages,
    setMessages,
    scrollRef,
    sentinelRef: topSentinelRef,
    onBeforePrepend: capturePrependAnchor,
  });

  const { updateNotice, maybeReload, requestReload, cancelScheduledReload } = useAutoReload({
    inputRef,
    uploadingRef,
  });

  const {
    user,
    onlineCount,
    connected,
    retrying,
    rateNotice,
    typingMap,
    showNotice,
    canSend,
    sendTyping,
    sendChat,
    sendImage,
  } = useChatSocket({
    setMessages,
    onFirstSnapshot: markSnapshot,
    onMissed: markMissed,
    onIncoming: markIncoming,
    onBuildChange: requestReload,
  });

  const { effects, triggerFx } = useComicFx();
  const sessionID = user?.sessionId ?? "";

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setInput(value);
    inputRef.current = value;

    try {
      if (value) sessionStorage.setItem(DRAFT_KEY, value);
      else sessionStorage.removeItem(DRAFT_KEY);
    } catch {
      // ignore
    }

    // 用户开始输入时取消已经排队的刷新；否则会在他打字时 reload。
    cancelScheduledReload();

    // 广播正在输入
    sendTyping(true);

    // 防抖：1.8秒没有新键入，广播停止输入
    if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);
    typingTimeoutRef.current = setTimeout(() => {
      sendTyping(false);
    }, TYPING_DEBOUNCE_MS);

    // 输入被清空（例如草稿删掉）时，如果服务端有新版本就立即刷新。
    maybeReload();
  };

  // 粘贴图片：读取剪贴板里的文件 → 上传 → 再发一条 image 消息
  const uploadAndSend = async (file: File) => {
    if (!canSend()) return;

    cancelScheduledReload();

    uploadingRef.current = true;
    setUploading(true);

    maybeReload();

    try {
      const form = new FormData();
      form.append("file", file);

      const res = await fetch("/api/upload", {
        method: "POST",
        credentials: "same-origin",
        body: form,
      });

      if (!res.ok) {
        const detail = await res.json().catch(() => ({}));
        showNotice(detail.error ?? `上传失败（${res.status}）`);

        return;
      }

      const media = (await res.json()) as { id: string };

      // 图片占一条独立消息，content 只放 media id
      if (!sendImage(media.id)) throw new Error("socket closed");
    } catch {
      showNotice("上传失败，请重试");
    } finally {
      uploadingRef.current = false;
      setUploading(false);
      maybeReload();
    }
  };

  const handlePaste = (e: React.ClipboardEvent<HTMLInputElement>) => {
    const files = Array.from(e.clipboardData?.files ?? []);
    const image = files.find((f) => f.type.startsWith("image/"));

    if (!image) return; // 普通文本粘贴走默认行为

    e.preventDefault();
    void uploadAndSend(image);
  };

  const sendMessage = (e: React.FormEvent) => {
    e.preventDefault();

    const content = input.trim();
    if (!content || !canSend()) return;

    // 立即解除输入状态
    if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);

    cancelScheduledReload();

    sendTyping(false);

    // sender/clientId/time are stamped by the server from the session cookie.
    if (!sendChat(content)) return;

    setInput("");
    inputRef.current = "";

    try {
      sessionStorage.removeItem(DRAFT_KEY);
    } catch {
      // ignore
    }

    // 自己发消息时回到底部：先即时跳到当前底部，回显到达后再按真实高度校正。
    // 不用 smooth，避免发送时的滚动目标和回显后的新高度打架。
    markSent();

    // 如果服务端已经发过新版本，发完这条就刷新；消息已经进入 socket 队列。
    maybeReload();

    triggerFx(); // 触发波普拟声词动效
  };

  // 格式化输入中提示文字
  const typingUsers = Object.values(typingMap);

  return (
    <div {...stylex.props(styles.comicPage)}>
      <div {...stylex.props(styles.comicPanel)}>
        <PanelHeader
          buildId={LOADED_BUILD_ID}
          connected={connected}
          retrying={retrying}
          onlineCount={onlineCount}
        />

        <MessageStream
          messages={messages}
          selfSessionId={sessionID}
          scrollRef={scrollRef}
          sentinelRef={topSentinelRef}
          hasMore={hasMore}
          loadingOlder={loadingOlder}
          olderError={olderError}
          onLoadOlder={() => void loadOlder()}
          onScroll={handleScroll}
          onWheel={cancelProgrammaticScroll}
          onTouchStart={cancelProgrammaticScroll}
          onZoom={(src) => setZoomed(src)}
        />

        <JumpButton
          visible={!atBottom}
          count={newMessages}
          onClick={() => scrollToBottom("smooth")}
        />

        <FxOverlay effects={effects} />
        <Lightbox src={zoomed} onClose={() => setZoomed(null)} />

        <Composer
          input={input}
          connected={connected}
          uploading={uploading}
          rateNotice={rateNotice}
          updateNotice={updateNotice}
          typingUsers={typingUsers}
          user={user}
          onInputChange={handleInputChange}
          onPaste={handlePaste}
          onSubmit={sendMessage}
        />
      </div>
    </div>
  );
}
