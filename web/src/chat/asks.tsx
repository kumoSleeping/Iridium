// The confirmation panel (plan "确认面板"): one card per open ask, above the composer, and the
// confirmations agents' ir asked a panel for from this session (invites, tokens). A title, one line
// of summary, a main and a second key; the details open under it. An approval: the tool and its
// arguments (commands in monospace), allow / deny / always allow this session. A plan: its
// Markdown, approve or keep planning. A question: its options as keys, or your own words. Every
// answer goes to the ask's card (POST /api/answer), as a plugin's does.
import { Menu } from "@base-ui/react/menu";
import { ChevronDown, ClipboardList, FilePen, Globe, LoaderCircle, MessageCircleQuestionMark, ShieldQuestion, SquareCheck, Square as SquareIcon, SquareTerminal } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Ask, CardAnswer, Confirm } from "../api/types.gen";
import { CardDetails, CardFrame, CardHead, SecondKey } from "../card";
import { ConfirmCard } from "../confirms";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { cn, tilde } from "../ui";
import { argOf, inputOf, kindOf } from "./fold";
import { Markdown } from "./markdown";

type Q = { question: string; header?: string; options: { label: string; description?: string }[]; multiSelect?: boolean };

/** What a key press answers: an option of the card's, or a choice per question and own words. */
type Reply = { option?: string; choices?: string[]; words?: string[]; message?: string };

export function Asks({ session, asks, cwd, confirms = [] }: { session: string; asks: Ask[]; cwd: string; confirms?: Confirm[] }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [leaving, setLeaving] = useState<string | null>(null);
  const answer = async (a: Ask, r: Reply) => {
    setBusy(a.requestId);
    try {
      await api.answer({ card: a.card?.id ?? `ask:${session}:${a.requestId}`, ...r } satisfies CardAnswer);
      setLeaving(a.requestId);
      setTimeout(() => setLeaving(null), 140);
      if (a.kind === "permission") toast(r.option === "deny" ? t("toast.denied") : t("toast.allowed"), { description: r.option === "always" ? t("ask.always") : undefined });
    } catch (e) {
      toast(t("err.answer"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  if (!asks.length && !confirms.length) return null;
  return (
    // Padded out under negative margins: the cards' shadows (12px down, 32px blur) are not cut where
    // the list scrolls, nor over the meters row under it.
    <div className="-mx-8 -mt-3 mb-[-36px] flex max-h-[55dvh] flex-col gap-2 overflow-y-auto px-8 pt-3 pb-11">
      {confirms.map((c) => (
        <ConfirmCard key={c.id} c={c} />
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
            <div className="max-h-[40dvh] overflow-y-auto rounded-md bg-fill/60 px-3 py-2">
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
              <dd className="m-0 max-h-40 overflow-auto font-mono text-xs leading-5 break-all whitespace-pre-wrap text-ink-2">{details}</dd>
            </dl>
          )}
          <input value={why} onChange={(e) => setWhy(e.target.value)} placeholder={t("ask.reason")} aria-label={t("ask.reason")} disabled={busy} className="no-ring h-8 rounded-md bg-fill px-2.5 text-sm placeholder:text-ink-3" />
        </CardDetails>
      )}
    </>
  );
}

/** Questions: each its options as keys; one question picked once answers as you tap; several, or
 *  a multiple choice, once sent. Your own words answer too. The card's option ids are
 *  "<question>.<option>", by place. */
function Question({ a, busy, send }: { a: Ask; busy: boolean; send: (r: Reply) => void }) {
  const qs = ((a.input as { questions?: Q[] } | null)?.questions ?? []).filter((q) => q?.question);
  const [picked, setPicked] = useState<number[][]>(() => qs.map(() => []));
  const [own, setOwn] = useState<string[]>(() => qs.map(() => ""));
  const tap = qs.length === 1 && !qs[0]!.multiSelect;
  const reply = (p = picked, w = own): Reply => ({
    choices: p.flatMap((js, i) => (w[i]!.trim() ? [] : js.map((j) => `${i}.${j}`))),
    words: w.some((x) => x.trim()) ? w.map((x) => x.trim()) : undefined,
  });
  const ready = qs.every((_, i) => own[i]!.trim() || picked[i]!.length);
  const pick = (i: number, j: number) => {
    if (tap) return send({ choices: [`0.${j}`] });
    setPicked((p) => p.map((was, k) => (k !== i ? was : was.includes(j) ? was.filter((x) => x !== j) : qs[i]!.multiSelect ? [...was, j] : [j])));
  };
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (ready && !busy) send(reply());
      }}
      className="flex flex-col gap-3"
    >
      {qs.map((q, i) => (
        <fieldset key={i} disabled={busy} className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0">
          <legend className="mb-2 flex items-start gap-2.5 p-0 text-sm font-semibold">
            <MessageCircleQuestionMark size={16} className="mt-0.5 shrink-0 text-ink-2" />
            <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">{q.question}</span>
          </legend>
          <div className="flex flex-wrap gap-1.5 pl-[26px]">
            {q.options.map((o, j) => {
              const on = picked[i]!.includes(j);
              const Box = on ? SquareCheck : SquareIcon;
              return (
                <button
                  key={j}
                  type="button"
                  aria-pressed={tap ? undefined : on}
                  onClick={() => pick(i, j)}
                  title={o.description}
                  className={cn("g-press flex min-h-8 max-w-full items-start gap-1.5 rounded-md px-3 py-1.5 text-left text-sm pointer-coarse:min-h-10", on ? "bg-ink text-on-ink" : "bg-fill hover:bg-fill-2")}
                >
                  {q.multiSelect && <Box size={14} className="mt-0.5 shrink-0" />}
                  <span className="flex min-w-0 flex-col">
                    <span className="font-medium">{o.label}</span>
                    {o.description && <span className={cn("text-xs", on ? "text-on-ink/75" : "text-ink-3")}>{o.description}</span>}
                  </span>
                </button>
              );
            })}
          </div>
          <div className="flex items-center gap-1.5 pl-[26px]">
            <input value={own[i]} onChange={(e) => setOwn((w) => w.map((x, j) => (j === i ? e.target.value : x)))} placeholder={t("ask.own")} aria-label={t("ask.own")} className="no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2.5 text-sm placeholder:text-ink-3" />
            {i === qs.length - 1 && (!tap || own[i]!.trim()) && (
              <button type="submit" disabled={busy || !ready} className="g-press flex h-8 items-center gap-1.5 rounded-md bg-ink px-3 text-sm font-medium text-on-ink disabled:opacity-30">
                {busy && <LoaderCircle size={13} className="animate-spin" />}
                {t("ask.send")}
              </button>
            )}
          </div>
        </fieldset>
      ))}
    </form>
  );
}
