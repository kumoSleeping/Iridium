// 设定 (docs/channels.md "资料" 设定; otae, 2026-11-21): an agent's own words on how it works, in
// its system prompt after its intro, none by default. Where an agent's card or a role is edited
// (chat/channel/edit.tsx, rolespage.tsx): a row with its first line (无 for none), which opens it
// whole to write; under it, the personas the agents' hosts give (GET /v1/internal/personas: DSH
// Desktop's persona plugin, docs/exec.md agent.personas), one pressed taken as it (a copy). All of
// those come from the hosts: with none, no list.
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api, missing } from "./api/client";
import { PersonaMax, type HostPersona } from "./api/types.gen";
import { t } from "./i18n";
import { errText } from "./toast";
import { cn } from "./ui";

/** The personas the hosts give, asked for as a persona is opened; none on a daemon without the route. */
export const hostPersonasQ = { queryKey: ["personas"], queryFn: () => api.personas().catch((e) => (missing(e) ? [] : Promise.reject(e))), staleTime: 30_000 };

/** A persona's first line, as its row shows it. */
export const personaHead = (v?: string) => (v ?? "").trim().split("\n")[0]?.trim() ?? "";

/** The row of a persona: its first line, else 无; pressed, open. */
export function PersonaRow({ value, open, className }: { value?: string; open: () => void; className?: string }) {
  const head = personaHead(value);
  return (
    <button type="button" onClick={open} className={cn("g-press -mx-1.5 flex min-h-10 items-center gap-3 rounded-md px-1.5 py-1 text-left hover:bg-fill", className)}>
      <span className="shrink-0 text-sm">{t("profile.persona")}</span>
      <span className={cn("min-w-0 flex-1 truncate text-right text-sm", head ? "text-ink-2" : "text-ink-3")}>{head || t("profile.personaNone")}</span>
      <ChevronRight size={15} className="shrink-0 text-ink-4" />
    </button>
  );
}

/** Where a host's persona is from, as its row says it: the host's name of it, the computer. */
const whence = (p: HostPersona) => [p.from, p.machineName].filter(Boolean).join(" · ");

/** The personas the hosts give, each a row; take: one pressed. None: nothing drawn. */
export function HostPersonas({ take, className }: { take: (p: HostPersona) => void; className?: string }) {
  const list = useQuery(hostPersonasQ).data ?? [];
  if (!list.length) return null;
  return (
    <section className={cn("flex flex-col pt-3", className)}>
      <h3 className="m-0 mb-1 text-xs font-medium text-ink-3">{t("profile.personaImport")}</h3>
      {list.map((p) => (
        <button key={`${p.machine ?? ""}\n${p.agent}\n${p.id}`} type="button" onClick={() => take(p)} className="g-press -mx-1.5 flex min-h-11 flex-col justify-center rounded-md px-1.5 py-1 text-left hover:bg-fill">
          <span className="flex w-full min-w-0 items-baseline gap-2">
            <span className="min-w-0 flex-1 truncate text-sm">{p.name}</span>
            {whence(p) && <span className="shrink-0 text-xs text-ink-3">{whence(p)}</span>}
          </span>
          {p.description && <span className="w-full truncate text-xs text-ink-3">{p.description}</span>}
        </button>
      ))}
    </section>
  );
}

/** A persona written whole: saved as it is left, if changed (Enter is a line's end); the hosts'
 *  personas under it, one pressed put in place of what is written and saved. Never dragged to a
 *  size (otae, 2026-11-22): fill, it takes its page's height (the settings), the hosts' list under
 *  it scrolling apart; else a fixed height (编辑资料's popover). */
export function PersonaEditor({ value, save, fill }: { value?: string; save: (v: string) => Promise<unknown>; fill?: boolean }) {
  const [draft, setDraft] = useState(value ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setDraft(value ?? "");
  }, [value]);
  const put = async (v: string) => {
    if (v.trim() === (value ?? "").trim() || busy) return;
    setBusy(true);
    try {
      await save(v.trim());
      setError("");
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };
  const n = [...draft].length;
  return (
    <div className={cn("flex flex-col", fill && "min-h-0 flex-1")}>
      <div className="flex shrink-0 items-baseline gap-2 pb-1.5">
        <span className="min-w-0 flex-1 truncate text-xs text-ink-3">{t("profile.personaHint")}</span>
        {busy && <LoaderCircle size={12} className="animate-spin self-center text-ink-3" />}
        <span className={cn("text-xs tabular-nums", n > PersonaMax ? "text-err" : "text-ink-3")}>{`${n}/${PersonaMax}`}</span>
      </div>
      <textarea
        aria-label={t("profile.persona")}
        value={draft}
        maxLength={PersonaMax}
        onFocus={() => void (focused.current = true)}
        onBlur={() => ((focused.current = false), void put(draft))}
        onChange={(e) => (setDraft(e.target.value), setError(""))}
        onKeyDown={(e) => e.key === "Escape" && (e.preventDefault(), e.stopPropagation(), setDraft(value ?? ""), (e.target as HTMLElement).blur())}
        className={cn("no-ring no-bar w-full min-w-0 resize-none overflow-y-auto rounded-lg bg-fill-2 px-3 py-2.5 text-sm leading-6 placeholder:text-ink-3", fill ? "min-h-40 flex-1" : "h-72")}
      />
      {error && <p className="m-0 shrink-0 px-0.5 pt-1 text-xs text-err">{error}</p>}
      <HostPersonas take={(p) => (setDraft(p.text), void put(p.text))} className={cn(fill && "no-bar max-h-[40%] shrink-0 overflow-y-auto")} />
    </div>
  );
}
