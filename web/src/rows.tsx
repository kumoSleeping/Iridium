// A settings list's rows (settings.tsx, device/page.tsx, apps.tsx, rolespage.tsx, manage.tsx): a
// group under its small title, a setting with its control, 管理's line and what it unfolds. Apart
// from the settings so that 管理 and the cache page, which use them, do not pull the settings in.
import { ChevronRight } from "lucide-react";
import { useState, type ReactNode } from "react";
import { cn, intoView } from "./ui";

/** fold: shut till its title is pressed (the approval modes, the user, 2026-11-06). */
export function Group({ title, children, fold, action }: { title: string; children: ReactNode; fold?: boolean; action?: ReactNode }) {
  const [open, setOpen] = useState(!fold);
  if (!fold)
    return (
      <section className="flex flex-col">
        <h2 className="m-0 mb-1 flex h-4 items-center text-xs font-medium text-ink-3">
          <span className="min-w-0 flex-1 truncate">{title}</span>
          {action}
        </h2>
        {children}
      </section>
    );
  return (
    <section className="flex flex-col">
      <h2 className="m-0 mb-1 text-xs font-medium text-ink-3">
        <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="-mx-1 flex items-center gap-1 rounded-sm px-1 hover:text-ink-2">
          {title}
          <ChevronRight size={12} className="transition-transform duration-(--dur-2) ease-(--ease-out)" style={{ transform: open ? "rotate(90deg)" : undefined }} />
        </button>
      </h2>
      {open && <div className="g-fade flex flex-col">{children}</div>}
    </section>
  );
}

/** below: its control under the words, the whole width (a select whose options are long). */
export function Item({ label, hint, children, below }: { label: ReactNode; hint?: ReactNode; children: ReactNode; below?: boolean }) {
  if (below)
    return (
      <div className="flex flex-col gap-1.5 py-1.5">
        <div>
          <div className="text-sm">{label}</div>
          {hint && <div className="text-xs text-ink-3">{hint}</div>}
        </div>
        {children}
      </div>
    );
  return (
    <div className="flex min-h-10 items-center gap-3 py-1">
      <div className="min-w-0 flex-1">
        <div className="text-sm">{label}</div>
        {hint && <div className="text-xs text-ink-3">{hint}</div>}
      </div>
      <div className="flex shrink-0 items-center">{children}</div>
    </div>
  );
}

/** A setting's line: its name at the left, its value or control at the right, one line (kumo,
 *  2026-11-27: no column of names beside the values); pressed (onClick), what
 *  is under it unfolds below (kumo, 2026-11-27: nothing inside goes to a page of its own; there is
 *  room), its chevron turned down while open. */
export function Field({ label, children, onClick, open }: { label: ReactNode; children: ReactNode; onClick?: () => void; open?: boolean }) {
  const body = (
    <>
      <span className="shrink-0 text-sm whitespace-nowrap">{label}</span>
      <span className="flex min-w-0 flex-1 items-center justify-end gap-2 text-sm text-ink-2">{children}</span>
      {onClick && <ChevronRight size={15} className={cn("shrink-0 text-ink-4 transition-transform duration-(--dur-2) ease-(--ease-out)", open && "rotate-90")} />}
    </>
  );
  return onClick ? (
    <button type="button" onClick={onClick} aria-expanded={open} className="g-press -mx-2 flex min-h-11 items-center gap-3 rounded-md px-2 text-left hover:bg-wash">
      {body}
    </button>
  ) : (
    <div className="flex min-h-11 items-center gap-3">{body}</div>
  );
}

/** What a line opens, unfolding under it (its height slides; shut, it stays drawn till folded),
 *  scrolled into sight as it opens. */
export function Unfold({ open, children }: { open: boolean; children: ReactNode }) {
  const [shown, setShown] = useState(open);
  if (open && !shown) setShown(true);
  return (
    <div
      aria-hidden={!open || undefined}
      inert={!open || undefined}
      onTransitionEnd={(e) => e.target === e.currentTarget && (open ? intoView(e.currentTarget) : setShown(false))}
      className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-3) ease-(--ease-out)", open ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}
    >
      <div className="min-h-0 overflow-hidden">{shown && <div className="pb-2">{children}</div>}</div>
    </div>
  );
}
