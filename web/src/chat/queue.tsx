// The queue above the composer (docs/api.md "待发队列"; Rhodium spec web-write 283–286): what you
// typed while a turn ran, to go as the next turn. Each line: steer (into the turn now; send, when
// paused), edit (in place: Enter keeps it, Esc leaves it as it was), delete. Interrupted or
// stopped, the queue pauses: a pause sign and continue. With two or more, one key sends them all now,
// in order: the first joins the turn (or starts one), the rest join it. Its height glides as a line
// comes or goes; a new line fades in.
import { Check, CornerUpRight, List, ListEnd, LoaderCircle, Paperclip, Pause, Pencil, Play, X } from "lucide-react";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { api, missing } from "../api/client";
import type { Queued } from "../api/types.gen";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { motion, reduced } from "../motion";
import { useSettings } from "../data";
import { cn, enterKey, IconKey, steerKey } from "../ui";
import { attachedOf } from "./refs";

/** The queue's height glides from what it was; lines not there before fade in. */
function useGrow(box: React.RefObject<HTMLElement | null>, ids: string[]) {
  const was = useRef<number | null>(null);
  const seen = useRef<Set<string> | null>(null);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return void ((was.current = null), (seen.current = null));
    const h = el.offsetHeight;
    const calm = reduced();
    if (was.current !== null && was.current !== h && !calm) el.animate([{ height: `${was.current}px` }, { height: `${h}px` }], { duration: motion.dur(2), easing: motion.ease("out") });
    if (seen.current)
      el.querySelectorAll<HTMLElement>("li[data-q]").forEach(
        (li) => seen.current!.has(li.dataset.q!) || li.animate([{ opacity: 0 }, { opacity: 1 }], { duration: motion.dur(2), easing: motion.ease("out"), delay: calm ? 0 : 60, fill: "backwards" }),
      );
    seen.current = new Set(ids);
    was.current = h;
  });
}

/** steered: a line sent into the turn, shown as yours till the transcript has it. */
export function Queue({ session, queue, steered }: { session: string; queue: Queued[]; steered: (q: Queued) => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(null);
  const box = useRef<HTMLElement>(null);
  useGrow(box, queue.map((q) => q.id));
  // The key that steers while typing, as the setting has it.
  const typed = (useSettings().data?.enterInTurn ?? "steer") === "queue" ? steerKey() : enterKey();
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
  const save = (q: Queued, text: string) => {
    const to = text.trim();
    if (!to || to === q.text) return setEditing(null);
    void run(`${q.id} edit`, () => api.editQueued(session, q.id, to), () => setEditing(null));
  };
  return (
    <section ref={box} aria-label={t("queue.title")} className="g-pop glass float rest mb-2 overflow-hidden rounded-lg text-sm">
      <div className="flex h-8 items-center gap-1.5 pr-1 pl-3 text-ink-3">
        <span role="img" aria-label={paused ? t("queue.paused") : t("queue.title")}>
          {paused ? <Pause size={14} /> : <List size={14} />}
        </span>
        <span className="tabular-nums">{queue.length}</span>
        <span className="ml-auto flex">
          {queue.length > 1 &&
            key("all", t("queue.all"), <ListEnd size={14} />, () =>
              void run("all", async () => {
                // One gone meanwhile (sent, deleted) is no matter.
                for (const q of queue)
                  await api.steer(session, q.id).then(
                    () => steered(q),
                    (e) => {
                      if (!missing(e)) throw e;
                    },
                  );
              }),
            )}
          {paused && key("go", t("queue.continue"), <Play size={14} />, () => void run("go", () => api.continueQueue(session)))}
        </span>
      </div>
      <ul className="m-0 max-h-[7.5rem] list-none overflow-y-auto p-0 pb-1 shadow-[inset_0_0.5px_0_var(--edge)]">
        {queue.map((q) =>
          editing?.id === q.id ? (
            <li key={q.id} data-q={q.id} className="flex min-h-8 items-center gap-0.5 pr-1 pl-2">
              <input
                autoFocus
                aria-label={t("queue.edit")}
                value={editing.text}
                onChange={(e) => setEditing({ id: q.id, text: e.target.value })}
                onKeyDown={(e) => {
                  if (e.nativeEvent.isComposing || e.keyCode === 229) return;
                  if (e.key === "Enter") (e.preventDefault(), save(q, editing.text));
                  else if (e.key === "Escape") (e.stopPropagation(), setEditing(null));
                }}
                className="no-ring h-7 min-w-0 flex-1 rounded-md bg-fill px-2 text-sm"
              />
              {key(`${q.id} edit`, t("queue.save"), <Check size={14} />, () => save(q, editing.text))}
              {key(`${q.id} cancel`, t("me.cancel"), <X size={14} />, () => setEditing(null))}
            </li>
          ) : (
            <li key={q.id} data-q={q.id} className="flex min-h-8 items-center gap-0.5 pr-1 pl-3">
              <QueuedText text={q.text} />
              {key(`${q.id} steer`, q.paused ? t("queue.sendNow") : t("queue.steer", { key: typed }), <CornerUpRight size={14} />, () => void run(`${q.id} steer`, () => api.steer(session, q.id), () => steered(q)))}
              {key(`${q.id} edit`, t("queue.edit"), <Pencil size={14} />, () => setEditing({ id: q.id, text: q.text }))}
              {key(`${q.id} drop`, t("queue.drop"), <X size={14} />, () => void run(`${q.id} drop`, () => api.unqueue(session, q.id)))}
            </li>
          ),
        )}
      </ul>
    </section>
  );
}

/** A queued message on its line: its files (lines [附件] `path`) by name after a clip, not as
 *  those lines. */
function QueuedText({ text }: { text: string }) {
  const { paths, words } = attachedOf(text);
  return (
    <p className="m-0 min-w-0 flex-1 truncate py-1">
      {words}
      {paths.length > 0 && (
        <span className={cn("text-ink-3", words && "ml-2")}>
          <Paperclip size={12} className="mr-0.5 inline align-[-1px]" aria-hidden />
          {paths.map((p) => p.slice(p.lastIndexOf("/") + 1)).join(", ")}
        </span>
      )}
    </p>
  );
}
