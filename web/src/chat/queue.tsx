// The queue above the composer (docs/api.md "待发队列"; Rhodium spec web-write 283–286): what you
// typed while a turn ran, to go as the next turn. Each line: steer (into the turn now; send, when
// paused), edit (back into the composer), delete. Interrupted or stopped, the queue pauses: a pause
// sign and continue.
import { CornerUpRight, List, LoaderCircle, Pause, Pencil, Play, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Queued } from "../api/types.gen";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { IconKey } from "../ui";

export function Queue({ session, queue, edit }: { session: string; queue: Queued[]; edit: (text: string) => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  if (!queue.length) return null;
  const paused = queue.some((q) => q.paused);
  const run = async (key: string, f: () => Promise<unknown>, then?: () => void) => {
    if (busy) return;
    setBusy(key);
    try {
      await f();
      then?.();
    } catch (e) {
      toast(t("err.action"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const key = (k: string, label: string, icon: ReactNode, press: () => void) => (
    <IconKey key={label} label={label} disabled={busy !== null} onClick={press} className="size-7 pointer-coarse:size-9">
      {busy === k ? <LoaderCircle size={14} className="animate-spin" /> : icon}
    </IconKey>
  );
  return (
    <section aria-label={t("queue.title")} className="g-pop glass float mb-2 rounded-lg text-sm">
      <div className="flex h-8 items-center gap-1.5 pr-1 pl-3 text-ink-3">
        <span role="img" aria-label={paused ? t("queue.paused") : t("queue.title")}>
          {paused ? <Pause size={14} /> : <List size={14} />}
        </span>
        <span className="tabular-nums">{queue.length}</span>
        {paused && <span className="ml-auto">{key("go", t("queue.continue"), <Play size={14} />, () => void run("go", () => api.continueQueue(session)))}</span>}
      </div>
      <ul className="m-0 max-h-[7.5rem] list-none overflow-y-auto p-0 pb-1 shadow-[inset_0_0.5px_0_var(--edge)]">
        {queue.map((q) => (
          <li key={q.id} className="flex min-h-8 items-center gap-0.5 pr-1 pl-3">
            <p className="m-0 min-w-0 flex-1 truncate py-1">{q.text}</p>
            {key(`${q.id} steer`, q.paused ? t("queue.sendNow") : t("queue.steer"), <CornerUpRight size={14} />, () => void run(`${q.id} steer`, () => api.steer(session, q.id)))}
            {key(`${q.id} edit`, t("queue.edit"), <Pencil size={14} />, () => void run(`${q.id} edit`, () => api.unqueue(session, q.id), () => edit(q.text)))}
            {key(`${q.id} drop`, t("queue.drop"), <X size={14} />, () => void run(`${q.id} drop`, () => api.unqueue(session, q.id)))}
          </li>
        ))}
      </ul>
    </section>
  );
}
