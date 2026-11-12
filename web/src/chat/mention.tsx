// Sessions named in what you write (a row dropped on the composer, chat.tsx, or picked after @ in
// it, composer.tsx): in the composer a card among the files; sent, the same card in the bubble's
// row of files (the user, 2026-11-08). One way a session shows in a conversation (the user,
// 2026-11-09): a session named among the words (older messages, letters) or by its id (an agent's
// words, ir send 4ceb1321 in a step) is the same card, in a row under it; the words stay as they
// were written. Each goes there. A place named (docs/plan.md "@", 2026-11-10) is a card of the same
// kind: its folder (a cloud on it at another machine), its name, where it is; pressed, the list
// shows it (places.ts).
// Sessions and places named in what is written (docs/plan.md "@"; the user, 2026-11-12): a link of
// Iridium's among the words (refs.ts Mention), where it was put, drawn there as a download link is:
// its agent's logo (a folder for a place), its name, underlined; pressed, it goes there (a place:
// the list shows it, places.ts). In the composer's box the same, under its letters (MentionMarks).
// A step's sessions (ir new, ir send 4ceb1321) are cards under it (MentionCard, NamedCards).
import { useLayoutEffect, useMemo, useRef, type ReactNode, type RefObject } from "react";
import type { Agent, Session } from "../api/types.gen";
import { openChat } from "../route";
import { useList } from "../store";
import { useMachines } from "../data";
import { t } from "../i18n";
import { Folder, MessageSquare } from "lucide-react";
import { CloudBadge, MachineFace, type Machine } from "../machines";
import { openPlace, type Place } from "../places";
import { AGENTS, agentName, cn, Logo, tilde, Tip, titleOf } from "../ui";
import { baseName, nativeAt, type NamedPlace } from "./at";
import { boxed, MARK, mentions, placeLink, sessionIds, sessionLink, shortId, type Mention } from "./refs";

/** How the composer names session to, among list: what a row dropped or picked after @ puts in. */
export const mentionOf = (to: Session, list: Session[]) => sessionLink(titleOf(to) || tilde(to.cwd), shortId(to.id, list.map((x) => x.id)));

/** How the composer names place p: a folder here by its path; one at another machine by the
 *  machine's name (its ID if the name has what would end it) and the path as it writes it. */
export function placeMention(p: NamedPlace, ms: Machine[]): string {
  const name = p.name || baseName(p.path) || "/";
  if (!p.machine) return placeLink(name, p.path);
  const m = ms.find((x) => x.id === p.machine);
  return placeLink(name, nativeAt(m, p.path), m && !/[\s:/]/.test(m.name) ? m.name : p.machine);
}

/** The machine a mention names: by its ID, else its name in any case. */
export const machineNamed = (name: string, ms: Machine[]) => ms.find((m) => m.id === name) ?? ms.find((m) => m.name.toLowerCase() === name.toLowerCase());

/** The place a mention names, as the page has it (its machine's ID, its path '/'-separated);
 *  none while its machine is not known (renamed, unpaired). */
export function placeOf(m: { machine?: string; path: string }, ms: Machine[]): Place | undefined {
  if (!m.machine) return { path: m.path };
  const x = machineNamed(m.machine, ms);
  return x && { machine: x.id, path: m.path.replaceAll("\\", "/") };
}

/** A folder's mark, a cloud on it at another machine. */
export function PlaceIcon({ remote, label, size = 16 }: { remote: boolean; label?: string; size?: number }) {
  return (
    <span className="relative shrink-0 text-ink-3">
      <Folder size={size} className="block" aria-hidden />
      {remote && <CloudBadge label={label ?? t("nav.remoteFolder")} />}
    </span>
  );
}

/** A place's card inside: its folder, its name (the machine's face after it at another), and where:
 *  "otae-2 · C:\Code\Ds_Benchmark", "本机 · /root". machine: the one it is at, known; path: as
 *  that machine writes it. */
