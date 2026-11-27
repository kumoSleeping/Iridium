// The confirmation panel (plan "确认面板"): one card per open ask, above the composer, and the
// confirmations agents' ir asked a panel for from this session (invites, tokens). A title, one line
// of summary, a main and a second key; the details open under it. An approval: the tool and its
// arguments (commands in monospace), allow / deny / always allow this session. A plan: its
// Markdown, approve or keep planning. A question: its options as keys, or your own words. Every
// answer goes to the ask's card (POST /v1/internal/button), as an app's does.
import { Menu } from "@base-ui/react/menu";
import { ArrowUp, Check, ChevronDown, ClipboardList, FilePen, Globe, Hand, LoaderCircle, MessageCircleQuestionMark, PenLine, ShieldQuestion, SquareTerminal } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Ask, CardAnswer, Need } from "../api/types.gen";
import { CardDetails, CardFrame, CardHead, SecondKey } from "../card";
import { ConfirmCard } from "../confirms";
import { SecretHere } from "../secret";
import { useSecret } from "../secrets";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { cn, tilde, Tip, MenuPortal } from "../ui";
import { argOf, inputOf, kindOf } from "./fold";
import { Markdown } from "./markdown";

type Q = { question: string; header?: string; options: { label: string; description?: string }[]; multiSelect?: boolean };

/** What a key press answers: an option of the card's, or a choice per question and own words. */
type Reply = { option?: string; choices?: string[]; words?: string[]; message?: string };

