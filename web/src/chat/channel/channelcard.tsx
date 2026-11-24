// The card the title's face opens (docs/前端设计规范.md "点头像弹卡，不换页"; kumo, 2026-11-16):
// a popover out of the face, never a page: the channel's name, intro, face and background. A
// session's own channel's are its own, not its agent's (kumo, 2026-11-16: changing the channel's
// face changed the agent's); its agent's card is at the agent's face (members.tsx AgentCard).
// 编辑资料 is a key of the card, the card turning into it in place (edit.tsx), for one of its
// people.
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { PencilLine } from "lucide-react";
import { api } from "../../api/client";
import type { Agent, Profile } from "../../api/types.gen";
import { t, useLang } from "../../i18n";
import { useList } from "../../store";
import { cn, titleOf } from "../../ui";
import { EditBody } from "./edit";
import { Face, FaceStack } from "./face";
import { mayAct, useMembers } from "./gate";
import { ProfileCard, type CardKey } from "./profilecard";

/** The card of channel id, inside the popover at its face; done puts the popover away. */
export function ChannelCard({ id, agent, done }: { id: string; agent: Agent; done: () => void }) {
  useLang();
  const members = useMembers(id);
  const may = mayAct(members);
  const row = useList()?.find((x) => x.id === id);
  const [edit, setEdit] = useState(false);
  // Asked as 编辑资料 asks (one query): what it shows is what is being changed there.
  const q = useQuery({ queryKey: ["profile", id, ""], queryFn: () => api.channelProfile(id) as unknown as Promise<Profile>, staleTime: 5_000, retry: false });
  if (edit) return <EditBody editing={{ channel: id }} back={() => setEdit(false)} done={done} />;
  const p = q.data;
  // Of several agents, with no face of its own: theirs; of one agent in a bare channel: its face.
  const group = members?.members.filter((m) => m.session && m.agent).map((m) => ({ id: m.session!, agent: m.agent! })) ?? [];
  const several = group.length > 1;
  const sole = !agent && group.length === 1 ? group[0] : undefined;
  const keys: CardKey[] = may ? [{ icon: <PencilLine size={14} />, label: t("profile.edit"), run: () => setEdit(true) }] : [];
  return (
    <ProfileCard
      face={
        <span className={cn("grid size-14 place-items-center rounded-full", !several && "bg-fill")}>
          <Face id={id} agent={sole?.agent ?? agent} size={56} logo={30} channel none={several ? <FaceStack members={group} size={56} /> : sole ? <Face id={sole.id} agent={sole.agent} size={56} logo={30} /> : undefined} />
        </span>
      }
      name={p?.name || (row ? titleOf(row) : "")}
      background={p?.background}
      keys={keys}
    />
  );
}
