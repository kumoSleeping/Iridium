// What a message's menu does to it besides 引用 and 复制 (docs/channels.md "表情" "撤回"): its
// top, two rows of the emoji pressed lately, then 更多, the whole table in a menu of its own; its
// foot, 撤回 (pressed once it turns red, the second press takes the message back for everyone).
// Under the message, its reactions: each emoji with how many put it on, yours marked; the emoji
// pressed puts yours on or takes it off, the count (or a long press, a right click) shows who.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { ChevronRight, SmilePlus, Undo2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { sat } from "../api/client";
import { LOCAL_ME, useApps, useMe } from "../data";
import { t } from "../i18n";
import { useList } from "../store";
import { errText, toast } from "../toast";
import { Avatar, cn, own, PopSurface, useLongPress } from "../ui";
import { Face } from "./channel/face";
import { acts } from "./copyable";
import { EMOJI, recentEmoji, ROW, ROWS, usedEmoji } from "./emoji";
import { refOf, useBlockSeq, useReactions, type BlockOf, type ReactionSum, type Reactor } from "./reactions";

export { reactionLine, recalls } from "./reactions";

/** A message as what may be done to it: in channel, its number (seq) or, a block of the channel's
 *  own session's record, the block (key: its place, or a person's input); may: the one looking
 *  is one of the channel's people (it speaks there, so it reacts and takes back). side: under a
 *  bubble with a face beside it (left, right), its reactions under the bubble. */
export type Act = { channel: string; seq?: number; key?: BlockOf; may: boolean; side?: "left" | "right" };

const ITEM = "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11";
const POPUP = "g-unfold glass thick float rounded-lg text-ink outline-none";

/** act's number, as given or as its block is known; and what the server is to be told it is by. */
function useTarget(act: Act): { seq?: number; id: string } {
  const known = useBlockSeq(act.channel, act.seq ? undefined : act.key);
  const seq = act.seq ?? known;
  return { seq, id: seq ? String(seq) : act.key ? refOf(act.channel, act.key) : "" };
}

/** Whether user is the one looking (its login; the host's page, Iridium). */
function useMine(): (u: Reactor) => boolean {
  const me = useMe().data ?? LOCAL_ME;
  return (u) => u.id === me.id || (!!me.host && u.id === "local");
}

/** Puts the one looking's emoji on message id of channel (its number, or what it is from: an
 *  input's id, "b:<session>:<at>"), or takes it off (on: it has it now): Satori's reactions. */
function toggle(act: Act, id: string, emoji: string, on: boolean) {
  usedEmoji(emoji);
  void sat(on ? "reaction.delete" : "reaction.create", { channel_id: act.channel, message_id: id, emoji_id: emoji }).catch(failed);
}
const failed = (e: unknown) => void toast(errText(e), { error: true });

/** The menu's top: two rows of the emoji pressed lately, then 更多. */
export function ReactRows({ act }: { act: Act }) {
  const { seq, id } = useTarget(act);
  const list = useReactions(act.channel, seq);
  const mine = useMine();
  const [recent] = useState(recentEmoji);
  if (!act.may || !id) return null;
  const has = (e: string) => list.some((r) => r.emoji === e && r.users.some(mine));
  const key = (e: string) => (
    <Menu.Item key={e} onClick={() => toggle(act, id, e, has(e))} aria-label={e} aria-pressed={has(e)} className={cn("grid size-8 cursor-default place-items-center rounded-md text-md leading-none outline-none select-none data-highlighted:bg-fill pointer-coarse:size-10", has(e) && "bg-fill-2")}>
      {e}
    </Menu.Item>
  );
  return (
    <>
      <div className="grid grid-cols-6 justify-items-center">{recent.slice(0, ROW * ROWS).map(key)}</div>
      <Menu.SubmenuRoot>
        <Menu.SubmenuTrigger className={ITEM}>
          <span className="text-ink-2">
            <SmilePlus size={15} />
          </span>
          <span className="min-w-0 flex-1 truncate">{t("top.more")}</span>
          <ChevronRight size={14} className="shrink-0 text-ink-4" />
        </Menu.SubmenuTrigger>
        <Menu.Portal>
          <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
            <Menu.Popup className={cn(POPUP, "no-bar max-h-[min(360px,var(--available-height))] w-[min(300px,calc(100vw-16px))] overflow-y-auto p-1.5")}>
              {EMOJI.map((g, i) => (
                <div key={g.key} className={cn("grid grid-cols-8 justify-items-center", i > 0 && "mt-1 border-t border-edge pt-1")}>
                  {g.list.map(key)}
                </div>
              ))}
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.SubmenuRoot>
      <div className="mx-1 my-1 border-t border-edge" />
    </>
  );
}

/** The menu's 撤回: pressed once, red and 确认撤回, the menu staying open; again, the message is
 *  taken back for everyone (docs/前端设计规范.md "确认在原处"). */
