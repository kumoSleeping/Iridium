// 编辑资料 (docs/next/group.md "改资料", "资料卡"; kumo, 2026-11-16: never a page of its own):
// what a card turns into in its own popover (g-slide, back to the card at its top left), or what
// pops out of a menu's item. An agent's: what it is called by, its intro, its persona (a page of
// the popover: persona.tsx), its face, its background; the role it is kept as; what it chose to
// remember. A channel's own: its name in
// place of the nick; a session's own channel's apart from its agent's (kumo, 2026-11-16). Each is saved as it is
// changed. The face itself changes the face (docs/前端设计规范.md "点头像就是换头像"): a picture
// chosen, cropped round as this panel's own avatar is (profile.tsx Crop). Yours (a person's):
// SelfEdit, this panel's own profile (profile.tsx), in the same frame.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, LoaderCircle, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api } from "../../api/client";
import { IntroMax, type Agent, type Profile, type ProfilePatch } from "../../api/types.gen";
import { asDataURL, BannerThumb, FacePick, Field } from "../../facepick";
import { PersonaEditor, PersonaRow } from "../../persona";
import { t, useLang } from "../../i18n";
import { AvatarCrop, Crop, Profile as Own, ProfileMore } from "../../profile";
import { openChat } from "../../route";
import { useCarried, useList } from "../../store";
import { errText, toast } from "../../toast";
import { agentName, cn, FaceChangeOver, IconKey, Tip } from "../../ui";
import type { Editing } from "./editing";
import { Face } from "./face";
import { memberError } from "./key";
import { validNick } from "./lib";

/** Asks again for what key names whenever the carried state does (not as it is first drawn). */
function useAgain(state: string, key: unknown[]) {
  const seq = useCarried<number>(state);
  const qc = useQueryClient();
  const first = useRef(true);
  const k = JSON.stringify(key);
  useEffect(() => {
    if (first.current) return void (first.current = false);
    void qc.invalidateQueries({ queryKey: JSON.parse(k) });
  }, [seq, k, qc]);
}

/** The popover's frame for 编辑资料 (and the like: a script's editor, title): its head (back to
 *  the card, when it came from one; its name), what scrolls under it. dir: "back", come back to. */
export function Frame({ back, title = t("profile.edit"), dir, children }: { back?: () => void; title?: string; dir?: "back"; children: ReactNode }) {
  return (
    <div data-dir={dir} className="g-slide flex max-h-[min(600px,var(--available-height))] flex-col" role="dialog" aria-label={title}>
      <header className="flex h-10 shrink-0 items-center gap-0.5 px-1.5 pointer-coarse:h-12">
        {back && (
          <IconKey label={t("nav.back")} onClick={back} tipSide="bottom" className="size-7 pointer-coarse:size-9">
            <ChevronLeft size={16} />
          </IconKey>
        )}
        <h2 className={cn("m-0 min-w-0 flex-1 truncate text-sm font-semibold", !back && "px-2")}>{title}</h2>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto px-3.5 pb-3.5">{children}</div>
    </div>
  );
}

/** 编辑资料 of a channel's own card, or of agent's (editing.agent). back: to the card it came
 *  from; done: the popover put away (going to a channel from a memory). */
