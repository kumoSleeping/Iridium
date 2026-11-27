// A settings list's rows (settings.tsx, device/page.tsx, apps.tsx, rolespage.tsx): a group under
// its small title, and a setting with its control. Apart from the settings so that 管理 and the
// cache page, which use them, do not pull the settings in with them.
import { ChevronRight } from "lucide-react";
import { useState, type ReactNode } from "react";

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
