// The confirmation panel (plan "确认面板"): one card per open ask, above the composer, and the
// confirmations agents' ir asked a panel for from this session (invites, tokens). A title, one line
// of summary, a main and a second key; the details open under it. An approval: the tool and its
// arguments (commands in monospace), allow / deny / always allow this session. A plan: its
// Markdown, approve or keep planning. A question: its options as keys, or your own words. Every
// answer goes to the ask's card (POST /v1/internal/sessions/{id}/answer), as an app's does.
import { Menu } from "@base-ui/react/menu";
import { Check, ChevronDown, ClipboardList, FilePen, Globe, LoaderCircle, MessageCircleQuestionMark, PenLine, ShieldAlert, ShieldQuestion, SquareTerminal } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Ask, CardAnswer, Need, Denial } from "../api/types.gen";
import { CardDetails, CardFrame, CardHead, Facts, MainKey, SecondKey } from "../card";
import { ConfirmCard } from "../confirms";
import { SecretHere } from "../secret";
import { useSecret } from "../secrets";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { cn, tilde } from "../ui";
import { argOf, inputOf, kindOf } from "./fold";
import { Markdown } from "./markdown";

type Q = { question: string; header?: string; options: { label: string; description?: string }[]; multiSelect?: boolean };

/** What a key press answers: an option of the card's, or a choice per question and own words. */
type Reply = { option?: string; choices?: string[]; words?: string[]; message?: string };

