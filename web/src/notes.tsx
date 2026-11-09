// Notes (便签, docs/plan.md "便签"): words jotted down to keep, an idea, a thing to do, on a phone or a
// computer alike. A box at the top takes a new one; below, the notes in no folder, then each folder,
// then the done ones, folded. A note is a row (its first line, the next under it); pressed, it opens
// in place to be written in. Dragged (a mouse at once, a finger once held): between two notes it
// goes there, onto one it is joined to it, onto a folder into it, onto a session's row or the chat
// into that chat's composer, to be read over and sent; held on a key that turns the page (the
// sessions' tab, the notes key), the page turns. A finger held still and let go is the note's menu.
// A phone's: the sheet's last page (app.tsx); a computer has the composer's (notekey.tsx).
import { Menu } from "@base-ui/react/menu";
import { Check, ChevronRight, Folder, FolderOpen, FolderPlus, Inbox, ListTree, Pencil, Scissors, SendHorizontal, Trash2, Undo2 } from "lucide-react";
import { createPortal } from "react-dom";
import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { api } from "./api/client";
import type { Color, Note, NoteFolder, Notes } from "./api/types.gen";
import { t, useLang } from "./i18n";
import { openChat, route } from "./route";
import { MenuItems, Marked, type Tags } from "./sidebar";
import { getCarried, setCarried, useCarried, useList } from "./store";
import { errText, toast } from "./toast";
import { ago, buzz, cn, IconKey, kept, keep, lampOf, LogoLamp, own, useMenuAt, useMinute } from "./ui";

// --- the notes, as carried, changed here first ---

const EMPTY: Notes = { notes: [], folders: [] };
const now = () => getCarried<Notes>("notes") ?? EMPTY;
/** Sets the notes as they will be, before the daemon says so; its own state follows. */
const shown = (f: (n: Notes) => Notes) => setCarried("notes", f(now()));
/** A write that failed: said, and the notes as the daemon has them. */
const failed = (e: unknown) => {
  toast(t("err.action"), { error: true, description: errText(e) });
  void api.notes().then((n) => setCarried("notes", n), () => {});
};
const patchNote = (id: string, p: Parameters<typeof api.patchNote>[1]) => {
  shown((n) => ({ ...n, notes: n.notes.map((x) => (x.id === id ? { ...x, ...applied(x, p) } : x)) }));
  return api.patchNote(id, p).catch(failed);
};
const applied = (x: Note, p: Parameters<typeof api.patchNote>[1]): Partial<Note> => ({
  ...(p.body !== undefined && { body: p.body }),
  ...(p.folder !== undefined && { folder: p.folder }),
  ...(p.pos !== undefined && { pos: p.pos }),
  ...(p.color !== undefined && { color: p.color || undefined }),
  ...(p.done !== undefined && { done: p.done ? x.done || Date.now() : undefined }),
  ...(p.session && { sessions: [...(x.sessions ?? []).filter((s) => s !== p.session), p.session] }),
  updated: Date.now(),
});
const addNote = (body: string, folder = 0, pos?: number, from?: string) => {
  const id = crypto.randomUUID();
  const at = pos ?? Math.min(0, ...now().notes.filter((x) => (x.folder ?? 0) === folder).map((x) => x.pos)) - 1;
  shown((n) => ({ ...n, notes: [...n.notes, { id, body, folder: folder || undefined, pos: at, created: Date.now(), updated: Date.now() }] }));
  return api.newNote({ id, body, folder: folder || undefined, pos: at, from }).catch(failed);
};
const deleteNote = (id: string) => {
  shown((n) => ({ ...n, notes: n.notes.filter((x) => x.id !== id) }));
  return api.deleteNote(id).catch(failed);
};
const byPos = (a: { pos: number }, b: { pos: number }) => a.pos - b.pos;
/** The notes of folder (0: none) not done, by place. */
const notesIn = (all: Note[], folder: number) => all.filter((x) => (x.folder ?? 0) === folder && !x.done).sort(byPos);
/** Between a and b, either maybe missing: a place for a note put there. */
const between = (a?: Note, b?: Note) => (a && b ? (a.pos + b.pos) / 2 : a ? a.pos + 1 : b ? b.pos - 1 : 0);

