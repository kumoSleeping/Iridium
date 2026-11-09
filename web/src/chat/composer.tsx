// Where you type to a session (web/sample's composer; Rhodium spec web-write): attach, the words,
// one round key whose icon says what it does now; the meters are under it, not in it (chat.tsx). Idle it sends; in a turn with
// nothing typed it stops the turn; with words it queues them (or steers them into the turn, with a
// leading !, Ctrl or ⌘+Enter as Claude Code's send now, or where the server has no queue). / at
// the start lists the session's commands; @ lists the sessions to name, as a row dropped here
// names one, on a phone too (the user, 2026-11-06): a card among the files, one piece, gone with
// its × or a Backspace at the box's start, its mention put before the words as it is sent (the
// user, 2026-11-08). Shift+Tab cycles the approval mode; Esc stops the turn. The draft is kept on
// this device; files pasted, dropped or attached go up in chunks while you type, a picture in its
// own shape under a veil drawn back as it goes; nothing is sent till all are up.
import { ArrowUp, Box, Brain, ChartPie, Eraser, FileText, FoldVertical, Gauge, LoaderCircle, MessageSquare, Paperclip, ShieldCheck, Slash, Square, X, type LucideIcon } from "lucide-react";
import { forwardRef, Fragment, useEffect, useImperativeHandle, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { Agent, Command, Session, SessionState as LiveSession } from "../api/types.gen";
import { runShell, shellLine, useShellable } from "./shell";
import { api, missing } from "../api/client";
import type { Sent } from "../chats";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { useList } from "../store";
import { agentName, cn, fine, IconKey, keep, kept, Logo, steerKey, tilde, Tip, titleOf } from "../ui";
import { hasMention, MentionMarks, mentionOf } from "./mention";
import { atSessions, atWord, shortId } from "./refs";
import { useSettings } from "../data";
import { useGlide } from "../motion";
import { previews, Zoom } from "./files";
import { upload } from "./upload";
import { findSecretish } from "./secretish";
import { SaveSecret } from "./savesecret";

/** The words' box, as MentionMarks draws them under it too. */
const BOX = "px-0.5 py-1 text-md leading-[21px]";
export const draftKey = (id: string) => `draft:${id}`;

/** What the key does now. steers: Enter puts words into a running turn (the setting enterInTurn,
 *  the default); other: as Ctrl or ⌘+Enter, which does the other. */
export type KeyMode = "send" | "stop" | "queue" | "steer";
export function keyMode(turn: boolean, text: string, files: number, queues: boolean, steers = true, other = false): KeyMode {
  if (!turn) return "send";
  const words = text.trim();
  if (words.startsWith("$")) return "send"; // a command line (shell.tsx), not for the turn
  if (!words && !files) return "stop";
  // A command waits for the turn's end: the server never steers one in.
  if (words.startsWith("/") && queues) return "queue";
  if (/^[!！]/.test(words) || !queues) return "steer";
  return steers !== other ? "steer" : "queue";
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

/** What the page says of the commands every session has (the server sends them without words). */
const COMMON = ["clear", "compact", "context", "usage", "model", "effort", "mode"] as const;
const describe = (c: Command): Command => (c.description || !COMMON.includes(c.name as (typeof COMMON)[number]) ? c : { ...c, description: t(`cmd.${c.name as (typeof COMMON)[number]}`) });

/** A session's commands (the server's list: the common ones, then its agent's own), or the common
 *  ones before its state has come. Those the page does itself (model, mode…) open their keys;
 *  /clear goes on in a new session. */
export function commandsOf(s: LiveSession): Command[] {
  if (s.commands?.length) return s.commands.map(describe);
  return COMMON.filter((n) => n !== "effort" && (n !== "usage" || s.agent !== "pi")).map((name) => describe({ name }));
}

type Att = { key: string; file: File; sent: number; ref?: string; path?: string; error?: string; url?: string; stop: AbortController };

/** put: words on a line of their own at the end; insert: at the caret, a space either side; name:
 *  a session named, a card among the attachments (a row dropped here, chat.tsx). */
export type ComposerHandle = { focus: () => void; put: (text: string) => void; insert: (text: string) => void; attach: (files: File[]) => void; name: (id: string) => void; box: () => HTMLTextAreaElement | null };

type Props = {
  s: LiveSession;
  turn: boolean;
  /** A message is going: shown at once as yours, faint till the transcript has it. */
  onSent: (sent: Sent) => void;
  onSentDone: (sent: Sent) => void;
  cycleMode: () => void;
  openModel: () => void;
  openMode: () => void;
  /** /usage: the quota popover, not the agent's (the user, 2026-11-06). */
  openUsage: () => void;
  /** /context: the context ring's popover; false if there is none yet. */
  openContext: () => boolean;
  /** /clear went on in session id. */
  cleared: (id: string) => void;
  /** The keys in the composer's foot, between attach and send: mode, model, quota, context. */
  tools?: ReactNode;
  /** The key at the far right, before send: the context ring. */
  end?: ReactNode;
};

export const Composer = forwardRef<ComposerHandle, Props>(function Composer({ s, turn, onSent, onSentDone, cycleMode, openModel, openMode, openUsage, openContext, cleared, tools, end }, ref) {
  const [text, setText] = useState(() => kept(draftKey(s.id)) ?? "");
  const [atts, setAtts] = useState<Att[]>([]);
  // Sessions named: cards before the files, one piece each, a Backspace at the box's start takes the
  // last (the user, 2026-11-08); sent as their mentions before the words. Kept with the draft.
  const [named, setNamedNow] = useState<string[]>(() => (kept(`${draftKey(s.id)}:named`) ?? "").split(" ").filter(Boolean));
  const setNamed = (f: (ids: string[]) => string[]) =>
    setNamedNow((ids) => {
      const next = f(ids);
      keep(`${draftKey(s.id)}:named`, next.length ? next.join(" ") : null);
      return next;
    });
  const nameOne = (id: string) => setNamed((ids) => (ids.includes(id) || id === s.id ? ids : [...ids, id]));
  const [busy, setBusy] = useState(false);
  const [at, setAt] = useState(0);
  const [shut, setShut] = useState<string | null>(null);
  const [composing, setComposing] = useState(false);
  // Where the caret is while the box has it: an @word there lists the sessions to name.
  const [caret, setCaret] = useState<number | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const marked = hasMention(text);
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
    el.style.height = `${Math.min(el.scrollHeight, 220)}px`;
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
        (d) => set({ ref: d.ref, path: d.path, sent: a.file.size }),
        (e) => a.stop.signal.aborted || set({ error: missing(e) ? t("err.notYet") : errText(e) }),
      );
    }
  };
  const remove = (key: string) =>
    setAtts((all) =>
      all.filter((a) => {
        if (a.key !== key) return true;
        a.stop.abort();
        // After the row has shut: the last one is drawn as it does.
        if (a.url) setTimeout(() => URL.revokeObjectURL(a.url!), 1000);
        return false;
      }),
    );
  // The keys under it glide as one changes width (UltraCode's mark, a longer name), never jump.
  const toolRow = useRef<HTMLDivElement>(null);
  useGlide(toolRow);
  useImperativeHandle(ref, () => ({
    focus: () => box.current?.focus(),
    put: (words) => (type([textNow.current, words].filter(Boolean).join("\n")), box.current?.focus()),
    insert: (words) => {
      const el = box.current;
      const v = textNow.current;
      // Where the caret was: a box keeps its selection when it loses focus.
      const a = el ? el.selectionStart : v.length;
      const b = el ? el.selectionEnd : v.length;
      const before = v.slice(0, a) + (a > 0 && !/\s$/.test(v.slice(0, a)) ? " " : "");
      const after = (/^\s/.test(v.slice(b)) ? "" : " ") + v.slice(b);
      type(before + words + after);
      requestAnimationFrame(() => {
        el?.focus();
        el?.setSelectionRange(before.length + words.length + 1, before.length + words.length + 1);
      });
    },
    attach,
    name: (id) => (nameOne(id), box.current?.focus()),
    box: () => box.current,
  }));

  // The pictures and files row opens and shuts (the user, 2026-11-06): the composer grows to it
  // instead of jumping taller; the last ones stay drawn as it shuts.
  const lastAtts = useRef({ atts, named });
  const row = atts.length + named.length > 0;
  if (row) lastAtts.current = { atts, named };
  const shownAtts = row ? atts : lastAtts.current.atts;
  const shownNamed = row ? named : lastAtts.current.named;

  const commands = commandsOf(s);
  // Nothing typed after the /: the common ones first, then the agent's own, under their heads.
  const list = /^\/\S*$/.test(text) && text !== shut ? (text === "/" ? [...commands.filter(isCommon), ...commands.filter((c) => !isCommon(c))] : matching(commands, text.slice(1))) : [];
  const sessions = useList();
  const word = caret !== null && text !== shut && !list.length ? atWord(text, caret) : null;
  const picks = word && sessions ? atSessions(sessions, word.q, s.id) : [];
  /** The @word made a card of session to: the word goes, the caret where it was. */
  const name = (to: Session) => {
    if (!word) return;
    const end = caret ?? word.from;
    type(text.slice(0, word.from) + text.slice(end).replace(/^ /, ""));
    nameOne(to.id);
    setCaret(word.from);
    requestAnimationFrame(() => (box.current?.focus(), box.current?.setSelectionRange(word.from, word.from)));
  };
  const shown = list.length || picks.length;
  // What looks like a key: store it as a credential and say {{NAME}} instead (savesecret.tsx).
  const [notSecret, setNotSecret] = useState<ReadonlySet<string>>(new Set());
  const secretish = shown ? null : findSecretish(text, notSecret);
  const uploading = atts.some((a) => !a.ref && !a.error);
  const empty = !text.trim() && !atts.length && !named.length;
  const queues = s.queue !== undefined;
  const steers = (useSettings().data?.enterInTurn ?? "steer") !== "queue";
  const mode = keyMode(turn, text, atts.length + named.length, queues, steers);
  const shellable = useShellable(s);

  /** Page-handled commands: true when done here. */
  const own = (words: string) => {
    const w = words.trim().toLowerCase();
    if (w === "/model" || w === "/effort") return (type(""), openModel(), true);
    if (w === "/mode") return (type(""), openMode(), true);
    if (w === "/usage") return (type(""), openUsage(), true);
    if (w === "/stop") return (type(""), void stop(), true);
    if (w === "/clear" || w === "/new") return (type(""), void clear(), true);
    if (w === "/context") {
      if (openContext()) return (type(""), true);
      // Claude says it itself; the others have nothing to say before a turn.
      if (s.agent !== "claude") return (type(""), toast(t("cmd.noContext")), true);
    }
    return false;
  };
  const clear = async () => {
    setBusy(true);
    try {
      const r = await api.send(s.id, "/clear");
      if (r.session && r.session !== s.id) cleared(r.session);
    } catch (e) {
      toast(t("err.clear"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
    }
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
  const go = async (other = false) => {
    const mode = keyMode(turn, text, atts.length + named.length, queues, steers, other);
    if (busy || uploading || (empty && mode !== "stop")) return;
    if (mode === "stop") return stop();
    if (atts.some((a) => a.error)) return toast(t("err.upload"), { error: true });
    // $ and a command line: run at the session's machine, not sent to the agent (shell.tsx).
    const line = shellable && !atts.length && !named.length ? shellLine(text) : null;
    if (line) {
      type("");
      setBusy(true);
      return void runShell(s, line).finally(() => setBusy(false));
    }
    const typed = text.trim().replace(/^[!！]\s*/, "");
    if (!atts.length && !named.length && own(typed)) return;
    // The sessions named, as their mentions, before the words: what the agent reads.
    const said = named.flatMap((id) => {
      const to = sessions?.find((x) => x.id === id);
      return to && sessions ? [mentionOf(to, sessions)] : [];
    });
    const words = [...said, typed].filter(Boolean).join(" ");
    // A picture's copy here stays: the bubble shows it while sending, and once landed till the
    // server's thumbnail has come (chat/files.tsx previews).
    for (const a of atts) if (a.url && a.path) previews.set(a.path, a.url);
    const files = atts.map((a) => ({ ref: a.ref!, name: a.file.name, type: a.file.type, url: a.url }));
    const sent: Sent = { key: crypto.randomUUID(), text: words, files, from: 0, steer: mode === "steer" };
    type("");
    setAtts([]);
    setNamed(() => []);
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
    onSent(sent);
    try {
      const r = await api.send(s.id, words, files.map((f) => f.ref), mode === "steer");
      sent.input = r.input;
      // A turn began meanwhile: it waits in the queue, which shows it. A command the agent runs
      // itself (Codex's /compact) never comes in the transcript: the turn's lamp says it.
      sent.queued = r.queued || r.command;
    } catch (e) {
      sent.failed = errText(e);
    }
    onSentDone(sent);
  };

  const KEYS: Record<KeyMode, [string, React.ReactNode]> = {
    send: [t("composer.send"), <ArrowUp size={16} strokeWidth={2.25} />],
    stop: [t("composer.stop"), <Square size={12} fill="currentColor" />],
    queue: [t("composer.queue", { key: steerKey() }), <ArrowUp size={16} strokeWidth={2.25} />], // one send key for all: what it does in its tip (the user, 2026-11-06)
    steer: [steers && queues && !/^[!！]/.test(text.trim()) ? t("composer.steerOr", { key: steerKey() }) : t("composer.steer"), <ArrowUp size={16} strokeWidth={2.25} />],
  };
  const [label, icon] = KEYS[mode];

  // The palette beside the composer, not in it: a frosted layer inside another sees only that one.
  return (
    <div className="relative">
      {list.length > 0 && <Palette list={list} at={Math.min(at, list.length - 1)} agent={s.agent} grouped={text === "/"} point={setAt} pick={(c) => (own(`/${c.name}`) ? undefined : (type(`/${c.name} `), box.current?.focus()))} />}
      {picks.length > 0 && <AtList list={picks} at={Math.min(at, picks.length - 1)} point={setAt} pick={name} />}
      {secretish && (
        <SaveSecret
          key={secretish.value}
          found={secretish}
          skip={() => setNotSecret(new Set([...notSecret, secretish.value]))}
          saved={(n) => {
            const words = `{{${n}}}`;
            type(text.slice(0, secretish.start) + words + text.slice(secretish.end));
            requestAnimationFrame(() => box.current?.focus());
          }}
        />
      )}
    <div className="glass float rest relative @container rounded-[20px]">
      <div aria-hidden={!row || undefined} className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-3) ease-(--ease-out)", row ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
        <div className="min-h-0 overflow-hidden">
        <ul data-attachments className="m-0 flex list-none gap-2 overflow-x-auto px-3.5 pt-3 pb-0">
          {shownNamed.map((id) => (
            <NamedCard key={`s:${id}`} to={sessions?.find((x) => x.id === id)} remove={() => setNamed((ids) => ids.filter((x) => x !== id))} />
          ))}
          {shownAtts.map((a) => {
            // Its part sent, 0..1; a picture keeps its own shape, its height the row's.
            const part = a.ref ? 1 : a.file.size ? a.sent / a.file.size : 0;
            const going = !a.ref && !a.error;
            return (
              <li key={a.key} className="g-pop relative shrink-0 pt-1.5 pr-1.5">
                <span className="relative block overflow-hidden rounded-lg shadow-[0_0_0_0.5px_var(--edge)]">
                  {a.url ? (
                    <Zoom full={a.url} name={a.file.name}>
                      <button type="button" aria-label={`${a.file.name} · ${t("msg.openImage")}`} className="block cursor-zoom-in">
                        <img src={a.url} alt={a.file.name} className={cn("block h-14 w-auto max-w-40 min-w-10 object-cover", a.error && "opacity-50")} />
                      </button>
                    </Zoom>
                  ) : (
                    <span className="flex h-14 w-36 items-center gap-1.5 bg-fill px-2 text-xs">
                      <FileText size={16} className="shrink-0 text-ink-3" />
                      <span className="line-clamp-2 [overflow-wrap:anywhere]">{a.file.name}</span>
                    </span>
                  )}
                  {/* Going up: a veil over what has not gone yet, drawn back as it goes, and a ring
                      in the middle; gone, it fades. Till then it cannot be sent (go). */}
                  <span aria-hidden className={cn("pointer-events-none absolute inset-y-0 right-0 bg-paper/60 backdrop-blur-[1.5px] transition-[left,opacity] duration-(--dur-3) ease-(--ease-out)", !going && "opacity-0")} style={{ left: `${part * 100}%` }} />
                  {going && (
                    <span className="pointer-events-none absolute inset-0 grid place-items-center" role="progressbar" aria-label={t("composer.uploading")} aria-valuenow={Math.round(part * 100)}>
                      <Ring part={part} />
                    </span>
                  )}
                </span>
                {a.error && (
                  <Tip label={a.error}>
                    <span className="absolute inset-x-0 bottom-0.5 text-center text-[10px] font-medium text-err">!</span>
                  </Tip>
                )}
                <button type="button" aria-label={t("composer.remove")} onClick={() => remove(a.key)} className="glass thick float absolute top-0 right-0 grid size-5 place-items-center rounded-full text-ink-2 hover:text-ink">
                  <X size={12} />
                </button>
              </li>
            );
          })}
        </ul>
        </div>
      </div>
      {/* The words first, at least two lines high; then the foot: attach, the meters, the context
          ring, send at the far right (the user, 2026-11-06). */}
      <div className="px-3.5 pt-3">
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
        {/* A session named (dropped here) lies on a soft mark: the box's own letters clear over it. */}
        <div className="relative">
        {marked && <MentionMarks text={text} box={box} className={BOX} />}
        <textarea
          ref={box}
          rows={1}
          value={text}
          data-draft="1"
          placeholder={t("composer.placeholder")}
          aria-label={t("composer.placeholder")}
          onChange={(e) => (type(e.target.value), setCaret(e.target.selectionStart))}
          onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
          onFocus={(e) => setCaret(e.currentTarget.selectionStart)}
          onBlur={() => setCaret(null)}
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
            const move = shown ? { ArrowDown: 1, ArrowUp: -1 }[e.key] : undefined;
            if (move) setAt((Math.min(at, shown - 1) + move + shown) % shown);
            else if (list.length && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) {
              const c = list[Math.min(at, list.length - 1)]!;
              if (!own(`/${c.name}`)) type(`/${c.name} `);
            } else if (picks.length && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) name(picks[Math.min(at, picks.length - 1)]!);
            // At the box's very start, Backspace takes the last session named, whole.
            else if (e.key === "Backspace" && named.length && e.currentTarget.selectionStart === 0 && e.currentTarget.selectionEnd === 0) setNamed((ids) => ids.slice(0, -1));
            else if (shown && e.key === "Escape") setShut(text);
            else if (e.key === "Tab" && e.shiftKey) cycleMode();
            // In a turn, Ctrl or ⌘+Enter does what Enter does not: steer in (Claude Code's send now) or queue.
            else if (e.key === "Enter" && !e.shiftKey && (e.ctrlKey || e.metaKey) && turn) void (empty || go(true));
            // Enter sends on a computer (Ctrl or ⌘ with it on a phone); in an empty box it does nothing,
            // as it would stop the turn: Esc does that.
            else if (e.key === "Enter" && !e.shiftKey && (fine() || e.ctrlKey || e.metaKey)) void (empty || go());
            else if (e.key === "Escape" && turn && !busy) void stop();
            else return;
            e.preventDefault();
          }}
          className={cn("no-ring relative block max-h-[220px] min-h-[52px] w-full resize-none bg-transparent placeholder:text-ink-3", BOX, marked && "text-transparent [caret-color:var(--ink)]")}
        />
        </div>
      </div>
      <div className="flex min-w-0 items-center gap-0.5 px-2 pt-0.5 pb-2">
        <IconKey label={t("composer.attach")} onClick={() => picker.current?.click()}>
          <Paperclip size={17} />
        </IconKey>
        <div ref={toolRow} className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
          {tools}
        </div>
        {end && <span className="flex shrink-0">{end}</span>}
        <Tip label={label}>
          <button
            type="button"
            aria-label={label}
            disabled={(empty && mode !== "stop") || busy || uploading}
            onClick={() => void go()}
            className="group/send g-press grid size-8 shrink-0 place-items-center pointer-coarse:size-10"
          >
            {/* A rounded square the size of the attach key's glyph area, not a large disc; the
                whole key still takes the press (the user, 2026-11-08). */}
            <span className="grid size-7 place-items-center rounded-[9px] bg-ink text-on-ink transition-opacity duration-(--dur-1) group-disabled/send:opacity-25 pointer-coarse:size-8 pointer-coarse:rounded-[10px]">
              {busy || uploading ? <LoaderCircle size={14} className="animate-spin" /> : <span key={mode} className="g-fade grid">{icon}</span>}
            </span>
          </button>
        </Tip>
      </div>
    </div>
    </div>
  );
});

