// Where you type to a session (web/sample's composer; Rhodium spec web-write): attach, the words,
// the context ring, one round key whose icon says what it does now. Idle it sends; in a turn with
// nothing typed it stops the turn; with words it queues them (or steers them into the turn, with a
// leading ! or where the server has no queue). / at the start lists the session's commands.
// Shift+Tab cycles the approval mode; Esc stops the turn. The draft is kept on this device; files
// pasted, dropped or attached go up in chunks while you type.
import { ArrowUp, CornerUpRight, FileText, ListPlus, LoaderCircle, Paperclip, Slash, Square, X } from "lucide-react";
import { forwardRef, useEffect, useImperativeHandle, useRef, useState, type CSSProperties } from "react";
import type { Command, SessionState as LiveSession } from "../api/types.gen";
import { api, missing } from "../api/client";
import type { Sent } from "../chats";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { cn, fine, IconKey, keep, kept, Tip } from "../ui";
import { askOnce, sentFromHere } from "../unread";
import { ContextRing, QuotaBars } from "./meters";
import { upload } from "./upload";

export const draftKey = (id: string) => `draft:${id}`;

/** What the key does now. */
export type KeyMode = "send" | "stop" | "queue" | "steer";
export function keyMode(turn: boolean, text: string, files: number, queues: boolean): KeyMode {
  if (!turn) return "send";
  const words = text.trim();
  if (!words && !files) return "stop";
  if (/^[!！]/.test(words) || !queues) return "steer";
  return "queue";
}

/** The commands for what was typed after the /: names that start with it first, then names that
 *  have it, then descriptions that do. */
export function matching(commands: Command[], typed: string): Command[] {
  const q = typed.toLowerCase();
  const rank = (c: Command) => (c.name.toLowerCase().startsWith(q) ? 0 : c.name.toLowerCase().includes(q) ? 1 : (c.description ?? "").toLowerCase().includes(q) ? 2 : 3);
  return commands
    .map((c) => [rank(c), c] as const)
    .filter(([r]) => r < 3)
    .sort((a, b) => a[0] - b[0])
    .map(([, c]) => c);
}

/** A session's commands, or its agent's usual ones. Those the page does itself (model, mode) open
 *  their keys. */
export function commandsOf(s: LiveSession): Command[] {
  if (s.commands?.length) return s.commands;
  if (s.agent !== "claude") return [];
  return ["compact", "context", "usage", "cost", "model", "mode", "clear"].map((name) => ({ name, description: t(`cmd.${name}` as Parameters<typeof t>[0]) }));
}

type Att = { key: string; file: File; sent: number; ref?: string; error?: string; url?: string; stop: AbortController };

export type ComposerHandle = { focus: () => void; put: (text: string) => void; attach: (files: File[]) => void; box: () => HTMLTextAreaElement | null };

type Props = {
  s: LiveSession;
  turn: boolean;
  /** A message is going: shown at once as yours, faint till the transcript has it. */
  onSent: (sent: Sent, from: { x: number; y: number }) => void;
  onSentDone: (sent: Sent) => void;
  cycleMode: () => void;
  openModel: () => void;
  openMode: () => void;
};