export function EditBody({ editing, back, done }: { editing: Editing; back?: () => void; done: () => void }) {
  useLang();
  const { channel, agent } = editing;
  const qc = useQueryClient();
  const key = ["profile", channel, agent ?? ""];
  const q = useQuery({ queryKey: key, queryFn: () => (agent ? api.memberCard(`agent:${agent}`).then((c): Profile => c) : (api.channelProfile(channel) as unknown as Promise<Profile>)), staleTime: 0, retry: false });
  useAgain("cards", key);
  const p = q.data;
  const row = useList()?.find((x) => x.id === (agent ?? channel));
  const kind = (p?.agent ?? row?.agent ?? "") as Agent;
  // The session whose card this is: the agent's; a session's own channel's.
  const session = agent ?? p?.session;
  const face = agent ?? channel;
  const save = async (patch: ProfilePatch) => {
    const next = agent ? await api.patchMemberCard(`agent:${agent}`, patch, channel) : await api.patchChannel(channel, patch);
    qc.setQueryData(key, (was: Profile | undefined) => ({ ...was, ...next }));
    void qc.invalidateQueries({ queryKey: ["members", channel] });
    void qc.invalidateQueries({ queryKey: ["card"] });
  };
  // A picture chosen for the face, being cropped (in the popover, as a page of it); one for the
  // background, its strip being chosen, the same way.
  const [picture, setPicture] = useState<string | null>(null);
  const [banner, setBanner] = useState<string | null>(null);
  // Its persona open whole, as a page of the popover.
  const [persona, setPersona] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  // Any size: the crop sends 256px.
  const chosen = (f: File) => setPicture(URL.createObjectURL(f));
  const cropped = (b: Blob) =>
    void asDataURL(b)
      .then((v) => save({ avatar: v }))
      .then(
        () => setPicture(null),
        (e) => toast(t("me.avatar"), { error: true, description: memberError(e) }),
      );
  const name = p?.name ?? "";
  if (picture)
    return (
      <div className="g-slide">
        <Crop src={picture} onCancel={() => setPicture(null)} onDone={cropped} />
      </div>
    );
  if (banner)
    return (
      <div className="g-slide">
        <Crop
          banner
          src={banner}
          onCancel={() => setBanner(null)}
          onDone={(b) =>
            void asDataURL(b)
              .then((v) => save({ background: v }))
              .then(
                () => setBanner(null),
                (e) => toast(t("profile.background"), { error: true, description: memberError(e) }),
              )
          }
        />
      </div>
    );
  if (persona && agent && p)
    return (
      <Frame back={() => setPersona(false)} title={t("profile.persona")}>
        <PersonaEditor value={p.persona} save={(v) => save({ persona: v })} />
      </Frame>
    );
  return (
    <Frame back={back}>
      <input
        ref={file}
        type="file"
        accept="image/png,image/jpeg,image/webp"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (f) chosen(f);
        }}
      />
      {q.error ? (
        <p className="m-0 py-10 text-center text-sm text-err">{errText(q.error)}</p>
      ) : !p ? (
        <div className="grid h-40 place-items-center text-ink-3">
          <LoaderCircle size={18} className="animate-spin" />
        </div>
      ) : (
        <>
          {/* As its card shows it: the background, the face on its lower edge; the face is the
              key that changes it. */}
          <div className="relative mb-9 h-20 rounded-lg bg-fill-2">
            {p.background && <img key={p.background} src={p.background} alt="" draggable={false} className="g-fade absolute inset-0 size-full rounded-lg object-cover" />}
            <Tip label={t("profile.avatarChange")}>
              <button type="button" aria-label={t("profile.avatarChange")} onClick={() => file.current?.click()} className="group g-press absolute -bottom-7 left-3.5 grid size-[62px] place-items-center rounded-full bg-paper">
                <span className="relative grid size-14 place-items-center overflow-hidden rounded-full bg-fill">
                  <Face id={face} agent={kind} size={56} logo={30} channel={!agent} />
                  <FaceChangeOver size={56} />
                </span>
              </button>
            </Tip>
          </div>
          <Field
            label={t(agent ? "channel.nick" : "profile.name")}
            value={name}
            max={agent ? 32 : 80}
            bad={(v) => (!v.trim() ? " " : agent && !validNick(v) ? t("channel.nickBad") : "")}
            save={(v) => save(agent ? { nick: v } : { name: v.trim() })}
            explain={memberError}
          />
          <Field label={t("profile.intro")} value={p.intro ?? ""} max={IntroMax} multi count save={(v) => save({ intro: v })} explain={memberError} />
          {agent && <PersonaRow value={p.persona} open={() => setPersona(true)} />}
          {/* The face's other ways (an emoji, a colour, none); its picture cropped as the face's. */}
          <FacePick
            label={t("profile.avatar")}
            preview={
              <span className="grid size-7 place-items-center overflow-hidden rounded-full bg-fill">
                <Face id={face} agent={kind} size={28} logo={16} channel={!agent} />
              </span>
            }
            set={(v) => save({ avatar: v })}
            upload={chosen}
          />
          <FacePick label={t("profile.background")} preview={<BannerThumb src={p.background} />} set={(v) => save({ background: v })} upload={(f) => setBanner(URL.createObjectURL(f))} />
          {session && <RoleRow session={session} role={p.role} name={name || agentName(kind)} again={() => void qc.invalidateQueries({ queryKey: key })} channel={channel} />}
          {session && <Memories session={session} done={done} />}
        </>
      )}
    </Frame>
  );
}

/** 编辑资料 of your own card (a person's): this panel's own profile (profile.tsx: the face a key
 *  that changes it, cropped; the name; the intro, the background), in the same frame. */
export function SelfEdit({ back }: { back?: () => void }) {
  useLang();
  const [picture, setPicture] = useState<string | null>(null);
  if (picture)
    return (
      <div className="g-slide">
        <AvatarCrop src={picture} onBack={() => setPicture(null)} />
      </div>
    );
  return (
    <Frame back={back}>
      <Own onPicture={setPicture} />
      <ProfileMore />
    </Frame>
  );
}

const KEY = "g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md bg-fill px-2.5 text-sm hover:bg-fill-2 disabled:opacity-50 pointer-coarse:h-9";

/** The role the session is kept as (docs/channels.md "角色"): none, 存为角色 (its card, model
 *  and effort kept to start sessions as); one, its name, 更新角色 (taken from the session
 *  again) and 移出角色 (the session no longer its; the role stays). */
