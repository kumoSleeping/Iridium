// The composer's notes (便签): a popover on its key, those of the session's root folder (its
// group's, else its cwd) and the older ones of none, the newest at the top. Put in by hand, never
// as it opens (the user, 2026-11-09): its head's key takes what is in the box, its foot's line a
// note typed there. One pressed takes the box's place (what
// was there kept first); dragged, it moves; a right click (a finger held) is its menu: its colour,
// delete. A note whose words are sent goes (sentNotes). The folders and the rest are a phone's
// notes page (notes.tsx).
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { ArrowDownToLine, Check, Trash2 } from "lucide-react";
import { useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { api } from "./api/client";
import type { Color, Note, NotePatch, Notes } from "./api/types.gen";
import { t } from "./i18n";
import { Marked, MenuItems } from "./sidebar";
import { getCarried, setCarried, useCarried } from "./store";
import { errText, toast } from "./toast";
import { ago, buzz, cn, PopSurface, Tip, useMenuAt, useMinute } from "./ui";

const notesNow = () => getCarried<Notes>("notes");
const shown = (f: (n: Notes) => Notes) => {
  const n = notesNow();
  if (n) setCarried("notes", f(n));
};
const failed = (e: unknown) => {
  toast(t("err.action"), { error: true, description: errText(e) });
  void api.notes().then((n) => setCarried("notes", n), () => {});
};
const patchNote = (id: string, p: NotePatch) => {
  shown((n) => ({ ...n, notes: n.notes.map((x) => (x.id === id ? { ...x, ...(p.pos !== undefined && { pos: p.pos }), ...(p.color !== undefined && { color: p.color || undefined }) } : x)) }));
  void api.patchNote(id, p).catch(failed);
};
const byPos = (a: Note, b: Note) => a.pos - b.pos;
/** The notes root folder serves: its own, and those of none (made before notes had one). */
const servesIn = (all: Note[], root: string) => all.filter((n) => !n.root || n.root === root);

/** Whether words would be a new note of root's: not empty, not one already. */
const newIn = (words: string, root: string) => !!words.trim() && !servesIn(notesNow()?.notes ?? [], root).some((n) => n.body.trim() === words.trim());

/** The words kept as root's newest note, unless empty or a note there already. */
function keepNote(words: string, root: string, from: string) {
  const body = words.trim();
  const all = notesNow()?.notes;
  if (!body || !all) return;
  const mine = servesIn(all, root);
  if (mine.some((n) => n.body.trim() === body)) return;
  const id = crypto.randomUUID();
  const pos = Math.min(0, ...mine.map((x) => x.pos)) - 1;
  shown((n) => ({ ...n, notes: [...n.notes, { id, body, pos, root: root || undefined, from, created: Date.now(), updated: Date.now() }] }));
  void api.newNote({ id, body, pos, root: root || undefined, from }).catch(failed);
}

/** Root's notes whose words went in what was sent, gone. */
export function sentNotes(words: string, root: string) {
  const gone = servesIn(notesNow()?.notes ?? [], root).filter((n) => n.body.trim() && words.includes(n.body.trim()));
  if (!gone.length) return;
  shown((n) => ({ ...n, notes: n.notes.filter((x) => !gone.includes(x)) }));
  for (const n of gone) void api.deleteNote(n.id).catch(failed);
}

/** A note deleted, with a key to take it back. */
function remove(n: Note) {
  shown((x) => ({ ...x, notes: x.notes.filter((y) => y.id !== n.id) }));
  void api.deleteNote(n.id).catch(failed);
  toast(t("note.deleted"), {
    undo: () => {
      shown((x) => ({ ...x, notes: [...x.notes, n] }));
      void api
        .newNote({ id: n.id, body: n.body, folder: n.folder, pos: n.pos, color: n.color, from: n.from, root: n.root })
        .then(() => void (n.done && api.patchNote(n.id, { done: true })))
        .catch(failed);
    },
  });
}

/** The notes as carried; undefined: not for this terminal. */
export const useNotes = () => useCarried<Notes>("notes");

const firstLine = (s: string) => s.split("\n").find((l) => l.trim())?.trim() ?? "";
const restOf = (s: string) =>
  s
    .split("\n")
    .filter((l) => l.trim())
    .slice(1)
    .join(" · ");

/** The notes' key in the composer's foot, and its popover: root's notes, from: the session. take:
 *  what the box holds now; pick: a note's words for the box. */
export function NoteKey({ root, from, take, pick }: { root: string; from: string; take: () => string; pick: (words: string) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o, d) => {
        // A press in a note's menu is not one outside.
        const e = d.event as Event | undefined;
        if (!o && (d.reason === "outside-press" || d.reason === "focus-out") && (e?.target as Element | null)?.closest?.("[role=menu]")) return d.cancel();
        setOpen(o);
      }}
    >
      <Tip label={t("note.title")}>
        <Popover.Trigger
          render={
            <button type="button" aria-label={t("note.title")} className="g-press grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill data-popup-open:text-ink pointer-coarse:size-10">
              <NoteMark size={17} />
            </button>
          }
        />
      </Tip>
      {/* One size, full or not: the notes scroll in it (the user, 2026-11-09). */}
      <PopSurface side="top" align="start" className="flex h-[min(440px,60dvh)] w-[min(340px,calc(100vw-16px))] flex-col">
        <NoteList root={root} from={from} take={take} pick={(n) => (keepNote(take(), root, from), pick(n.body), setOpen(false))} />
      </PopSurface>
    </Popover.Root>
  );
}