/** A note's words, in the composer of session id: that chat's open, else its kept draft (the
 *  composer's draft:<id>, chat/composer.tsx draftKey) with the chat opened. */
export function putIntoSession(n: Note, id: string) {
  const open = route().name === "chat" && (route() as { id: string }).id === id;
  const e = new CustomEvent("ir:note", { detail: n.body, cancelable: true });
  if (!open || dispatchEvent(e)) {
    const was = kept(`draft:${id}`);
    keep(`draft:${id}`, [was, n.body].filter(Boolean).join("\n"));
  }
  void patchNote(n.id, { session: id });
  if (!open) openChat(id);
}

/** One note joined onto another, the one dropped gone, with a key to take it back. */
function join(from: Note, onto: Note) {
  const was = onto.body;
  void patchNote(onto.id, { body: `${onto.body.trimEnd()}\n${from.body}` });
  void deleteNote(from.id);
  toast(t("note.joined"), {
    undo: () => {
      void patchNote(onto.id, { body: was });
      shown((n) => ({ ...n, notes: [...n.notes, from] }));
      void api.newNote({ id: from.id, body: from.body, folder: from.folder, pos: from.pos, color: from.color, from: from.from }).catch(failed);
    },
  });
}

/** A note split: each line of words after the first a note of its own, under it. */
function split(n: Note) {
  const lines = n.body.split("\n").filter((l) => l.trim());
  if (lines.length < 2) return;
  const list = notesIn(now().notes, n.folder ?? 0);
  const next = list[list.indexOf(n) + 1];
  void patchNote(n.id, { body: lines[0]! });
  const step = ((next ? next.pos : n.pos + 1) - n.pos) / lines.length;
  lines.slice(1).forEach((l, i) => void addNote(l, n.folder ?? 0, n.pos + step * (i + 1)));
}

/** Words taken out of note n into a note of their own, under it. */
function splitOut(n: Note, a: number, b: number) {
  const words = n.body.slice(a, b).trim();
  if (!words) return;
  const list = notesIn(now().notes, n.folder ?? 0);
  void patchNote(n.id, { body: (n.body.slice(0, a) + n.body.slice(b)).replace(/\n{3,}/g, "\n\n").trim() });
  void addNote(words, n.folder ?? 0, between(n, list[list.indexOf(n) + 1]));
}

// --- dragging a note ---

/** Where a note dragged would land, as it is over it. */
type Over =
  | { kind: "note"; id: string; at: "before" | "after" | "into" }
  | { kind: "folder"; id: number }
  | { kind: "session"; id: string }
  | { kind: "chat"; id: string }
  | null;
type Drag = { note: Note; x: number; y: number; over: Over } | null;
let drag: Drag = null;
const dragSubs = new Set<() => void>();
const setDrag = (d: Drag) => ((drag = d), dragSubs.forEach((f) => f()));
const useDrag = () =>
  useSyncExternalStore(
    (f) => (dragSubs.add(f), () => void dragSubs.delete(f)),
    () => drag,
  );
// A finger dragging a note: nothing scrolls, no page turns (pager.tsx sees it prevented). On window
// from the first, not as the drag begins: iOS heeds only a listener there before the touch began.
if (typeof window !== "undefined") addEventListener("touchmove", (e) => drag && e.cancelable && e.preventDefault(), { passive: false, capture: true });

/** The element marked as where the note would land (a session's row, the chat: not ours to draw). */
let marked: Element | null = null;
const mark = (el: Element | null, how?: string) => {
  if (marked && marked !== el) marked.removeAttribute("data-drop");
  marked = el;
  if (el) el.setAttribute("data-drop", how ?? "");
};

/** What is under x, y for a note: a note (its top third before it, its foot after, its middle
 *  onto it), a folder, a session's row, the chat. Also the key under it that turns a page. */
