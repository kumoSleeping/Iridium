// A merged forward in what is said (forward.ts), drawn as the card a relay in an agent's own record
// is (said.tsx Relayed: 聊天记录 · N 条, a line per message, who and what): folded to its head line;
// pressed, it opens in place, its height gliding as a tool's fold does; a forward inside one of
// its messages is a card of its own, opened in turn.
import { ChevronRight, MessagesSquare } from "lucide-react";
import { useState, type ReactNode } from "react";
import { t } from "../i18n";
import { cn } from "../ui";
import { forwardCount, msgWords, type Fwd } from "./forward";

/** A message of a forward as its line: who said it, its words (dim: an earlier one, faint). */
export function ForwardRow({ who, dim, clamp, children }: { who: string; dim?: boolean; clamp?: boolean; children: ReactNode }) {
  return (
    <div className={cn("flex min-w-0 gap-2 text-sm", dim && "text-ink-3")}>
      <span className={cn("shrink-0 font-medium", dim ? "text-ink-3" : "text-ink-2")}>{who}</span>
      <div className={cn("min-w-0 flex-1 whitespace-pre-wrap [overflow-wrap:anywhere]", clamp ? "line-clamp-3" : "flex flex-col gap-1")}>{children}</div>
    </div>
  );
}

/** The card's ground: a forward's, a relay's. */
export const FORWARD_CARD = "flex max-w-[min(560px,100%)] min-w-0 flex-col self-start rounded-xl rounded-tl-sm bg-fill px-3.5 py-2.5";

/** Its head's mark and words: 聊天记录 · N 条. */
export function ForwardHead({ n }: { n: number }) {
  return (
    <>
      <MessagesSquare size={12} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">{t("channel.forward", { n })}</span>
    </>
  );
}

export function ForwardCard({ f, inner }: { f: Fwd; inner?: boolean }) {
  const [open, setOpen] = useState(false);
  // What it holds is drawn once it has been opened.
  const [ever, setEver] = useState(false);
  return (
    <div className={cn(FORWARD_CARD, inner && "w-full rounded-lg px-3 py-2")}>
      <button type="button" aria-expanded={open} onClick={() => (setEver(true), setOpen(!open))} className="flex min-w-0 items-center gap-1.5 text-left text-xs text-ink-3 hover:text-ink-2">
        <ForwardHead n={forwardCount(f)} />
        <ChevronRight size={12} className={cn("shrink-0 text-ink-4 transition-transform duration-(--dur-2)", open && "rotate-90")} />
      </button>
      <div inert={!open || undefined} aria-hidden={!open || undefined} className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-2) ease-(--ease-out)", open ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
        <div className="min-h-0 overflow-hidden">
          {ever && (
            <div className="flex min-w-0 flex-col gap-1 pt-1.5">
              {f.msgs.map((m, i) => (
                <ForwardRow key={`${m.id}:${i}`} who={m.who || "?"}>
                  {m.text && <span>{m.text}</span>}
                  {m.forwards.map((g, j) => (
                    <ForwardCard key={j} f={g} inner />
                  ))}
                </ForwardRow>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/** The forwards of a message, each its card, under its words. */
export function Forwards({ fs }: { fs?: Fwd[] }) {
  if (!fs?.length) return null;
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {fs.map((f, i) => (
        <ForwardCard key={i} f={f} />
      ))}
    </div>
  );
}

/** A message as a quote of it shows it: its words, or the forward it is (聊天记录 · N 条). */
export function quoteText(m: { text: string }): string {
  const w = msgWords(m);
  return w.words || (w.forwards.length ? t("channel.forward", { n: forwardCount(w.forwards[0]!) }) : "");
}
