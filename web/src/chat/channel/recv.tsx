// The senders a channel's subscriptions may name (docs/channels.md "订阅事件"), each with its name
// and face, and an agent's subscriptions in a few words. The entries: "*" every person, an app's logins ("<app>/*"; an app of one login is
// the app alone, docs/前端设计规范.md "一个登录的应用合成一行"), a login, another agent.
import { UsersRound } from "lucide-react";
import type { ReactNode } from "react";
import type { App, Login, Trigger } from "../../api/types.gen";
import { isComputer, useApps } from "../../data";
import { t } from "../../i18n";
import { agentName, Avatar, isHost } from "../../ui";
import { Face, MemberFace } from "./face";
import { useMembers } from "./gate";
import { personName } from "./lib";
import { usePeople } from "./person";
import { EVENTS, recvWhole, triggerLine, type CronWords, type RecvWords, type TriggerWords } from "./scripts";
import { appAll, hasApp, hasLogin, loginOf, peopleOf } from "./setup";

export type RecvOption = { entry: string; label: string; face: ReactNode };
export type RecvApp = { id: string; name: string; face: ReactNode; direct?: string; all?: string; logins: RecvOption[] };
export type RecvGroups = { main: RecvOption[]; apps: RecvApp[] };

export type RecvAppEntry = { id: string; name: string; host?: boolean; avatar?: string; type?: string; direct?: string; all?: string; logins: { entry: string; name: string }[] };
export type RecvSplit = { webLogins: { entry: string; name: string }[]; apps: RecvAppEntry[] };

/** Splits a channel's people into web logins (shown directly in 唤醒; ir_web/* itself is dropped as
 *  it duplicates 全体成员) and non-web apps (shown under the bottom 应用 page: direct toggle when
 *  at most one login is in the channel, else a login list; kumo, 2026-11-27). Pure, for tests. */
export function splitRecvPeople(ps: string[], apps: App[], logins: Login[]): RecvSplit {
  const webLogins: { entry: string; name: string }[] = [];
  const outApps: RecvAppEntry[] = [];
  for (const a of apps) {
    if (isComputer(a)) continue;
    const appLogins = logins.filter((l) => l.app === a.id && hasLogin(ps, a.id, l.id));
    if (a.web) {
      for (const l of appLogins) webLogins.push({ entry: loginOf(a.id, l.id), name: l.name });
      continue;
    }
    const all = hasApp(ps, a.id);
    if (!all && appLogins.length === 0) continue;
    const name = a.host && !a.named ? "Iridium" : a.name;
    if (appLogins.length <= 1) {
      const direct = all ? appAll(a.id) : loginOf(a.id, appLogins[0]!.id);
      outApps.push({ id: a.id, name, host: a.host, avatar: a.avatar, type: a.type, direct, logins: [] });
    } else {
      outApps.push({
        id: a.id,
        name,
        host: a.host,
        avatar: a.avatar,
        type: a.type,
        all: all ? appAll(a.id) : undefined,
        logins: appLogins.map((l) => ({ entry: loginOf(a.id, l.id), name: l.name })),
      });
    }
  }
  return { webLogins, apps: outApps };
}

export const recvWords = (): RecvWords => ({ at: t("channel.recvAt"), all: t("channel.recvEveryone"), sep: t("channel.what.sep"), n: t("channel.recvCount") });

/** What a receive set of channel may name, split into main (everyone, web logins, other agents)
 *  and apps (non-web apps for the bottom 应用 page in WakePick). */
export function useRecvGroups(channel: string, skip?: string): RecvGroups {
  const members = useMembers(channel);
  const d = useApps().data;
  const split = splitRecvPeople(peopleOf(members), d?.apps ?? [], d?.logins ?? []);
  const main: RecvOption[] = [{ entry: "*", label: t("channel.recvEveryone"), face: <UsersRound size={16} className="shrink-0 text-ink-2" /> }];
  for (const l of split.webLogins) main.push({ entry: l.entry, label: l.name, face: <MemberFace member={l.entry} name={l.name} size={16} /> });
  for (const m of members?.members ?? []) {
    if (!m.session || m.session === skip) continue;
    const agent = m.agent ?? "claude";
    main.push({
      entry: m.member,
      label: `@${m.nick || agentName(agent)}`,
      face: (
        <span className="grid size-4 shrink-0 place-items-center rounded-full bg-fill">
          <Face id={m.session} agent={agent} size={16} logo={10} />
        </span>
      ),
    });
  }
  const apps: RecvApp[] = split.apps.map((a) => ({
    id: a.id,
    name: a.name,
    face: <Avatar src={a.avatar} name={a.name} size={16} type={a.type} host={!!a.host} />,
    direct: a.direct,
    all: a.all,
    logins: a.logins.map((l) => ({ entry: l.entry, label: l.name, face: <MemberFace member={l.entry} name={l.name} size={16} /> })),
  }));
  return { main, apps };
}