function overAt(x: number, y: number, self: string): { over: Over; el: Element | null; spring: HTMLElement | null } {
  const el = document.elementFromPoint(x, y);
  const spring = el?.closest<HTMLElement>("[data-spring]") ?? null;
  const n = el?.closest<HTMLElement>("[data-note]");
  if (n) {
    const id = n.dataset.note!;
    if (id === self) return { over: null, el: null, spring };
    const r = n.getBoundingClientRect();
    const f = (y - r.top) / r.height;
    const at = f < 0.3 ? "before" : f > 0.7 ? "after" : "into";
    return { over: { kind: "note", id, at }, el: n, spring };
  }
  const folder = el?.closest<HTMLElement>("[data-note-folder]");
  if (folder) return { over: { kind: "folder", id: Number(folder.dataset.noteFolder) }, el: folder, spring };
  const s = el?.closest<HTMLElement>("[data-session]");
  if (s) return { over: { kind: "session", id: s.dataset.session! }, el: s, spring };
  const chat = el?.closest<HTMLElement>("[data-chat-drop]");
  if (chat) return { over: { kind: "chat", id: chat.dataset.chatDrop! }, el: chat, spring };
  return { over: null, el: null, spring };
}

/** A note let go where over says. */
function land(n: Note, over: Over) {
  if (!over) return;
  const all = now().notes;
  if (over.kind === "note") {
    const to = all.find((x) => x.id === over.id);
    if (!to) return;
    if (over.at === "into") return join(n, to);
    const list = notesIn(all, to.folder ?? 0).filter((x) => x.id !== n.id);
    const i = list.indexOf(to);
    const pos = over.at === "before" ? between(list[i - 1], to) : between(to, list[i + 1]);
    void patchNote(n.id, { pos, folder: to.folder ?? 0, ...(to.done ? {} : n.done ? { done: false } : {}) });
  } else if (over.kind === "folder") {
    if ((n.folder ?? 0) === over.id && !n.done) return;
    const top = notesIn(all, over.id)[0];
    void patchNote(n.id, { folder: over.id, pos: between(undefined, top), ...(n.done ? { done: false } : {}) });
  } else putIntoSession(n, over.id);
}

const HOLD = 400; // ms a finger is held still before the note is lifted
const STILL = 8; // px it may move meanwhile
const SPRING = 550; // ms held over a key that turns a page before it turns

/** The pointer handlers that drag note n: a mouse once it moved, a finger once held still (let go
 *  unmoved, that is the menu: onHold). */
function useNoteDrag(n: Note, onHold: (x: number, y: number) => void, off: boolean) {
  const cur = useRef(n);
  cur.current = n;
  const moved = useRef(false);
  return {
    onPointerDown: (e: React.PointerEvent) => {
      if (off || (e.pointerType === "mouse" && e.button !== 0) || !own(e) || (e.target as Element).closest("button, textarea, input")) return;
      const touch = e.pointerType !== "mouse";
      const from = { x: e.clientX, y: e.clientY };
      let lifted = false;
      let travelled = false;
      let spring: { el: HTMLElement; timer: number } | null = null;
      let scroll = 0;
      let last = from;
      moved.current = false;
      const lift = () => {
        lifted = true;
        if (touch) buzz();
        getSelection()?.removeAllRanges();
        setDrag({ note: cur.current, x: last.x, y: last.y, over: null });
      };
      const hold = touch ? window.setTimeout(lift, HOLD) : 0;
      // Near the list's top or foot, it scrolls on its own while held there.
      const edge = () => {
        const box = document.querySelector<HTMLElement>("[data-notes-scroll]");
        if (box && drag) {
          const r = box.getBoundingClientRect();
          const by = last.y < r.top + 40 ? -8 : last.y > r.bottom - 40 && last.y < r.bottom + 4 ? 8 : 0;
          if (by) (box.scrollTop += by), at(last.x, last.y);
        }
        scroll = requestAnimationFrame(edge);
      };
      const at = (x: number, y: number) => {
        const o = overAt(x, y, cur.current.id);
        mark(o.el, o.over?.kind === "note" ? o.over.at : "on");
        if (o.spring !== spring?.el) {
          if (spring) clearTimeout(spring.timer);
          spring = o.spring ? { el: o.spring, timer: window.setTimeout(() => (o.spring!.click(), buzz()), SPRING) } : null;
        }
        setDrag({ note: cur.current, x, y, over: o.over });
      };
      const move = (ev: PointerEvent) => {
        last = { x: ev.clientX, y: ev.clientY };
        const far = Math.hypot(ev.clientX - from.x, ev.clientY - from.y) > (touch ? STILL : 5);
        if (!lifted) {
          if (!far) return;
          if (touch) return stop(); // a finger moving first is scrolling
          lift();
          scroll = requestAnimationFrame(edge);
        }
        if (far) travelled = true;
        if (touch && !scroll) scroll = requestAnimationFrame(edge);
        at(ev.clientX, ev.clientY);
      };
      const up = (ev: PointerEvent) => {
        const d = drag;
        stop();
        if (!lifted) return;
        moved.current = true; // its click is not a press on the row
        // After the click this let-go makes: that one would shut the menu at once as a press outside.
        if (touch && !travelled) return void setTimeout(() => onHold(ev.clientX, ev.clientY), 60);
        if (d && ev.type === "pointerup") land(d.note, d.over);
      };
      const stop = () => {
        clearTimeout(hold);
        if (spring) clearTimeout(spring.timer);
        cancelAnimationFrame(scroll);
        mark(null);
        setDrag(null);
        removeEventListener("pointermove", move);
        removeEventListener("pointerup", up);
        removeEventListener("pointercancel", up);
      };
      addEventListener("pointermove", move);
      addEventListener("pointerup", up);
      addEventListener("pointercancel", up);
    },
    onClickCapture: (e: React.MouseEvent) => {
      if (moved.current) (e.stopPropagation(), e.preventDefault(), (moved.current = false));
    },
  };
}

