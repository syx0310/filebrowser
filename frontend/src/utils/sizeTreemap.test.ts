import { describe, expect, it } from "vitest";
import { buildSizeMap, childrenForMap, treeHasRemainder, type SizeNode } from "./sizeTreemap.ts";

const file = (name: string, size: number): SizeNode => ({ name, path: `/${name}`, size, isDir: false });

describe("directory size map", () => {
  it("uses each directory total once, with nested entries contained in their parent", () => {
    const root: SizeNode = { name: "root", path: "/", size: 100, isDir: true, expanded: true, children: [
      { name: "dir", path: "/dir/", size: 75, isDir: true, expanded: true, children: [file("one", 50), file("two", 25)] }, file("three", 25),
    ] };
    const rects = buildSizeMap(root, 1000, 600);
    const top = rects.filter((r) => !r.nested);
    expect(top.reduce((sum, r) => sum + r.width * r.height, 0)).toBeCloseTo(600000);
    const folder = top.find((r) => r.node.isDir);
    if (!folder) throw new Error("expected folder rectangle");
    expect(folder.width * folder.height).toBeCloseTo(450000);
    for (const rect of rects.filter((r) => r.nested)) {
      expect(rect.x).toBeGreaterThanOrEqual(folder.x);
      expect(rect.y).toBeGreaterThan(folder.y + 20);
      expect(rect.x + rect.width).toBeLessThanOrEqual(folder.x + folder.width);
      expect(rect.y + rect.height).toBeLessThanOrEqual(folder.y + folder.height);
    }
  });

  it("retains undisplayed bytes in a remainder without treating them as files", () => {
    const root = { name: "root", path: "/", size: 100, isDir: true, expanded: true, hasMore: true, children: [file("one", 30)] };
    const children = childrenForMap(root);
    expect(children.map((n) => n.size)).toEqual([30, 70]);
    expect(children[1]?.remainder).toBe(true);
    expect(children[1]?.fileCount).toBeUndefined();
    expect(treeHasRemainder(root)).toBe(true);
  });

  it("handles empty files, partial index rollups and very different sizes without invalid geometry", () => {
    const root = { name: "root", path: "/", size: 0, isDir: true, expanded: true, children: [file("empty", 0), file("large", 100e12), file("tiny", 1)] };
    const rects = buildSizeMap(root);
    expect(rects).toHaveLength(2);
    for (const r of rects) for (const n of [r.x, r.y, r.width, r.height]) expect(Number.isFinite(n) && n >= 0).toBe(true);
    expect(buildSizeMap({ ...root, children: [file("empty", 0)] })).toEqual([]);
  });
});