export function PlaceLines({ name, remote, machine, machineName, path }: { name: string; remote: boolean; machine?: Machine; machineName?: string; path: string }) {
  return (
    <>
      <PlaceIcon remote={remote} label={machine?.name} />
      <span className="min-w-0">
        <span className="flex min-w-0 items-center">
          <span className="truncate text-sm text-ink">{name}</span>
          {machine && <MachineFace m={machine} size={12} className={cn("ml-1 -translate-y-[1.5px]", !machine.online && "opacity-50 grayscale")} />}
        </span>
        <span className="block truncate text-xs text-ink-3">
          {remote ? (machine?.name ?? machineName) : t("new.local")} · {path}
        </span>
      </span>
    </>
  );
}

/** Whether the words name a session or a place. */
export const hasMention = (text: string) => mentions(text).some((p) => typeof p !== "string");

/** Behind the composer's box, whose own letters are clear while it names something: the same words
 *  in the same place (refs.ts boxed), each named one on a soft ground, its mark in the slot before
 *  its name. Only colours and grounds, never a width, so each letter lies under the caret's.
 *  Scrolls with the box. */
export function MentionMarks({ text, box, className }: { text: string; box: RefObject<HTMLTextAreaElement | null>; className: string }) {
  const back = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const sync = () => back.current && (back.current.scrollTop = el.scrollTop);
    sync();
    el.addEventListener("scroll", sync);
    return () => el.removeEventListener("scroll", sync);
  }, [box, text]);
  const b = boxed(text);
  const parts: ReactNode[] = [];
  let at = 0;
  for (const r of b.refs) {
    if (r.from > at) parts.push(b.value.slice(at, r.from));
    parts.push(
      <span key={r.from} className="mention-mark">
        <span className="relative">
          {MARK}
          <span className="absolute inset-0 grid place-items-center">
            <Mark m={r.m} size={14} />
          </span>
        </span>
        {b.value.slice(r.from + 1, r.to)}
      </span>,
    );
    at = r.to;
  }
  parts.push(b.value.slice(at));
  return (
    <div ref={back} aria-hidden className={cn("pointer-events-none absolute inset-0 overflow-hidden whitespace-pre-wrap text-ink [overflow-wrap:break-word]", className)}>
      {parts}
      {/* A last empty line is as high as the box draws it. */}
      {"\u200b"}
    </div>
  );
}

/** A named one's mark: a session's agents' logos (one now; a channel of several, each), a place's
 *  folder. */
function Mark({ m, size }: { m: Mention; size: number }) {
  const list = useList();
  if (m.kind === "place") return <PlaceIcon remote={!!m.machine} size={size} />;
  const s = list?.find((x) => x.id.startsWith(m.id));
  const agents: Agent[] = s ? [s.agent] : m.agent ? AGENTS.filter((a) => a === m.agent || agentName(a) === m.agent) : [];
  if (!agents.length) return <MessageSquare size={size} className="shrink-0 text-ink-3" />;
  return (
    <span className="inline-flex shrink-0">
      {agents.map((a, i) => (
        <Logo key={a} agent={a} size={size} className={cn(i > 0 && "-ml-1 rounded-full ring-1 ring-paper")} />
      ))}
    </span>
  );
}

/** A session or a place named among the words: its mark, its name underlined, as a file link is;
 *  pressed, it goes there. A session's name is the sidebar's now; one gone, or a place at a machine
 *  not known, is its words faint. */
export function NamedLink({ m }: { m: Mention }) {
  const list = useList();
  const ms = useMachines();
  const s = m.kind === "session" ? list?.find((x) => x.id.startsWith(m.id)) : undefined;
  const at = m.kind === "place" ? placeOf(m, ms) : undefined;
  const name = m.kind === "place" ? m.name : (s && (titleOf(s) || tilde(s.cwd))) || m.title || m.id;
  const go = s ? () => openChat(s.id) : at ? () => openPlace(at) : undefined;
  const tip = m.kind === "place" ? `${m.machine ?? t("new.local")} · ${m.path}` : s ? `${agentName(s.agent)} · ${shortId(s.id, (list ?? []).map((x) => x.id))}` : t("composer.goneSession");
  return (
    <Tip label={tip}>
      <button
        type="button"
        disabled={!go}
        onClick={go}
        className="inline-flex max-w-full items-baseline gap-1 rounded-sm align-baseline text-ink underline decoration-ink-4 underline-offset-2 enabled:hover:decoration-ink-2 disabled:text-ink-3 disabled:no-underline"
      >
        <span className="self-center">
          <Mark m={m} size={14} />
        </span>
        <span className="min-w-0 [overflow-wrap:anywhere]">{name}</span>
      </button>
    </Tip>
  );
}

