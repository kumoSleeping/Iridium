// A person's card in a channel (the user, 2026-11-13: people, apps and agents are peers): their
// face pressed opens it, as an agent's does (members.tsx Card). Their face and name; 私聊, the
// channel of the two of you (POST /v1/internal/dm), for a login or the host only (an app, an
// author that is no login have none: the owner, 2026-11-14); and 提及, "@name" in the composer.
import { Menu } from "@base-ui/react/menu";
import { AtSign, MessageCircle, PencilLine } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { api } from "../../api/client";
import type { ChannelMembers } from "../../api/types.gen";
import { t } from "../../i18n";
import { openChat } from "../../route";
import { isComputer, useApps } from "../../data";
import { errText, toast } from "../../toast";
import { Avatar, cn, isHost } from "../../ui";
import { ITEM, POPUP } from "./invite";
import { mayDM, userOf, type AgentRow } from "./lib";
import { SelfEdit } from "./edit";
import { AgentCard } from "./members";
import { lendActs } from "./menu";
import { namesOf, type Named } from "./nick";
import { ProfileCard, useCard, type CardKey } from "./profilecard";
import { hasApp, hasLogin, peopleOf } from "./setup";

/** user: their Satori user id (a login's, "local" the host's); app: the app they came by. dm:
 *  false for the one looking (no channel of one). The one card (profilecard.tsx): their
 *  background, face, name, signature; 编辑资料 on one's own, this panel's profile in place
 *  (edit.tsx SelfEdit). */
export function PersonCard({ user, name, avatar, app, call, dm: other = true }: { user: string; name: string; avatar?: string; app?: string; call?: (name: string) => void; dm?: boolean }) {
  const [busy, setBusy] = useState(false);
  // 编辑资料 of one's own: the card turns into it in place (kumo, 2026-11-16: no settings page).
  const [edit, setEdit] = useState(false);
  const dm = mayDM(user, useApps().data?.logins ?? []) && other;
  const card = useCard(user);
  const talk = () => {
    setBusy(true);
    void api.dm(user).then(
      (r) => openChat(r.channel),
      (e) => (setBusy(false), toast(t("err.action"), { error: true, description: errText(e) })),
    );
  };
  const keys: CardKey[] = [];
  if (dm) keys.push({ icon: <MessageCircle size={14} />, label: t("channel.dm"), run: talk, disabled: busy });
  if (call) keys.push({ icon: <AtSign size={14} />, label: t("channel.call"), run: () => call(name) });
  if (card?.mine) keys.push({ icon: <PencilLine size={14} />, label: t("profile.edit"), run: () => setEdit(true) });
  if (edit) return <SelfEdit back={() => setEdit(false)} />;
  return <ProfileCard face={<Avatar src={avatar ?? card?.avatar} name={name} size={56} host={isHost(user)} />} name={name} sub={app} intro={card?.intro} background={card?.background} keys={keys} />;
}

/** A person's face's menu, the right button or a finger held down (kumo, 2026-11-16: an agent's
 *  face has one, a person's had none): 提及, 私聊, as their card has them. */
export function PersonMenu({ user, name, call, dm: other = true, at, close }: { user: string; name: string; call?: (name: string) => void; dm?: boolean; at: { x: number; y: number } | null; close: () => void }) {
  const dm = mayDM(user, useApps().data?.logins ?? []) && other;
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  // After a frame: the menu shuts first, and what opens is not shut with it.
  const later = (f: () => void) => () => requestAnimationFrame(f);
  const talk = () => void api.dm(user).then((r) => openChat(r.channel), (e) => toast(t("err.action"), { error: true, description: errText(e) }));
  const items = [
    call && { icon: <AtSign size={15} />, label: t("channel.call"), run: later(() => call(name)) },
    dm && { icon: <MessageCircle size={15} />, label: t("channel.dm"), run: talk },
  ].filter((x) => !!x);
  if (!items.length) return null;
  return (
    <Menu.Root open={at !== null} onOpenChange={(o) => !o && close()}>
      <Menu.Portal>
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className={cn(POPUP, "min-w-52 p-1.5")}>
            <div className="g-stagger flex flex-col">
              {items.map((x, i) => (
                <Menu.Item key={x.label} onClick={x.run} style={{ "--i": i } as CSSProperties} className={ITEM}>
                  <span className="text-ink-2">{x.icon}</span>
                  <span className="flex-1">{x.label}</span>
                </Menu.Item>
              ))}
            </div>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** A channel's people and apps as an @ names them (peers of its agents): each login among its
 *  people, each app whose all are; no computer (not invited, the owner, 2026-11-14). */
export function usePeople(members?: ChannelMembers): AgentRow[] {
  const apps = useApps().data;
  return useMemo(() => {
    const ps = peopleOf(members);
    const out: AgentRow[] = [];
    for (const a of apps?.apps ?? []) {
      if (isComputer(a)) continue;
      const appName = a.web ? t("term.panel") : a.name;
      if (hasApp(ps, a.id)) out.push({ session: "", member: `${a.id}/*`, agent: "" as AgentRow["agent"], nick: appName, name: t("channel.app"), status: "idle", recv: "", avatar: a.avatar, person: true });
      for (const l of (apps?.logins ?? []).filter((x) => x.app === a.id)) {
        if (hasLogin(ps, a.id, l.id)) out.push({ session: "", member: `${a.id}/${l.id}`, agent: "" as AgentRow["agent"], nick: l.name, name: appName, status: "idle", recv: "", avatar: l.avatar, person: true });
      }
    }
    return out;
  }, [apps, members]);
}

/** A channel's members as its words name them (nick.tsx Nicks) and as its composer's @ lists
 *  them: named, for the words (the one looking among them: others name it); others, the people
 *  and apps but the one looking, for the @ list. Pressed in the words, a member's card: the one
 *  its face opens. call: "@name" into the composer; record, talk: as an agent's face takes them. */
export function useNamed({ channel, agents, members, may, call, record, talk }: { channel: string; agents: AgentRow[]; members?: ChannelMembers; may: boolean; call: (name: string) => void; record?: (session: string) => void; talk?: () => void }): { named: Named; others: AgentRow[] } {
  const people = usePeople(members);
  const me = members?.me;
  // As they are now, for a card opened later.
  const fns = useRef({ call, record, talk });
  fns.current = { call, record, talk };
  // Lent to the member's menu opened from the bar's list (menu.ts): the same @, the same way
  // to a session's record, as the faces here have.
  useEffect(() => lendActs(channel, { call: may ? (n) => fns.current.call(n) : undefined, record: record && ((x) => fns.current.record?.(x)) }), [channel, may, !record]);
  const sig = agents.map((a) => `${a.session}:${a.nick}:${a.name}:${a.status}:${a.agent}`).join(" ");
  const named = useMemo<Named>(
    () => ({
      agents,
      people,
      names: namesOf(agents, people),
      card: (m, done) => {
        const name = (n: string) => (done(), fns.current.call(n));
        if (m.person) return <PersonCard user={userOf(m.member)} name={m.nick} avatar={m.avatar} app={m.name} dm={m.member !== me} call={may ? name : undefined} />;
        return <AgentCard channel={channel} a={m} call={may ? name : undefined} record={fns.current.record && ((x) => fns.current.record?.(x))} may={may} talk={fns.current.talk && (() => fns.current.talk?.())} done={done} />;
      },
    }),
    [channel, sig, people, may, me],
  );
  const others = useMemo(() => people.filter((p) => p.member !== me), [people, me]);
  return { named, others };
}
