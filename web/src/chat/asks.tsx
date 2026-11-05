// The confirmation panel (plan "确认面板"): one card per open ask, above the composer. A title, one
// line of summary, a main and a second key; the details open under it. An approval: the tool and
// its arguments (commands in monospace), allow / deny / always allow this session. A plan: its
// Markdown, approve or keep planning. A question: its options as keys, or your own words.
import { Menu } from "@base-ui/react/menu";
import { ChevronDown, ClipboardList, FilePen, Globe, LoaderCircle, MessageCircleQuestionMark, ShieldQuestion, SquareCheck, Square as SquareIcon, SquareTerminal } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Ask } from "../api/types.gen";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { cn, tilde } from "../ui";
import { argOf, inputOf, kindOf } from "./fold";
import { Markdown } from "./markdown";

type Q = { question: string; header?: string; options: { label: string; description?: string }[]; multiSelect?: boolean };

export function Asks({ session, asks, cwd }: { session: string; asks: Ask[]; cwd: string }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [leaving, setLeaving] = useState<string | null>(null);
  const answer = async (a: Ask, body: { allow?: boolean; message?: string; answers?: Record<string, string>; always?: boolean }) => {
    setBusy(a.requestId);
    try {
      await api.answer(session, { requestId: a.requestId, kind: a.kind, ...body });
      setLeaving(a.requestId);
      setTimeout(() => setLeaving(null), 140);
      if (a.kind === "permission") toast(body.allow ? t("toast.allowed") : t("toast.denied"), { description: body.always ? t("ask.always") : undefined });
    } catch (e) {
      toast(t("err.answer"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  if (!asks.length) return null;
  return (
    <div className="mb-2 flex max-h-[55dvh] flex-col gap-2 overflow-y-auto">
      {asks.map((a) => (
        <div key={a.requestId} data-leaving={leaving === a.requestId || undefined} className="g-pop glass float @container rounded-lg p-2.5 pl-3" role="alertdialog" aria-label={titleOf(a)}>
          {a.kind === "question" ? <Question a={a} busy={busy === a.requestId} send={(answers) => void answer(a, { answers })} /> : <Decide a={a} cwd={cwd} busy={busy === a.requestId} send={(b) => void answer(a, b)} />}
        </div>
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
function Decide({ a, cwd, busy, send }: { a: Ask; cwd: string; busy: boolean; send: (b: { allow: boolean; message?: string; always?: boolean }) => void }) {
  const [open, setOpen] = useState(false);
  const [why, setWhy] = useState("");
  const input = JSON.stringify(a.input ?? {});
  const plan = a.kind === "plan" ? String(inputOf(input).plan ?? "") : "";
  const { arg } = plan ? { arg: plan.split("\n").find((l) => l.trim())?.replace(/^#+\s*/, "") ?? "" } : argOf({ name: a.tool, input }, cwd);
  const icon = a.kind === "plan" ? <ClipboardList size={16} /> : (ICON[kindOf(a.tool)] ?? <ShieldQuestion size={16} />);
  const details = a.kind === "plan" ? null : JSON.stringify(a.input ?? {}, null, 2);
  return (
    <>
      <div className="flex flex-col gap-2.5 @[34rem]:flex-row @[34rem]:items-center">
        <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="group flex min-w-0 flex-1 items-start gap-2.5 rounded-sm text-left">
          <span className="mt-0.5 shrink-0 text-ink-2">{icon}</span>
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-1.5 text-sm font-semibold">
              {titleOf(a)}
              {a.kind === "permission" && <span className="font-normal text-ink-3">{a.tool}</span>}
              <ChevronDown size={13} className="text-ink-4 transition-transform duration-(--dur-2) ease-(--ease-out)" style={{ transform: open ? "rotate(180deg)" : undefined }} aria-label={t("ask.details")} />
            </span>
            <span className={cn("block text-xs leading-5 text-ink-2", a.kind === "permission" && "font-mono", open ? "break-all whitespace-pre-wrap" : "truncate")}>{arg || a.description || a.tool}</span>
          </span>
        </button>
        <div className="flex shrink-0 justify-end gap-1.5">
          <button type="button" disabled={busy} onClick={() => send({ allow: false, message: why.trim() || undefined })} className="g-press h-8 rounded-md bg-fill px-3 text-sm hover:bg-fill-2 disabled:opacity-50 pointer-coarse:h-10">
            {a.kind === "plan" ? t("ask.keepPlanning") : t("ask.deny")}
          </button>
          <div className="flex">
            <button
              type="button"
              disabled={busy}
              onClick={() => send({ allow: true })}
              className={cn("g-press flex h-8 items-center gap-1.5 bg-ink pr-2.5 pl-3 text-sm font-medium text-on-ink disabled:opacity-60 pointer-coarse:h-10", a.kind === "permission" ? "rounded-l-md" : "rounded-md pr-3")}
            >
              {busy && <LoaderCircle size={13} className="animate-spin" />}
              {a.kind === "plan" ? t("ask.approve") : t("ask.allow")}
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
                      <Menu.Item onClick={() => send({ allow: true, always: true })} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none data-highlighted:bg-fill pointer-coarse:h-11">
                        {t("ask.always")}
                      </Menu.Item>
                    </Menu.Popup>
                  </Menu.Positioner>
                </Menu.Portal>
              </Menu.Root>
            )}
          </div>
        </div>
      </div>
      {open && (
        <div className="g-fade mt-2.5 grid gap-2 pl-[26px] text-sm">
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
        </div>
      )}
    </>
  );
}

/** Questions: each its options as keys; one question picked once answers as you tap; several, or
 *  a multiple choice, once sent. Your own words answer too. */
function Question({ a, busy, send }: { a: Ask; busy: boolean; send: (answers: Record<string, string>) => void }) {
  const qs = ((a.input as { questions?: Q[] } | null)?.questions ?? []).filter((q) => q?.question);
  const [picked, setPicked] = useState<string[][]>(() => qs.map(() => []));
  const [own, setOwn] = useState<string[]>(() => qs.map(() => ""));
  const tap = qs.length === 1 && !qs[0]!.multiSelect;
  const answers = () => Object.fromEntries(qs.map((q, i) => [q.question, own[i]!.trim() || picked[i]!.join(", ")]));
  const ready = qs.every((_, i) => own[i]!.trim() || picked[i]!.length);
  const pick = (i: number, label: string) => {
    if (tap) return send({ [qs[0]!.question]: label });
    setPicked((p) => p.map((was, j) => (j !== i ? was : was.includes(label) ? was.filter((x) => x !== label) : qs[i]!.multiSelect ? [...was, label] : [label])));
  };
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (ready && !busy) send(answers());
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
            {q.options.map((o) => {
              const on = picked[i]!.includes(o.label);
              const Box = on ? SquareCheck : SquareIcon;
              return (
                <button
                  key={o.label}
                  type="button"
                  aria-pressed={tap ? undefined : on}
                  onClick={() => pick(i, o.label)}
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