export function RecallItem({ act }: { act: Act }) {
  const { id } = useTarget(act);
  const [armed, setArmed] = useState(false);
  if (!act.may || !id) return null;
  return (
    <Menu.Item
      closeOnClick={armed}
      onClick={() => (armed ? void sat("message.delete", { channel_id: act.channel, message_id: id }).catch(failed) : setArmed(true))}
      className={cn(ITEM, armed && "text-err")}
    >
      <span className={armed ? "text-err" : "text-ink-2"}>
        <Undo2 size={15} />
      </span>
      {armed ? t("msg.recallSure") : t("msg.takeBack")}
    </Menu.Item>
  );
}

/** The reactions under a message: none, nothing (the place they take slides open as the first
 *  comes, shut as the last goes, docs/前端设计规范.md "出现、消失都要动画"). */
export function Reactions({ act }: { act: Act }) {
  const { seq, id } = useTarget(act);
  const list = useReactions(act.channel, seq);
  const mine = useMine();
  // What was there stays while it shuts; what came with the message is there at once, what
  // comes later slides open.
  const last = useRef<ReactionSum[]>(list);
  if (list.length) last.current = list;
  const shown = list.length ? list : last.current;
  const [open, setOpen] = useState(list.length > 0);
  useEffect(() => {
    if (!list.length) return setOpen(false);
    const f = requestAnimationFrame(() => setOpen(true));
    return () => cancelAnimationFrame(f);
  }, [list.length > 0]);
  if (!shown.length) return null;
  return (
    <div className={cn("grid transition-[grid-template-rows] duration-(--dur-2) ease-out", open && list.length ? "grid-rows-[1fr]" : "grid-rows-[0fr]")}>
      <div className="min-h-0 overflow-hidden">
        <div className={cn("flex flex-wrap gap-1 pt-1.5", act.side === "left" && "pl-[38px]", act.side === "right" && "justify-end pr-[38px]")}>
          {shown.map((r) => (
            <Chip key={r.emoji} r={r} on={r.users.some(mine)} press={act.may && id ? () => toggle(act, id, r.emoji, r.users.some(mine)) : undefined} />
          ))}
        </div>
      </div>
    </div>
  );
}

/** One emoji under a message: pressed, yours on or off; its count (a long press, a right click
 *  on it) shows who put it on. */
function Chip({ r, on, press }: { r: ReactionSum; on: boolean; press?: () => void }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLSpanElement>(null);
  const lp = useLongPress(() => setOpen(true));
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <span
        ref={box}
        {...lp}
        // The message's own long press is not this one's.
        onPointerDown={(e) => (e.stopPropagation(), lp.onPointerDown(e))}
        onContextMenu={(e) => {
          e.stopPropagation();
          if (lp.onContextMenu(e) || !own(e)) return;
          e.preventDefault();
          setOpen(true);
        }}
        className={cn("g-fade inline-flex h-6 items-center rounded-full text-xs select-none [-webkit-touch-callout:none] pointer-coarse:h-7", on ? "bg-fill-2 text-ink shadow-[inset_0_0_0_0.5px_var(--edge)]" : "bg-fill text-ink-3")}
      >
        <button type="button" disabled={!press} onClick={press} aria-label={r.emoji} aria-pressed={on} className="g-press grid h-full place-items-center pr-1 pl-2 text-sm leading-none">
          {r.emoji}
        </button>
        <button type="button" onClick={() => setOpen(true)} aria-label={`${r.emoji} ${r.count}`} className="h-full pr-2 pl-0.5 tabular-nums">
          {r.count}
        </button>
      </span>
      <PopSurface anchor={box.current} still align="start" className="no-bar max-h-[min(360px,var(--available-height))] min-w-44 overflow-y-auto p-1.5">
        <div className="truncate px-2.5 pt-1 pb-0.5 text-xs text-ink-3">
          {r.emoji} {r.count}
        </div>
        {r.users.map((u) => (
          <div key={u.id} className="flex h-8 items-center gap-2.5 px-2.5 text-base pointer-coarse:h-11">
            <ReactorFace u={u} />
            <span className="min-w-0 flex-1 truncate">{u.name || u.id}</span>
          </div>
        ))}
      </PopSurface>
    </Popover.Root>
  );
}

/** Who put an emoji on: an agent's own face, a login's avatar; Iridium's mark for the host. */
function ReactorFace({ u }: { u: Reactor }) {
  const list = useList();
  const d = useApps().data;
  if (u.id.startsWith("agent:")) {
    const s = list?.find((x) => x.id === u.id.slice(6));
    return (
      <span className="grid size-5 shrink-0 place-items-center rounded-full bg-fill">
        <Face id={u.id.slice(6)} agent={s?.agent ?? "claude"} size={20} logo={13} />
      </span>
    );
  }
  const login = d?.logins.find((l) => l.id === u.id);
  return <Avatar src={login?.avatar} name={u.name || u.id} size={20} host={u.id === "local"} />;
}

acts.Top = ReactRows;
acts.Foot = RecallItem;
acts.Under = Reactions;