/** The sessions an @ may name, over the composer as the commands are: each its logo, title and
 *  folder; ↑↓ and Enter or Tab on a keyboard, a tap on a phone. */
function AtList({ list, at, pick, point }: { list: Session[]; at: number; pick: (s: Session) => void; point: (i: number) => void }) {
  return (
    <div role="listbox" aria-label={t("composer.sessions")} className="g-pop glass thick float absolute inset-x-0 bottom-full z-10 mb-2 max-h-72 overflow-y-auto rounded-lg p-1.5">
      {list.map((x, i) => (
        <button
          key={x.id}
          type="button"
          role="option"
          aria-selected={i === at}
          ref={(el) => void (i === at && el?.scrollIntoView({ block: "nearest" }))}
          onPointerDown={(e) => e.preventDefault()}
          onPointerMove={() => i === at || point(i)}
          onClick={() => pick(x)}
          style={{ "--i": i } as CSSProperties}
          className={cn("flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-base pointer-coarse:h-11", i === at && "bg-fill")}
        >
          <Logo agent={x.agent} size={14} className="shrink-0" />
          <span className="min-w-0 shrink truncate">{titleOf(x)}</span>
          <span className="ml-auto max-w-[40%] min-w-0 shrink-0 truncate font-mono text-xs text-ink-3 [direction:rtl]">
            <bdi>{tilde(x.cwd)}</bdi>
          </span>
        </button>
      ))}
    </div>
  );
}

