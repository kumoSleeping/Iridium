// Where you type to a session (web/sample's composer; Rhodium spec web-write): attach, the words,
// one round key whose icon says what it does now; the meters are under it, not in it (chat.tsx). The
// composer is the person's, not the agent's (kumo, 2026-11-16): its key sends, and is never the
// stop key (that is on the agent's row at work, live.tsx StopKey); empty, it waits; with words in
// the agent's turn it queues them (or steers them into the turn, with a
// leading !, Ctrl or ⌘+Enter as Claude Code's send now, or where the server has no queue). / at
// the start lists the session's commands; @ lists what to name (docs/plan.md "@", 2026-11-10):
// sessions, this machine's groups, each machine's workspaces, a folder by its path; a session's or
// a group's row dropped here names one too, on a phone the list alone: a link where the caret is,
// the box showing its mark and its name, one piece a Backspace takes whole (the user, 2026-11-12;
// refs.ts boxed, edited). Only a reference: nothing delivered, no one notified, nothing
// granted. Shift+Tab cycles the approval mode; Esc stops the turn. The draft is kept on
// this device; files pasted, dropped or attached go up in chunks while you type, a picture in its
// own shape under a veil drawn back as it goes; nothing is sent till all are up.
import { ArrowUp, Box, Brain, ChartPie, Eraser, FileCode2, FileText, FoldVertical, Gauge, LoaderCircle, Paperclip, Reply, ShieldCheck, X, type LucideIcon } from "lucide-react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { forwardRef, Fragment, useEffect, useImperativeHandle, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import type { Agent, Command, SessionState as LiveSession } from "../api/types.gen";
import { runShell, shellLine, useShellable } from "./shell";
import { api, missing } from "../api/client";
import type { Sent } from "../chats";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { stopTurn } from "./live";
import { useList } from "../store";
import { useHome, useMachines, useSettings, useWorkspaces } from "../data";
import { ago, agentName, cn, fine, IconKey, keep, kept, Logo, steerKey, tilde, Tip, titleOf, useMinute } from "../ui";
import { hasMention, MentionMarks, mentionOf, PlaceIcon, placeMention } from "./mention";
import { atWord, boxAt, boxed, edited, outOf, wordsAt } from "./refs";
import { arrowInto, atPath, atSections, pathSection, typedPath, type AtHead, type AtRow, type AtSection, type NamedPlace } from "./at";
import { MachineFace, MachineLink, type Machine } from "../machines";
import { foldersQ } from "../create";
import { CODE, previews, size, Zoom } from "./files";
import { upload } from "./upload";
import { findSecretish } from "./secretish";
import { ALL_AGENTS, ALL_MEMBERS, insertNick, matchAgents, type AgentRow } from "./channel/lib";
import { NickList } from "./channel/nicklist";
import { SaveSecret } from "./savesecret";

const hasDsh = (m: Machine) => m.online && m.agents.some((a) => a.agent === "dsh");

/** The words' box, as MentionMarks draws them under it too. */
const BOX = "px-0.5 py-1 text-md leading-[21px]";
export const draftKey = (id: string) => `draft:${id}`;

/** What the key does with what is typed: sends it; in the agent's turn (turn), queues it or
 *  steers it in. Nothing typed, it sends (the key waits): stopping the turn is not the composer's.
 *  steers: Enter puts words into a running turn (the setting enterInTurn, the default); other: as
 *  Ctrl or ⌘+Enter, which does the other. */
export type KeyMode = "send" | "queue" | "steer";
export function keyMode(turn: boolean, text: string, files: number, queues: boolean, steers = true, other = false): KeyMode {
  if (!turn) return "send";
  const words = text.trim();
  if (words.startsWith("$")) return "send"; // a command line (shell.tsx), not for the turn
  if (!words && !files) return "send";
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
  return COMMON.filter((n) => n !== "effort" && (n !== "usage" || (s.agent !== "pi" && s.agent !== "agy"))).map((name) => describe({ name }));
}

type Att = { key: string; file: File; sent: number; ref?: string; path?: string; error?: string; url?: string; stop: AbortController };

/** Each chat's files not yet sent, kept on this page while another chat is open (kumo,
 *  2026-11-17: a picture attached was gone on coming back); an upload under way goes on into it. */
