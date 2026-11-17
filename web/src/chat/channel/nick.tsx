// A member named among the words of a channel (docs/channels.md; agents, people and apps are
// peers, the user, 2026-11-13): drawn as a session named is (mention.tsx NamedLink), the @ kept
// in front (the user, 2026-11-12), its name, then its face (kumo, 2026-11-17; an agent's own, as
// beside its words, its model's face.tsx; a person's or an app's avatar), underlined; pressed, its card, the one its face opens. The names of all
// (全体成员, 全体Agent) a mark and the words, no key. One form, in a person's words and in an
// agent's Markdown (chat/markdown.tsx, by the link lib.ts linkNicks makes of it).
import { Popover } from "@base-ui/react/popover";
import { Bot, Users } from "lucide-react";
import { createContext, useContext, useRef, useState, type ReactNode } from "react";
import { Avatar, PopSurface } from "../../ui";
import { Face } from "./face";
import { Worded } from "../mention";
import { fromSatori } from "../refs";
import { ALL_AGENTS, ALL_MEMBERS, nickParts, type AgentRow } from "./lib";
import { CARD_POP } from "./profilecard";

/** The channel's members as words may name them: its agents, its people and apps (person rows:
 *  their name as nick); names, all that an @ may name, the names of all among them; card, what a
 *  press on one opens (done: put it away). */
export type Named = { agents: AgentRow[]; people: AgentRow[]; names: string[]; card: (m: AgentRow, done: () => void) => ReactNode };
export const Nicks = createContext<Named | null>(null);

/** All that an @ may name in a channel of these. */
export const namesOf = (agents: AgentRow[], people: AgentRow[]) => [...agents.map((a) => a.nick), ...people.map((p) => p.nick), ALL_MEMBERS, ALL_AGENTS].filter(Boolean);

const LINK = "inline-flex max-w-full items-baseline gap-1 rounded-sm align-baseline text-ink";

export function NickLink({ nick }: { nick: string }) {
  const ch = useContext(Nicks);
  const [open, setOpen] = useState(false);
  const key = useRef<HTMLButtonElement>(null);
  const w = nick.toLowerCase();
  const all = w === ALL_MEMBERS.toLowerCase() ? Users : w === ALL_AGENTS.toLowerCase() ? Bot : undefined;
  if (ch && all) {
    const Mark = all;
    return (
      <span className={LINK}>
        <span className="-mr-0.5 text-ink-3">@</span>
        <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{nick}</span>
        <Mark size={13} className="shrink-0 self-center text-ink-2" />
      </span>
    );
  }
  const m = ch && (ch.agents.find((x) => x.nick.toLowerCase() === w) ?? ch.people.find((x) => x.nick.toLowerCase() === w));
  if (!ch || !m) return <>@{nick}</>;
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger
        render={
          <button ref={key} type="button" className={`${LINK} underline decoration-ink-4 underline-offset-2 hover:decoration-ink-2`}>
            <span className="-mr-0.5 text-ink-3">@</span>
            <span className="min-w-0 [overflow-wrap:anywhere]">{m.nick}</span>
            <span className="self-center">{m.person ? <Avatar src={m.avatar} name={m.nick} size={14} /> : <Face id={m.session} agent={m.agent} size={14} />}</span>
          </button>
        }
      />
      <PopSurface anchor={key.current} still className={CARD_POP}>{ch.card(m, () => setOpen(false))}</PopSurface>
    </Popover.Root>
  );
}

/** A person's words in a channel: what they name drawn in place (Worded), the members they name
 *  too. Outside a channel's page (no members given): the words as Worded draws them. */
export function NickWords({ text }: { text: string }) {
  const ch = useContext(Nicks);
  if (!ch) return <Worded text={text} />;
  // An <at> written in them reads as "@nick" (Satori's elements and Markdown mixed).
  const words = fromSatori(text, (session) => ch.agents.find((a) => a.session === session)?.nick);
  const parts = nickParts(words, ch.names);
  return <>{parts.map((p, i) => (typeof p === "string" ? <Worded key={i} text={p} /> : <NickLink key={i} nick={p.nick} />))}</>;
}
