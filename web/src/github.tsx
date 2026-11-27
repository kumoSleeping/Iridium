// GitHub on a login's row in 应用管理 (kumo, 2026-11-27: only a GitHub mark there for now, pressed
// its details; the page is to be redone): the account a person bound, whose git and gh their
// agents' turns act as (docs/api.md "GitHub"). One's own row, unbound, has the mark faint: pressed,
// binding, by GitHub's link (its device flow), or a token pasted.
import { Popover } from "@base-ui/react/popover";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, LoaderCircle, Unlink } from "lucide-react";
import { useEffect, useState } from "react";
import { api, ApiError } from "./api/client";
import type { GitHubDevice, GitHubList } from "./api/types.gen";
import { useQuery } from "./api/query";
import { MainKey, SecondKey } from "./card";
import { t } from "./i18n";
import { ago, Avatar, cn, CopyKey, PopSurface, SureKey, Tip } from "./ui";

const githubQ = { queryKey: ["github"], queryFn: () => api.github(), staleTime: 60_000 };

/** GitHub's mark (its octocat outline is not among the icons). */
export function GitHubMark({ size = 15, className }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 16 16" width={size} height={size} aria-hidden className={className} fill="currentColor">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

/** The GitHub mark of member's row: bound, its account; one's own unbound (mine), faint, binding. */
export function GitHubKey({ member, mine }: { member: string; mine: boolean }) {
  const d = useQuery<GitHubList>(githubQ).data;
  const b = d?.bindings.find((x) => x.member === member);
  const [open, setOpen] = useState(false);
  if (!d || (!b && !mine)) return null;
  const label = b ? `GitHub · @${b.login}` : t("github.bind");
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Tip label={label}>
        <Popover.Trigger aria-label={label} className={cn("g-press grid size-7 shrink-0 place-items-center rounded-md hover:bg-fill data-popup-open:bg-fill", b ? "text-ink-2 hover:text-ink" : "text-ink-4 hover:text-ink-2")}>
          <GitHubMark size={15} />
        </Popover.Trigger>
      </Tip>
      <PopSurface align="end" className="w-[min(300px,calc(100vw-16px))] p-3">
        {b && <Bound member={member} mine={mine} done={() => setOpen(false)} />}
        {!b && <Bind done={() => setOpen(false)} />}
      </PopSurface>
    </Popover.Root>
  );
}

export function Bound({ member, mine, done }: { member: string; mine: boolean; done: () => void }) {
  const qc = useQueryClient();
  const b = useQuery<GitHubList>(githubQ).data?.bindings.find((x) => x.member === member);
  const [again, setAgain] = useState(false);
  if (!b) return null;
  const unbind = () =>
    void (mine ? api.unbindGitHub() : api.unbindGitHub(member)).then(() => (qc.invalidateQueries({ queryKey: ["github"] }), done()));
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2.5">
        <Avatar src={b.avatar} name={b.login} size={32} />
        <span className="min-w-0 flex-1">
          <a href={`https://github.com/${b.login}`} target="_blank" rel="noreferrer" className="block truncate text-sm font-medium hover:underline">
            @{b.login}
          </a>
          <span className="block text-xs text-ink-3">{t("github.boundAt", { when: ago(b.at) })}</span>
        </span>
        <SureKey label={t("github.unbind")} sure={t("github.unbindSure")} run={unbind} className="size-8">
          <Unlink size={15} />
        </SureKey>
      </div>
      <p className="m-0 text-xs text-ink-3">{t("github.what")}</p>
      {mine && (again ? <Bind done={done} /> : <SecondKey onClick={() => setAgain(true)}>{t("github.rebind")}</SecondKey>)}
    </div>
  );
}

/** Binding: by GitHub's link (its device flow: a code typed at github.com/login/device, kumo
 *  2026-11-27), the page asking the server again at GitHub's pace till it is allowed; a token
 *  pasted the other way, under it. */
export function Bind({ done }: { done: () => void }) {
  const [paste, setPaste] = useState(false);
  return paste ? (
    <PasteToken done={done} />
  ) : (
    <div className="flex flex-col gap-2">
      <DeviceBind done={done} />
      <button type="button" onClick={() => setPaste(true)} className="self-start text-xs text-ink-3 underline-offset-2 hover:text-ink-2 hover:underline">
        {t("github.usePaste")}
      </button>
    </div>
  );
}