/** The note dragged, under the pointer. */
function Ghost() {
  const d = useDrag();
  if (!d) return null;
  // On the body: in the sheet, a page's transform would hold a fixed box to it.
  return createPortal(
    <div className="glass thick float pointer-events-none fixed z-[70] max-w-64 truncate rounded-md px-3 py-1.5 text-sm text-ink" style={{ left: d.x + 12, top: d.y - 14 }}>
      {firstLine(d.note.body) || t("note.empty")}
      {d.over?.kind === "note" && d.over.at === "into" && <span className="ml-2 text-ink-3">{t("note.join")}</span>}
      {(d.over?.kind === "session" || d.over?.kind === "chat") && <span className="ml-2 text-ink-3">{t("note.put")}</span>}
    </div>,
    document.body,
  );
}

const firstLine = (s: string) => s.split("\n").find((l) => l.trim())?.trim() ?? "";
const restOf = (s: string) =>
  s
    .split("\n")
    .filter((l) => l.trim())
    .slice(1)
    .join(" · ");

// --- the panel ---

/** The notes: the box for a new one, then the list. */
export function NotesPanel() {
  useLang();
  const notes = useNotes();
  const [folding, setFolding] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(kept("notes.folded") ?? "{}");
    } catch {
      return {};
    }
  });
  // Folders open, but the done ones.
  const folded = (k: string) => folding[k] ?? k === "done";
  const toggle = (k: string) => setFolding((f) => ((f = { ...f, [k]: !folded(k) }), keep("notes.folded", JSON.stringify(f)), f));
  const [editing, setEditing] = useState<string | null>(null);
  const [making, setMaking] = useState(false);
  const all = notes.notes;
  const loose = notesIn(all, 0);
  const done = all.filter((x) => x.done).sort((a, b) => (b.done ?? 0) - (a.done ?? 0));
  const folders = [...notes.folders].sort(byPos);
  const row = (n: Note) => <NoteRow key={n.id} n={n} folders={folders} editing={editing === n.id} edit={(on) => setEditing(on ? n.id : null)} />;
  return (
    <div className="flex h-full min-h-0 flex-col">
      <Capture onFolder={() => setMaking(true)} />
      <div data-notes-scroll className="relative min-h-0 flex-1 no-bar overflow-y-auto px-2 pb-3" role="list" aria-label={t("note.title")}>
        {/* Let go here, a note leaves its folder. */}
        <div data-note-folder="0" className="min-h-2 rounded-md data-[drop]:bg-fill">
          {loose.map(row)}
        </div>
        {folders.map((f) => (
          <div key={f.id} className="mt-3" role="group">
            <FolderRow f={f} open={!folded(`f${f.id}`)} toggle={() => toggle(`f${f.id}`)} count={notesIn(all, f.id).length} />
            {!folded(`f${f.id}`) && <div className="pl-3">{notesIn(all, f.id).map(row)}</div>}
          </div>
        ))}
        {making && <NewFolder done={() => setMaking(false)} />}
        {done.length > 0 && (
          <div className="mt-3" role="group">
            <button type="button" onClick={() => toggle("done")} className="flex h-[30px] w-full cursor-default items-center gap-2 rounded-md px-2 text-left text-base text-ink-2 hover:bg-fill pointer-coarse:h-10">
              <ChevronRight size={14} className={cn("text-ink-3 transition-transform duration-(--dur-2)", !folded("done") && "rotate-90")} />
              <span className="flex-1">{t("note.done")}</span>
              <span className="text-xs text-ink-4 tabular-nums">{done.length}</span>
            </button>
            {!folded("done") && <div className="opacity-70">{done.map(row)}</div>}
          </div>
        )}
        {!all.length && !folders.length && <div className="g-fade px-2 pt-10 text-center text-sm text-ink-3">{t("note.none")}</div>}
      </div>
      <Ghost />
    </div>
  );
}

