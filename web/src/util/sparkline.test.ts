import { describe, expect, it } from "vitest";
import { areaPath, linePath, scale } from "./sparkline";

describe("scale", () => {
  it("maps endpoints across the padded box and inverts Y", () => {
    const s = scale([10, 20], 100, 50, 3);
    expect(s.min).toBe(10);
    expect(s.max).toBe(20);
    expect(s.x(0)).toBeCloseTo(3);
    expect(s.x(1)).toBeCloseTo(97);
    expect(s.y(10)).toBeCloseTo(47); // lowest value sits at the bottom
    expect(s.y(20)).toBeCloseTo(3); // highest value at the top
  });

  it("centres a single sample", () => {
    expect(scale([42], 100, 50).x(0)).toBe(50);
  });

  it("does not divide by zero on a flat series", () => {
    const s = scale([5, 5, 5], 100, 50, 3);
    expect(Number.isFinite(s.y(5))).toBe(true);
  });
});

describe("linePath", () => {
  it("starts with a moveto then lineto per sample", () => {
    const s = scale([10, 20], 100, 50, 3);
    expect(linePath([10, 20], s)).toBe("M 3.00 47.00 L 97.00 3.00");
  });

  it("is empty for no samples", () => {
    expect(linePath([], scale([], 100, 50))).toBe("");
  });
});

describe("areaPath", () => {
  it("closes the line down to the baseline", () => {
    const s = scale([10, 20], 100, 50, 3);
    expect(areaPath([10, 20], s, 50, 3)).toBe(
      "M 3.00 47.00 L 97.00 3.00 L 97.00 47 L 3.00 47 Z",
    );
  });
});