function DeviceBind({ done }: { done: () => void }) {
  const qc = useQueryClient();
  const [flow, setFlow] = useState<GitHubDevice | null>(null);
  const [state, setState] = useState<"" | "waiting" | "expired" | "denied">("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const begin = () => {
    setBusy(true);
    setErr("");
    api.githubDevice().then(
      (d) => (setFlow(d), setState("waiting"), setBusy(false)),
      (e) => (setErr(e instanceof ApiError ? e.message : String(e)), setBusy(false)),
    );
  };
  // Asked again at GitHub's pace while it waits; gone when the page is.
  useEffect(() => {
    if (!flow || state !== "waiting") return;
    let live = true;
    const timer = setTimeout(() => {
      api.githubDeviceState(flow.id).then(
        (s) => {
          if (!live) return;
          if (s.state === "done") return void (qc.invalidateQueries({ queryKey: ["github"] }), done());
          setState(s.state === "expired" || s.state === "denied" ? s.state : "waiting");
          if (s.state === "waiting") setFlow({ ...flow });
        },
        (e) => live && (setErr(e instanceof ApiError ? e.message : String(e)), setState("expired")),
      );
    }, flow.interval * 1000);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [flow, state]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!flow || state !== "waiting")
    return (
      <div className="flex flex-col gap-2">
        <span className="text-sm font-medium">{t("github.bind")}</span>
        {state && <span className="text-xs text-ink-3">{t(state === "denied" ? "github.denied" : "github.expired")}</span>}
        {err && <span className="text-xs text-err">{err}</span>}
        <MainKey busy={busy} onClick={begin} className="self-start">
          <GitHubMark size={14} />
          {t("github.authorize")}
        </MainKey>
      </div>
    );
  const copy = () => void navigator.clipboard?.writeText(flow.code).catch(() => {});
  return (
    <div className="flex flex-col gap-2">
      <span className="text-sm font-medium">{t("github.bind")}</span>
      <div className="flex items-center gap-1.5 rounded-md bg-fill px-3 py-2">
        <code className="min-w-0 flex-1 font-mono text-xl font-semibold tracking-widest select-all">{flow.code}</code>
        <CopyKey text={flow.code} />
      </div>
      <a href={flow.uri} target="_blank" rel="noreferrer" onClick={copy} className="g-press flex h-9 items-center justify-center gap-1.5 rounded-md bg-ink text-sm font-medium text-on-ink">
        <GitHubMark size={14} />
        {t("github.open")}
        <ExternalLink size={13} />
      </a>
      <span className="flex items-center gap-1.5 text-xs text-ink-3">
        <LoaderCircle size={12} className="animate-spin" />
        {t("github.waiting")}
      </span>
    </div>
  );
}

function PasteToken({ done }: { done: () => void }) {
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const go = () => {
    if (!token.trim() || busy) return;
    setBusy(true);
    setErr("");
    api.bindGitHub(token.trim()).then(
      () => (qc.invalidateQueries({ queryKey: ["github"] }), done()),
      (e) => (setBusy(false), setErr(e instanceof ApiError ? e.message : String(e))),
    );
  };
  return (
    <div className="flex flex-col gap-2">
      <span className="text-sm font-medium">{t("github.bind")}</span>
      <input
        autoFocus
        type="password"
        autoComplete="off"
        value={token}
        placeholder={t("github.token")}
        aria-label={t("github.token")}
        onChange={(e) => setToken(e.target.value)}
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing) return;
          if (e.key === "Enter") go();
        }}
        className="no-ring h-8 w-full min-w-0 rounded-md bg-fill px-2 font-mono text-[13px] select-text"
      />
      <span className="text-xs text-ink-3">
        {t("github.tokenHint")}{" "}
        <a href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer" className="text-ink-2 underline-offset-2 hover:underline">
          {t("github.newToken")}
        </a>
      </span>
      {err && <span className="text-xs text-err">{err}</span>}
      <MainKey busy={busy} disabled={!token.trim()} onClick={go} className="self-end">
        {t("github.bindKey")}
      </MainKey>
    </div>
  );
}
