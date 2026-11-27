// GitHub in 管理 (manage.tsx): the account a person bound, whose git and gh their agents' turns act
// as (docs/api.md "GitHub"), as what its line unfolds, in the window's own way: one line, the
// words at the left and the keys at the right (kumo, 2026-11-27: not a popover's card dropped
// in). Bound: when, and 重新绑定 (one's own) and 解绑. Binding: by GitHub's link (its device flow:
// the code shown here, typed at github.com/login/device, the server asked again at GitHub's pace
// till it is allowed), or a token pasted.
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, LoaderCircle, Unlink } from "lucide-react";
import { useEffect, useState } from "react";
import { api, ApiError } from "./api/client";
import type { GitHubDevice, GitHubList } from "./api/types.gen";
import { useQuery } from "./api/query";
import { keptQuery } from "./data";
import { MainKey, SecondKey } from "./card";
import { t } from "./i18n";
import { ago, Avatar, CopyKey, SureKey } from "./ui";

/** Kept across reloads (data.ts keptQuery): the line drawn whole at once. */
export const githubQ = { queryKey: ["github"], staleTime: 60_000, ...keptQuery("github", () => api.github()) };

/** GitHub's mark (its octocat outline is not among the icons). */
export function GitHubMark({ size = 15, className }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 16 16" width={size} height={size} aria-hidden className={className} fill="currentColor">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

/** A bound account as its line's value: its GitHub avatar and @login. */
export function GitHubWho({ login, avatar }: { login: string; avatar?: string }) {
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <Avatar src={avatar} name={login} size={18} />
      <span className="truncate">@{login}</span>
    </span>
  );
}

const LINE = "flex min-h-11 items-center gap-2";
const TEXT_KEY = "shrink-0 px-1 text-sm text-ink-3 hover:text-ink-2";
const errOf = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

/** What member's GitHub line unfolds: bound, when, and its keys; else binding (mine: one's own,
 *  the only one a person binds). */
export function GitHubFold({ member, mine }: { member: string; mine: boolean }) {
  const qc = useQueryClient();
  const b = useQuery<GitHubList>(githubQ).data?.bindings.find((x) => x.member === member);
  const [again, setAgain] = useState(false);
  if (b && !again)
    return (
      <div className={LINE}>
        <a href={`https://github.com/${b.login}`} target="_blank" rel="noreferrer" className="flex min-w-0 flex-1 items-center gap-1 truncate text-sm text-ink-3 hover:text-ink-2">
          {t("github.boundAt", { when: ago(b.at) })}
          <ExternalLink size={12} className="shrink-0" />
        </a>
        {mine && <SecondKey onClick={() => setAgain(true)}>{t("github.rebind")}</SecondKey>}
        <SureKey label={t("github.unbind")} sure={t("github.unbindSure")} run={() => void (mine ? api.unbindGitHub() : api.unbindGitHub(member)).then(() => qc.invalidateQueries({ queryKey: ["github"] }))} className="size-8">
          <Unlink size={15} />
        </SureKey>
      </div>
    );
  if (!mine) return null;
  return <Bind done={() => setAgain(false)} cancel={b ? () => setAgain(false) : undefined} />;
}

/** Binding, by GitHub's link first; cancel: back to the account bound. */
function Bind({ done, cancel }: { done: () => void; cancel?: () => void }) {
  const qc = useQueryClient();
  const [paste, setPaste] = useState(false);
  const [flow, setFlow] = useState<GitHubDevice | null>(null);
  const [state, setState] = useState<"" | "waiting" | "expired" | "denied">("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [token, setToken] = useState("");
  const bound = () => (qc.invalidateQueries({ queryKey: ["github"] }), done());
  const begin = () => {
    setBusy(true);
    setErr("");
    api.githubDevice().then(
      (d) => (setFlow(d), setState("waiting"), setBusy(false)),
      (e) => (setErr(errOf(e)), setBusy(false)),
    );
  };
  const put = () => {
    if (!token.trim() || busy) return;
    setBusy(true);
    setErr("");
    api.bindGitHub(token.trim()).then(bound, (e) => (setBusy(false), setErr(errOf(e))));
  };
  // Asked again at GitHub's pace while it waits; gone when the page is.
  useEffect(() => {
    if (!flow || state !== "waiting") return;
    let live = true;
    const timer = setTimeout(() => {
      api.githubDeviceState(flow.id).then(
        (s) => {
          if (!live) return;
          if (s.state === "done") return void bound();
          if (s.state === "waiting") setFlow({ ...flow });
          else setState(s.state === "denied" ? "denied" : "expired");
        },
        (e) => live && (setErr(errOf(e)), setState("expired")),
      );
    }, flow.interval * 1000);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [flow, state]); // eslint-disable-line react-hooks/exhaustive-deps
  if (paste)
    return (
      <form
        className={LINE}
        onSubmit={(e) => {
          e.preventDefault();
          put();
        }}
      >
        <input autoFocus type="password" autoComplete="off" value={token} placeholder={t("github.token")} aria-label={t("github.token")} onChange={(e) => setToken(e.target.value)} className="no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2.5 font-mono text-[13px] select-text" />
        <button type="button" onClick={() => (setPaste(false), setErr(""))} className={TEXT_KEY}>
          {t("me.cancel")}
        </button>
        <MainKey type="submit" busy={busy} disabled={!token.trim()}>
          {t("github.bindKey")}
        </MainKey>
      </form>
    );
  if (flow && state === "waiting")
    return (
      <div className={LINE}>
        <code className="font-mono text-lg font-semibold tracking-widest select-all">{flow.code}</code>
        <CopyKey text={flow.code} />
        <span className="flex min-w-0 flex-1 items-center justify-end gap-1.5 truncate text-sm text-ink-3">
          <LoaderCircle size={13} className="shrink-0 animate-spin" />
          {t("github.waiting")}
        </span>
        <a href={flow.uri} target="_blank" rel="noreferrer" onClick={() => void navigator.clipboard?.writeText(flow.code).catch(() => {})} className="g-press flex h-8 shrink-0 items-center gap-1.5 rounded-md bg-ink px-3 text-sm font-medium text-on-ink pointer-coarse:h-10">
          {t("github.open")}
          <ExternalLink size={13} />
        </a>
      </div>
    );
  return (
    <div className={LINE}>
      <span className={err ? "min-w-0 flex-1 truncate text-sm text-err" : "min-w-0 flex-1 truncate text-sm text-ink-3"}>{err || (state === "denied" ? t("github.denied") : state === "expired" ? t("github.expired") : "")}</span>
      {cancel && (
        <button type="button" onClick={cancel} className={TEXT_KEY}>
          {t("me.cancel")}
        </button>
      )}
      <button type="button" onClick={() => (setPaste(true), setErr(""))} className={TEXT_KEY}>
        {t("github.usePaste")}
      </button>
      <MainKey busy={busy} onClick={begin}>
        {t("github.authorize")}
      </MainKey>
    </div>
  );
}