/** The notes as carried; undefined: not for this terminal (a panel's only), or not come yet. */
export const useNotesState = () => useCarried<Notes>("notes");
const useNotes = () => useNotesState() ?? EMPTY;

/** The box at the top: Enter keeps what was typed as a new note (Shift+Enter a new line). */
function Capture({ onFolder }: { onFolder: () => void }) {
  const [text, setText] = useState(() => kept("notes.typing") ?? "");
  const box = useRef<HTMLTextAreaElement>(null);
  useAutoHeight(box, text);
  const type = (v: string) => (setText(v), keep("notes.typing", v || null));
  const save = () => {
    const v = text.trim();
    if (!v) return;
    void addNote(v);
    type("");
  };
  return (
    <div className="flex shrink-0 items-end gap-1 px-2 pt-1 pb-2">
      <div className="flex min-w-0 flex-1 items-end rounded-lg bg-fill px-2.5 py-1.5">
        <textarea
          ref={box}
          rows={1}
          value={text}
          onChange={(e) => type(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) (e.preventDefault(), save());
          }}
          placeholder={t("note.placeholder")}
          aria-label={t("note.new")}
          className="max-h-40 min-w-0 flex-1 resize-none bg-transparent text-base leading-6 text-ink outline-none placeholder:text-ink-4 pointer-coarse:text-md"
        />
        {text.trim() && (
          <IconKey label={t("note.keep")} onClick={save} className="-mr-1 size-6 shrink-0 text-ink-2 pointer-coarse:size-8">
            <Check size={15} />
          </IconKey>
        )}
      </div>
      <IconKey label={t("note.newFolder")} onClick={onFolder} className="size-8 shrink-0 text-ink-3 hover:text-ink-2 pointer-coarse:size-10">
        <FolderPlus size={16} />
      </IconKey>
    </div>
  );
}

/** A box of words as tall as what is in it. */
function useAutoHeight(box: React.RefObject<HTMLTextAreaElement | null>, text: string) {
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [text]);
}

/** One note: done or not at its left, its first line and the next, when, and the session it went to
 *  last (its lamp; pressed, that chat). Pressed, it opens to be written in. */
