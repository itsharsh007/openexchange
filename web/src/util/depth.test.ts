import { describe, expect, it } from "vitest";
import { askDepthAt, bidDepthAt, cumulate, stepArea, stepLine, type Level } from "./depth";

// A small two-sided book. Bids arrive highest-first, asks lowest-first, matching
// the wire order the gateway sends.
const bids: Level[] = [
  { priceTicks: 10000, quantity: 5 },
  { priceTicks: 9990, quantity: 10 },
  { priceTicks: 9980, quantity: 20 },
];
const asks: Level[] = [
  { priceTicks: 10010, quantity: 4 },
  { priceTicks: 10020, quantity: 6 },
  { priceTicks: 10030, quantity: 15 },
];

describe("cumulate", () => {
  it("accumulates quantity away from the touch", () => {
    expect(cumulate(bids, 10)).toEqual([
      { price: 10000, cum: 5 },
      { price: 9990, cum: 15 },
      { price: 9980, cum: 35 },
    ]);
  });

  it("truncates to the requested depth", () => {
    expect(cumulate(asks, 2)).toEqual([
      { price: 10010, cum: 4 },
      { price: 10020, cum: 10 },
    ]);
  });

  it("returns an empty curve for an empty side", () => {
    expect(cumulate([], 10)).toEqual([]);
  });
});

describe("bidDepthAt", () => {
  const pts = cumulate(bids, 10);

  it("is the size resting at or above the price", () => {
    expect(bidDepthAt(pts, 10000)).toBe(5); // best bid only
    expect(bidDepthAt(pts, 9990)).toBe(15); // top two levels
    expect(bidDepthAt(pts, 9980)).toBe(35); // whole side
  });

  it("counts a price that sits between levels by the next level up", () => {
    expect(bidDepthAt(pts, 9985)).toBe(15); // nothing rests in (9980,9990)
  });

  it("is zero above the best bid", () => {
    expect(bidDepthAt(pts, 10001)).toBe(0);
  });
});

describe("askDepthAt", () => {
  const pts = cumulate(asks, 10);

  it("is the size resting at or below the price", () => {
    expect(askDepthAt(pts, 10010)).toBe(4);
    expect(askDepthAt(pts, 10020)).toBe(10);
    expect(askDepthAt(pts, 10030)).toBe(25);
  });

  it("is zero below the best ask", () => {
    expect(askDepthAt(pts, 10000)).toBe(0);
  });
});

describe("step paths", () => {
  const px = [
    { x: 0, y: 100 },
    { x: 10, y: 60 },
    { x: 20, y: 20 },
  ];

  it("stepLine moves horizontally then vertically at each point", () => {
    expect(stepLine(px)).toBe("M 0 100 L 10 100 L 10 60 L 20 60 L 20 20");
  });

  it("stepArea closes the staircase down to the baseline", () => {
    expect(stepArea(px, 150)).toBe(
      "M 0 150 L 0 100 L 10 100 L 10 60 L 20 60 L 20 20 L 20 150 Z",
    );
  });

  it("empty input yields an empty path", () => {
    expect(stepLine([])).toBe("");
    expect(stepArea([], 150)).toBe("");
  });
});
