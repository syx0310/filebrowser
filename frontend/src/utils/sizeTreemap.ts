export interface SizeNode {
  name: string;
  path: string;
  size: number;
  isDir: boolean;
  fileCount?: number;
  modified?: string;
  created?: string;
  expanded?: boolean;
  hasMore?: boolean;
  children?: SizeNode[];
  remainder?: boolean;
}

export interface SizeRect {
  node: SizeNode;
  x: number;
  y: number;
  width: number;
  height: number;
  color: string;
  nested: boolean;
}

export function childrenForMap(node: SizeNode): SizeNode[] {
  const children = [...(node.children || [])];
  const represented = children.reduce((sum, child) => sum + Math.max(0, child.size), 0);
  const rest = Math.max(0, node.size - represented);
  if (rest > 0 && node.expanded) {
    children.push({ name: "", path: node.path, size: rest, isDir: false, remainder: true });
  }
  return children;
}

export function directoryColor(path: string, nested = false): string {
  let hash = 0;
  for (const char of path) hash = ((hash << 5) - hash + char.charCodeAt(0)) | 0;
  const hues = [208, 160, 275, 32, 345, 185, 235, 75];
  return `hsl(${hues[(hash >>> 0) % hues.length]} 48% ${nested ? 34 : 25}%)`;
}

// Squarified layout uses only sibling weights. Directory totals are never
// added to their descendants; each nested level stays inside its parent box.
function squarify(nodes: SizeNode[], x: number, y: number, width: number, height: number): Omit<SizeRect, "color" | "nested">[] {
  const positive = nodes.filter((node) => Number.isFinite(node.size) && node.size > 0).sort((a, b) => b.size - a.size || a.path.localeCompare(b.path));
  const sum = positive.reduce((value, node) => value + node.size, 0);
  if (!sum || width <= 0 || height <= 0) return [];
  const pending = positive.map((node) => ({ node, area: node.size / sum * width * height }));
  const output: Omit<SizeRect, "color" | "nested">[] = [];
  let cursor = 0;
  const worst = (row: typeof pending, side: number) => {
    const first = row[0];
    const last = row[row.length - 1];
    if (!first || !last) return Infinity;
    const area = row.reduce((value, item) => value + item.area, 0);
    return Math.max(side * side * first.area / (area * area), area * area / (side * side * last.area));
  };
  while (cursor < pending.length) {
    const first = pending[cursor++];
    if (!first) break;
    const row = [first];
    const side = Math.min(width, height);
    while (cursor < pending.length) {
      const next = pending[cursor];
      if (!next || worst([...row, next], side) > worst(row, side)) break;
      row.push(next);
      cursor++;
    }
    const area = row.reduce((value, item) => value + item.area, 0);
    const vertical = width >= height;
    const thickness = area / (vertical ? height : width);
    let offset = 0;
    for (const item of row) {
      const length = item.area / thickness;
      output.push({ node: item.node, x: x + (vertical ? 0 : offset), y: y + (vertical ? offset : 0), width: vertical ? thickness : length, height: vertical ? length : thickness });
      offset += length;
    }
    if (vertical) { x += thickness; width = Math.max(0, width - thickness); }
    else { y += thickness; height = Math.max(0, height - thickness); }
  }
  return output;
}

export function buildSizeMap(root: SizeNode, width = 1200, height = 660): SizeRect[] {
  const top = squarify(childrenForMap(root), 0, 0, width, height);
  const output: SizeRect[] = [];
  for (const rect of top) {
    output.push({ ...rect, nested: false, color: directoryColor(rect.node.path) });
    if (rect.node.isDir && rect.node.expanded && rect.width > 48 && rect.height > 52) {
      const nested = squarify(childrenForMap(rect.node), rect.x + 4, rect.y + 27, rect.width - 8, rect.height - 31);
      output.push(...nested.map((child) => ({ ...child, nested: true, color: directoryColor(rect.node.path, true) })));
    }
  }
  return output;
}

export function treeHasRemainder(node: SizeNode): boolean {
  return !!node.hasMore || childrenForMap(node).some((child) => child.remainder || (child.isDir && child.expanded && treeHasRemainder(child)));
}