/** Answering session's asks: the one being answered (busy), the one going away (leaving). */
function useAnswer(session: string, toasts = true) {
  const [busy, setBusy] = useState<string | null>(null);
  const [leaving, setLeaving] = useState<string | null>(null);
  const answer = async (a: Ask, r: Reply) => {
    setBusy(a.requestId);
    try {
      await api.answer(session, { card: a.card?.id ?? `ask:${session}:${a.requestId}`, ...r } satisfies CardAnswer);
      setLeaving(a.requestId);
      setTimeout(() => setLeaving(null), 140);
      if (toasts && a.kind === "permission") toast(r.option === "deny" ? t("toast.denied") : t("toast.allowed"), { description: r.option === "always" ? t("ask.always") : undefined });
    } catch (e) {
      toast(t("err.answer"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  return { busy, leaving, answer };
}

/** An ask in the conversation, among its agent's words (kumo, 2026-11-27: in it, not over the
 *  composer; the head over its run says whose): open, its card, flat, keys and all; off: shown,
 *  not to be answered. No toast: the line it becomes says it. */
export function AskInline({ session, ask, cwd, off }: { session: string; ask: Ask; cwd: string; off?: boolean }) {
  const { busy, answer } = useAnswer(session, false);
  const b = busy === ask.requestId;
  return (
    <div inert={off} role="alertdialog" aria-label={titleOf(ask)} className={cn("@container rounded-lg border border-edge p-2.5 pl-3", off && "opacity-60")}>
      {ask.kind === "question" ? <Question a={ask} busy={b} send={(r) => void answer(ask, r)} /> : <Decide a={ask} cwd={cwd} busy={b} send={(r) => void answer(ask, r)} />}
    </div>
  );
}

/** What an answer's option says done, by its id and the ask's kind; "" for one that is its own
 *  words (a question's). */
function doneWord(kind: string | undefined, option: string | undefined): string {
  switch (option) {
    case "allow":
      return kind === "manual" ? t("ask.didAgree") : t("ask.didAllow");
    case "deny":
      return kind === "manual" ? t("ask.didRefuse") : t("ask.didDeny");
    case "always":
      return t("ask.didAlways");
    case "approve":
      return t("ask.didApprove");
    case "reject":
      return t("ask.didKeepPlanning");
  }
  return "";
}

/** An ask over, as a line among its agent's words: what it asked (its kind's icon and title, its
 *  command, path or reason), then how it went and who answered it (kumo, 2026-11-27), 已结束 for
 *  one no person answered. asked: what it asked as its message keeps it; text: its message's
 *  words, for one from before that was kept. */
export function AskDone({ asked, answer, text }: { asked?: { kind?: string; tool?: string; summary?: string }; answer?: { name?: string; option?: string; label?: string }; text: string }) {
  const lines = text.split("\n").map((l) => l.trim()).filter(Boolean);
  const a = { requestId: "", kind: asked?.kind ?? "permission", tool: asked?.tool ?? lines[0] ?? "" } as Ask;
  const summary = asked?.summary ?? lines[1] ?? "";
  const icon = a.kind === "plan" ? <ClipboardList size={14} /> : a.kind === "manual" ? <Hand size={14} /> : a.kind === "question" ? <MessageCircleQuestionMark size={14} /> : ICON_SMALL[kindOf(a.tool)] ?? <ShieldQuestion size={14} />;
  const word = answer ? doneWord(a.kind, answer.option) || answer.label || "" : t("ask.over");
  const no = answer?.option === "deny" || answer?.option === "reject";
  return (
    <div className="flex h-7 min-w-0 items-center gap-1.5 text-sm text-ink-3 pointer-coarse:h-9">
      <span className="shrink-0">{icon}</span>
      <span className="shrink-0 text-ink-2">{a.kind === "permission" && a.tool ? a.tool : titleOf(a)}</span>
      {summary && <span className={cn("min-w-0 flex-1 truncate", a.kind === "permission" && "font-mono text-[13px]")}>{summary}</span>}
      {!summary && <span className="flex-1" />}
      <span className={cn("shrink-0 pl-2 text-xs", no ? "text-err" : "text-ink-3")}>{word}</span>
      {answer?.name && <span className="max-w-32 shrink-0 truncate text-xs text-ink-4">{answer.name}</span>}
    </div>
  );
}

export function Asks({ session, asks, cwd, confirms = [] }: { session: string; asks: Ask[]; cwd: string; confirms?: Need[] }) {
  const { busy, leaving, answer } = useAnswer(session);
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

const ICON: Record<string, ReactNode> = {
  command: <SquareTerminal size={16} />,
  edit: <FilePen size={16} />,
  web: <Globe size={16} />,
};
const ICON_SMALL: Record<string, ReactNode> = {
  command: <SquareTerminal size={14} />,
  edit: <FilePen size={14} />,
  web: <Globe size={14} />,
};

function titleOf(a: Ask): string {
  if (a.kind === "plan") return t("ask.plan");
  if (a.kind === "manual") return t("ask.manual");
  if (a.kind === "question") return t("ask.question");
  const k = kindOf(a.tool);
  return k === "command" ? t("ask.runCommand") : k === "edit" ? t("ask.edit") : k === "web" ? t("ask.web") : k === "read" || k === "search" ? t("ask.read") : t("ask.useTool");
}

/** An approval or a plan: allow (with "always" for approvals) or deny, a reason in the details. A
 *  manual ask (session/manual.go): Claude, a step denied by auto mode, asks to be switched to
 *  manual approval for a while; its reason the summary, agree or refuse. */
function Decide({ a, cwd, busy, send }: { a: Ask; cwd: string; busy: boolean; send: (r: Reply) => void }) {
  const [open, setOpen] = useState(false);
  const [why, setWhy] = useState("");
  const input = JSON.stringify(a.input ?? {});
  const plan = a.kind === "plan" ? String(inputOf(input).plan ?? "") : "";
  const manual = a.kind === "manual";
  const steps = manual ? Number(inputOf(input).steps ?? 0) : 0;
  const { arg } = plan ? { arg: plan.split("\n").find((l) => l.trim())?.replace(/^#+\s*/, "") ?? "" } : manual ? { arg: a.description ?? "" } : argOf({ name: a.tool, input }, cwd);
  const icon = a.kind === "plan" ? <ClipboardList size={16} /> : manual ? <Hand size={16} /> : (ICON[kindOf(a.tool)] ?? <ShieldQuestion size={16} />);
  const details = a.kind === "plan" || manual ? null : JSON.stringify(a.input ?? {}, null, 2);
  const isPlan = a.kind === "plan";
  return (
    <>
      <CardHead
        icon={icon}
        title={titleOf(a)}
        tag={a.kind === "permission" ? a.tool : manual && steps ? t("ask.manualSteps", { n: steps }) : undefined}
        summary={arg || a.description || a.tool}
        mono={a.kind === "permission"}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={busy} onClick={() => send({ option: isPlan ? "reject" : "deny", message: why.trim() || undefined })}>
              {isPlan ? t("ask.keepPlanning") : manual ? t("ask.refuse") : t("ask.deny")}
            </SecondKey>
            <div className="flex">
              <button
                type="button"
                disabled={busy}
                onClick={() => send({ option: isPlan ? "approve" : "allow" })}
                className={cn("g-press flex h-8 items-center gap-1.5 bg-ink pr-2.5 pl-3 text-sm font-medium text-on-ink disabled:opacity-60 pointer-coarse:h-10", a.kind === "permission" ? "rounded-l-md" : "rounded-md pr-3")}
              >
                {busy && <LoaderCircle size={13} className="animate-spin" />}
                {isPlan ? t("ask.approve") : manual ? t("ask.agree") : t("ask.allow")}
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
                  <MenuPortal>
                    <Menu.Positioner side="top" align="end" sideOffset={6} collisionPadding={8} className="z-50">
                      <Menu.Popup className="g-unfold glass thick float rounded-lg p-1.5 text-ink outline-none">
                        <Menu.Item onClick={() => send({ option: "always" })} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none data-highlighted:bg-fill pointer-coarse:h-9">
                          {t("ask.always")}
                          {a.pattern && <span className="font-mono text-xs text-ink-3">{a.pattern}</span>}
                        </Menu.Item>
                      </Menu.Popup>
                    </Menu.Positioner>
                  </MenuPortal>
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
              {details && (
                <>
                  <dt className="text-ink-3">{t("ask.input")}</dt>
                  <dd className="m-0 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-ink-2">{details}</dd>
                </>
              )}
            </dl>
          )}
          {!manual && <input value={why} onChange={(e) => setWhy(e.target.value)} placeholder={t("ask.reason")} aria-label={t("ask.reason")} disabled={busy} className="no-ring h-8 rounded-md bg-fill px-2.5 text-sm placeholder:text-ink-3" />}
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
      // Enter sends from anywhere in the card, an option key focused too (Space picks it); not
      // while an input method composes, nor with Shift.
      onKeyDown={(e) => {
        if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing || e.keyCode === 229) return;
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
                  // The composer's send key: an arrow in a rounded square.
                  <Tip label={t("ask.send")}>
                    <button type="submit" aria-label={t("ask.send")} disabled={busy || !ready} className="group/send g-press grid size-8 shrink-0 place-items-center pointer-coarse:size-10">
                      <span className="grid size-7 place-items-center rounded-[9px] bg-ink text-on-ink transition-opacity duration-(--dur-1) group-disabled/send:opacity-25 pointer-coarse:size-8 pointer-coarse:rounded-[10px]">
                        {busy ? <LoaderCircle size={14} className="animate-spin" /> : <ArrowUp size={16} strokeWidth={2.25} />}
                      </span>
                    </button>
                  </Tip>
                )}
              </label>
            </div>
          </fieldset>
        );
      })}
    </form>
  );
}
