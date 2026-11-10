// Command lines at a session's machine and folder, without its agent (docs/api.md "命令"): typed
// in the composer after a $, run there, shown above the composer as they come. Each session keeps
// its folder (cd moves it, runs nothing) and its last runs while the page is open.
import { LoaderCircle, Square, SquareTerminal, X } from "lucide-react";
import { useSyncExternalStore } from "react";
import { api } from "../api/client";
import type { SessionState as LiveSession } from "../api/types.gen";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { useMachines } from "../data";
import { IconKey } from "../ui";

type Run = { key: string; line: string; cwd: string; run?: string; out: string; done: boolean; code?: number; signal?: string; err?: string; stopping?: boolean };
type Shell = { cwd?: string; runs: Run[] };

const KEEP = 5; // runs shown
const MAX = 200_000; // characters of a run's output kept

const shells = new Map<string, Shell>();
const subs = new Set<() => void>();
const changed = (id: string, s: Shell) => (shells.set(id, { ...s, runs: [...s.runs] }), subs.forEach((f) => f()));
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));
const EMPTY: Shell = { runs: [] };
const useShell = (id: string) => useSyncExternalStore(subscribe, () => shells.get(id) ?? EMPTY);

/** A command line: what follows a leading $, or null. */
export const shellLine = (text: string): string | null => {
  const m = /^\$\s*([\s\S]*)$/.exec(text.trim());
  return m ? m[1].trim() : null;
};

/** Whether command lines run where session s does (not at a machine that refuses them). */
export function useShellable(s: LiveSession): boolean {
  const ms = useMachines();
  return !s.machine || !!ms.find((m) => m.id === s.machine)?.shell;
}

/** Runs line at session s; resolves once it started (its output comes on by itself). */
export async function runShell(s: LiveSession, line: string): Promise<void> {
  const sh = shells.get(s.id) ?? { runs: [] };
  const r: Run = { key: crypto.randomUUID(), line, cwd: sh.cwd ?? s.cwd, out: "", done: false };
  let st;
  try {
    st = await api.shell(s.id, line, sh.cwd);
  } catch (e) {
    return void toast(t("shell.failed"), { error: true, description: errText(e) });
  }
  const now = shells.get(s.id) ?? sh;
  if (!st.run) return changed(s.id, { ...now, cwd: st.cwd }); // a cd
  Object.assign(r, { run: st.run, cwd: st.cwd });
  changed(s.id, { cwd: st.cwd, runs: [...now.runs, r].slice(-KEEP) });
  void follow(s.id, r);
}

/** Reads r's output till it ends. */
async function follow(id: string, r: Run) {
  let from = 0;
  for (let wait = 150; !r.done; ) {
    try {
      const o = await api.shellRead(r.run!, from);
      from = o.next;
      if (o.data) r.out = (r.out + o.data).slice(-MAX);
      if (o.done) Object.assign(r, { done: true, code: o.code, signal: o.signal, err: o.err });
      wait = o.data ? 150 : Math.min(wait * 1.5, 1000);
    } catch (e) {
      Object.assign(r, { done: true, err: errText(e) });
    }
    const sh = shells.get(id);
    if (sh) changed(id, sh);
    if (!r.done) await new Promise((f) => setTimeout(f, wait));
  }
}

async function stopRun(id: string, r: Run) {
  r.stopping = true;
  changed(id, shells.get(id)!);
  try {
    await api.shellStop(r.run!);
  } catch (e) {
    r.stopping = false;
    toast(t("shell.failed"), { error: true, description: errText(e) });
  }
}

/** The runs above the composer; also the folder once a cd moved it. */
export function ShellRuns({ s }: { s: LiveSession }) {
  const sh = useShell(s.id);
  if (!sh.runs.length && (!sh.cwd || sh.cwd === s.cwd)) return null;
  const close = () => changed(s.id, { cwd: sh.cwd, runs: sh.runs.filter((r) => !r.done) });
  return (
    <section aria-label={t("shell.title")} className="g-pop glass float rest mb-2 overflow-hidden rounded-lg text-sm">
      <div className="flex h-8 items-center gap-1.5 pr-1 pl-3 text-ink-3">
        <SquareTerminal size={14} aria-hidden />
        <span className="min-w-0 truncate font-mono text-xs" title={sh.cwd ?? s.cwd}>
          {sh.cwd ?? s.cwd}
        </span>
        <IconKey label={t("shell.clear")} onClick={close} className="ml-auto size-7 pointer-coarse:size-9">
          <X size={14} />
        </IconKey>
      </div>
      <ol className="m-0 max-h-[40vh] list-none overflow-y-auto p-0">
        {sh.runs.map((r) => (
          <li key={r.key} className="border-t border-edge px-3 py-1.5">
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate font-mono text-xs text-ink">$ {r.line}</code>
              <span className="shrink-0 text-xs text-ink-3 tabular-nums">{state(r)}</span>
              {!r.done && (
                <IconKey label={t("shell.stop")} disabled={r.stopping} onClick={() => void stopRun(s.id, r)} className="size-6 pointer-coarse:size-8">
                  {r.stopping ? <LoaderCircle size={12} className="animate-spin" /> : <Square size={10} fill="currentColor" />}
                </IconKey>
              )}
            </div>
            {(r.out || r.err) && (
              <pre className="m-0 mt-1 max-h-60 overflow-auto font-mono text-xs leading-[18px] whitespace-pre-wrap text-ink-2">
                {r.out}
                {r.err && <span className="text-err">{r.err}</span>}
              </pre>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

function state(r: Run): string {
  if (!r.done) return t("shell.running");
  if (r.signal) return t("shell.signal", { signal: r.signal });
  if (r.code === undefined) return t("shell.ended");
  return t("shell.code", { code: String(r.code) });
}
