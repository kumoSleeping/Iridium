// A harness's own subagent in its parent's chat, read as a conversation (kumo, 2026-11-16:
// 劳务派遣, 也算个人的): the parent's dispatch and its later words to it are the parent's words
// addressed to it, a line that opens to the whole of them; what it hands back is one message
// of its own, its face and name over it, folded when long. Which call, which notice goes with
// which subagent: dispatch.ts. Its face and card: subwork.tsx; its turn at work: chat.tsx SubTurn.
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, ChevronRight, CircleX, LoaderCircle } from "lucide-react";
import { useContext, useState } from "react";
import { api } from "../api/client";
import type { Block, Session } from "../api/types.gen";
import { t } from "../i18n";
import { cn, tokens as tokensText } from "../ui";
import { linesOf } from "./channel/merge";
import { Copyable } from "./copyable";
import { backAt, firstWords, handBack, launchOf, messageOf, reportsOf, toldOf, tookText, type Start, type Told } from "./dispatch";
import type { Tool } from "./fold";
import { LongWords } from "./longwords";
import { Markdown } from "./markdown";
import { useWhenRow, When } from "./said";
import { Dispatching, SubFace, SubFaceKey, SubHead, useSub } from "./subwork";

/** Text the page may have only the start of: more, where its whole is and how to read it. */
type More = { session: string; at: string; length: number; read: (whole: string) => string };

/** The whole of some words, once asked for (the key under them). */
function useWhole(text: string, more?: More) {
  const [whole, setWhole] = useState<string>();
  const [state, setState] = useState<"idle" | "busy" | "failed">("idle");
  const key = more && whole === undefined && (
    <span className="flex items-center gap-2">
      <button
        type="button"
        disabled={state === "busy"}
        onClick={() => {
          setState("busy");
          api.block(more.session, more.at).then(
            (d) => (setWhole(more.read(d.text)), setState("idle")),
            () => setState("failed"),
          );
        }}
        className="g-press rounded-sm text-xs text-ink-3 underline decoration-ink-4 underline-offset-2 hover:text-ink"
      >
        {state === "busy" ? <LoaderCircle size={12} className="inline animate-spin" /> : t("msg.whole", { n: more.length })}
      </button>
      {state === "failed" && <span className="text-xs text-err">{t("err.whole")}</span>}
    </span>
  );
  return { text: whole ?? text, key };
}

const LINE = "flex h-7 w-full min-w-0 cursor-default items-center gap-2 rounded-sm text-left text-sm text-ink-3 outline-none pointer-coarse:h-9";

/** The parent's words to a subagent, a call's: "→ its face and name: their first line";
 *  pressed, the whole of them under the line (the height glides). live: the call is being
 *  written or runs: the line alone, a light sweeping across its name. */
export function Sent({ call, session, live }: { call: Pick<Tool, "name" | "input"> & Partial<Pick<Tool, "at" | "id" | "length" | "result">>; session?: string; live?: boolean }) {
  const told = toldOf(call);
  const { parent, d } = useContext(Dispatching);
  const of = session ?? parent;
  const start = call.at ? d?.of.get(call.at) : undefined;
  // One addressed by an id the page cannot name is "子代理".
  const to = told?.kind === "send" ? (start?.name ?? (/^[0-9a-f-]{12,}$/i.test(told.to) ? "" : told.to)) : (told?.name ?? "");
  const sub = useSub(of, { id: told?.kind === "start" ? call.id : start?.id, name: to, model: start?.model ?? (told?.kind === "start" ? told.model : undefined) });
  const [open, setOpen] = useState(false);
  const cut = !!call.length && !!call.at && !!of;
  const said = useWhole(told?.words ?? "", cut ? { session: of, at: call.at!, length: call.length!, read: (whole) => toldOf({ name: call.name, input: whole })?.words ?? "" } : undefined);
  const failed = !!call.result?.error;
  const more = !live && (!!said.text || failed);
  const line = (
    <>
      <ArrowRight size={14} aria-hidden className="shrink-0" />
      <span className={cn("flex max-w-[55%] min-w-0 shrink-0 items-center gap-1.5 text-ink-2", live && "shine")}>
        <SubFace agent={sub.agent} size={16} />
        <span className="min-w-0 truncate">{sub.name}</span>
      </span>
      <span className="min-w-0 flex-1 truncate text-xs">{firstWords(said.text)}</span>
      {failed && <CircleX size={13} className="shrink-0 text-err" aria-label={t("msg.failed")} />}
      {more && <ChevronRight size={13} aria-hidden className={cn("shrink-0 text-ink-4 transition-transform duration-(--dur-2)", open && "rotate-90")} />}
    </>
  );
  if (!more) return <div className={cn(LINE, live && "g-fade")}>{line}</div>;
  return (
    <div className="min-w-0">
      <button type="button" aria-expanded={open} aria-label={t("sub.to", { name: sub.name })} onClick={() => setOpen(!open)} className={cn(LINE, "hover:text-ink-2 focus-visible:text-ink-2", open && "text-ink-2")}>
        {line}
      </button>
      <div className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-3) ease-(--ease-out)", open ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
        <div className="min-h-0 overflow-hidden">
          <div className="mt-0.5 mb-1 ml-[7px] grid min-w-0 gap-1.5 pl-3 shadow-[inset_1px_0_0_var(--edge)]">
            {said.text && <Markdown text={said.text} className="w-full text-sm text-ink-2" />}
            {said.key}
            {failed && <span className="text-xs whitespace-pre-wrap text-err [overflow-wrap:anywhere]">{call.result?.text}</span>}
          </div>
        </div>
      </div>
    </div>
  );
}