function NoteRow({ n, folders, editing, edit }: { n: Note; folders: NoteFolder[]; editing: boolean; edit: (on: boolean) => void }) {
  const minute = useMinute();
  const sessions = useList();
  const went = [...(n.sessions ?? [])].reverse().map((id) => sessions?.find((s) => s.id === id)).find(Boolean);
  const [menuAt, setMenuAt] = useMenuAt();
  const d = useDrag();
  const lifted = d?.note.id === n.id;
  const handlers = useNoteDrag(n, (x, y) => setMenuAt({ x, y }), editing);
  const rest = restOf(n.body);
  return (
    <div
      data-note={n.id}
      role="listitem"
      tabIndex={0}
      onClick={() => !editing && edit(true)}
      onKeyDown={(e) => e.target === e.currentTarget && e.key === "Enter" && (e.preventDefault(), edit(true))}
      onContextMenu={(e) => {
        if (!own(e) || editing) return;
        e.preventDefault();
        // A finger's long press is the drag's (useNoteDrag): no menu here, nor the browser's.
        if ((e.nativeEvent as PointerEvent).pointerType === "touch" || drag) return;
        setMenuAt({ x: e.clientX, y: e.clientY });
      }}
      onPointerDown={handlers.onPointerDown}
      onClickCapture={handlers.onClickCapture}
      className={cn(
        "group relative flex cursor-default items-start gap-2 rounded-md py-[5px] pr-2 pl-1.5 select-none [-webkit-touch-callout:none] hover:bg-fill pointer-coarse:py-2",
        editing && "bg-fill",
        lifted && "opacity-40",
        // Where a note dragged would land: a line above or under, or the whole row to join it.
        "data-[drop=before]:shadow-[inset_0_2px_0_var(--ink-3)] data-[drop=after]:shadow-[inset_0_-2px_0_var(--ink-3)] data-[drop=into]:bg-fill-2",
      )}
    >
      <button
        type="button"
        onClick={(e) => (e.stopPropagation(), void patchNote(n.id, { done: !n.done }))}
        aria-label={n.done ? t("note.undone") : t("note.markDone")}
        className="g-press mt-[3px] grid size-[15px] shrink-0 place-items-center rounded-full border border-ink-4 text-paper hover:border-ink-2 data-[on]:border-ink-3 data-[on]:bg-ink-3 pointer-coarse:mt-[4px] pointer-coarse:size-[17px]"
        data-on={n.done ? "" : undefined}
      >
        {n.done && <Check size={10} strokeWidth={3} />}
      </button>
      <span className="min-w-0 flex-1">
        {editing ? (
          <Editor n={n} close={() => edit(false)} />
        ) : (
          <>
            <span className="flex min-w-0 items-baseline leading-5 pointer-coarse:leading-[23px]">
              <span className={cn("flex min-w-0 flex-1 text-base pointer-coarse:text-md", !!n.done && "text-ink-3 line-through decoration-ink-4")}>
                <Marked color={n.color}>{firstLine(n.body) || <span className="text-ink-3">{t("note.empty")}</span>}</Marked>
              </span>
              <span className="ml-2 flex shrink-0 items-center gap-1.5 self-center">
                {went ? (
                  <button type="button" onClick={(e) => (e.stopPropagation(), openChat(went.id))} aria-label={t("note.openSession")} className="g-press grid size-5 place-items-center rounded-sm hover:bg-fill-2">
                    <LogoLamp agent={went.agent} status={lampOf(went)} size={14} />
                  </button>
                ) : (
                  <span className="text-xs whitespace-nowrap text-ink-4 tabular-nums">{ago(n.updated, Math.max(minute, Date.now()), true)}</span>
                )}
              </span>
            </span>
            {rest && <span className="block truncate text-sm text-ink-3">{rest}</span>}
          </>
        )}
      </span>
      <NoteMenu n={n} folders={folders} at={menuAt} close={() => setMenuAt(null)} edit={() => edit(true)} />
    </div>
  );
}

/** A note opened to be written in: kept as it is typed, a moment after; Esc or a press outside
 *  shuts it. Words chosen in it may be taken out into a note of their own. Left empty, it goes. */
