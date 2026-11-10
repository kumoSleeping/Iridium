// Sessions named in what you write (a row dropped on the composer, chat.tsx, or picked after @ in
// it, composer.tsx): in the composer a card among the files; sent, the same card in the bubble's
// row of files (the user, 2026-11-08). One way a session shows in a conversation (the user,
// 2026-11-09): a session named among the words (older messages, letters) or by its id (an agent's
// words, ir send 4ceb1321 in a step) is the same card, in a row under it; the words stay as they
// were written. Each goes there. A place named (docs/plan.md "@", 2026-11-10) is a card of the same
// kind: its folder (a cloud on it at another machine), its name, where it is; pressed, the list
// shows it (places.ts).
import { useLayoutEffect, useMemo, useRef, type RefObject } from "react";
import type { Agent, Session } from "../api/types.gen";
import { openChat } from "../route";
import { useList } from "../store";
import { useMachines } from "../data";
import { t } from "../i18n";
import { Folder, MessageSquare } from "lucide-react";
import { CloudBadge, MachineFace, type Machine } from "../machines";
import { openPlace, type Place } from "../places";
import { AGENTS, agentName, cn, Logo, tilde, titleOf } from "../ui";
import { baseName, nativeAt, type NamedPlace } from "./at";
import { mentions, sessionIds, shortId, type Mention } from "./refs";

/** How the composer names session to, among list: what a row dropped or picked after @ puts in. */
export const mentionOf = (to: Session, list: Session[]) => t("composer.mention", { title: titleOf(to) || tilde(to.cwd), agent: agentName(to.agent), id: shortId(to.id, list.map((x) => x.id)) });

/** How the composer names place p: a folder here by its path; one at another machine by the
 *  machine's name (its ID if the name has what would end it) and the path as it writes it. Its
 *  name without the marks that close it (" 」 as ” 』), on one line, at most 200 long, so it reads
 *  back (refs.ts mentions). */
export function placeMention(p: NamedPlace, ms: Machine[]): string {
  const name = sayable(p.name) || sayable(baseName(p.path)) || "/";
  if (!p.machine) return t("composer.mentionFolder", { name, path: p.path });
  const m = ms.find((x) => x.id === p.machine);
  const machine = m && !/[\n:「」（）"()]/.test(m.name) ? m.name : p.machine;
  return t("composer.mentionWorkspace", { name, machine, path: nativeAt(m, p.path) });
}

function sayable(name: string): string {
  let out = "";
  for (const c of name.replaceAll('"', "”").replaceAll("」", "』").replace(/\s+/g, " ").trim()) {
    if (out.length + c.length > 200) break;
    out += c;
  }
  return out;
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

/** Behind the composer's box, whose own letters are clear while it names a session: the same words
 *  in the same place, a mention on a soft ground, its agent and id faint. Only colours and grounds,
 *  never a width, so each letter lies under the caret's. Scrolls with the box. */
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
  return (
    <div ref={back} aria-hidden className={cn("pointer-events-none absolute inset-0 overflow-hidden whitespace-pre-wrap text-ink [overflow-wrap:break-word]", className)}>
      {mentions(text).map((p, i) =>
        typeof p === "string" ? (
          p
        ) : (
          <span key={i} className="mention-mark">
            {p.text.slice(0, head(p))}
            <span className="text-ink-3">{p.text.slice(head(p))}</span>
          </span>
        ),
      )}
      {/* A last empty line is as high as the box draws it. */}
      {"\u200b"}
    </div>
  );
}

/** The quoted title of a mention, before its agent and id (a place's, before its path). */
function head(p: Mention) {
  if (p.kind === "place") {
    const i = p.text.indexOf("」（");
    return i > 0 ? i + 1 : p.text.indexOf('" (') + 1 || p.text.length;
  }
  const i = p.text.endsWith("）") ? p.text.lastIndexOf("（") : p.text.lastIndexOf(" (");
  return i > 0 ? i : p.text.length;
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
  const agent: Agent | undefined = s?.agent ?? AGENTS.find((a) => agentName(a) === m.agent);
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
          {agent ? agentName(agent) : m.agent} · {m.id}
        </span>
      </span>
    </button>
  );
}

/** A session met by its id, as a mention names it. */
const named = (s: Session, id: string, list: Session[]): Mention => ({ kind: "session", title: titleOf(s) || tilde(s.cwd), agent: agentName(s.agent), id: shortId(s.id, list.map((x) => x.id)), text: id });

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
