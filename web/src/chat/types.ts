export interface EventMessage {
  type:
    | "chat"
    | "image"
    | "delete"
    | "snapshot"
    | "presence"
    | "typing"
    | "notice"
    | "profile";
  clientId?: string;
  sender?: string;
  content?: string;
  time?: string;
  history?: EventMessage[];
  online?: number;
  isTyping?: boolean;
  seq?: number;
  truncated?: boolean;
  buildId?: string;
  connections?: number;
  notice?: string;
  name?: string;
  me?: Profile;
}

export interface Profile {
  sessionId: string;
  name: string;
  emoji: string;
}

export interface HistoryResponse {
  messages?: EventMessage[];
  hasMore?: boolean;
}

export interface EffectItem {
  id: string;
  text: string;
  rotation: number;
}

export interface PrependAnchor {
  anchorSeq: string;
  anchorTop: number;
  scrollHeight: number;
  scrollTop: number;
}
