// The settings' 角色 page (docs/channels.md "角色"; kumo, 2026-11-16): the roles in one flat
// list, each by its face, name, line and how much it remembers; a row opens the role's own page,
// where its name, intro, persona, face, background, model (dsh: preset) are changed, each saved
// as it is changed (PATCH /v1/internal/roles/{id}), and the role deleted (asked first). No effort:
// chosen at each start (otae, 2026-11-21). A role is made from a session's 编辑资料 (存为角色),
// or here (the + at the top right: its name and its agent, where it runs; then its own page).
import { Menu } from "@base-ui/react/menu";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, Plus, Trash2 } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { api, ApiError } from "./api/client";
import type { Agent, HostPersona, Role, RolePatch } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { defaultFirst } from "./chat/channel/pick";
import { choicesQ } from "./chat/choices";
import { validNick } from "./chat/channel/lib";
import { ModelList, modelView, presetName } from "./chat/meters";
import { Choose } from "./choose";
import { asDataURL, FacePick } from "./facepick";
import { Crop } from "./profile";
import { t, useLang } from "./i18n";
import { agentsQ, useMachines } from "./data";
import { HostPersonas, PersonaEditor, PersonaRow } from "./persona";
import { RoleBanner, RoleFace, RoleLine, roleModel, useRoles } from "./roles";
import { Item } from "./settings";
import { errText, toast } from "./toast";
import { agentName, cn, IconKey, intoView } from "./ui";

const BOX = "no-ring min-w-0 rounded-md bg-fill px-2.5 text-sm outline-none placeholder:text-ink-3 focus:bg-fill-2";
const POPUP = "g-unfold glass thick float rounded-lg p-1.5 text-ink outline-none";

