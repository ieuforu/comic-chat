import * as stylex from "@stylexjs/stylex";

export const themeStyles = stylex.create({
  bubble0: { backgroundColor: "#fef08a", color: "#000", boxShadow: "4px 4px 0px #ca8a04" },
  badge0: { backgroundColor: "#eab308", color: "#000" },
  bubble1: { backgroundColor: "#f3e8ff", color: "#000", boxShadow: "4px 4px 0px #9333ea" },
  badge1: { backgroundColor: "#a855f7", color: "#fff" },
  bubble2: { backgroundColor: "#ffe4e6", color: "#000", boxShadow: "4px 4px 0px #e11d48" },
  badge2: { backgroundColor: "#f43f5e", color: "#fff" },
  bubble3: { backgroundColor: "#e0f2fe", color: "#000", boxShadow: "4px 4px 0px #0284c7" },
  badge3: { backgroundColor: "#0284c7", color: "#fff" },
  bubble4: { backgroundColor: "#dcfce7", color: "#000", boxShadow: "4px 4px 0px #16a34a" },
  badge4: { backgroundColor: "#22c55e", color: "#000" },
  bubble5: { backgroundColor: "#ffedd5", color: "#000", boxShadow: "4px 4px 0px #ea580c" },
  badge5: { backgroundColor: "#f97316", color: "#fff" },
  bubble6: { backgroundColor: "#fce7f3", color: "#000", boxShadow: "4px 4px 0px #db2777" },
  badge6: { backgroundColor: "#ec4899", color: "#fff" },
  bubble7: { backgroundColor: "#f1f5f9", color: "#000", boxShadow: "4px 4px 0px #475569" },
  badge7: { backgroundColor: "#64748b", color: "#fff" },
  bubble8: { backgroundColor: "#ccfbf1", color: "#000", boxShadow: "4px 4px 0px #0d9488" },
  badge8: { backgroundColor: "#14b8a6", color: "#000" },
  bubble9: { backgroundColor: "#e0e7ff", color: "#000", boxShadow: "4px 4px 0px #4f46e5" },
  badge9: { backgroundColor: "#6366f1", color: "#fff" },
});

export const getThemeBubble = (idx: number) => {
  const map = [
    themeStyles.bubble0,
    themeStyles.bubble1,
    themeStyles.bubble2,
    themeStyles.bubble3,
    themeStyles.bubble4,
    themeStyles.bubble5,
    themeStyles.bubble6,
    themeStyles.bubble7,
    themeStyles.bubble8,
    themeStyles.bubble9,
  ];
  return map[idx % 10];
};

export const getThemeBadge = (idx: number) => {
  const map = [
    themeStyles.badge0,
    themeStyles.badge1,
    themeStyles.badge2,
    themeStyles.badge3,
    themeStyles.badge4,
    themeStyles.badge5,
    themeStyles.badge6,
    themeStyles.badge7,
    themeStyles.badge8,
    themeStyles.badge9,
  ];
  return map[idx % 10];
};

export const getHeroIndex = (str: string = "") => {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = (hash << 5) - hash + str.charCodeAt(i);
    hash |= 0;
  }
  return Math.abs(hash) % 10;
};