export function Asks({ session, asks, cwd, confirms = [] }: { session: string; asks: Ask[]; cwd: string; confirms?: Need[] }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [leaving, setLeaving] = useState<string | null>(null);
  const answer = async (a: Ask, r: Reply) => {
    setBusy(a.requestId);
    try {
      await api.answer(session, { card: a.card?.id ?? `ask:${session}:${a.requestId}`, ...r } satisfies CardAnswer);
      setLeaving(a.requestId);
      setTimeout(() => setLeaving(null), 140);
      if (a.kind === "permission") toast(r.option === "deny" ? t("toast.denied") : t("toast.allowed"), { description: r.option === "always" ? t("ask.always") : undefined });
    } catch (e) {
      toast(t("err.answer"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const secretHere = useSecret()?.at === session;
  if (!asks.length && !confirms.length && !secretHere) return null;
  return (
    // Padded out under negative margins: the cards' shadows (12px down, 32px blur) are not cut, nor
    // over the meters row under it. Whole, however long (the user, 2026-11-06): it scrolls only
    // where it would go past the top bar.
    <div className="-mx-8 -mt-3 mb-[-36px] flex max-h-[calc(100dvh-10rem)] flex-col gap-2 overflow-y-auto px-8 pt-3 pb-11">
      <SecretHere at={session} />
      {confirms.map((c) => (
        <ConfirmCard key={c.id} c={c} at={session} />
      ))}
      {asks.map((a) => (
        <CardFrame key={a.requestId} leaving={leaving === a.requestId} label={titleOf(a)}>
          {a.kind === "question" ? <Question a={a} busy={busy === a.requestId} send={(r) => void answer(a, r)} /> : <Decide a={a} cwd={cwd} busy={busy === a.requestId} send={(r) => void answer(a, r)} />}
        </CardFrame>
      ))}
    </div>
  );
}

/** The steps Claude's auto mode turned down (session/release.go), at the conversation's end once the
 *  turn is over: one line as a compaction's, "auto 拦了 2 步", folded, for Claude may have gone
 *  another way; open, a card each. stuck: the turn stopped on one, the session waits for you: open
 *  (the user, 2026-11-08). They go with your next message. */
export function Denials({ session, denials, cwd, stuck }: { session: string; denials: Denial[]; cwd: string; stuck: boolean }) {
  const [open, setOpen] = useState(stuck);
  const [was, setWas] = useState(stuck);
  if (stuck !== was) {
    setWas(stuck);
    if (stuck) setOpen(true);
  }
  return (
    <div className="-mt-3">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="g-fade flex h-7 w-full cursor-pointer items-center gap-3 text-xs text-ink-3 hover:text-ink-2 pointer-coarse:h-9">
        <span aria-hidden className="h-px flex-1 bg-edge" />
        <span className={cn("flex items-center gap-1", stuck && "text-need")}>
          {t("denied.count", { n: denials.length })}
          <ChevronDown size={12} className={cn("text-ink-4 transition-transform", open && "rotate-180")} />
        </span>
        <span aria-hidden className="h-px flex-1 bg-edge" />
      </button>
      {open && (
        <div className="mt-1 flex flex-col gap-2">
          {denials.map((d) => (
            <DenialCard key={d.toolUseId} d={d} session={session} cwd={cwd} />
          ))}
        </div>
      )}
    </div>
  );
}

/** A step auto mode turned down: switch to manual for it — nothing said to Claude; run again, it
 *  is asked, and the mode is back after it or at the turn's end — or let it be (the user,
 *  2026-11-08). */
function DenialCard({ d, session, cwd }: { d: Denial; session: string; cwd: string }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"release" | "dismiss" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const input = JSON.stringify(d.input ?? {});
  const { arg } = argOf({ name: d.tool, input }, cwd);
  const run = async (dismiss: boolean) => {
    setBusy(dismiss ? "dismiss" : "release");
    try {
      await api.release(session, d.toolUseId, dismiss);
      setLeaving(true);
      if (!dismiss) toast(t("denied.released"), { description: t("denied.releasedHint") });
    } catch (e) {
      setBusy(null);
      toast(t("err.action"), { error: true, description: errText(e) });
    }
  };
  return (
    <CardFrame leaving={leaving} label={t("denied.title")}>
      <CardHead
        icon={<ShieldAlert size={16} className="text-need" />}
        title={t("denied.title")}
        tag={d.reason}
        summary={arg || d.tool}
        mono
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void run(true)}>
              {t("denied.dismiss")}
            </SecondKey>
            <MainKey busy={busy === "release"} disabled={!!busy} onClick={() => void run(false)}>
              {t("denied.release")}
            </MainKey>
          </>
        }
      />
      {open && (
        <CardDetails>
          <Facts
            rows={[
              [t("ask.cwd"), <span className="font-mono text-xs">{tilde(cwd)}</span>],
              ...(d.reason ? [[t("denied.reason"), d.reason] as [string, ReactNode]] : []),
              [t("ask.input"), <span className="font-mono text-xs whitespace-pre-wrap">{JSON.stringify(d.input ?? {}, null, 2)}</span>],
            ]}
          />
        </CardDetails>
      )}
    </CardFrame>
  );
}

const ICON: Record<string, ReactNode> = {
  command: <SquareTerminal size={16} />,
  edit: <FilePen size={16} />,
  web: <Globe size={16} />,
};

function titleOf(a: Ask): string {
  if (a.kind === "plan") return t("ask.plan");
  if (a.kind === "question") return t("ask.question");
  const k = kindOf(a.tool);
  return k === "command" ? t("ask.runCommand") : k === "edit" ? t("ask.edit") : k === "web" ? t("ask.web") : k === "read" || k === "search" ? t("ask.read") : t("ask.useTool");
}

/** An approval or a plan: allow (with "always" for approvals) or deny, a reason in the details. */
function Decide({ a, cwd, busy, send }: { a: Ask; cwd: string; busy: boolean; send: (r: Reply) => void }) {
  const [open, setOpen] = useState(false);
  const [why, setWhy] = useState("");
  const input = JSON.stringify(a.input ?? {});
  const plan = a.kind === "plan" ? String(inputOf(input).plan ?? "") : "";
  const { arg } = plan ? { arg: plan.split("\n").find((l) => l.trim())?.replace(/^#+\s*/, "") ?? "" } : argOf({ name: a.tool, input }, cwd);
  const icon = a.kind === "plan" ? <ClipboardList size={16} /> : (ICON[kindOf(a.tool)] ?? <ShieldQuestion size={16} />);
  const details = a.kind === "plan" ? null : JSON.stringify(a.input ?? {}, null, 2);
  const isPlan = a.kind === "plan";
  return (
    <>
      <CardHead
        icon={icon}
        title={titleOf(a)}
        tag={a.kind === "permission" ? a.tool : undefined}
        summary={arg || a.description || a.tool}
        mono={a.kind === "permission"}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={busy} onClick={() => send({ option: isPlan ? "reject" : "deny", message: why.trim() || undefined })}>
              {isPlan ? t("ask.keepPlanning") : t("ask.deny")}
            </SecondKey>
            <div className="flex">
              <button
                type="button"
                disabled={busy}
                onClick={() => send({ option: isPlan ? "approve" : "allow" })}
                className={cn("g-press flex h-8 items-center gap-1.5 bg-ink pr-2.5 pl-3 text-sm font-medium text-on-ink disabled:opacity-60 pointer-coarse:h-10", a.kind === "permission" ? "rounded-l-md" : "rounded-md pr-3")}
              >
                {busy && <LoaderCircle size={13} className="animate-spin" />}
                {isPlan ? t("ask.approve") : t("ask.allow")}
              </button>
              {a.kind === "permission" && (
                <Menu.Root>
                  <Menu.Trigger
                    disabled={busy}
                    aria-label={t("ask.always")}
                    className="grid h-8 w-7 place-items-center rounded-r-md bg-ink text-on-ink/80 shadow-[inset_1px_0_0_color-mix(in_oklch,var(--on-ink)_22%,transparent)] hover:text-on-ink pointer-coarse:h-10 pointer-coarse:w-9"
                  >
                    <ChevronDown size={14} />
                  </Menu.Trigger>
                  <Menu.Portal>
                    <Menu.Positioner side="top" align="end" sideOffset={6} className="z-50">
                      <Menu.Popup className="g-unfold glass thick float rounded-lg p-1.5 text-ink outline-none">
                        <Menu.Item onClick={() => send({ option: "always" })} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none data-highlighted:bg-fill pointer-coarse:h-11">
                          {t("ask.always")}
                          {a.pattern && <span className="font-mono text-xs text-ink-3">{a.pattern}</span>}
                        </Menu.Item>
                      </Menu.Popup>
                    </Menu.Positioner>
                  </Menu.Portal>
                </Menu.Root>
              )}
            </div>
          </>
        }
      />
      {open && (
        <CardDetails>
          {plan ? (
            <div className="rounded-md bg-fill/60 px-3 py-2">
              <Markdown text={plan} className="text-base" />
            </div>
          ) : (
            <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
              <dt className="text-ink-3">{t("ask.cwd")}</dt>
              <dd className="m-0 font-mono text-xs leading-5 break-all text-ink-2">{tilde(cwd)}</dd>
              {a.description && (
                <>
                  <dt className="text-ink-3">{t("ask.why")}</dt>
                  <dd className="m-0 text-ink-2">{a.description}</dd>
                </>
              )}
              <dt className="text-ink-3">{t("ask.input")}</dt>
              <dd className="m-0 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-ink-2">{details}</dd>
            </dl>
          )}
          <input value={why} onChange={(e) => setWhy(e.target.value)} placeholder={t("ask.reason")} aria-label={t("ask.reason")} disabled={busy} className="no-ring h-8 rounded-md bg-fill px-2.5 text-sm placeholder:text-ink-3" />
        </CardDetails>
      )}
    </>
  );
}

/** An option's mark, a solid disc (a rounded square for a multiple choice): grey with its number,
 *  ink with a check once picked (the user, 2026-11-06: solid, not a small ring). */
function Mark({ n, on, square }: { n: number; on: boolean; square: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "grid size-5 shrink-0 place-items-center text-[11px] font-semibold tabular-nums transition-[background-color,color,transform] duration-(--dur-2) ease-(--ease-out)",
        square ? "rounded-[6px]" : "rounded-full",
        on ? "scale-105 bg-ink text-on-ink" : "bg-ink/10 text-ink-2 group-hover:text-ink-2",
      )}
    >
      {on ? <Check size={12} strokeWidth={3} /> : n}
    </span>
  );
}

/** Questions: each its options as keys, picked, then sent: no answer on a pick alone (a tap landed
 *  on one as the card came in and answered it, the user 2026-11-08). Your own words answer too. The card's option ids are
 *  "<question>.<option>", by place. */
function Question({ a, busy, send }: { a: Ask; busy: boolean; send: (r: Reply) => void }) {
  const qs = ((a.input as { questions?: Q[] } | null)?.questions ?? []).filter((q) => q?.question);
  const [picked, setPicked] = useState<number[][]>(() => qs.map(() => []));
  const [own, setOwn] = useState<string[]>(() => qs.map(() => ""));
  const reply = (p = picked, w = own): Reply => ({
    choices: p.flatMap((js, i) => (w[i]!.trim() ? [] : js.map((j) => `${i}.${j}`))),
    words: w.some((x) => x.trim()) ? w.map((x) => x.trim()) : undefined,
  });
  const ready = qs.every((_, i) => own[i]!.trim() || picked[i]!.length);
  const pick = (i: number, j: number) => {
    setPicked((p) => p.map((was, k) => (k !== i ? was : was.includes(j) ? was.filter((x) => x !== j) : qs[i]!.multiSelect ? [...was, j] : [j])));
  };
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (ready && !busy) send(reply());
      }}
      className="flex flex-col gap-4"
    >
      {qs.map((q, i) => {
        const last = i === qs.length - 1;
        const typed = !!own[i]!.trim();
        return (
          <fieldset key={i} disabled={busy} className="m-0 flex min-w-0 flex-col gap-2.5 border-0 p-0">
            <legend className="mb-2.5 flex w-full items-start gap-2.5 p-0">
              <MessageCircleQuestionMark size={16} className="mt-0.5 shrink-0 text-ink-2" />
              {/* The header a faint word before the question, on its line. */}
              <span className="min-w-0 text-sm leading-[21px] whitespace-pre-wrap [overflow-wrap:anywhere]">
                {(q.header || q.multiSelect) && (
                  <span className="mr-2 text-ink-3">
                    {q.header}
                    {q.multiSelect && `${q.header ? " · " : ""}${t("ask.multi")}`}
                  </span>
                )}
                {q.question}
              </span>
            </legend>
            {/* The options, plain rows lined up with the question, a soft ground under the one you
                point at or picked; your own words the last row. */}
            <div className="ml-4 flex flex-col gap-px">
              {q.options.map((o, j) => {
                const on = picked[i]!.includes(j) && !typed;
                return (
                  <button
                    key={j}
                    type="button"
                    aria-pressed={on}
                    onClick={() => pick(i, j)}
                    className={cn(
                      "group flex w-full items-center gap-2.5 rounded-md border-0 px-2.5 py-1.5 text-left text-sm transition-colors duration-(--dur-1) pointer-coarse:py-2.5",
                      on ? "bg-fill" : "hover:bg-fill/70",
                    )}
                  >
                    <Mark n={j + 1} on={on} square={!!q.multiSelect} />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span>{o.label}</span>
                      {o.description && <span className="text-xs leading-[18px] text-ink-3">{o.description}</span>}
                    </span>
                  </button>
                );
              })}
              <label className="flex items-center gap-2.5 rounded-md py-0.5 pr-0.5 pl-2.5 focus-within:bg-fill/70">
                <PenLine size={13} className="shrink-0 text-ink-4" />
                <input
                  value={own[i]}
                  onChange={(e) => setOwn((w) => w.map((x, j) => (j === i ? e.target.value : x)))}
                  placeholder={t("ask.own")}
                  aria-label={t("ask.own")}
                  className="no-ring h-8 min-w-0 flex-1 bg-transparent text-sm placeholder:text-ink-3"
                />
                {last && (
                  <button type="submit" disabled={busy || !ready} className="g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md bg-ink px-2.5 text-sm text-on-ink disabled:opacity-25 pointer-coarse:h-9">
                    {busy && <LoaderCircle size={13} className="animate-spin" />}
                    {t("ask.send")}
                  </button>
                )}
              </label>
            </div>
          </fieldset>
        );
      })}
    </form>
  );
}
