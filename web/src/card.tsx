// The confirmation panel's one card (plan "确认面板"): approvals, plans, questions, ir's invites and
// tokens, the owner's word for Claude, deleting. A title, one line of summary, a main and a second
// key; the details open under it. The chat's asks (chat/asks.tsx), the confirmations agents ask a
// panel for (confirms.tsx) and the settings' own (terminals.tsx) all draw with these.
import { ChevronDown, LoaderCircle } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { t } from "./i18n";
import { cn } from "./ui";

/** A card's frame: frosted, popping in; leaving, it shrinks away. */
export function CardFrame({ label, leaving, children, className }: { label: string; leaving?: boolean; children: ReactNode; className?: string }) {
  return (
    <div data-leaving={leaving || undefined} className={cn("g-pop glass float rest @container rounded-lg p-2.5 pl-3", className)} role="alertdialog" aria-label={label}>
      {children}
    </div>
  );
}

/** A card's top line: the icon, the title (a faint tag after it), the summary under it, cut to one
 *  line till the details open; the keys at the end, under it on a narrow card. */
export function CardHead({ icon, title, tag, summary, mono, open, setOpen, keys }: { icon: ReactNode; title: string; tag?: string; summary: ReactNode; mono?: boolean; open?: boolean; setOpen?: (o: boolean) => void; keys: ReactNode }) {
  const body = (
    <>
      <span className="mt-0.5 shrink-0 text-ink-2">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 text-sm font-semibold">
          <span className="truncate">{title}</span>
          {tag && <span className="truncate font-normal text-ink-3">{tag}</span>}
          {setOpen && <ChevronDown size={13} className="shrink-0 text-ink-4 transition-transform duration-(--dur-2) ease-(--ease-out)" style={{ transform: open ? "rotate(180deg)" : undefined }} aria-label={t("ask.details")} />}
        </span>
        <span className={cn("block text-xs leading-5 text-ink-2", mono && "font-mono", open ? "break-all whitespace-pre-wrap" : "truncate")}>{summary}</span>
      </span>
    </>
  );
  return (
    <div className="flex flex-col gap-2.5 @[34rem]:flex-row @[34rem]:items-center">
      {setOpen ? (
        <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="group flex min-w-0 flex-1 items-start gap-2.5 rounded-sm text-left">
          {body}
        </button>
      ) : (
        <div className="flex min-w-0 flex-1 items-start gap-2.5">{body}</div>
      )}
      <div className="flex shrink-0 justify-end gap-1.5">{keys}</div>
    </div>
  );
}

/** The second key: deny, cancel, keep planning. */
export function SecondKey({ className, ...p }: ComponentProps<"button">) {
  return <button type="button" {...p} className={cn("g-press h-8 rounded-md bg-fill px-3 text-sm hover:bg-fill-2 disabled:opacity-50 pointer-coarse:h-10", className)} />;
}

/** The main key: allow, approve, confirm; busy, it spins. */
export function MainKey({ busy, className, children, ...p }: ComponentProps<"button"> & { busy?: boolean }) {
  return (
    <button type="button" disabled={busy || p.disabled} {...p} className={cn("g-press flex h-8 items-center gap-1.5 rounded-md bg-ink px-3 text-sm font-medium text-on-ink disabled:opacity-60 pointer-coarse:h-10", className)}>
      {busy && <LoaderCircle size={13} className="animate-spin" />}
      {children}
    </button>
  );
}

/** The details under a card's head, lined up with its title. */
export function CardDetails({ children }: { children: ReactNode }) {
  return <div className="g-fade mt-2.5 grid gap-2 pl-[26px] text-sm">{children}</div>;
}

/** Rows of a card's details: a faint name, its value. */
export function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-ink-3">{k}</dt>
          <dd className="m-0 min-w-0 break-all text-ink-2">{v}</dd>
        </div>
      ))}
    </dl>
  );
}
