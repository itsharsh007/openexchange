import { useEffect, useRef, useState } from "react";

// Track the rendered pixel width of an element via ResizeObserver, so SVG charts
// can draw at 1 unit = 1px (no viewBox scaling to undo when handling the mouse).
// Shared by the depth and price charts.
export function useElementWidth() {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      setWidth(entries[0].contentRect.width);
    });
    ro.observe(el);
    setWidth(el.clientWidth);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}
