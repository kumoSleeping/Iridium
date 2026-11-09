// Sessions named in what you write (a row dropped on the composer, chat.tsx, or picked after @ in
// it, composer.tsx): in the composer a card among the files; sent, the same card in the bubble's
// row of files (the user, 2026-11-08). One way a session shows in a conversation (the user,
// 2026-11-09): a session named among the words (older messages, letters) or by its id (an agent's
// words, ir send 4ceb1321 in a step) is the same card, in a row under it; the words stay as they
// were written. Each goes there.
import { useLayoutEffect, useMemo, useRef, type RefObject } from "react";
import type { Agent, Session } from "../api/types.gen";
import { openChat } from "../route";
import { useList } from "../store";
import { t } from "../i18n";
import { MessageSquare } from "lucide-react";
import { AGENTS, agentName, cn, Logo, tilde, titleOf } from "../ui";
import { mentions, sessionIds, shortId, type Mention } from "./refs";

/** How the composer names session to, among list: what a row dropped or picked after @ puts in. */
export const mentionOf = (to: Session, list: Session[]) => t("composer.mention", { title: titleOf(to) || tilde(to.cwd), agent: agentName(to.agent), id: shortId(to.id, list.map((x) => x.id)) });

/** Whether the words name a session. */
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

/** The quoted title of a mention, before its agent and id. */
function head(p: Mention) {
  const i = p.text.endsWith("）") ? p.text.lastIndexOf("（") : p.text.lastIndexOf(" (");
  return i > 0 ? i : p.text.length;
}

/** A session named before a message's words, as its card was in the composer: its logo, its title
 *  (as the sidebar has it now), its agent and short id; pressed, it goes there. */
export function MentionCard({ m }: { m: Mention }) {
  const list = useList();
  const s = list?.find((x) => x.id.startsWith(m.id));
  const agent: Agent | undefined = s?.agent ?? AGENTS.find((a) => agentName(a) === m.agent);
  const title = (s && (titleOf(s) || tilde(s.cwd))) || m.title;
  return (
    <button
      type="button"
      disabled={!s}
      onClick={() => s && openChat(s.id)}
      className="g-press flex h-11 w-44 max-w-full items-center gap-2 rounded-lg bg-paper/70 px-2.5 text-left shadow-[0_0_0_0.5px_var(--edge)] enabled:hover:bg-paper disabled:opacity-60"
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
const named = (s: Session, id: string, list: Session[]): Mention => ({ title: titleOf(s) || tilde(s.cwd), agent: agentName(s.agent), id: shortId(s.id, list.map((x) => x.id)), text: id });

/** The sessions text names, each once, in order: as the composer names them, and by id (or the
 *  start of one, as ir prints it); also's too while it names few (ir new, not ir ls's list). */
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
    const key = (m: Mention) => list?.find((x) => x.id.startsWith(m.id))?.id ?? m.id;
    const more = also ? of(also) : [];
    const all = [...of(text), ...(new Set(more.map(key)).size <= 3 ? more : [])];
    return all.filter((m, i) => all.findIndex((n) => key(n) === key(m)) === i);
  }, [text, also, list]);
}

/** The sessions text names, as the cards a message's mentions are: a row, or nothing. */
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
