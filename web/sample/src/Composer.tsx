// The bottom stack: the confirmation card above the composer, the composer (attach, text, context
// ring, send) with the quota bars on its top-right, and the approval-mode key under it.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { ArrowUp, Check, ChevronDown, FilePen, ListChecks, Paperclip, ShieldOff, ShieldQuestion, SquareTerminal } from "lucide-react";
import { forwardRef, useState, type CSSProperties, type ReactNode } from "react";
import { approval, context, modes, pick, quotas, type CtxKey, type Mode } from "./data";
import { t, useLang } from "./i18n";
import { cn, IconKey, PopSurface, Tip } from "./ui";

export function ApprovalCard({ leaving, onAnswer, detailsOpen, setDetailsOpen }: { leaving: boolean; onAnswer: (a: "allow" | "deny" | "always") => void; detailsOpen: boolean; setDetailsOpen: (o: boolean) => void }) {
  const lang = useLang();
  return (
    <div data-leaving={leaving || undefined} className="g-pop glass float @container mb-2 rounded-lg p-2.5 pl-3" role="alertdialog" aria-label={t("ask.runCommand")}>
      <div className="flex flex-col gap-2.5 @[34rem]:flex-row @[34rem]:items-center">
        <button type="button" onClick={() => setDetailsOpen(!detailsOpen)} aria-expanded={detailsOpen} className="group flex min-w-0 flex-1 items-start gap-2.5 rounded-sm text-left">
          <SquareTerminal size={16} className="mt-0.5 shrink-0 text-ink-2" />
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-1.5 text-sm font-semibold">
              {t("ask.runCommand")}
              <span className="font-normal text-ink-3">{approval.tool}</span>
              <ChevronDown size={13} className="text-ink-4 transition-transform duration-(--dur-2) ease-(--ease-out)" style={{ transform: detailsOpen ? "rotate(180deg)" : undefined }} aria-label={t("ask.details")} />
            </span>
            <span className={cn("block font-mono text-xs leading-5 text-ink-2", detailsOpen ? "break-all whitespace-pre-wrap" : "truncate")}>{approval.command}</span>
          </span>
        </button>
        <div className="flex shrink-0 justify-end gap-1.5">
          <button type="button" onClick={() => onAnswer("deny")} className="g-press h-8 rounded-md bg-fill px-3 text-sm hover:bg-fill-2 pointer-coarse:h-10">
            {t("ask.deny")}
          </button>
          <div className="flex">
            <button type="button" onClick={() => onAnswer("allow")} className="g-press h-8 rounded-l-md bg-ink pr-2.5 pl-3 text-sm font-medium text-on-ink pointer-coarse:h-10">
              {t("ask.allow")}
            </button>
            <Menu.Root>
              <Menu.Trigger
                aria-label={t("ask.always")}
                className="grid h-8 w-7 place-items-center rounded-r-md bg-ink text-on-ink/80 shadow-[inset_1px_0_0_color-mix(in_oklch,var(--on-ink)_22%,transparent)] hover:text-on-ink pointer-coarse:h-10 pointer-coarse:w-9"
              >
                <ChevronDown size={14} />
              </Menu.Trigger>
              <Menu.Portal>
                <Menu.Positioner side="top" align="end" sideOffset={6} className="z-50">
                  <Menu.Popup className="g-unfold glass thick float rounded-lg p-1.5 text-ink outline-none">
                    <Menu.Item onClick={() => onAnswer("always")} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none data-highlighted:bg-fill pointer-coarse:h-11">
                      {t("ask.always")}
                    </Menu.Item>
                  </Menu.Popup>
                </Menu.Positioner>
              </Menu.Portal>
            </Menu.Root>
          </div>
        </div>
      </div>
      {detailsOpen && (
        <dl className="g-fade m-0 mt-2.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 pl-[26px] text-sm">
          <dt className="text-ink-3">{t("ask.cwd")}</dt>
          <dd className="m-0 font-mono text-xs leading-5 text-ink-2">{approval.cwd}</dd>
          <dt className="text-ink-3">{t("ask.why")}</dt>
          <dd className="m-0 text-ink-2">{pick(approval.why, lang)}</dd>
        </dl>
      )}
    </div>
  );
}