const unsent = new Map<string, Att[]>();
const unsentSubs = new Set<() => void>();
const NO_ATTS: Att[] = [];
const subscribeUnsent = (f: () => void) => (unsentSubs.add(f), () => void unsentSubs.delete(f));
const setUnsent = (id: string, f: (a: Att[]) => Att[]) => {
  const next = f(unsent.get(id) ?? NO_ATTS);
  if (next.length) unsent.set(id, next);
  else unsent.delete(id);
  for (const x of unsentSubs) x();
};

/** insert: words at the caret, a space either side; name:
 *  a session (its ID) or a place named, a link where the caret is (a row dropped here, chat.tsx). */
export type ComposerHandle = { focus: () => void; insert: (text: string) => void; attach: (files: File[]) => void; name: (to: string | NamedPlace) => void; box: () => HTMLTextAreaElement | null };

type Props = {
  s: LiveSession;
  /** The session's own agent is in a turn: what is typed queues or steers in (keyMode), Esc
   *  stops it. Nothing of the composer shows it. */
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
  /** Unseen: the mode and model keys whose menus /mode and /model open (chat.tsx Under). */
  tools?: ReactNode;
  /** A channel of several agents (chat/channel): these, which an @ lists and calls by nick; no /
   *  commands, no command lines, nothing of one agent's own. */
  nicks?: AgentRow[];
  /** The message being answered (Satori's <quote>): shown over the words, sent with them;
   *  unquote: put it away (Esc, or once sent). */
  quote?: { n: number; who: string; text: string };
  unquote?: () => void;
  /** The channel's people and apps, which @ names beside its agents (peers, the user, 2026-11-13);
   *  in one agent's chat (no nicks), all its @ lists: the agent by its nick, the people. */
  people?: AgentRow[];
};

/** The names that call more than one in a channel (internal/deliver AllMembers, AllAgents), as
 *  rows of the @ list. */
const everyoneRows = (): AgentRow[] => [
  { session: "", member: "*", agent: "" as Agent, nick: ALL_MEMBERS, name: t("channel.atMembers"), status: "idle", recv: "" },
  { session: "", member: "agent:*", agent: "" as Agent, nick: ALL_AGENTS, name: t("channel.atAgents"), status: "idle", recv: "" },
];