/** What subagent sub handed back, as its own record's last page has it: of total on its parent's
 *  page, the one fromLast from the last. Asked once per hand-back. */
function useReport(sub: Session | undefined, total: number, fromLast: number, off: boolean) {
  const on = !!sub && !off;
  const q = useQuery({ queryKey: ["handback", sub?.id, total], queryFn: () => api.messages(sub!.id).then((p) => reportsOf(p.blocks)), enabled: on, staleTime: Infinity, retry: false });
  return { report: q.data?.at(-1 - fromLast), pending: on && q.isPending };
}

/** What a subagent handed back, one message of its own: its face (its card) and its name, how
 *  long it took and what it used in small grey after them; its words folded when long. text:
 *  the words, when the parent's record has them (more: cut short there); else they are read
 *  from its own record, a place kept for them till they come. start: its dispatch, when it is
 *  on the page; at: the block that says it handed back. */
export function HandBack({ at, start, name, session, took, used, time, failed, text, more }: { at: string; start?: Start; name: string; session?: string; took?: string; used?: number; time?: number; failed?: boolean; text?: string; more?: More }) {
  const { parent } = useContext(Dispatching);
  const sub = useSub(session ?? parent, { id: start?.id, name: start?.name ?? name, model: start?.model });
  const n = start ? backAt(start, at) : { total: 1, fromLast: 0 };
  const read = useReport(sub.session, n.total, n.fromLast, !!text);
  const r = read.report;
  const said = useWhole(text || r?.text || "", text ? more : r?.length && sub.session ? { session: sub.session.id, at: r.at, length: r.length, read: (whole) => (r.tool ? messageOf(whole) : whole) } : undefined);
  const row = useWhenRow();
  return (
    <div {...row} className={cn(row.className, "flex min-w-0 items-start gap-2.5")}>
      <SubFaceKey sub={sub} />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <SubHead name={sub.name}>
          {took && <span className="shrink-0 text-xs text-ink-4 tabular-nums">{took}</span>}
          {!!used && <span className="shrink-0 text-xs text-ink-4 tabular-nums">{t("usage.tokens", { n: tokensText(used) })}</span>}
          {failed && <CircleX size={12} className="shrink-0 text-err" aria-label={t("msg.failed")} />}
          <When time={time} />
        </SubHead>
        {said.text ? (
          <Copyable copy={said.text}>
            <LongWords fold lines={linesOf(said.text)}>
              <Markdown text={said.text} className="w-full" />
            </LongWords>
            {said.key}
          </Copyable>
        ) : (
          // Its words come after the head: their folded height kept, so they push nothing.
          read.pending && <div aria-hidden className="h-[69px]" />
        )}
      </div>
    </div>
  );
}

/** A task's notice that is a subagent's hand-back (Said draws any other as a line). */
export function TaskBack({ b, session }: { b: Block; session?: string }) {
  const { d } = useContext(Dispatching);
  const h = handBack(b.text)!;
  return <HandBack at={b.at} start={d?.of.get(b.at)} name={h.name} session={session} took={h.took} time={b.time} failed={h.failed} />;
}

/** A call to a subagent, done: the parent's words to it; under a dispatch that came back with
 *  the call (Claude's Agent not in the background; Codex's, once its thread is done), what it
 *  handed back. */
export function Dispatch({ call, session }: { call: Tool; session: string }) {
  const told = toldOf(call);
  return told?.kind === "start" ? <Started call={call} told={told} session={session} /> : <Sent call={call} session={session} />;
}

function Started({ call, told, session }: { call: Tool; told: Told & { kind: "start" }; session: string }) {
  const { d } = useContext(Dispatching);
  const start = d?.of.get(call.at);
  const sub = useSub(session, { id: call.id, name: told.name });
  const r = call.result && !call.result.error ? call.result : undefined;
  const l = r && (call.name ?? "").toLowerCase() !== "spawn_agent" ? launchOf(r.text) : undefined;
  const back = l ? !l.background : !!sub.session?.done && !sub.working && !call.result?.error;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <Sent call={call} session={session} />
      {back && (
        <HandBack
          at={call.at}
          start={start}
          name={told.name}
          session={session}
          took={l?.ms ? tookText(l.ms) : undefined}
          used={l?.tokens}
          time={r?.time}
          text={l?.report}
          more={r?.length ? { session, at: r.at, length: r.length, read: (whole) => launchOf(whole).report } : undefined}
        />
      )}
    </div>
  );
}
