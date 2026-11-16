// Faces (docs/channels.md "头像"; the user, 2026-11-13): a session's agent's, under the session's
// id; a channel's as a channel (channel), a session's own channel's its own, else its agent's
// (kumo, 2026-11-16). One with none of its own has the one last given to a session of its model; with
// neither, the harness's logo.
import { useState, type ReactNode } from "react";
import type { Agent } from "../../api/types.gen";
import { useApps } from "../../data";
import { useCarried } from "../../store";
import { Avatar, cn, isHost, Logo } from "../../ui";

/** The face of session id's agent, or with channel of channel id as a channel; round, size
 *  across; none given: the agent's logo, as large as logo, or what else stands for it. */
export function Face({ id, agent, size, logo, className, none: instead, low, channel }: { id: string; agent: Agent; size: number; logo?: number; className?: string; none?: ReactNode; low?: boolean; channel?: boolean }) {
  // Asked again as any face changes (the carried state "avatars", a count).
  const v = useCarried<number>("avatars") ?? 0;
  const src = `/v1/internal/channels/${encodeURIComponent(id)}/avatar?${channel ? "of=channel&" : ""}v=${v}`;
  const [none, setNone] = useState<string | null>(null);
  // Something else standing for it (wider than the face): shown until the picture has come.
  const [got, setGot] = useState<string | null>(null);
  // The picture shown: the last that came, kept while the next one loads, so that a face asked
  // for again does not blank and come back (kumo, 2026-11-16: every face in the list blinked).
  const [shown, setShown] = useState<string | null>(null);
  if (none === src) return instead ? <span className={cn("grid", className)}>{instead}</span> : <Logo agent={agent} size={logo ?? size} className={className} />;
  const came = () => (setGot(src), setShown(src));
  // low: one of many (the sidebar's rows), fetched after what the open page asks for.
  const next = <img key={src} src={src} alt="" draggable={false} fetchPriority={low ? "low" : undefined} onLoad={came} onError={() => setNone(src)} className={cn("absolute inset-0 size-full rounded-full object-cover", shown && shown !== src && "opacity-0")} />;
  const was = shown && shown !== src ? <img key={shown} src={shown} alt="" draggable={false} className="absolute inset-0 size-full rounded-full object-cover" /> : null;
  if (instead && got !== src && !shown)
    return (
      <span className={cn("relative grid", className)}>
        {instead}
        <span className="invisible absolute size-0 overflow-hidden">{next}</span>
      </span>
    );
  return (
    <span className={cn("relative grid shrink-0 place-items-center", className)} style={{ width: size, height: size }}>
      {!instead && !shown && <Logo agent={agent} size={logo ?? size} />}
      {was}
      {next}
    </span>
  );
}

/** A member's own face, small (a read mark, one who is looking): an agent's ("agent:<session>":
 *  its Face), a login's avatar ("<app>/<login>"), an app's for one that names no login. */
export function MemberFace({ member, name, agent, size }: { member: string; name?: string; agent?: Agent; size: number }) {
  const d = useApps().data;
  if (member.startsWith("agent:") && agent)
    return (
      <span className="grid shrink-0 place-items-center rounded-full bg-fill" style={{ width: size, height: size }}>
        <Face id={member.slice(6)} agent={agent} size={size} logo={Math.round(size * 0.64)} />
      </span>
    );
  const i = member.indexOf("/");
  const login = d?.logins.find((l) => l.app === member.slice(0, i) && l.id === member.slice(i + 1));
  const app = d?.apps.find((a) => a.id === member.slice(0, i));
  return <Avatar src={login ? login.avatar : app?.avatar} name={name || login?.name || app?.name || member} size={size} host={isHost(member) || !!app?.host} />;
}
