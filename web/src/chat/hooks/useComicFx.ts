import { useCallback, useState } from "react";

import { FX_DURATION_MS, SFX_WORDS } from "../constants";
import type { EffectItem } from "../types";

export function useComicFx() {
  const [effects, setEffects] = useState<EffectItem[]>([]);

  const triggerFx = useCallback(() => {
    const text = SFX_WORDS[Math.floor(Math.random() * SFX_WORDS.length)];
    const rotation = Math.floor(Math.random() * 26) - 13;
    const id = crypto.randomUUID();

    setEffects((prev) => [...prev, { id, text, rotation }]);

    setTimeout(() => {
      setEffects((prev) => prev.filter((item) => item.id !== id));
    }, FX_DURATION_MS);
  }, []);

  return { effects, triggerFx };
}