/** What a receive set of channel may name: every person, its people, its agents but skip (the
 *  agent whose set it is). */
export function useRecvOptions(channel: string, skip?: string): RecvOption[] {
  const members = useMembers(channel);
  const people = usePeople(members);
  const d = useApps().data;
  const logins = d?.logins ?? [];
  const webIds = new Set((d?.apps ?? []).filter((a) => a.web).map((a) => a.id));
  const out: RecvOption[] = [{ entry: "*", label: t("channel.recvEveryone"), face: <UsersRound size={16} className="shrink-0 text-ink-2" /> }];
  for (const p of people) {
    const app = p.member.slice(0, p.member.indexOf("/"));
    if (p.member.endsWith("/*") && webIds.has(app)) continue;
    // A login of an app listed whole that has no other: the app's row is it.
    if (!p.member.endsWith("/*") && people.some((x) => x.member === `${app}/*`) && logins.filter((l) => l.app === app).length <= 1) continue;
    out.push({ entry: p.member, label: isHost(p.member) && p.member.endsWith("/*") ? "Iridium" : p.nick, face: <MemberFace member={p.member} name={p.nick} size={16} /> });
  }
  for (const m of members?.members ?? []) {
    if (!m.session || m.session === skip) continue;
    const agent = m.agent ?? "claude";
    out.push({
      entry: m.member,
      label: `@${m.nick || agentName(agent)}`,
      face: (
        <span className="grid size-4 shrink-0 place-items-center rounded-full bg-fill">
          <Face id={m.session} agent={agent} size={16} logo={10} />
        </span>
      ),
    });
  }
  return out;
}

/** An entry of channel's receive sets by name: an agent "@nick", an app, a login. */
export function useRecvName(channel: string): (entry: string) => string {
  const members = useMembers(channel);
  const d = useApps().data;
  const apps = (d?.apps ?? []).map((a) => (a.web ? { ...a, name: t("term.panel") } : a.host && !a.named ? { ...a, name: "Iridium" } : a));
  return (entry) => {
    if (entry === "*") return t("channel.recvEveryone");
    if (entry.startsWith("agent:")) {
      const m = members?.members.find((x) => x.member === entry);
      return `@${m?.nick || (m?.agent ? agentName(m.agent) : entry.slice(6, 14))}`;
    }
    return personName(entry, apps, d?.logins ?? []);
  };
}

/** A receive set whole (an older event's line); an agent's subscriptions whole, a sentence each
 *  (a line of the channel: who changed them, to what). */
export function useRecvLabel(channel: string): { whole: (r: string[]) => string; onWhole: (on: Trigger[]) => string } {
  const name = useRecvName(channel);
  const w = recvWords();
  const tw = triggerWords();
  return { whole: (r) => recvWhole(r, name, w), onWhole: (on) => [t("on.at"), ...on.map((x) => triggerLine(x, name, tw))].join(t("on.sep")) };
}

/** The words a subscription is said in, in the page's language. */
export function triggerWords(): TriggerWords {
  return {
    said: t("on.said"),
    saidWords: t("on.saidWords"),
    words: t("on.words"),
    any: t("on.any"),
    quote: t("on.quote"),
    or: t("on.or"),
    command: t("on.command"),
    cron: t("on.cron"),
    event: t("on.event"),
    events: Object.fromEntries(EVENTS.map((e) => [e, t(`on.ev.${e}`)])),
    then: t("on.then"),
    sep: t("channel.what.sep"),
    recv: { all: t("channel.recvEveryone") },
    when: cronWords(),
  };
}
export const cronWords = (): CronWords => ({ minute: t("cron.minute"), everyMin: t("cron.everyMin"), hourly: t("cron.hourly"), everyHour: t("cron.everyHour"), daily: t("cron.daily"), weekdays: t("cron.weekdays"), weekly: t("cron.weekly"), monthly: t("cron.monthly"), days: t("cron.days").split(","), sep: t("channel.what.sep") });
