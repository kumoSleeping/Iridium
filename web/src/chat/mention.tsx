// Sessions named in what you write (a row dropped on the composer, chat.tsx, or picked after @ in
// it, composer.tsx): in the composer a
// soft mark under the words, in a sent message a chip with the session's logo and title that goes
// there (the user, 2026-11-06). The words themselves stay plain text, what the agent reads.
import { useLayoutEffect, useRef, type RefObject } from "react";
import type { Agent, Session } from "../api/types.gen";
import { openChat } from "../route";
import { useList } from "../store";
import { t } from "../i18n";
import { AGENTS, agentName, cn, Logo, tilde, Tip, titleOf } from "../ui";
import { mentions, shortId, type Mention } from "./refs";

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

/** A message's words, a session it names a chip: its logo and title (as the sidebar has it now),
 *  going to it; its agent and id in the tip. */
export function Mentioned({ text }: { text: string }) {
  const list = useList();
  const parts = mentions(text);
  if (parts.length === 1 && typeof parts[0] === "string") return <>{text}</>;
  return (
    <>
      {parts.map((p, i) => (typeof p === "string" ? p : <Chip key={i} m={p} s={list?.find((x) => x.id.startsWith(p.id))} />))}
    </>
  );
}

function Chip({ m, s }: { m: Mention; s?: Session }) {
  const agent: Agent | undefined = s?.agent ?? AGENTS.find((a) => agentName(a) === m.agent);
  const title = (s && titleOf(s)) || m.title;
  return (
    <Tip label={`${title} · ${agent ? agentName(agent) : m.agent} · ${m.id}`}>
      <button
        type="button"
        disabled={!s}
        onClick={() => s && openChat(s.id)}
        className="g-press mx-[3px] inline-flex max-w-[min(100%,14em)] items-center gap-[5px] rounded-[5px] bg-paper/85 py-px pr-1.5 pl-[5px] align-[-2px] text-[0.86em] leading-[17px] text-ink-2 shadow-[0_0_0_0.5px_var(--edge)] enabled:hover:bg-paper enabled:hover:text-ink disabled:opacity-60"
      >
        {agent && <Logo agent={agent} size={12} className="shrink-0" />}
        <span className="truncate">{title}</span>
      </button>
    </Tip>
  );
}