/** Plain words (yours, a letter) with what they name drawn in place. */
export function Worded({ text }: { text: string }) {
  return (
    <>
      {mentions(text).map((p, i) => (typeof p === "string" ? p : <NamedLink key={i} m={p} />))}
    </>
  );
}

const CARD = "g-press flex h-11 w-44 max-w-full items-center gap-2 rounded-lg bg-paper/70 px-2.5 text-left shadow-[0_0_0_0.5px_var(--edge)] enabled:hover:bg-paper disabled:opacity-60";

/** A session named before a message's words, as its card was in the composer: its logo, its title
 *  (as the sidebar has it now), its agent and short id; pressed, it goes there. A place: its card;
 *  pressed, the list shows it, or the "+" opens there (places.ts). */
export function MentionCard({ m }: { m: Mention }) {
  return m.kind === "place" ? <PlaceCard m={m} /> : <SessionCard m={m} />;
}

function PlaceCard({ m }: { m: Extract<Mention, { kind: "place" }> }) {
  const ms = useMachines();
  const at = placeOf(m, ms);
  const machine = at?.machine ? ms.find((x) => x.id === at.machine) : undefined;
  return (
    <button type="button" disabled={!at} onClick={() => at && openPlace(at)} className={CARD}>
      <PlaceLines name={m.name} remote={!!m.machine} machine={machine} machineName={m.machine} path={m.path} />
    </button>
  );
}

function SessionCard({ m }: { m: Extract<Mention, { kind: "session" }> }) {
  const list = useList();
  const s = list?.find((x) => x.id.startsWith(m.id));
  const agent: Agent | undefined = s?.agent ?? AGENTS.find((a) => a === m.agent || agentName(a) === m.agent);
  const title = (s && (titleOf(s) || tilde(s.cwd))) || m.title;
  return (
    <button
      type="button"
      disabled={!s}
      onClick={() => s && openChat(s.id)}
      className={CARD}
    >
      {agent ? <Logo agent={agent} size={16} className="shrink-0" /> : <MessageSquare size={16} className="shrink-0 text-ink-3" />}
      <span className="min-w-0">
        <span className="block truncate text-sm text-ink">{title}</span>
        <span className="block truncate text-xs text-ink-3">
          {agent ? agentName(agent) : (m.agent ?? "")} · {m.id}
        </span>
      </span>
    </button>
  );
}

/** A session met by its id, as a mention names it. */
const named = (s: Session, id: string, list: Session[]): Mention => ({ kind: "session", title: titleOf(s) || tilde(s.cwd), agent: s.agent, id: shortId(s.id, list.map((x) => x.id)), text: id });

/** The sessions and places text names, each once, in order: as the composer names them, and
 *  sessions by id (or the start of one, as ir prints it); also's too while it names few (ir new,
 *  not ir ls's list). */
export function useNamed(text: string, also?: string): Mention[] {
  const list = useList();
  return useMemo(() => {
    const of = (x: string) => {
      const out: Mention[] = [];
      for (const p of mentions(x))
        if (typeof p !== "string") out.push(p);
        else for (const q of list ? sessionIds(p, list) : []) if (typeof q !== "string") out.push(named(q.s, q.id, list!));
      return out;
    };
    const key = (m: Mention) => (m.kind === "place" ? `p:${m.machine?.toLowerCase() ?? ""}:${m.path.replaceAll("\\", "/")}` : (list?.find((x) => x.id.startsWith(m.id))?.id ?? m.id));
    const more = also ? of(also) : [];
    const all = [...of(text), ...(new Set(more.map(key)).size <= 3 ? more : [])];
    return all.filter((m, i) => all.findIndex((n) => key(n) === key(m)) === i);
  }, [text, also, list]);
}

/** The sessions and places text names, as the cards a message's mentions are: a row, or nothing. */
export function NamedCards({ text, also, className }: { text: string; also?: string; className?: string }) {
  const named = useNamed(text, also);
  if (!named.length) return null;
  return (
    <div className={cn("flex flex-wrap gap-1.5", className)}>
      {named.map((m, i) => (
        <MentionCard key={i} m={m} />
      ))}
    </div>
  );
}
