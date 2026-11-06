// The push menus (push.ts): a level's 跟随上级 / 开 / 关 and its events, and the 通知 submenu of a
// folder's or a session's menu. Fetched only once the total switch is seen on.
import { Menu } from "@base-ui/react/menu";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Bell, BellOff, BellRing, Check, ChevronRight, CircleAlert, FoldVertical, MessageSquareDot } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { api } from "./api/client";
import type { PushEvents, PushMode, PushRule, PushState } from "./api/types.gen";
import { t } from "./i18n";
import { pushEvents, pushOn, ruleAt, type PushLevel } from "./push";
import { errText, toast } from "./toast";

export const PUSH_EVENTS: { k: keyof PushEvents; icon: ReactNode; label: Parameters<typeof t>[0] }[] = [
  { k: "needsYou", icon: <BellRing size={15} />, label: "push.needsYou" },
  { k: "error", icon: <CircleAlert size={15} />, label: "push.error" },
  { k: "turnEnd", icon: <MessageSquareDot size={15} />, label: "push.turnEnd" },
  { k: "compact", icon: <FoldVertical size={15} />, label: "push.compact" },
];

const same = (a: PushEvents, b: PushEvents) => PUSH_EVENTS.every(({ k }) => a[k] === b[k]);

/** Sets the rule at once in the page, then as the daemon answers; failed, as it was. */
function setRule(qc: QueryClient, st: PushState, r: PushRule) {
  const rest = st.rules.filter((x) => !(x.scope === r.scope && x.target === r.target));
  qc.setQueryData(["push"], { ...st, rules: r.mode === "inherit" ? rest : [...rest, r] });
  api.pushRule(r).then(
    (next) => qc.setQueryData(["push"], next),
    (e) => (qc.invalidateQueries({ queryKey: ["push"] }), toast(t("err.settings"), { error: true, description: errText(e) })),
  );
}

const ITEM = "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-disabled:opacity-40 pointer-coarse:h-11";

/** The choices of one level: 跟随上级 (with what that is, faint), 开, 关; under them its events,
 *  ticked as they now are, to be changed only while it is 开. i: where the menu's stagger is. */
export function PushItems({ st, levels, i = 0 }: { st: PushState; levels: PushLevel[]; i?: number }) {
  const qc = useQueryClient();
  const [self, ...above] = levels as [PushLevel, ...PushLevel[]];
  const mode: PushMode = ruleAt(st, self)?.mode ?? "inherit";
  const events = mode === "on" ? pushEvents(st, levels) : pushEvents(st, above);
  const set = (r: Omit<PushRule, "scope" | "target">) => setRule(qc, st, { ...self, ...r });
  const modes: { m: PushMode; label: string; hint?: string }[] = [
    { m: "inherit", label: above.length ? t("push.inherit") : t("push.inheritDefault"), hint: pushOn(st, above) ? t("push.on") : t("push.off") },
    { m: "on", label: t("push.on") },
    { m: "off", label: t("push.off") },
  ];
  // Plain items with the radio's and the checkbox's roles, ticked in a column of their own.
  const tick = (on: boolean) => <span className="grid w-4 shrink-0 place-items-center text-ink-2">{on && <Check size={14} strokeWidth={2.5} className="g-pop" />}</span>;
  return (
    <>
      <div role="group" style={{ "--i": i } as CSSProperties} className="flex flex-col">
        {modes.map(({ m, label, hint }) => (
          <Menu.Item key={m} role="menuitemradio" aria-checked={mode === m} closeOnClick={false} onClick={() => mode !== m && set({ mode: m })} className={ITEM}>
            {tick(mode === m)}
            <span className="flex-1">{label}</span>
            {hint && <span className="text-xs text-ink-3">{hint}</span>}
          </Menu.Item>
        ))}
      </div>
      <div role="separator" style={{ "--i": i + 1 } as CSSProperties} className="mx-1 my-1.5 h-px bg-edge" />
      {PUSH_EVENTS.map((e, j) => (
        <Menu.Item
          key={e.k}
          role="menuitemcheckbox"
          aria-checked={events[e.k]}
          disabled={mode !== "on"}
          closeOnClick={false}
          onClick={() => {
            const next = { ...events, [e.k]: !events[e.k] };
            // As the level above's again: it follows them, not a copy of them.
            set({ mode: "on", events: same(next, pushEvents(st, above)) ? undefined : next });
          }}
          style={{ "--i": i + 2 + j } as CSSProperties}
          className={ITEM}
        >
          <span className="text-ink-2">{e.icon}</span>
          <span className="flex-1">{t(e.label)}</span>
          {tick(events[e.k])}
        </Menu.Item>
      ))}
    </>
  );
}

/** 通知 in a folder's or a session's menu: a submenu of PushItems; the row's state at its end. */
export function PushSubmenu({ st, levels, i }: { st: PushState; levels: PushLevel[]; i: number }) {
  const on = pushOn(st, levels);
  const own = ruleAt(st, levels[0]!);
  return (
    <Menu.SubmenuRoot>
      <Menu.SubmenuTrigger style={{ "--i": i } as CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill pointer-coarse:h-11">
        <span className="text-ink-2">{on ? <Bell size={15} /> : <BellOff size={15} />}</span>
        <span className="flex-1">{t("push.menu")}</span>
        <span className="text-xs text-ink-3">{own ? (own.mode === "on" ? t("push.on") : t("push.off")) : t("push.inherit")}</span>
        <ChevronRight size={14} className="text-ink-4" />
      </Menu.SubmenuTrigger>
      <Menu.Portal>
        <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
          <Menu.Popup className="g-unfold glass thick float min-w-52 rounded-lg p-1.5 text-ink outline-none">
            <div className="g-stagger flex flex-col">
              <PushItems st={st} levels={levels} />
            </div>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.SubmenuRoot>
  );
}