/** The common commands' icons; an agent's own have the slash. */
const CMD_ICONS: Record<(typeof COMMON)[number], LucideIcon> = { clear: Eraser, compact: FoldVertical, context: ChartPie, usage: Gauge, model: Box, effort: Brain, mode: ShieldCheck };
const isCommon = (c: Command) => COMMON.includes(c.name as (typeof COMMON)[number]);

/** The commands as the page's menus are (the model's, a right press's), not a bar as wide as the
 *  composer over it (the user, 2026-11-09): a card at the box's corner, grown out of the /; a row
 *  each, its icon, its name, what it does faint at its end; while nothing is typed after the /, the
 *  common ones, then the agent's own, each under its head. ↑↓ and Enter or Tab on a keyboard. */
function Palette({ list, at, agent, grouped, pick, point }: { list: Command[]; at: number; agent: Agent; grouped: boolean; pick: (c: Command) => void; point: (i: number) => void }) {
  return (
    <div role="listbox" aria-label={t("composer.commands")} className="g-pop glass thick float absolute bottom-full left-2 z-10 mb-2 max-h-[min(360px,50dvh)] w-80 max-w-[calc(100%-16px)] origin-bottom-left overflow-y-auto overscroll-contain rounded-lg p-1.5 text-ink">
      <div className="g-stagger flex flex-col">
        {list.map((c, i) => {
          const Icon = isCommon(c) ? CMD_ICONS[c.name as (typeof COMMON)[number]] : Slash;
          const head = grouped && (i === 0 || isCommon(list[i - 1]!) !== isCommon(c));
          return (
            <Fragment key={c.name}>
              {head && <div className="truncate px-2.5 pt-2 pb-0.5 text-xs text-ink-3 select-none first:pt-1">{isCommon(c) ? t("composer.cmdCommon") : agentName(agent)}</div>}
              <button
                type="button"
                role="option"
                aria-selected={i === at}
                ref={(el) => void (i === at && el?.scrollIntoView({ block: "nearest" }))}
                onPointerDown={(e) => e.preventDefault()}
                onPointerMove={() => i === at || point(i)}
                onClick={() => pick(c)}
                style={{ "--i": i } as CSSProperties}
                className={cn("flex h-8 w-full shrink-0 cursor-default items-center gap-2.5 rounded-md px-2.5 text-left text-base outline-none select-none pointer-coarse:h-11", i === at && "bg-fill")}
              >
                <Icon size={15} className="shrink-0 text-ink-2" aria-hidden />
                <span className="shrink-0">{c.name}</span>
                {c.argumentHint && <span className="max-w-[30%] min-w-0 shrink truncate font-mono text-xs text-ink-3">{c.argumentHint}</span>}
                <span className="min-w-0 flex-1 truncate text-right text-xs text-ink-3">{c.description}</span>
              </button>
            </Fragment>
          );
        })}
      </div>
    </div>
  );
}