export function RolesPage({ onBack }: { onBack: () => void }) {
  useLang();
  const roles = useRoles();
  const [open, setOpen] = useState<string | null>(null);
  const role = roles.find((r) => r.id === open);
  // Back from a role's page the list slides in from the other side; first shown, it does not.
  const moved = useRef(false);
  if (role) moved.current = true;
  if (open === NEW) moved.current = true;
  if (role) return <RolePage key={role.id} role={role} onBack={() => setOpen(null)} />;
  if (open === NEW) return <NewRolePage onBack={() => setOpen(null)} made={(r) => setOpen(r.id)} />;
  return (
    <div className={cn("flex h-full flex-col", moved.current && "g-slide")} data-dir="back">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.me")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("role.title")}</h1>
        <IconKey label={t("role.new")} onClick={() => setOpen(NEW)} tipSide="bottom" className="size-7">
          <Plus size={16} />
        </IconKey>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col px-1.5">
          {!roles.length && <p className="m-0 px-1.5 text-sm text-ink-3">{t("role.none")}</p>}
          {roles.map((r) => (
            <button key={r.id} type="button" onClick={() => setOpen(r.id)} className="g-press flex min-h-12 items-center gap-2.5 rounded-lg px-1.5 py-1 text-left hover:bg-fill">
              <RoleFace role={r} size={28} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">{r.name}</span>
                <RoleLine role={r} at={placeOf(r)} className="block truncate text-xs text-ink-3" />
              </span>
              {!!r.memories && (
                <span className="shrink-0 text-xs text-ink-3 tabular-nums">
                  {t("profile.memory")} {r.memories}
                </span>
              )}
              <ChevronRight size={15} className="shrink-0 text-ink-4" />
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

/** The page open in place of the list for a new role. */
const NEW = "\u0000new";

/** The agents a role may be at machine (""; the host): those it runs and has on, the host's
 *  installed ones. */
function useKinds(machine: string): Agent[] {
  const host = useQuery(agentsQ).data;
  const ms = useMachines(true);
  if (!machine) return (host ?? []).filter((a) => !a.missing).map((a) => a.agent);
  return (ms.find((m) => m.id === machine)?.agents ?? []).map((a) => a.agent as Agent);
}

/** A new role: its name and its agent (where it runs, with more than one computer); a host's
 *  persona taken gives its name and intro where none is typed, and its persona. Made, its own
 *  page in place of this one. */
function NewRolePage({ onBack, made }: { onBack: () => void; made: (r: Role) => void }) {
  useLang();
  const qc = useQueryClient();
  const ms = useMachines(true);
  const [machine, setMachine] = useState("");
  const kinds = useKinds(machine);
  const [agent, setAgent] = useState<Agent | "">("");
  const kind = agent && kinds.includes(agent) ? agent : (kinds[0] ?? "");
  const [name, setName] = useState("");
  const [took, setTook] = useState<HostPersona | null>(null);
  const [busy, setBusy] = useState(false);
  const bad = name.trim() !== "" && !validNick(name.trim());
  const ok = !!kind && name.trim() !== "" && !bad && !busy;
  const take = (p: HostPersona) => {
    setTook(p);
    if (!name.trim() && validNick(p.name)) setName(p.name);
  };
  const create = async () => {
    if (!ok) return;
    setBusy(true);
    try {
      const r = await api.newRole({ name: name.trim(), agent: kind, ...(machine ? { machine } : {}), ...(took ? { persona: took.text, ...(took.description ? { intro: [...took.description].slice(0, 200).join("") } : {}) } : {}) });
      qc.setQueriesData<Role[]>({ queryKey: ["roles"] }, (old) => (old ? [...old.filter((x) => x.id !== r.id), r] : old));
      made(r);
    } catch (e) {
      toast(t("err.action"), { error: true, description: refused(e) });
      setBusy(false);
    }
  };
  return (
    <div className="g-slide flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("role.title")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("role.new")}</h1>
        <IconKey label={t("role.create")} disabled={!ok} onClick={() => void create()} tipSide="bottom" className="size-7">
          <Check size={16} />
        </IconKey>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col px-3">
          <Item label={t("channel.nick")}>
            <input
              aria-label={t("channel.nick")}
              value={name}
              maxLength={32}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => !e.nativeEvent.isComposing && e.keyCode !== 229 && e.key === "Enter" && void create()}
              className={cn(BOX, "h-7 w-36 text-right", bad && "text-err")}
            />
          </Item>
          {ms.length > 1 && (
            <Item label={t("role.computer")}>
              <Choose label={t("role.computer")} value={machine} onChange={(m) => setMachine(m)} options={ms.map((m) => ({ value: m.id, label: m.name }))} />
            </Item>
          )}
          <Item label={t("role.agent")}>
            <Choose label={t("role.agent")} value={kind} onChange={(a) => setAgent(a as Agent)} options={kinds.map((a) => ({ value: a, label: agentName(a) }))} disabled={!kinds.length} />
          </Item>
          {took && (
            <Item label={t("profile.persona")}>
              <span className="max-w-48 truncate text-sm text-ink-2">{took.name}</span>
            </Item>
          )}
          <HostPersonas take={take} />
        </div>
      </div>
    </div>
  );
}

/** Where a role's agent runs: its machine, else here. */
const placeOf = (r: Role) => (r.machine ? { machine: r.machine } : NO_PLACE);
const NO_PLACE = {};

/** What a refused change says: a name taken or not one, else the server's words. */
const refused = (e: unknown) => (e instanceof ApiError && e.status === 409 ? t("channel.nickTaken") : e instanceof ApiError && e.status === 400 && /^name:/.test(e.message) ? t("channel.nickBad") : errText(e));

/** One role's page. */
function RolePage({ role, onBack }: { role: Role; onBack: () => void }) {
  useLang();
  const qc = useQueryClient();
  const [sure, setSure] = useState(false);
  const [busy, setBusy] = useState(false);
  const [persona, setPersona] = useState(false);
  // A picture chosen for the face, being cropped round as 编辑资料's is (a page of its own).
  const [picture, setPicture] = useState<string | null>(null);
  // One chosen for the background, its strip being chosen.
  const [banner, setBanner] = useState<string | null>(null);
  // The models' menu opens to the side with more room: under the row it is cut to what is left
  // below, a row low on the page leaving room for two (otae, 2026-11-22: the others seemed gone).
  const modelKey = useRef<HTMLButtonElement>(null);
  const [up, setUp] = useState(false);
  const patch = async (p: RolePatch) => {
    try {
      const got = await api.patchRole(role.id, p);
      // Shown at once; the list is asked again as the change is carried (roles.tsx useRoles).
      qc.setQueriesData<Role[]>({ queryKey: ["roles"] }, (old) => old?.map((r) => (r.id === got.id ? got : r)));
      return true;
    } catch (e) {
      toast(t("err.action"), { error: true, description: refused(e) });
      return false;
    }
  };
  const remove = async () => {
    setBusy(true);
    try {
      await api.deleteRole(role.id);
      qc.setQueriesData<Role[]>({ queryKey: ["roles"] }, (old) => old?.filter((r) => r.id !== role.id));
    } catch (e) {
      toast(t("err.action"), { error: true, description: errText(e) });
      setBusy(false);
    }
  };
  const asked = useQuery(choicesQ(role.agent, placeOf(role))).data;
  const v = modelView({ agent: role.agent, models: asked?.models.length ? asked.models : undefined, efforts: asked?.efforts, pick: role.model, model: role.model, ran: asked?.ran, effortOwn: asked?.effort });
  const presets = asked?.presets?.presets ?? [];
  // A crop as a page of its own: back to the role, what it is for in its title.
  const cropPage = (title: string, back: () => void, crop: ReactNode) => (
    <div className="g-slide flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={role.name} onClick={back} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{title}</h1>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto">{crop}</div>
    </div>
  );
  if (picture)
    return cropPage(
      t("profile.avatar"),
      () => setPicture(null),
      <>
        <Crop
          src={picture}
          onCancel={() => setPicture(null)}
          onDone={(b) =>
            void asDataURL(b)
              .then((avatar) => patch({ avatar }))
              .then((ok) => ok && setPicture(null))
          }
        />
      </>,
    );
  if (banner)
    return cropPage(
      t("profile.background"),
      () => setBanner(null),
      <>
        <Crop
          banner
          src={banner}
          onCancel={() => setBanner(null)}
          onDone={(b) =>
            void asDataURL(b)
              .then((background) => patch({ background }))
              .then((ok) => ok && setBanner(null))
          }
        />
      </>,
    );
  if (persona)
    return (
      <div className="g-slide flex h-full flex-col">
        <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
          <IconKey label={role.name} onClick={() => setPersona(false)} tipSide="bottom" className="size-7">
            <ChevronLeft size={16} />
          </IconKey>
          <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("profile.persona")}</h1>
        </header>
        <div className="flex min-h-0 flex-1 flex-col px-3 pt-2 pb-4">
          <PersonaEditor fill value={role.persona} save={(v) => patch({ persona: v }).then((ok) => ok || Promise.reject(new Error(t("err.action"))))} />
        </div>
      </div>
    );
  return (
    <div className="g-slide flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("role.title")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{role.name}</h1>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col px-3">
          <Item label={t("channel.nick")}>
            <Field key={role.name} value={role.name} label={t("channel.nick")} save={(name) => patch({ name })} className="h-7 w-36 text-right" />
          </Item>
          <Item label={t("profile.intro")} below>
            <Field key={role.intro ?? ""} value={role.intro ?? ""} label={t("profile.intro")} save={(intro) => patch({ intro })} lines maxLength={200} className="w-full py-1.5" />
          </Item>
          <PersonaRow value={role.persona} open={() => setPersona(true)} />
          <FacePick label={t("profile.avatar")} preview={<RoleFace role={role} size={28} />} set={(avatar) => patch({ avatar })} upload={(f) => setPicture(URL.createObjectURL(f))} />
          <FacePick label={t("profile.background")} preview={<RoleBanner role={role} className="h-7 w-12 rounded-md" />} set={(background) => patch({ background })} upload={(f) => setBanner(URL.createObjectURL(f))} />
          {v.models.length > 0 && (
            <Item label={t("composer.model")}>
              <Menu.Root
                onOpenChange={(open) => {
                  const r = modelKey.current?.getBoundingClientRect();
                  if (open && r) setUp(r.top + r.height / 2 > window.innerHeight / 2);
                }}
              >
                <Menu.Trigger ref={modelKey} aria-label={t("composer.model")} className="g-press flex h-7 max-w-48 min-w-0 cursor-default items-center gap-1 rounded-md bg-fill px-2.5 text-sm outline-none hover:bg-fill-2 data-popup-open:bg-fill-2">
                  <span className="min-w-0 truncate">{v.name || roleModel(role, asked?.models)}</span>
                </Menu.Trigger>
                <Menu.Portal>
                  <Menu.Positioner side={up ? "top" : "bottom"} align="end" sideOffset={6} collisionPadding={8} className="z-50">
                    <Menu.Popup finalFocus={false} className={cn(POPUP, "flex max-h-[min(560px,var(--available-height))] w-[min(280px,calc(100vw-16px))] flex-col")}>
                      {/* The models alone: the effort is the next row's. */}
                      <ModelList models={defaultFirst(v.models, v.def)} value={v.value} pickModel={(model) => void patch({ model })} levels={[]} level="" setEffort={async () => false} />
                    </Menu.Popup>
                  </Menu.Positioner>
                </Menu.Portal>
              </Menu.Root>
            </Item>
          )}
          {role.agent === "dsh" && presets.length > 1 && (
            <Item label={t("composer.preset")}>
              <Choose label={t("composer.preset")} value={role.preset || asked?.presets?.default || presets[0].id} onChange={(preset) => void patch({ preset })} options={presets.map((p) => ({ value: p.id, label: presetName(p.id, p) }))} />
            </Item>
          )}
          <Item label={t("profile.memory")}>
            <span className="text-xs text-ink-3 tabular-nums">{role.memories ?? 0}</span>
          </Item>
          {sure ? (
            <div ref={intoView} className="g-fade pt-2">
              <CardFrame label={t("role.deleteTitle", { name: role.name })}>
                <CardHead
                  icon={<Trash2 size={16} />}
                  title={t("role.deleteTitle", { name: role.name })}
                  summary={t("role.deleteHint")}
                  keys={
                    <>
                      <SecondKey onClick={() => setSure(false)}>{t("me.cancel")}</SecondKey>
                      <MainKey busy={busy} onClick={() => void remove()}>
                        {t("secrets.delete")}
                      </MainKey>
                    </>
                  }
                />
              </CardFrame>
            </div>
          ) : (
            <button type="button" onClick={() => setSure(true)} className="g-press -mr-1.5 mt-2 flex h-8 max-w-full items-center gap-1.5 self-end rounded-md px-2 text-sm text-ink-3 hover:bg-fill hover:text-ink pointer-coarse:h-10">
              <Trash2 size={14} className="shrink-0" />
              <span className="min-w-0 truncate">{t("role.delete")}</span>
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

/** A value typed in place: saved as it is left or Enter is pressed (lines: Enter is a line's
 *  end), if changed; one refused goes back to what it was. */
function Field({ value, save, label, lines, maxLength, className }: { value: string; save: (v: string) => Promise<boolean>; label: string; lines?: boolean; maxLength?: number; className?: string }) {
  const [v, setV] = useState(value);
  const commit = () => {
    const next = v.trim();
    if (next === value) return setV(value);
    void save(next).then((ok) => ok || setV(value));
  };
  const p = { "aria-label": label, value: v, maxLength, onBlur: commit, className: cn(BOX, className) };
  if (lines) return <textarea {...p} rows={3} onChange={(e) => setV(e.target.value)} className={cn(p.className, "resize-none")} />;
  return <input {...p} onChange={(e) => setV(e.target.value)} onKeyDown={(e) => !e.nativeEvent.isComposing && e.keyCode !== 229 && e.key === "Enter" && e.currentTarget.blur()} />;
}
