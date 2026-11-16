// A harness's own subagent as a person in its parent's chat (kumo, 2026-11-15: subagents are
// members that only read, no rows of theirs in the sidebar; 2026-11-16: 劳务派遣, 也算个人的):
// its face (its logo with the subagent badge), its task as its name, its card. At work, a turn
// of its own drawn live from its record (chat.tsx SubTurn); done, what it handed back is a
// message of its own (dispatched.tsx).
import { Popover } from "@base-ui/react/popover";
import { Bot, ScrollText } from "lucide-react";
import { createContext, useContext, useState } from "react";
import type { Agent, Session } from "../api/types.gen";
import { t } from "../i18n";
import { openChat } from "../route";
import { useList } from "../store";
import { working } from "../tree";
import { cn, Lamp, Logo, PopSurface } from "../ui";
import { CARD_POP, ProfileCard } from "./channel/profilecard";
import { subFor, type Dispatched } from "./dispatch";

/** What a chat lends the subagents drawn in it: parent, its session; by, what its agent is
 *  called (who dispatched them); d, the dispatches on its page. */
export const Dispatching = createContext<{ parent?: string; by?: string; d?: Dispatched }>({});

/** A subagent's name: its task; a workflow's agent, the workflow's name first. */
export const subName = (s: Pick<Session, "title" | "workflow">) => (s.workflow?.name ? `${s.workflow.name}${s.title ? ` · ${s.title}` : ""}` : s.title || t("row.subagent"));

/** A subagent as it is drawn: its session when the list has one; model: as it was dispatched. */
export type Sub = { name: string; agent: Agent; session?: Session; model?: string; working: boolean };

/** The subagent of a dispatch (of: its call's id and name; a session known already) in the chat
 *  of session parent. */
export function useSub(parent: string | undefined, of: { id?: string; name: string; model?: string; session?: Session }): Sub {
  const list = useList();
  const row = list?.find((x) => x.id === parent);
  const s = of.session ?? (parent ? subFor(of, (list ?? []).filter((x) => x.subagent && x.parent === parent)) : undefined);
  return { name: s && (s.workflow?.name || !of.name) ? subName(s) : of.name || t("row.subagent"), agent: s?.agent ?? row?.agent ?? "claude", ...(s ? { session: s } : {}), ...(of.model ? { model: of.model } : {}), working: !!s && working(s, row, Date.now()) };
}

/** Its face, size across: the harness's logo on a round ground, the subagent badge on its
 *  corner; under 24 the logo alone with the badge. */
export function SubFace({ agent, size = 28 }: { agent: Agent; size?: number }) {
  const small = size < 24;
  const badge = Math.max(11, Math.round(size * 0.46));
  return (
    <span className={cn("relative grid shrink-0 place-items-center", !small && "rounded-full bg-fill")} style={{ width: size, height: size }}>
      <Logo agent={agent} size={small ? size : Math.round(size * 0.57)} />
      <span role="img" aria-label={t("row.subagent")} className="absolute -right-0.5 -bottom-0.5 grid place-items-center rounded-full bg-paper text-ink-2" style={{ width: small ? 9 : badge, height: small ? 9 : badge }}>
        <Bot size={small ? 7 : Math.round(badge * 0.7)} strokeWidth={2.5} />
      </span>
    </span>
  );
}

/** Its face and its name on a line (the members' key, a line addressed to it). */
export function SubTag({ agent, name, className }: { agent: Agent; name: string; className?: string }) {
  return (
    <span className={cn("flex min-w-0 items-center gap-1.5", className)}>
      <SubFace agent={agent} size={16} />
      <span className="min-w-0 truncate">{name}</span>
    </span>
  );
}

/** Its card, the one card (profilecard.tsx), read only: its name, 派遣 and its model, at work
 *  or handed back, who dispatched it; a key to its record when it has a session. No @, no
 *  message, nothing to edit. */
export function SubCard({ sub, by, done }: { sub: Sub; by?: string; done?: () => void }) {
  const id = sub.session?.id;
  return (
    <ProfileCard
      face={<SubFace agent={sub.agent} size={56} />}
      name={sub.name}
      sub={[t("sub.kind"), sub.model].filter(Boolean).join(" · ")}
      state={
        <>
          <Lamp status={sub.working ? "running" : "idle"} />
          {t(sub.working ? "sub.working" : "sub.back")}
        </>
      }
      intro={by ? t("sub.by", { name: by }) : undefined}
      keys={id ? [{ icon: <ScrollText size={14} />, label: t("channel.session"), run: () => (done?.(), openChat(id)) }] : []}
    />
  );
}

/** Its face as a key: pressed, its card. */
export function SubFaceKey({ sub, size = 28 }: { sub: Sub; size?: number }) {
  const [open, setOpen] = useState(false);
  const { by } = useContext(Dispatching);
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger
        render={
          <button type="button" aria-label={`${sub.name} · ${t("sub.kind")}`} className="g-press grid shrink-0 rounded-full hover:brightness-95">
            <SubFace agent={sub.agent} size={size} />
          </button>
        }
      />
      <PopSurface className={CARD_POP}>
        <SubCard sub={sub} by={by} done={() => setOpen(false)} />
      </PopSurface>
    </Popover.Root>
  );
}

/** A message's head of its: its name, 派遣, then what else the head says (how long it took). */
export function SubHead({ name, children }: { name: string; children?: React.ReactNode }) {
  return (
    <span className="flex h-4 min-w-0 items-center gap-1.5">
      <span className="min-w-0 truncate text-xs font-medium text-ink-2">{name}</span>
      <span className="shrink-0 text-xs text-ink-4">{t("sub.kind")}</span>
      {children}
    </span>
  );
}