/** A ring that fills as an upload goes. */
function Ring({ part }: { part: number }) {
  const r = 9;
  const c = 2 * Math.PI * r;
  return (
    <svg width="24" height="24" viewBox="0 0 24 24" className="drop-shadow-[0_0_2px_var(--paper)]">
      <circle cx="12" cy="12" r={r} fill="none" stroke="var(--fill-2)" strokeWidth="2.5" />
      <circle cx="12" cy="12" r={r} fill="none" stroke="var(--ink)" strokeWidth="2.5" strokeLinecap="round" strokeDasharray={c} strokeDashoffset={c * (1 - Math.max(0.04, part))} transform="rotate(-90 12 12)" className="transition-[stroke-dashoffset] duration-(--dur-2) ease-(--ease-out)" />
    </svg>
  );
}

/** A session named, as a file is: its agent's mark, its title, its agent and short id; its ×. */
function NamedCard({ to, remove }: { to?: Session; remove: () => void }) {
  const list = useList();
  return (
    <li className="g-pop relative shrink-0 pt-1.5 pr-1.5">
      <span className="flex h-14 w-44 items-center gap-2 rounded-lg bg-fill px-2.5">
        {to ? <Logo agent={to.agent} size={16} /> : <MessageSquare size={16} className="text-ink-3" />}
        <span className="min-w-0">
          <span className="block truncate text-sm">{to ? titleOf(to) || tilde(to.cwd) : t("composer.goneSession")}</span>
          {to && <span className="block truncate text-xs text-ink-3">{agentName(to.agent)} · {shortId(to.id, (list ?? []).map((x) => x.id))}</span>}
        </span>
      </span>
      <button type="button" aria-label={t("composer.remove")} onClick={remove} className="glass thick float absolute top-0 right-0 grid size-5 place-items-center rounded-full text-ink-2 hover:text-ink">
        <X size={12} />
      </button>
    </li>
  );
}