/** One thin bar per quota window; amber when nearly full, red when full. */
function QuotaBars({ stale }: { stale?: boolean }) {
  const lang = useLang();
  return (
    <div className={cn("absolute top-[3px] right-4 flex gap-1", stale && "opacity-50")}>
      {quotas.map((q) => {
        const label = `${t(`quota.${q.key}`)} · ${t("quota.tip", { pct: q.pct, time: pick(q.reset, lang) })}`;
        const tone = q.pct >= 95 ? "bg-err" : q.pct >= 80 ? "bg-need" : "bg-ink-3";
        return (
          <Tip key={q.key} label={label}>
            <span role="meter" aria-label={label} aria-valuenow={q.pct} aria-valuemin={0} aria-valuemax={100} className="relative flex h-3 w-7 items-center before:absolute before:-inset-x-0.5 before:-inset-y-2 before:content-['']">
              <span className="relative h-[3px] w-full overflow-hidden rounded-full bg-fill-2">
                <span className={cn("absolute inset-y-0 left-0 w-full origin-left rounded-full", tone)} style={{ transform: `scaleX(${q.pct / 100})` }} />
              </span>
            </span>
          </Tip>
        );
      })}
    </div>
  );
}

const CTX_TONE: Record<CtxKey, string> = {
  messages: "var(--ctx-1)",
  tools: "var(--ctx-2)",
  mcp: "var(--ctx-3)",
  memory: "var(--ctx-4)",
  system: "var(--ctx-5)",
  reserve: "repeating-linear-gradient(135deg, var(--ink-4) 0 1.5px, transparent 1.5px 4px)",
  free: "var(--fill-2)",
};

function ContextRing({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const used = context.parts.filter((p) => p.key !== "free" && p.key !== "reserve").reduce((a, p) => a + p.k, 0);
  const pct = Math.round((used / context.total) * 100);
  const r = 7;
  const c = 2 * Math.PI * r;
  const label = `${t("ctx.title")} · ${t("ctx.used", { used: `${used.toFixed(0)}k`, total: `${context.total}k` })}`;
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={label}>
        <Popover.Trigger
          render={
            <button type="button" aria-label={label} className="g-press flex h-8 items-center gap-1.5 rounded-md px-1.5 text-xs text-ink-3 tabular-nums hover:bg-fill hover:text-ink-2 data-popup-open:bg-fill pointer-coarse:h-10">
              <svg width="18" height="18" viewBox="0 0 18 18" className="-rotate-90">
                <circle cx="9" cy="9" r={r} fill="none" stroke="var(--fill-2)" strokeWidth="2.25" />
                <circle cx="9" cy="9" r={r} fill="none" stroke="var(--ink-2)" strokeWidth="2.25" strokeLinecap="round" strokeDasharray={`${(c * pct) / 100} ${c}`} />
              </svg>
              {pct}%
            </button>
          }
        />
      </Tip>
      <PopSurface side="top" align="end" className="w-[min(300px,calc(100vw-16px))] p-3.5">
        <div className="flex items-baseline justify-between">
          <span className="text-sm font-semibold">{t("ctx.title")}</span>
          <span className="text-xs text-ink-3 tabular-nums">{t("ctx.used", { used: `${used.toFixed(1)}k`, total: `${context.total}k` })}</span>
        </div>
        <div className="mt-2.5 flex h-2 gap-[2px]" role="img" aria-label={context.parts.map((p) => `${t(`ctx.${p.key}`)} ${p.k}k`).join(", ")}>
          {context.parts.map((p, i) => (
            <Tip key={p.key} label={`${t(`ctx.${p.key}`)} · ${p.k}k`}>
              <span
                className={cn("g-fade h-full", i === 0 && "rounded-l-full", i === context.parts.length - 1 && "rounded-r-full")}
                style={{ flexGrow: p.k, flexBasis: 0, minWidth: 3, background: CTX_TONE[p.key], animationDelay: `${i * 20}ms` } as CSSProperties}
              />
            </Tip>
          ))}
        </div>
        <ul className="m-0 mt-3 grid list-none gap-1 p-0">
          {context.parts.map((p) => (
            <li key={p.key} className="flex items-center gap-2 text-sm">
              <span className="size-2 shrink-0 rounded-full" style={{ background: CTX_TONE[p.key], boxShadow: p.key === "free" || p.key === "reserve" ? "inset 0 0 0 1px var(--ink-4)" : undefined }} />
              <span className="flex-1 text-ink-2">{t(`ctx.${p.key}`)}</span>
              <span className="text-ink tabular-nums">{p.k}k</span>
            </li>
          ))}
        </ul>
      </PopSurface>
    </Popover.Root>
  );
}