function Editor({ n, close }: { n: Note; close: () => void }) {
  const [text, setText] = useState(n.body);
  const [chosen, setChosen] = useState<[number, number] | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const timer = useRef(0);
  const sent = useRef(n.body);
  useAutoHeight(box, text);
  useEffect(() => {
    const el = box.current;
    el?.focus();
    el?.setSelectionRange(el.value.length, el.value.length);
  }, []);
  const flush = (v = box.current?.value ?? text) => {
    clearTimeout(timer.current);
    if (v === sent.current) return;
    sent.current = v;
    void patchNote(n.id, { body: v });
  };
  const type = (v: string) => {
    setText(v);
    clearTimeout(timer.current);
    timer.current = window.setTimeout(() => flush(v), 600);
  };
  const shut = () => {
    const v = box.current?.value ?? text;
    if (!v.trim()) {
      clearTimeout(timer.current);
      void deleteNote(n.id);
    } else flush(v);
    close();
  };
  const now = useRef(shut);
  now.current = shut;
  // A press outside the row shuts it.
  useEffect(() => {
    const down = (e: PointerEvent) => !box.current?.closest("[data-note]")?.contains(e.target as Node) && !(e.target as Element).closest?.("[role=menu]") && now.current();
    addEventListener("pointerdown", down, true);
    return () => (removeEventListener("pointerdown", down, true), clearTimeout(timer.current));
  }, []);
  const choose = () => {
    const el = box.current;
    setChosen(el && el.selectionEnd > el.selectionStart && el.value.slice(el.selectionStart, el.selectionEnd).trim() ? [el.selectionStart, el.selectionEnd] : null);
  };
  return (
    <span className="flex flex-col">
      <textarea
        ref={box}
        value={text}
        rows={1}
        onChange={(e) => type(e.target.value)}
        onSelect={choose}
        onKeyDown={(e) => e.key === "Escape" && (e.preventDefault(), e.stopPropagation(), shut())}
        aria-label={t("note.edit")}
        className="max-h-[60dvh] w-full resize-none bg-transparent text-base leading-5 text-ink outline-none select-text pointer-coarse:text-md pointer-coarse:leading-[23px]"
      />
      {chosen && (
        <span className="mt-1 flex justify-end">
          <button
            type="button"
            // Not a blur first: the words chosen stay chosen till it is pressed.
            onPointerDown={(e) => e.preventDefault()}
            onClick={() => {
              const v = box.current!.value;
              flush(v);
              splitOut({ ...n, body: v }, chosen[0], chosen[1]);
              setText(sent.current = (v.slice(0, chosen[0]) + v.slice(chosen[1])).replace(/\n{3,}/g, "\n\n").trim());
              setChosen(null);
            }}
            className="g-press flex h-7 items-center gap-1.5 rounded-full bg-paper px-2.5 text-xs text-ink-2 shadow-[0_0_0_0.5px_var(--edge)] hover:text-ink"
          >
            <Scissors size={13} />
            {t("note.splitOut")}
          </button>
        </span>
      )}
    </span>
  );
}