export const Composer = forwardRef<ComposerHandle, Props>(function Composer({ s, turn, onSent, onSentDone, cycleMode, openModel, openMode, openUsage, openContext, cleared, tools, nicks, quote, unquote, people }, ref) {
  const multi = !!nicks;
  // The quote as last shown: its row keeps its words while it shuts.
  const lastQuote = useRef(quote);
  if (quote) lastQuote.current = quote;
  const shownQuote = quote ?? lastQuote.current;
  const [text, setText] = useState(() => kept(draftKey(s.id)) ?? "");
  const atts = useSyncExternalStore(subscribeUnsent, () => unsent.get(s.id) ?? NO_ATTS);
  const sid = s.id;
  const setAtts = (f: (a: Att[]) => Att[]) => setUnsent(sid, f);
  const [busy, setBusy] = useState(false);
  // The row pointed at in the open list; null: its first (in a typed path with a name begun, the
  // first folder that name finds).
  const [at, setAt] = useState<number | null>(null);
  const [shut, setShut] = useState<string | null>(null);
  const [composing, setComposing] = useState(false);
  // Where the caret is in the words (not the box: a link there is its name) while the box has it:
  // an @word there lists the sessions to name.
  const [caret, setCaret] = useState<number | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const marked = hasMention(text);
  const shownBox = boxed(text);
  const textNow = useRef(text);
  textNow.current = text;
  /** The box's caret at the words' position i of words v, once the box shows them. */
  const caretAt = (v: string, i: number) => requestAnimationFrame(() => {
    const at = boxAt(boxed(v), i);
    box.current?.focus();
    box.current?.setSelectionRange(at, at);
  });

  const type = (v: string) => {
    setText(v);
    keep(draftKey(s.id), v || null);
    setAt(null);
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

  // Every chat takes files, a channel with no agent too (kumo, 2026-11-16: uploaded to the
  // channel, kept with its message).
  const noFiles = false;
  const attach = (files: File[]) => {
    if (noFiles) return;
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
  // The ring comes once the agent has said its context: faded in as the keys are.
  /** words put where the caret is, a space either side. */
  const insert = (words: string) => {
    const el = box.current;
    const v = textNow.current;
    // Where the caret was: a box keeps its selection when it loses focus.
    const bx = boxed(v);
    const a = el ? wordsAt(bx, el.selectionStart) : v.length;
    const b = el ? wordsAt(bx, el.selectionEnd) : v.length;
    const before = v.slice(0, a) + (a > 0 && !/\s$/.test(v.slice(0, a)) ? " " : "");
    const after = (/^\s/.test(v.slice(b)) ? "" : " ") + v.slice(b);
    const next = before + words + after;
    type(next);
    caretAt(next, before.length + words.length + 1);
  };
  /** How words name session id: its link; none for this one or one not listed. */
  const sessionWords = (id: string) => {
    const to = id !== s.id ? sessions?.find((x) => x.id === id) : undefined;
    return to && sessions ? mentionOf(to, sessions) : "";
  };
  useImperativeHandle(ref, () => ({
    focus: () => box.current?.focus(),
    insert,
    attach,
    name: (to) => {
      const words = typeof to === "string" ? sessionWords(to) : placeMention(to, machines);
      if (words) insert(words);
    },
    box: () => box.current,
  }));

  // The pictures and files row opens and shuts (the user, 2026-11-06): the composer grows to it
  // instead of jumping taller; the last ones stay drawn as it shuts.
  const lastAtts = useRef(atts);
  const row = atts.length > 0;
  if (row) lastAtts.current = atts;
  const shownAtts = row ? atts : lastAtts.current;

  const commands = commandsOf(s);
  // Nothing typed after the /: the common ones first, then the agent's own, under their heads.
  const list = !multi && /^\/\S*$/.test(text) && text !== shut ? (text === "/" ? [...commands.filter(isCommon), ...commands.filter((c) => !isCommon(c))] : matching(commands, text.slice(1))) : [];
  const sessions = useList();
  const machines = useMachines();
  const workspaces = useWorkspaces().data ?? [];
  const home = useHome().data; // ~ typed after the @ is this; named, a whole path
  // & names a session or a place; @ calls a member of a channel of several, by nick, or all of
  // them (Satori's <at>; the user, 2026-11-13).
  const open = caret !== null && text !== shut && !list.length;
  const word = open ? atWord(text, caret, "&") : null;
  const nickWord = (multi || !!people?.length) && open ? atWord(text, caret) : null;
  // The names of all only among several agents.
  const nickOpts = nickWord ? matchAgents([...(nicks ?? []), ...(people ?? []), ...(multi ? everyoneRows() : [])], nickWord.q) : [];
  const nickSel = nickOpts.length ? Math.min(at ?? 0, nickOpts.length - 1) : 0;
  const call = (r: AgentRow) => {
    if (!nickWord) return;
    const next = insertNick(text, nickWord.from, caret ?? nickWord.from, r.nick);
    type(next.text);
    setCaret(next.caret);
    caretAt(next.text, next.caret);
  };
  // A path typed after the @: its folder and those in it, asked for as the picker does (~ the
  // daemon's to resolve, the folder named by the path it gives); else each
  // machine's DSH workspaces, while the list is open (online ones with dsh).
  const path = word ? atPath(word.q, machines, home) : null;
  const folders = useQuery({ ...foldersQ(path?.dir ?? "", path?.m?.id), enabled: !!path });
  const dsh = useQueries({ queries: machines.map((m) => ({ queryKey: ["dshWorkspaces", m.id], queryFn: () => api.dshWorkspaces(m.id), enabled: !!word && !path && hasDsh(m), retry: false, staleTime: 30_000 })) });
  const sections: AtSection[] = !word
    ? []
    : path
      ? [pathSection(path, { path: folders.data?.path, folders: folders.data?.folders, missing: !!folders.error })]
      : sessions
        ? atSections({ list: sessions, here: s.id, workspaces, machines, dsh: Object.fromEntries(machines.map((m, i) => [m.id, hasDsh(m) ? dsh[i]?.data : undefined])) }, word.q)
        : [];
  const opts = sections.flatMap((x) => x.rows).filter((r) => !r.off);
  const sel = opts.length ? Math.min(at ?? (path?.part && opts.length > 1 && opts[0]!.kind === "place" && opts[0]!.self ? 1 : 0), opts.length - 1) : 0;
  /** The @word made row r's link, a space after it, the caret after that. */
  const name = (r: AtRow) => {
    if (!word) return;
    const end = caret ?? word.from;
    const words = r.kind === "session" ? sessionWords(r.s.id) : placeMention(r.p, machines);
    const head = text.slice(0, word.from) + (words ? `${words} ` : "");
    const next = head + text.slice(end).replace(/^ /, "");
    type(next);
    setCaret(head.length);
    caretAt(next, head.length);
  };
  /** Into place r's folder: its path after the @, the list then the folders in it. */
  const enter = (r: AtRow) => {
    if (!word || r.kind !== "place") return;
    const end = caret ?? word.from;
    const typed = `&${typedPath(r.p, machines)}`;
    const next = text.slice(0, word.from) + typed + text.slice(end);
    type(next);
    const to = word.from + typed.length;
    setCaret(to);
    caretAt(next, to);
  };
  /** Whether → goes into row r's folder (arrowInto), the box as it is. */
  const into = (r: AtRow | undefined, el: HTMLTextAreaElement, held: boolean) => !!word && r?.kind === "place" && arrowInto(text, word.from, wordsAt(shownBox, el.selectionStart), wordsAt(shownBox, el.selectionEnd), `&${typedPath(r.p, machines)}`, held);
  const atOpen = sections.length > 0;
  const shown = list.length || opts.length || nickOpts.length;
  // What looks like a key: store it as a credential and say {{NAME}} instead (savesecret.tsx).
  const [notSecret, setNotSecret] = useState<ReadonlySet<string>>(new Set());
  const secretish = shown || atOpen ? null : findSecretish(text, notSecret);
  const uploading = atts.some((a) => !a.ref && !a.error);
  const empty = !text.trim() && !atts.length;
  const queues = s.queue !== undefined;
  const steers = (useSettings().data?.enterInTurn ?? "steer") !== "queue";
  const mode = keyMode(turn, text, atts.length, queues, steers);
  const shellable = useShellable(s);

  /** Page-handled commands: true when done here. */
  const own = (words: string) => {
    const w = words.trim().toLowerCase();
    if (w === "/model" || w === "/effort") return (type(""), openModel(), true);
    if (w === "/mode") return (type(""), openMode(), true);
    if (w === "/usage") return (type(""), openUsage(), true);
    if (w === "/stop") return (type(""), void stopTurn(s.id), true);
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
  const go = async (other = false) => {
    const mode = keyMode(turn, text, atts.length, queues, steers, other);
    if (busy || uploading || empty) return;
    if (atts.some((a) => a.error)) return toast(t("err.upload"), { error: true });
    // $ and a command line: run at the session's machine, not sent to the agent (shell.tsx).
    const line = shellable && !multi && !atts.length ? shellLine(text) : null;
    if (line) {
      type("");
      setBusy(true);
      return void runShell(s, line).finally(() => setBusy(false));
    }
    const typed = text.trim().replace(/^[!！]\s*/, "");
    if (!multi && !atts.length && own(typed)) return;
    // The words as they are, the links in them what the agent reads.
    const words = typed;
    // A picture's copy here stays: the bubble shows it while sending, and once landed till the
    // server's thumbnail has come (chat/files.tsx previews).
    for (const a of atts) if (a.url && a.path) previews.set(a.path, a.url);
    const files = atts.map((a) => ({ ref: a.ref!, name: a.file.name, type: a.file.type, size: a.file.size, url: a.url }));
    // Its id chosen here: the transcript's block is found by it as it comes, whenever the answer does.
    const sent: Sent = { key: crypto.randomUUID(), text: words, files, from: 0, input: crypto.randomUUID(), steer: mode === "steer", ...(quote ? { quote: quote.n } : {}) };
    type("");
    setAtts(() => []);
    unquote?.();
    // While a turn runs the server queues what is sent; steering joins the turn. Queued, the queue
    // shows it, not a bubble; turned away, the words go back where they were typed.
    if (mode === "queue" && queues) {
      try {
        await api.send(s.id, words, files.map((f) => f.ref), false, sent.input, sent.quote);
      } catch (e) {
        type([words, textNow.current].filter(Boolean).join("\n"));
        toast(t("err.queue"), { error: true, description: errText(e) });
      }
      return;
    }
    onSent(sent);
    try {
      const r = await api.send(s.id, words, files.map((f) => f.ref), mode === "steer", sent.input, sent.quote);
      sent.input = r.input || sent.input;
      // A turn began meanwhile: it waits in the queue, which shows it. A command the agent runs
      // itself (Codex's /compact) never comes in the transcript: the turn's lamp says it.
      sent.queued = r.queued || r.command;
      sent.seq = r.seq;
    } catch (e) {
      sent.failed = errText(e);
    }
    onSentDone(sent);
  };

  const KEYS: Record<KeyMode, [string, React.ReactNode]> = {
    send: [t("composer.send"), <ArrowUp size={16} strokeWidth={2.25} />],
    queue: [t("composer.queue", { key: steerKey() }), <ArrowUp size={16} strokeWidth={2.25} />], // one send key for all: what it does in its tip (the user, 2026-11-06)
    steer: [steers && queues && !/^[!！]/.test(text.trim()) ? t("composer.steerOr", { key: steerKey() }) : t("composer.steer"), <ArrowUp size={16} strokeWidth={2.25} />],
  };
  const [label, icon] = KEYS[mode];

  // The palette beside the composer, not in it: a frosted layer inside another sees only that one.
  return (
    <div className="relative">
      {list.length > 0 && <Palette list={list} at={Math.min(at ?? 0, list.length - 1)} agent={s.agent} grouped={text === "/"} point={setAt} pick={(c) => (own(`/${c.name}`) ? undefined : (type(`/${c.name} `), box.current?.focus()))} />}
      {atOpen && <AtList sections={sections} at={sel} point={setAt} pick={name} />}
      {nickOpts.length > 0 && <NickList rows={nickOpts} at={nickSel} point={setAt} pick={call} />}
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
      {/* The message answered: its row opens and shuts as the attachments' does, no × (docs/
          前端设计规范.md): Esc, a Backspace in an empty box, or a press on it put it away. */}
      <div aria-hidden={!quote || undefined} className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-3) ease-(--ease-out)", quote ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
        <div className="min-h-0 overflow-hidden">
          <button type="button" tabIndex={quote ? 0 : -1} aria-label={t("msg.unquote")} onClick={() => unquote?.()} className="flex w-full min-w-0 items-center gap-2 px-3.5 pt-2.5 text-left text-xs text-ink-3 hover:text-ink-2">
            <Reply size={13} className="shrink-0" />
            <span className="shrink-0 font-medium text-ink-2">{shownQuote?.who}</span>
            <span className="min-w-0 flex-1 truncate">{shownQuote?.text}</span>
          </button>
        </div>
      </div>
      <div aria-hidden={!row || undefined} className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-3) ease-(--ease-out)", row ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
        <div className="min-h-0 overflow-hidden">
        <ul data-attachments className="m-0 flex list-none gap-2 overflow-x-auto px-3.5 pt-3 pb-0">
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
                    <span className="flex h-14 w-44 items-center gap-2 bg-fill px-2.5">
                      {CODE.test(a.file.name) ? <FileCode2 size={16} className="shrink-0 text-ink-3" /> : <FileText size={16} className="shrink-0 text-ink-3" />}
                      <span className="min-w-0">
                        <span className="block truncate text-sm">{a.file.name}</span>
                        <span className="block truncate text-xs text-ink-3 tabular-nums">{size(a.file.size)}</span>
                      </span>
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
      {/* One row (kumo, 2026-11-16: no keys under it any more, the box smaller): attach, the
          words, one line high and growing to their height, send at the far right; the keys at the
          words' last line. */}
      <div className="flex min-w-0 items-end gap-0.5 p-2">
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
        {!noFiles && (
          <IconKey label={t("composer.attach")} onClick={() => picker.current?.click()}>
            <Paperclip size={17} />
          </IconKey>
        )}
        {/* What is named lies on a soft mark, its logo or folder in the slot before its name: the
            box's own letters clear over it. */}
        <div className="min-w-0 flex-1 py-[1.5px] pointer-coarse:py-[5.5px]">
        <div className="relative">
        {marked && <MentionMarks text={text} box={box} className={BOX} />}
        <textarea
          ref={box}
          rows={1}
          value={shownBox.value}
          data-draft="1"
          placeholder={t("composer.placeholder")}
          aria-label={t("composer.placeholder")}
          onChange={(e) => {
            const el = e.target;
            const r = edited(text, el.value, el.selectionStart);
            const want = boxed(r.words).value;
            type(r.words);
            setCaret(wordsAt(boxed(r.words), r.caret));
            // A link taken whole, or letters typed inside one: the box then differs from what it
            // was given, its caret put where the words say. Never while an input method composes
            // (its letters would be split), nor once the box has moved on (a later key's caret).
            if (want !== el.value && !composing && !(e.nativeEvent as InputEvent).isComposing) requestAnimationFrame(() => {
              const b = box.current;
              if (b && b.value === want) b.setSelectionRange(r.caret, r.caret);
            });
          }}
          onSelect={(e) => {
            const el = e.currentTarget;
            // Not inside a name: the caret to its nearer edge.
            if (el.selectionStart === el.selectionEnd && !composing && el.value === shownBox.value) {
              const out = outOf(shownBox, el.selectionStart);
              if (out !== el.selectionStart) el.setSelectionRange(out, out);
            }
            setCaret(wordsAt(shownBox, el.selectionStart));
          }}
          onFocus={(e) => setCaret(wordsAt(shownBox, e.currentTarget.selectionStart))}
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
            const now = list.length ? Math.min(at ?? 0, list.length - 1) : nickOpts.length ? nickSel : sel;
            if (move) setAt((now + move + shown) % shown);
            else if (list.length && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) {
              const c = list[now]!;
              if (!own(`/${c.name}`)) type(`/${c.name} `);
            } else if (opts.length && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) name(opts[sel]!);
            else if (nickOpts.length && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey) call(nickOpts[nickSel]!);
            // → goes into the folder pointed at, only while the list is open on one.
            else if (e.key === "ArrowRight" && word && into(opts[sel], e.currentTarget, e.shiftKey || e.ctrlKey || e.metaKey || e.altKey)) enter(opts[sel]!);
            else if ((shown || atOpen) && e.key === "Escape") setShut(text);
            else if (quote && (e.key === "Escape" || (e.key === "Backspace" && !text && e.currentTarget.selectionStart === 0))) unquote?.();
            else if (e.key === "Tab" && e.shiftKey) cycleMode();
            // In a turn, Ctrl or ⌘+Enter does what Enter does not: steer in (Claude Code's send now) or queue.
            else if (e.key === "Enter" && !e.shiftKey && (e.ctrlKey || e.metaKey) && turn) void (empty || go(true));
            // Enter sends on a computer (Ctrl or ⌘ with it on a phone); in an empty box it does nothing.
            // Esc stops the session's own agent's turn, as its stop key does.
            else if (e.key === "Enter" && !e.shiftKey && (fine() || e.ctrlKey || e.metaKey)) void (empty || go());
            else if (e.key === "Escape" && turn) void stopTurn(s.id);
            else return;
            e.preventDefault();
          }}
          className={cn("no-ring relative block max-h-[220px] min-h-[29px] w-full resize-none bg-transparent placeholder:text-ink-3", BOX, marked && "text-transparent [caret-color:var(--ink)]")}
        />
        </div>
        </div>
        {/* /mode and /model's menus, unseen, where their keys were (chat.tsx Under). */}
        <div className="flex w-0 shrink-0">
          {tools}
        </div>
        <Tip label={label}>
          <button
            type="button"
            aria-label={label}
            disabled={empty || busy || uploading}
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

/** What an @ may name, as the commands are (the user, 2026-11-10): a card at the box's corner, grown
 *  out of the @, a section each under its head: 会话, 本机, each machine (its face, its name, its
 *  lamp; away, faint, none to pick); a typed path, its machine and folder. A session's row: its
 *  logo, its title, how long ago; a place's: its folder (a cloud on it at another machine), its
 *  name, its path faint at the end. ↑↓, → into a folder, Enter or Tab on a keyboard, a tap on a
 *  phone. */
function AtList({ sections, at, pick, point }: { sections: AtSection[]; at: number; pick: (r: AtRow) => void; point: (i: number) => void }) {
  const now = useMinute();
  let i = -1;
  let n = 0;
  return (
    <div role="listbox" aria-label={t("composer.atList")} className="g-pop glass thick float absolute bottom-full left-2 z-10 mb-2 max-h-[min(360px,50dvh)] w-[400px] max-w-[calc(100%-16px)] origin-bottom-left no-bar overflow-y-auto overscroll-contain rounded-lg p-1.5 text-ink">
      <div className="g-stagger flex flex-col">
        {sections.map((sec) => (
          <Fragment key={sec.key}>
            <AtHeadRow head={sec.head} off={sec.off} />
            {sec.rows.map((r) => {
              const idx = r.off ? -1 : ++i;
              const on = idx === at;
              return (
                <button
                  key={r.key}
                  type="button"
                  role="option"
                  aria-selected={on}
                  aria-disabled={r.off || undefined}
                  disabled={r.off}
                  ref={(el) => void (on && el?.scrollIntoView({ block: "nearest" }))}
                  onPointerDown={(e) => e.preventDefault()}
                  onPointerMove={() => r.off || on || point(idx)}
                  onClick={() => r.off || pick(r)}
                  style={{ "--i": n++ } as CSSProperties}
                  className={cn("flex h-8 w-full shrink-0 cursor-default items-center gap-2.5 rounded-md px-2.5 text-left text-base outline-none select-none pointer-coarse:h-11", on && "bg-fill", r.off && "opacity-50")}
                >
                  {r.kind === "session" ? (
                    <>
                      <Logo agent={r.s.agent} size={14} className="shrink-0" />
                      <span className="min-w-0 flex-1 truncate">{titleOf(r.s)}</span>
                      <span className="shrink-0 text-xs text-ink-3 tabular-nums">{ago(r.s.updatedAt, now, true)}</span>
                    </>
                  ) : (
                    <>
                      <PlaceIcon remote={!!r.p.machine} size={15} />
                      <span className={cn("min-w-0 truncate", r.hint ? "shrink" : "flex-1")}>{r.p.name}</span>
                      {r.hint && (
                        <span className="ml-auto max-w-[55%] min-w-0 shrink-0 truncate text-xs text-ink-3 [direction:rtl]">
                          <bdi>{r.hint}</bdi>
                        </span>
                      )}
                    </>
                  )}
                </button>
              );
            })}
          </Fragment>
        ))}
      </div>
    </div>
  );
}

/** A section's head in the @ list. A machine's is a component of its own: its lamp is a hook's. */
function AtHeadRow({ head, off }: { head: AtHead; off?: boolean }) {
  const cls = cn("flex min-w-0 shrink-0 items-center gap-1.5 px-2.5 pt-2 pb-1 text-xs text-ink-3 select-none first:pt-1", off && "opacity-50");
  if (head.kind === "sessions") return <div className={cls}>{t("composer.atSessions")}</div>;
  if (head.kind === "local") return <div className={cls}>{t("new.local")}</div>;
  if (head.kind === "machine")
    return (
      <div className={cls}>
        <MachineFace m={head.m} size={14} />
        <span className="min-w-0 truncate">{head.m.name}</span>
        <MachineLink m={head.m} />
      </div>
    );
  return (
    <div className={cls}>
      {head.m && <MachineFace m={head.m} size={14} />}
      <span className="shrink-0">{head.m ? head.m.name : t("new.local")}</span>
      <span className="min-w-0 truncate [direction:rtl]">
        <bdi>{head.m ? head.path : tilde(head.path)}</bdi>
      </span>
    </div>
  );
}

/** The common commands' icons; an agent's own have none (no slash: the user, 2026-11-09). */
const CMD_ICONS: Record<(typeof COMMON)[number], LucideIcon> = { clear: Eraser, compact: FoldVertical, context: ChartPie, usage: Gauge, model: Box, effort: Brain, mode: ShieldCheck };
const isCommon = (c: Command) => COMMON.includes(c.name as (typeof COMMON)[number]);

/** The commands as the page's menus are (the model's, a right press's), not a bar as wide as the
 *  composer over it (the user, 2026-11-09): a card at the box's corner, grown out of the /; a row
 *  each, its icon, its name, what it does faint at its end; while nothing is typed after the /, the
 *  common ones, then the agent's own, each under its head. ↑↓ and Enter or Tab on a keyboard. */
function Palette({ list, at, agent, grouped, pick, point }: { list: Command[]; at: number; agent: Agent; grouped: boolean; pick: (c: Command) => void; point: (i: number) => void }) {
  return (
    <div role="listbox" aria-label={t("composer.commands")} className="g-pop glass thick float absolute bottom-full left-2 z-10 mb-2 max-h-[min(360px,50dvh)] w-80 max-w-[calc(100%-16px)] origin-bottom-left no-bar overflow-y-auto overscroll-contain rounded-lg p-1.5 text-ink">
      <div className="g-stagger flex flex-col">
        {list.map((c, i) => {
          const Icon = isCommon(c) ? CMD_ICONS[c.name as (typeof COMMON)[number]] : undefined;
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
                {Icon && <Icon size={15} className="shrink-0 text-ink-2" aria-hidden />}
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