const MODE_ICON: Record<Mode, ReactNode> = {
  default: <ShieldQuestion size={14} />,
  acceptEdits: <FilePen size={14} />,
  plan: <ListChecks size={14} />,
  bypass: <ShieldOff size={14} />,
};

export function ModeKey({ mode, setMode, open, onOpenChange }: { mode: Mode; setMode: (m: Mode) => void; open: boolean; onOpenChange: (o: boolean) => void }) {
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("composer.mode")}>
        <Menu.Trigger
          render={
            <button type="button" aria-label={`${t("composer.mode")}: ${t(`mode.${mode}`)}`} className="g-press flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:h-9">
              {MODE_ICON[mode]}
              <span key={mode} className="g-fade">
                {t(`mode.${mode}`)}
              </span>
            </button>
          }
        />
      </Tip>
      <Menu.Portal>
        <Menu.Positioner side="top" align="start" sideOffset={6} collisionPadding={8} className="z-50">
          <Menu.Popup className="g-unfold glass thick float min-w-52 rounded-lg p-1.5 text-ink outline-none">
            <Menu.RadioGroup value={mode} onValueChange={(v) => setMode(v as Mode)} className="g-stagger flex flex-col">
              {modes.map((m, i) => (
                <Menu.RadioItem key={m} value={m} style={{ "--i": i } as CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11">
                  <span className="text-ink-2">{MODE_ICON[m]}</span>
                  <span className="flex-1">{t(`mode.${m}`)}</span>
                  <Menu.RadioItemIndicator className="text-ink">
                    <Check size={14} />
                  </Menu.RadioItemIndicator>
                </Menu.RadioItem>
              ))}
            </Menu.RadioGroup>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

export const Composer = forwardRef<HTMLTextAreaElement, { value: string; setValue: (v: string) => void; onSend: () => void; onCycleMode: () => void; ctxOpen: boolean; setCtxOpen: (o: boolean) => void }>(function Composer(
  { value, setValue, onSend, onCycleMode, ctxOpen, setCtxOpen },
  ref,
) {
  const [composing, setComposing] = useState(false);
  const empty = !value.trim();
  return (
    <div className="glass float relative rounded-xl">
      <QuotaBars />
      <div className="flex items-end gap-0.5 px-1.5 pt-3 pb-1.5 pointer-coarse:pt-3.5">
        <IconKey label={t("composer.attach")}>
          <Paperclip size={17} />
        </IconKey>
        <textarea
          ref={ref}
          rows={1}
          value={value}
          placeholder={t("composer.placeholder")}
          aria-label={t("composer.placeholder")}
          onChange={(e) => {
            setValue(e.target.value);
            e.target.style.height = "auto";
            e.target.style.height = `${Math.min(e.target.scrollHeight, 180)}px`;
          }}
          onCompositionStart={() => setComposing(true)}
          onCompositionEnd={() => setComposing(false)}
          onKeyDown={(e) => {
            if (e.key === "Tab" && e.shiftKey) {
              e.preventDefault();
              onCycleMode();
            }
            if (e.key === "Enter" && !e.shiftKey && !composing) {
              e.preventDefault();
              if (!empty) onSend();
            }
          }}
          className="no-ring max-h-[180px] min-h-8 min-w-0 flex-1 resize-none bg-transparent px-1.5 py-[5.5px] text-md leading-[21px] placeholder:text-ink-3"
        />
        <ContextRing open={ctxOpen} onOpenChange={setCtxOpen} />
        <Tip label={t("composer.send")}>
          <button
            type="button"
            aria-label={t("composer.send")}
            disabled={empty}
            onClick={onSend}
            className="g-press grid size-8 shrink-0 place-items-center rounded-full bg-ink text-on-ink transition-opacity duration-(--dur-1) disabled:opacity-25 pointer-coarse:size-10"
          >
            <ArrowUp size={17} strokeWidth={2.25} />
          </button>
        </Tip>
      </div>
    </div>
  );
});