function RoleRow({ session, role, name, again, channel }: { session: string; role?: string; name: string; again: () => void; channel: string }) {
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.roles, staleTime: 5_000, retry: false, enabled: !!role });
  const [busy, setBusy] = useState("");
  const run = (what: string, f: () => Promise<unknown>, said?: string) => () => {
    setBusy(what);
    void f()
      .then(
        () => (said && toast(said), again(), void qc.invalidateQueries({ queryKey: ["roles"] }), void qc.invalidateQueries({ queryKey: ["members", channel] })),
        (e) => toast(t("err.action"), { error: true, description: errText(e) }),
      )
      .finally(() => setBusy(""));
  };
  const key = (what: string, label: string, f: () => Promise<unknown>, said?: string): ReactNode => (
    <button type="button" disabled={!!busy} onClick={run(what, f, said)} className={KEY}>
      {busy === what && <LoaderCircle size={13} className="animate-spin" />}
      {label}
    </button>
  );
  return (
    <div className="flex min-h-10 flex-wrap items-center justify-end gap-x-3 gap-y-1 py-1">
      <span className="min-w-0 flex-1 truncate text-sm">{role ? t("profile.roleIs", { name: roles.data?.find((r) => r.id === role)?.name ?? name }) : t("profile.role")}</span>
      <span className="flex shrink-0 items-center gap-1.5">
        {role ? (
          <>
            {key("update", t("profile.roleUpdate"), () => api.patchRole(role, { session }), t("profile.roleUpdated"))}
            {key("leave", t("profile.roleLeave"), () => api.roleLeave(session))}
          </>
        ) : (
          key("save", t("profile.roleSave"), () => api.roleOf(session), t("profile.roleSaved"))
        )}
      </span>
    </div>
  );
}

const day = (ms: number, lang: string) => new Date(ms).toLocaleString(lang === "zh" ? "zh-CN" : "en", { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

/** 记忆: what the agent chose to remember, the newest first: its words, when, the channel and
 *  the message it was at (pressed: that channel), and a key that deletes it, pressed twice. */
function Memories({ session, done }: { session: string; done: () => void }) {
  const lang = useLang();
  const qc = useQueryClient();
  const key = ["memory", session];
  const q = useQuery({ queryKey: key, queryFn: () => api.memory(session), staleTime: 0, retry: false });
  useAgain("memory", key);
  const [sure, setSure] = useState<number | null>(null);
  useEffect(() => {
    if (sure === null) return;
    const id = setTimeout(() => setSure(null), 3000);
    return () => clearTimeout(id);
  }, [sure]);
  const forget = (id: number) => {
    if (sure !== id) return setSure(id);
    setSure(null);
    void api.forget(session, id).then(
      () => qc.invalidateQueries({ queryKey: key }),
      (e) => toast(t("err.action"), { error: true, description: errText(e) }),
    );
  };
  const list = [...(q.data ?? [])].sort((a, b) => b.at - a.at);
  return (
    <section className="mt-4 flex flex-col">
      <h2 className="m-0 mb-1 flex h-4 items-center text-xs font-medium text-ink-3">{t("profile.memory")}</h2>
      {q.error ? <p className="m-0 py-3 text-sm text-err">{errText(q.error)}</p> : q.data && !list.length ? <p className="m-0 py-3 text-sm text-ink-3">{t("profile.memoryNone")}</p> : null}
      <ul className="m-0 flex list-none flex-col p-0">
        {list.map((m) => (
          <li key={m.id} className="g-fade flex items-start gap-2 py-1.5">
            <div className="min-w-0 flex-1">
              <p className="m-0 text-sm leading-5 whitespace-pre-wrap [overflow-wrap:anywhere]">{m.text}</p>
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 text-xs leading-5 text-ink-3 tabular-nums">
                <span>{day(m.at, lang)}</span>
                {m.channel && (
                  <button type="button" onClick={() => (done(), openChat(m.channel!))} className="min-w-0 truncate rounded-sm underline decoration-ink-4 underline-offset-2 hover:text-ink hover:decoration-ink-2">
                    {[m.name || m.channel.slice(0, 8), m.seq ? `#${m.seq}` : ""].filter(Boolean).join(" ")}
                  </button>
                )}
              </div>
            </div>
            <Tip label={t(sure === m.id ? "profile.forgetSure" : "profile.forget")}>
              <button
                type="button"
                aria-label={t(sure === m.id ? "profile.forgetSure" : "profile.forget")}
                onClick={() => forget(m.id)}
                onBlur={() => setSure(null)}
                className={cn("g-press grid size-7 shrink-0 place-items-center rounded-md hover:bg-fill pointer-coarse:size-9", sure === m.id ? "bg-fill text-err" : "text-ink-3 hover:text-ink")}
              >
                <Trash2 size={14} />
              </button>
            </Tip>
          </li>
        ))}
      </ul>
    </section>
  );
}
