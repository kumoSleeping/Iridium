// The notes' key and mark (notes.tsx), apart from it: the key stands in the session list's head from
// the first, the notes' code comes only as they are opened.
import type { CSSProperties, ReactNode } from "react";
import { t } from "./i18n";
import { IconKey } from "./ui";

/** The key that shows the notes in the session list's place, and back (a computer's; sidebar.tsx).
 *  Out, a note dragged and held on it turns back to the sessions. */
export function NotesKey({ open, onClick }: { open: boolean; onClick: () => void }) {
  return (
    <IconKey
      label={open ? t("nav.sessions") : t("note.title")}
      onClick={onClick}
      data-spring={open ? "" : undefined}
      data-on={open ? "" : undefined}
      className="size-8 text-ink-3 hover:bg-fill hover:text-ink-2 data-[on]:bg-fill data-[on]:text-ink-2 pointer-coarse:size-10"
    >
      <NoteMark size={17} />
    </IconKey>
  );
}

/** A note's mark: a sheet with its corner folded. */
export function NoteMark({ size = 16, className, style }: { size?: number; className?: string; style?: CSSProperties }): ReactNode {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" className={className} style={style} aria-hidden>
      <path d="M15.5 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V8.5L15.5 3Z" />
      <path d="M15 3v5a1 1 0 0 0 1 1h5" />
      <path d="M7.5 12.5h7M7.5 16.5h4.5" />
    </svg>
  );
}
