// A quote pressed goes to the message it answers (kumo, 2026-11-21): scrolled to the middle of the
// chat and lit a moment; one the page has not got yet brought first (the log around it, or the
// pages before, as the page reads them).
import { flushSync } from "react-dom";
import { api, type ChannelMessage, type ChannelPage } from "../../api/client";
import { motion, reduced } from "../../motion";
import { letGo } from "../stick";
import type { ChannelLog } from "./log";

type Get = (id: string, q: { around?: number; after?: number; limit?: number }) => Promise<ChannelPage>;

/** Message n of log and those around it, when it is before those held (Satori's message.list
 *  around), and all between it and them: what is held stays one stretch. Says whether it brought
 *  any. */
export async function around(log: ChannelLog, n: number, get: Get = (id, q) => api.channelMessages(id, q)): Promise<boolean> {
  const first = log.msgs[0]?.n;
  if (first === undefined || n >= first || !log.more) return false;
  const p = await get(log.id, { around: n });
  const num = (m: ChannelMessage) => Number(m.id);
  const got = p.data.filter((m) => num(m) < first);
  if (!got.length) return false;
  // The gap after it, page by page.
  for (let to = num(got.at(-1)!); to < first - 1; ) {
    const q = await get(log.id, { after: to, limit: 500 });
    const more = q.data.filter((m) => num(m) > to && num(m) < first);
    if (!more.length) break;
    got.push(...more);
    to = num(more.at(-1)!);
  }
  if (log.msgs[0]?.n !== first) return true;
  log.take({ data: got, start: num(got[0]!), end: num(got.at(-1)!), more: p.more });
  return true;
}

/** Message n as drawn under root (its data-seq). */
export const seqEl = (root: ParentNode | null | undefined, n: number) => root?.querySelector<HTMLElement>(`[data-seq="${n}"]`) ?? null;

/** Goes to what find gives in the chat scroller: none yet, more brings more of the log (false:
 *  nothing came), drawn at once (draw), at most tries times. */
export async function goTo(scroller: HTMLElement | null, find: () => HTMLElement | null, more: () => Promise<boolean>, draw: () => void, tries = 1): Promise<void> {
  let el = find();
  for (let i = 0; !el && i < tries; i++) {
    if (!(await more())) break;
    flushSync(draw);
    el = find();
  }
  if (!el || !scroller) return;
  letGo(scroller);
  el.scrollIntoView({ block: "center", behavior: reduced() ? "auto" : "smooth" });
  spot(el);
}

/** Where the message pressed to is, a moment, gone by itself (kumo, 2026-11-26: a dark ground as
 *  wide as the row, with a ring, staying more than a second, was too much): a bubble's own ground
 *  a step deeper, an agent's words a faint ground just round them, fading out in under a second. */
function spot(el: HTMLElement) {
  const bubble = el.querySelector<HTMLElement>("[data-bubble-bg]");
  const css = getComputedStyle(document.documentElement);
  const tone = (bubble ? css.getPropertyValue("--fill-2") : css.getPropertyValue("--fill")).trim() || "rgba(127,127,127,.12)";
  const at = { duration: motion.dur(4) * 2, easing: motion.ease("out"), delay: reduced() ? 0 : motion.dur(3) };
  if (bubble) {
    const own = getComputedStyle(bubble).backgroundColor;
    return void bubble.animate([{ backgroundColor: tone }, { backgroundColor: tone, offset: 0.25 }, { backgroundColor: own }], at);
  }
  el.style.borderRadius = "8px";
  el.animate([{ backgroundColor: tone, boxShadow: `0 0 0 4px ${tone}` }, { backgroundColor: tone, boxShadow: `0 0 0 4px ${tone}`, offset: 0.25 }, { backgroundColor: "transparent", boxShadow: "0 0 0 4px transparent" }], at);
}