/** Root's notes from a group's menu (sidebar.tsx), at the point it was opened: the same list, a note
 *  pressed copied. */
export function NotesAt({ root, at, close }: { root: string; at: { x: number; y: number } | null; close: () => void }) {
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  return (
    <Popover.Root
      open={at !== null}
      onOpenChange={(o, d) => {
        const e = d.event as Event | undefined;
        if (!o && (d.reason === "outside-press" || d.reason === "focus-out") && (e?.target as Element | null)?.closest?.("[role=menu]")) return d.cancel();
        if (!o) close();
      }}
    >
      <Popover.Portal>
        <Popover.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Popover.Popup initialFocus={(type) => type === "keyboard"} className="g-unfold glass thick float flex h-[min(440px,60dvh)] w-[min(340px,calc(100vw-16px))] flex-col rounded-lg text-ink outline-none">
            <NoteList
              root={root}
              from=""
              pick={(n) =>
                void navigator.clipboard.writeText(n.body).then(
                  () => (toast(t("toast.copied")), close()),
                  (e: unknown) => toast(t("err.action"), { error: true, description: errText(e) }),
                )
              }
            />
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

const HOLD = 400; // ms a finger is held still before the note is lifted
const STILL = 8; // px it may move meanwhile

// A finger dragging a note: the list does not scroll. On window from the first: iOS heeds only a
// listener there before the touch began.
let dragging = false;
if (typeof window !== "undefined") addEventListener("touchmove", (e) => dragging && e.cancelable && e.preventDefault(), { passive: false, capture: true });

/** Root's notes: a head (its key: the composer's words, take, kept as a note), the list, a line to
 *  type a new one in. A note pressed is pick's; one the box holds and not kept is kept first. */
function NoteList({ root, from, take, pick }: { root: string; from: string; take?: () => string; pick: (n: Note) => void }) {
  const notes = servesIn(useNotes()?.notes ?? [], root).sort(byPos);
  const minute = useMinute();
  const list = useRef<HTMLDivElement>(null);
  // The note lifted, how far it has gone, and the place it would land (an index into notes).
  const [drag, setDrag] = useState<{ id: string; dy: number; to: number } | null>(null);
  const [menu, setMenu] = useState<{ n: Note; x: number; y: number } | null>(null);
  const [menuAt, setMenuAt] = useMenuAt();
  const moved = useRef(false);

  /** Where a note let go at y lands: before the first row whose middle is under it. */
  const slotAt = (y: number) => {
    const rows = [...(list.current?.querySelectorAll<HTMLElement>("[data-note-row]") ?? [])];
    const i = rows.findIndex((r) => {
      const b = r.getBoundingClientRect();
      return y < b.top + b.height / 2;
    });
    return i < 0 ? rows.length : i;
  };
  const land = (n: Note, to: number) => {
    const from = notes.indexOf(n);
    if (to === from || to === from + 1) return;
    const a = notes[to - 1];
    const b = notes[to];
    void patchNote(n.id, { pos: a && b ? (a.pos + b.pos) / 2 : a ? a.pos + 1 : b ? b.pos - 1 : 0 });
  };
  const openMenu = (n: Note, x: number, y: number) => (setMenu({ n, x, y }), setMenuAt({ x, y }));

  const down = (n: Note) => (e: React.PointerEvent) => {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    const touch = e.pointerType !== "mouse";
    const from = { x: e.clientX, y: e.clientY };
    let lifted = false;
    let travelled = false;
    moved.current = false;
    const lift = () => {
      lifted = dragging = true;
      if (touch) buzz();
      setDrag({ id: n.id, dy: 0, to: notes.indexOf(n) });
    };
    const hold = touch ? window.setTimeout(lift, HOLD) : 0;
    const move = (ev: PointerEvent) => {
      const far = Math.hypot(ev.clientX - from.x, ev.clientY - from.y) > (touch ? STILL : 4);
      if (!lifted) {
        if (!far) return;
        if (touch) return stop(); // a finger moving first scrolls
        lift();
      }
      if (far) travelled = true;
      setDrag({ id: n.id, dy: ev.clientY - from.y, to: slotAt(ev.clientY) });
    };
    const up = (ev: PointerEvent) => {
      stop();
      if (!lifted) return;
      moved.current = true; // its click is not a press on the row
      // Held still and let go: the menu, after the click this let-go makes.
      if (touch && !travelled) return void setTimeout(() => openMenu(n, ev.clientX, ev.clientY), 60);
      if (ev.type === "pointerup") land(n, slotAt(ev.clientY));
    };
    const stop = () => {
      clearTimeout(hold);
      dragging = false;
      setDrag(null);
      removeEventListener("pointermove", move);
      removeEventListener("pointerup", up);
      removeEventListener("pointercancel", up);
    };
    addEventListener("pointermove", move);
    addEventListener("pointerup", up);
    addEventListener("pointercancel", up);
  };

  const lifted = drag && notes.findIndex((x) => x.id === drag.id);
  return (
    <>
      <div className="flex h-10 shrink-0 items-center gap-2 pr-1.5 pl-3.5">
        <span className="text-sm font-semibold">{t("note.title")}</span>
        {notes.length > 0 && <span className="text-xs text-ink-3 tabular-nums">{notes.length}</span>}
        <span className="flex-1" />
        {take && newIn(take(), root) && (
          <button type="button" onClick={() => keepNote(take(), root, from)} className="g-press flex h-7 items-center gap-1 rounded-md px-2 text-xs text-ink-2 hover:bg-fill hover:text-ink pointer-coarse:h-9">
            <ArrowDownToLine size={13} />
            {t("note.takeBox")}
          </button>
        )}
      </div>
      {notes.length ? (
        <div ref={list} role="listbox" aria-label={t("note.title")} className="relative min-h-0 flex-1 overflow-y-auto px-1.5 pb-1.5 no-bar">
          {notes.map((n, i) => {
            const rest = restOf(n.body);
            const isLifted = drag?.id === n.id;
            // The line where it would land, not at its own place.
            const line = drag && lifted !== null && drag.to === i && i !== lifted && i !== lifted + 1;
            return (
              <div
                key={n.id}
                data-note-row
                role="option"
                aria-selected={false}
                tabIndex={0}
                onPointerDown={down(n)}
                onClick={() => (moved.current ? (moved.current = false) : pick(n))}
                onKeyDown={(e) => e.key === "Enter" && (e.preventDefault(), pick(n))}
                onContextMenu={(e) => {
                  e.preventDefault();
                  // A finger's long press is the drag's: its menu comes as it is let go.
                  if ((e.nativeEvent as PointerEvent).pointerType === "touch") return;
                  openMenu(n, e.clientX, e.clientY);
                }}
                style={isLifted ? ({ transform: `translateY(${drag.dy}px)` } as CSSProperties) : undefined}
                className={cn(
                  "relative cursor-default rounded-md px-2.5 py-1.5 outline-none select-none [-webkit-touch-callout:none] hover:bg-fill focus-visible:bg-fill pointer-coarse:py-2",
                  isLifted && "z-10 bg-paper shadow-[0_6px_20px_-6px_rgb(0_0_0/0.3),0_0_0_0.5px_var(--edge)]",
                  menu?.n.id === n.id && menuAt && "bg-fill",
                  line && "shadow-[inset_0_2px_0_var(--ink-3)]",
                )}
              >
                <span className="flex min-w-0 items-baseline gap-2 leading-5">
                  <span className="flex min-w-0 flex-1 text-base">
                    <Marked color={n.color}>{firstLine(n.body)}</Marked>
                  </span>
                  <span className="shrink-0 text-xs text-ink-4 tabular-nums">{ago(n.updated, Math.max(minute, Date.now()), true)}</span>
                </span>
                {rest && <span className="block truncate text-sm leading-5 text-ink-3">{rest}</span>}
              </div>
            );
          })}
          {drag && lifted !== null && drag.to === notes.length && lifted !== notes.length - 1 && <div className="mx-2.5 h-0.5 rounded-full bg-ink-3" />}
        </div>
      ) : (
        <div className="grid flex-1 place-items-center text-sm text-ink-3">{t("note.none")}</div>
      )}
      <NewLine add={(w) => keepNote(w, root, from)} />
      {menu && menuAt && (
        <Menu.Root open onOpenChange={(o) => !o && setMenuAt(null)}>
          <Menu.Portal>
            <Menu.Positioner anchor={{ getBoundingClientRect: () => DOMRect.fromRect({ x: menuAt.x, y: menuAt.y, width: 0, height: 0 }) }} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
              <MenuItems items={[{ icon: <Trash2 size={15} />, label: t("note.delete"), run: () => remove(menu.n) }]} tags={{ value: menu.n.color, set: (c) => patchNote(menu.n.id, { color: c as Color }) }} />
            </Menu.Positioner>
          </Menu.Portal>
        </Menu.Root>
      )}
    </>
  );
}

/** The popover's foot: a note typed, kept on Enter (Shift+Enter a new line). */
function NewLine({ add }: { add: (words: string) => void }) {
  const [text, setText] = useState("");
  const box = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    const el = box.current;
    if (el) (el.style.height = "auto"), (el.style.height = `${el.scrollHeight}px`);
  }, [text]);
  const save = () => {
    if (!text.trim()) return;
    add(text);
    setText("");
  };
  return (
    <div className="flex shrink-0 items-end gap-1 border-t border-edge px-2 py-1.5">
      <textarea
        ref={box}
        rows={1}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && (e.preventDefault(), save())}
        placeholder={t("note.placeholder")}
        aria-label={t("note.new")}
        className="no-ring max-h-28 min-w-0 flex-1 resize-none bg-transparent px-1.5 py-1 text-base leading-5 text-ink outline-none placeholder:text-ink-4"
      />
      {text.trim() && (
        <button type="button" aria-label={t("note.keep")} onClick={save} className="g-press grid size-7 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink">
          <Check size={15} />
        </button>
      )}
    </div>
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
