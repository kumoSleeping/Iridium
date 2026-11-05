// The topology view (plan "网页": 工作区 → 目录 → 会话, no lines of who started whom), in the main
// pane. React Flow draws it, dagre lays it out left to right. A session opens its chat. Loaded only
// once it is opened, or the page is idle.
import dagre from "@dagrejs/dagre";
import { Background, Controls, Handle, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import { ChevronLeft, Folder, Layers } from "lucide-react";
import { memo, useMemo } from "react";
import type { Session } from "../api/types.gen";
import { t, useLang } from "../i18n";
import { openChat } from "../route";
import type { Tree } from "../tree";
import { cn, IconKey, LogoLamp, statusText, tilde, titleOf } from "../ui";
import { useUnread } from "../unread";
import "./topology.css";

type WsData = { kind: "ws"; name: string; count: number };
type FolderData = { kind: "folder"; name: string; count: number };
type SessionData = { kind: "session"; s: Session };
type Data = WsData | FolderData | SessionData;

const SIZE = { ws: [176, 40], folder: [168, 34], session: [232, 34] } as const;

/** The graph of a tree, laid out: workspaces on the left, their folders, their sessions. */
export function graphOf(tree: Tree): { nodes: Node<Data>[]; edges: Edge[] } {
  const nodes: Node<Data>[] = [];
  const edges: Edge[] = [];
  const add = (id: string, data: Data, parent?: string) => {
    nodes.push({ id, data, type: data.kind, position: { x: 0, y: 0 }, draggable: false, connectable: false });
    if (parent) edges.push({ id: `${parent}>${id}`, source: parent, target: id, type: "smoothstep" });
  };
  for (const w of tree.workspaces) {
    add(w.key, { kind: "ws", name: w.name, count: w.count });
    for (const r of w.rows) {
      if (r.kind === "session") add(`s:${r.s.id}`, { kind: "session", s: r.s }, w.key);
      else {
        add(r.f.key, { kind: "folder", name: w.id === 0 ? tilde(r.f.name) : r.f.name, count: r.f.sessions.length }, w.key);
        for (const s of r.f.sessions) add(`s:${s.id}`, { kind: "session", s }, r.f.key);
      }
    }
  }
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "LR", nodesep: 10, ranksep: 56, marginx: 24, marginy: 24 });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes) {
    const [w, h] = SIZE[n.data.kind];
    g.setNode(n.id, { width: w, height: h });
  }
  for (const e of edges) g.setEdge(e.source, e.target);
  dagre.layout(g);
  for (const n of nodes) {
    const p = g.node(n.id);
    const [w, h] = SIZE[n.data.kind];
    n.position = { x: p.x - w / 2, y: p.y - h / 2 };
  }
  return { nodes, edges };
}

const In = () => <Handle type="target" position={Position.Left} isConnectable={false} />;
const Out = () => <Handle type="source" position={Position.Right} isConnectable={false} />;

const WsNode = memo(function WsNode({ data }: NodeProps<Node<WsData>>) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-fill px-3 text-base font-medium text-ink" style={{ width: SIZE.ws[0], height: SIZE.ws[1] }}>
      <Layers size={15} className="shrink-0 text-ink-3" />
      <span className="min-w-0 flex-1 truncate">{data.name}</span>
      <span className="text-xs text-ink-4 tabular-nums">{data.count}</span>
      <Out />
    </div>
  );
});
const FolderNode = memo(function FolderNode({ data }: NodeProps<Node<FolderData>>) {
  return (
    <div className="flex items-center gap-2 rounded-md px-2.5 text-sm text-ink shadow-[0_0_0_1px_var(--edge)]" style={{ width: SIZE.folder[0], height: SIZE.folder[1], background: "var(--paper)" }}>
      <In />
      <Folder size={14} className="shrink-0 text-ink-3" />
      <span className="min-w-0 flex-1 truncate">{data.name}</span>
      <span className="text-xs text-ink-4 tabular-nums">{data.count}</span>
      <Out />
    </div>
  );
});
const SessionNode = memo(function SessionNode({ data: { s } }: NodeProps<Node<SessionData>>) {
  const unread = useUnread(s.id);
  return (
    <button
      type="button"
      onClick={() => openChat(s.id)}
      aria-label={`${titleOf(s)}, ${statusText(s.status)}`}
      className={cn("g-press flex items-center gap-2 rounded-md px-2.5 text-left text-sm text-ink shadow-[0_0_0_1px_var(--edge)] hover:bg-fill hover:[--row-bg:var(--fill)]", s.archived && "opacity-55")}
      style={{ width: SIZE.session[0], height: SIZE.session[1], background: "var(--row-bg, var(--paper))" }}
    >
      <In />
      <LogoLamp agent={s.agent} status={s.status} size={15} />
      <span className="min-w-0 flex-1 truncate">{titleOf(s)}</span>
      {unread && <span className="unread shrink-0" aria-hidden />}
    </button>
  );
});
const TYPES = { ws: WsNode, folder: FolderNode, session: SessionNode };

export function TopologyView({ phone, onBack, tree }: { phone: boolean; onBack: () => void; tree: Tree }) {
  useLang();
  const shape = tree.workspaces.map((w) => `${w.key}:${w.rows.map((r) => (r.kind === "session" ? `${r.s.id}${r.s.status}${titleOf(r.s)}` : `${r.f.key}[${r.f.sessions.map((s) => `${s.id}${s.status}${titleOf(s)}`).join()}]`)).join()}`).join("|");
  const { nodes, edges } = useMemo(() => graphOf(tree), [shape]);
  return (
    <div className="relative h-full">
      <header className="glass absolute inset-x-0 top-0 z-20 flex h-12 items-center gap-1 pr-2 pl-2 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13">
        {phone && (
          <IconKey label={t("nav.back")} onClick={onBack} tipSide="bottom" className="-mr-1">
            <ChevronLeft size={20} />
          </IconKey>
        )}
        <h1 className={cn("m-0 text-base font-semibold", !phone && "pl-2")}>{t("nav.topology")}</h1>
      </header>
      <div className="ir-flow absolute inset-0 pt-12">
        {nodes.length ? (
          <ReactFlow nodes={nodes} edges={edges} nodeTypes={TYPES} fitView fitViewOptions={{ padding: 0.12, maxZoom: 1 }} minZoom={0.2} maxZoom={1.6} nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} proOptions={{ hideAttribution: true }}>
            <Background gap={22} size={1} color="var(--fill-2)" />
            <Controls showInteractive={false} position="bottom-right" />
          </ReactFlow>
        ) : (
          <p className="m-0 pt-24 text-center text-sm text-ink-3">{t("nav.empty")}</p>
        )}
      </div>
    </div>
  );
}