/** A note's menu: into the chat open, done, split, to a folder, delete, its colour. */
function NoteMenu({ n, folders, at, close, edit }: { n: Note; folders: NoteFolder[]; at: { x: number; y: number } | null; close: () => void; edit: () => void }) {
  if (!at) return null;
  const anchor = { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  const r = route();
  const open = r.name === "chat" ? r.id : null;
  const lines = n.body.split("\n").filter((l) => l.trim()).length;
  const items = [
    ...(open ? [{ icon: <SendHorizontal size={15} />, label: t("note.putHere"), run: () => putIntoSession(n, open) }] : []),
    { icon: <Pencil size={15} />, label: t("note.edit"), run: edit },
    { icon: n.done ? <Undo2 size={15} /> : <Check size={15} />, label: n.done ? t("note.undone") : t("note.markDone"), run: () => void patchNote(n.id, { done: !n.done }) },
    ...(lines > 1 ? [{ icon: <ListTree size={15} />, label: t("note.split"), run: () => split(n) }] : []),
    ...folders.filter((f) => f.id !== (n.folder ?? 0)).map((f) => ({ icon: <Folder size={15} />, label: t("note.moveTo", { name: f.name }), run: () => void patchNote(n.id, { folder: f.id, pos: between(undefined, notesIn(now().notes, f.id)[0]) }) })),
    ...(n.folder ? [{ icon: <Inbox size={15} />, label: t("note.moveOut"), run: () => void patchNote(n.id, { folder: 0, pos: between(undefined, notesIn(now().notes, 0)[0]) }) }] : []),
    { icon: <Trash2 size={15} />, label: t("note.delete"), run: () => remove(n) },
  ];
  const tags: Tags = { value: n.color, set: (c) => void patchNote(n.id, { color: c as Color }) }; // "" takes it off
  return (
    <Menu.Root open onOpenChange={(o) => !o && close()}>
      <Menu.Portal>
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <MenuItems items={items} tags={tags} />
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** A note deleted, with a key to take it back. */
function remove(n: Note) {
  void deleteNote(n.id);
  toast(t("note.deleted"), {
    undo: () => {
      shown((x) => ({ ...x, notes: [...x.notes, n] }));
      void api
        .newNote({ id: n.id, body: n.body, folder: n.folder, pos: n.pos, color: n.color, from: n.from })
        .then(() => void (n.done && api.patchNote(n.id, { done: true })))
        .catch(failed);
    },
  });
}

/** A folder's row: open or folded on a press; its menu renames or deletes it. Notes dropped on it go in. */
function FolderRow({ f, open, toggle, count }: { f: NoteFolder; open: boolean; toggle: () => void; count: number }) {
  const [menuAt, setMenuAt] = useMenuAt();
  const [renaming, setRenaming] = useState(false);
  const timer = useRef(0);
  const anchor = menuAt && { getBoundingClientRect: () => DOMRect.fromRect({ x: menuAt.x, y: menuAt.y, width: 0, height: 0 }) };
  return (
    <>
      <div
        data-note-folder={f.id}
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={() => !renaming && toggle()}
        onContextMenu={(e) => own(e) && (e.preventDefault(), setMenuAt({ x: e.clientX, y: e.clientY }))}
        onPointerDown={(e) => {
          if (e.pointerType === "mouse") return;
          const at = { x: e.clientX, y: e.clientY };
          timer.current = window.setTimeout(() => (buzz(), setMenuAt(at)), 450);
        }}
        onPointerUp={() => clearTimeout(timer.current)}
        onPointerCancel={() => clearTimeout(timer.current)}
        onPointerMove={() => clearTimeout(timer.current)}
        className="flex h-[30px] cursor-default items-center gap-2 rounded-md px-2 text-base text-ink select-none [-webkit-touch-callout:none] hover:bg-fill data-[drop]:bg-fill-2 pointer-coarse:h-10"
      >
        {open ? <FolderOpen size={15} className="shrink-0 text-ink-2" /> : <Folder size={15} className="shrink-0 text-ink-2" />}
        {renaming ? <FolderName f={f} done={() => setRenaming(false)} /> : <span className="min-w-0 flex-1 truncate font-medium">{f.name}</span>}
        {!renaming && count > 0 && <span className="text-xs text-ink-4 tabular-nums">{count}</span>}
      </div>
      <Menu.Root open={menuAt !== null} onOpenChange={(o) => !o && setMenuAt(null)}>
        <Menu.Portal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <MenuItems
              items={[
                { icon: <Pencil size={15} />, label: t("menu.rename"), run: () => requestAnimationFrame(() => setRenaming(true)) },
                {
                  icon: <Trash2 size={15} />,
                  label: t("note.deleteFolder"),
                  run: () => {
                    shown((n) => ({ folders: n.folders.filter((x) => x.id !== f.id), notes: n.notes.map((x) => (x.folder === f.id ? { ...x, folder: undefined } : x)) }));
                    void api.deleteNoteFolder(f.id).catch(failed);
                  },
                },
              ]}
            />
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </>
  );
}

/** A folder's name typed in place: Enter or a press away keeps it, Esc does not. */
function FolderName({ f, done }: { f?: NoteFolder; done: () => void }) {
  const [v, setV] = useState(f?.name ?? "");
  const gone = useRef(false);
  const end = (keepIt: boolean) => {
    if (gone.current) return;
    gone.current = true;
    const name = v.trim();
    if (keepIt && name && name !== f?.name) {
      if (f) {
        shown((n) => ({ ...n, folders: n.folders.map((x) => (x.id === f.id ? { ...x, name } : x)) }));
        void api.patchNoteFolder(f.id, { name }).catch(failed);
      } else void api.newNoteFolder({ name }).catch(failed);
    }
    done();
  };
  return (
    <input
      autoFocus
      value={v}
      onChange={(e) => setV(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onBlur={() => end(true)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && !e.nativeEvent.isComposing) (e.preventDefault(), end(true));
        else if (e.key === "Escape") (e.preventDefault(), e.stopPropagation(), end(false));
      }}
      placeholder={t("note.folderName")}
      aria-label={t("note.folderName")}
      className="min-w-0 flex-1 bg-transparent font-medium text-ink outline-none placeholder:text-ink-4"
    />
  );
}

/** A new folder's row, its name typed in place. */
function NewFolder({ done }: { done: () => void }) {
  return (
    <div className="mt-3 flex h-[30px] items-center gap-2 rounded-md bg-fill px-2 text-base pointer-coarse:h-10">
      <Folder size={15} className="shrink-0 text-ink-2" />
      <FolderName done={done} />
    </div>
  );
}
