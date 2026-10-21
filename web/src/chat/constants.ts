export const SFX_WORDS = ["POW!", "BOOM!", "BAM!", "KAPOW!", "WHAM!", "ZAP!"];

export const DRAFT_KEY = "comic:draft";

// 页面真正加载到的前端版本。旧页面会一直保留旧值，直到它被 reload。
export const LOADED_BUILD_ID =
  typeof document === "undefined"
    ? ""
    : (document.querySelector<HTMLMetaElement>('meta[name="app-build"]')?.content ?? "");

// 刷新/部署后恢复未发送的草稿；sessionStorage 按标签页隔离。
export const readDraft = (): string => {
  if (typeof sessionStorage === "undefined") return "";

  try {
    return sessionStorage.getItem(DRAFT_KEY) ?? "";
  } catch {
    return "";
  }
};

export const SNAPSHOT_HISTORY = 50;
export const HISTORY_PAGE_SIZE = 50;
export const SCROLL_NEAR_BOTTOM = 80;
// 最新这些消息始终真实渲染：它们的真实高度决定底部位置。更早的消息才做
// content-visibility 虚拟化，避免 intrinsic-size 估算让最新气泡被截掉。
export const ALWAYS_RENDER_RECENT = 20;

export const TYPING_DEBOUNCE_MS = 1800;
export const RELOAD_DELAY_MS = 900;
export const PROGRAMMATIC_SCROLL_TIMEOUT_MS = 800;
export const RATE_NOTICE_MS = 2500;
export const FX_DURATION_MS = 850;
