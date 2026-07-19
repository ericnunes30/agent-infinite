export const TERMINAL_PREVIEW_BATCH_MS = 100;
export const BULK_START_CONCURRENCY = 2;

interface LayoutNode {
  readonly id: string;
  readonly position: { readonly x: number; readonly y: number };
  readonly width?: number;
  readonly height?: number;
}

interface LayoutEdge {
  readonly id: string;
  readonly source: string;
  readonly target: string;
}

interface LayoutViewport {
  readonly x: number;
  readonly y: number;
  readonly zoom: number;
}

export function canvasLayoutSignature(
  nodes: readonly LayoutNode[],
  edges: readonly LayoutEdge[],
  viewport: LayoutViewport,
): string {
  return nodes
    .map(
      (node) =>
        `${node.id}:${String(node.position.x)}:${String(node.position.y)}:${node.width === undefined ? '' : String(node.width)}:${node.height === undefined ? '' : String(node.height)}`,
    )
    .concat(edges.map((edge) => `${edge.id}:${edge.source}:${edge.target}`))
    .concat(`${String(viewport.x)}:${String(viewport.y)}:${String(viewport.zoom)}`)
    .join('|');
}