export const Composer = forwardRef<ComposerHandle, Props>(function Composer({ s, turn, onSent, onSentDone, cycleMode, openModel, openMode }, ref) {
  const [text, setText] = useState(() => kept(draftKey(s.id)) ?? "");
  const [atts, setAtts] = useState<Att[]>([]);
  const [busy, setBusy] = useState(false);
  const [at, setAt] = useState(0);
  const [shut, setShut] = useState<string | null>(null);
  const [composing, setComposing] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const textNow = useRef(text);
  textNow.current = text;

  const type = (v: string) => {
    setText(v);
    keep(draftKey(s.id), v || null);
    setAt(0);
    setShut(null);
  };
  const grow = () => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 180)}px`;
  };
  useEffect(grow, [text]);
  // On a computer the caret is here as a chat opens; a phone would bring up its keyboard.
  useEffect(() => void (fine() && box.current?.focus({ preventScroll: true })), [s.id]);

  const attach = (files: File[]) => {
    const add = files.map((file): Att => ({ key: crypto.randomUUID(), file, sent: 0, url: file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined, stop: new AbortController() }));
    setAtts((a) => [...a, ...add]);
    for (const a of add) {
      const set = (p: Partial<Att>) => setAtts((all) => all.map((x) => (x.key === a.key ? { ...x, ...p } : x)));
      upload(s.id, a.file, (sent) => set({ sent }), a.stop.signal).then(
        (d) => set({ ref: d.ref, sent: a.file.size }),
        (e) => a.stop.signal.aborted || set({ error: missing(e) ? t("err.notYet") : errText(e) }),
      );
    }
  };
  const remove = (key: string) =>
    setAtts((all) =>
      all.filter((a) => {
        if (a.key !== key) return true;
        a.stop.abort();
        if (a.url) URL.revokeObjectURL(a.url);
        return false;
      }),
    );
  useImperativeHandle(ref, () => ({
    focus: () => box.current?.focus(),
    put: (words) => (type([textNow.current, words].filter(Boolean).join("\n")), box.current?.focus()),
    attach,
    box: () => box.current,
  }));

  const commands = commandsOf(s);
  const list = /^\/\S*$/.test(text) && text !== shut ? matching(commands, text.slice(1)) : [];
  const uploading = atts.some((a) => !a.ref && !a.error);
  const empty = !text.trim() && !atts.length;
  const queues = s.queue !== undefined;
  const mode = keyMode(turn, text, atts.length, queues);

  /** Page-handled commands: true when done here. */
  const own = (words: string) => {
    const w = words.trim();
    if (w === "/model") return (type(""), openModel(), true);
    if (w === "/mode") return (type(""), openMode(), true);
    if (w === "/stop") return (type(""), void stop(), true);
    return false;
  };
  const stop = async () => {
    setBusy(true);
    try {
      await api.interrupt(s.id);
    } catch (e) {
      toast(t("err.stop"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  const go = async () => {
    if (busy || uploading || (empty && mode !== "stop")) return;
    if (mode === "stop") return stop();
    if (atts.some((a) => a.error)) return toast(t("err.upload"), { error: true });
    const words = text.trim().replace(/^[!！]\s*/, "");
    if (!atts.length && own(words)) return;
    const files = atts.map((a) => ({ ref: a.ref!, name: a.file.name, type: a.file.type }));
    const sent: Sent = { key: crypto.randomUUID(), text: words, files, from: 0 };
    const el = box.current!;
    const r = el.getBoundingClientRect();
    const cs = getComputedStyle(el);
    type("");
    setAtts([]);
    // While a turn runs the server queues what is sent; steering joins the turn. Queued, the queue
    // shows it, not a bubble; turned away, the words go back where they were typed.
    if (mode === "queue" && queues) {
      try {
        await api.send(s.id, words, files.map((f) => f.ref));
      } catch (e) {
        type([words, textNow.current].filter(Boolean).join("\n"));
        toast(t("err.queue"), { error: true, description: errText(e) });
      }
      return;
    }
    askOnce();
    sentFromHere(s.id);
    onSent(sent, { x: r.left + parseFloat(cs.paddingLeft), y: r.top + parseFloat(cs.paddingTop) });
    try {
      const r = await api.send(s.id, words, files.map((f) => f.ref), mode === "steer");
      sent.input = r.input;
      // A turn began meanwhile: it waits in the queue, which shows it.
      sent.queued = r.queued;
    } catch (e) {
      sent.failed = errText(e);
    }
    onSentDone(sent);
  };

  const KEYS: Record<KeyMode, [string, React.ReactNode]> = {
    send: [t("composer.send"), <ArrowUp size={17} strokeWidth={2.25} />],
    stop: [t("composer.stop"), <Square size={12} fill="currentColor" />],
    queue: [t("composer.queue"), <ListPlus size={17} />],
    steer: [t("composer.steer"), <CornerUpRight size={16} />],
  };
  const [label, icon] = KEYS[mode];

  // The palette beside the composer, not in it: a frosted layer inside another sees only that one.
  return (
    <div className="relative">
      {list.length > 0 && <Palette list={list} at={Math.min(at, list.length - 1)} point={setAt} pick={(c) => (own(`/${c.name}`) ? undefined : (type(`/${c.name} `), box.current?.focus()))} />}
    <div className="glass float relative rounded-xl">
      <QuotaBars agent={s.agent} live={s.live} />
      {atts.length > 0 && (
        <ul data-attachments className="m-0 flex list-none gap-2 overflow-x-auto px-3 pt-3 pb-0">
          {atts.map((a) => (
            <li key={a.key} className="relative shrink-0 pt-1.5 pr-1.5">
              {a.url ? <img src={a.url} alt={a.file.name} className={cn("size-14 rounded-lg object-cover shadow-[0_0_0_0.5px_var(--edge)]", a.error && "opacity-50")} /> : (
                <span className="flex h-14 w-36 items-center gap-1.5 rounded-lg bg-fill px-2 text-xs">
                  <FileText size={16} className="shrink-0 text-ink-3" />
                  <span className="line-clamp-2 [overflow-wrap:anywhere]">{a.file.name}</span>
                </span>
              )}
              {!a.ref && !a.error && <span className="absolute right-1.5 bottom-0 left-0 h-[2px] origin-left rounded-full bg-ink-3 transition-transform" style={{ transform: `scaleX(${a.file.size ? a.sent / a.file.size : 0})` }} />}
              {a.error && (
                <Tip label={a.error}>
                  <span className="absolute inset-x-0 bottom-0.5 text-center text-[10px] font-medium text-err">!</span>
                </Tip>
              )}
              <button type="button" aria-label={t("composer.remove")} onClick={() => remove(a.key)} className="glass thick float absolute top-0 right-0 grid size-5 place-items-center rounded-full text-ink-2 hover:text-ink">
                <X size={12} />
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex items-end gap-0.5 px-1.5 pt-3 pb-1.5 pointer-coarse:pt-3.5">
        <input
          ref={picker}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            attach([...(e.target.files ?? [])]);
            e.target.value = "";
          }}
        />
        <IconKey label={t("composer.attach")} onClick={() => picker.current?.click()}>
          <Paperclip size={17} />
        </IconKey>
        <textarea
          ref={box}
          rows={1}
          value={text}
          data-draft="1"
          placeholder={t("composer.placeholder")}
          aria-label={t("composer.placeholder")}
          onChange={(e) => type(e.target.value)}
          onCompositionStart={() => setComposing(true)}
          onCompositionEnd={() => setComposing(false)}
          onPaste={(e) => {
            const pasted = [...e.clipboardData.files];
            if (!pasted.length || e.clipboardData.getData("text/plain")) return;
            e.preventDefault();
            attach(pasted);
          }}
          onKeyDown={(e) => {
            if (composing || e.nativeEvent.isComposing || e.keyCode === 229) return;
            const move = list.length ? { ArrowDown: 1, ArrowUp: -1 }[e.key] : undefined;
            if (move) setAt((Math.min(at, list.length - 1) + move + list.length) % list.length);
            else if (list.length && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) {
              const c = list[Math.min(at, list.length - 1)]!;
              if (!own(`/${c.name}`)) type(`/${c.name} `);
            } else if (list.length && e.key === "Escape") setShut(text);
            else if (e.key === "Tab" && e.shiftKey) cycleMode();
            // Enter sends on a computer (Ctrl or ⌘ with it on a phone); in an empty box it does nothing,
            // as it would stop the turn: Esc does that.
            else if (e.key === "Enter" && !e.shiftKey && (fine() || e.ctrlKey || e.metaKey)) void (empty || go());
            else if (e.key === "Escape" && turn && !busy) void stop();
            else return;
            e.preventDefault();
          }}
          className="no-ring max-h-[180px] min-h-8 min-w-0 flex-1 resize-none bg-transparent px-1.5 py-[5.5px] text-md leading-[21px] placeholder:text-ink-3"
        />
        <ContextRing session={s.id} summary={s.context} />
        <Tip label={label}>
          <button
            type="button"
            aria-label={label}
            disabled={(empty && mode !== "stop") || busy || uploading}
            onClick={() => void go()}
            className="g-press grid size-8 shrink-0 place-items-center rounded-full bg-ink text-on-ink transition-opacity duration-(--dur-1) disabled:opacity-25 pointer-coarse:size-10"
          >
            {busy || uploading ? <LoaderCircle size={15} className="animate-spin" /> : <span key={mode} className="g-fade grid">{icon}</span>}
          </button>
        </Tip>
      </div>
    </div>
    </div>
  );
});

/** The commands over the composer, as wide: a row each, ↑↓ and Enter or Tab on a keyboard. */
function Palette({ list, at, pick, point }: { list: Command[]; at: number; pick: (c: Command) => void; point: (i: number) => void }) {
  return (
    <div role="listbox" aria-label={t("composer.commands")} className="g-pop glass thick float absolute inset-x-0 bottom-full z-10 mb-2 max-h-72 overflow-y-auto rounded-lg p-1.5">
      {list.map((c, i) => (
        <button
          key={c.name}
          type="button"
          role="option"
          aria-selected={i === at}
          ref={(el) => void (i === at && el?.scrollIntoView({ block: "nearest" }))}
          onPointerDown={(e) => e.preventDefault()}
          onPointerMove={() => i === at || point(i)}
          onClick={() => pick(c)}
          style={{ "--i": i } as CSSProperties}
          className={cn("flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-base pointer-coarse:h-11", i === at && "bg-fill")}
        >
          <Slash size={13} className="shrink-0 text-ink-3" />
          <span className="shrink-0 font-medium">{c.name}</span>
          {c.argumentHint && <span className="max-w-[33%] min-w-0 shrink truncate font-mono text-xs text-ink-3">{c.argumentHint}</span>}
          <span className="min-w-0 flex-1 truncate text-sm text-ink-3">{c.description}</span>
        </button>
      ))}
    </div>
  );
}
