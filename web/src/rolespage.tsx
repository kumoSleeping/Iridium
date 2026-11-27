// A role in 管理 (docs/channels.md "角色"; kumo, 2026-11-16; 2026-11-27: in an agent's own detail,
// drawn as the window draws the rest): what its row unfolds, one line each, the name at the left
// and the value or control at the right: its name typed in place, 设定 unfolding its persona to
// write, its face (pressed, a picture cropped there), its model (dsh: preset), what it remembers;
// at the foot 删除角色, pressed twice. Each saved as it is changed (PATCH /v1/internal/roles/{id}).
// No effort: chosen at each start (otae, 2026-11-21). A new one: its name, where it runs, a
// host's persona to take; 创建 at the foot. Nothing opens a page of its own (kumo, 2026-11-27).
import { Menu } from "@base-ui/react/menu";
import { useQueryClient } from "@tanstack/react-query";
import { useQuery } from "./api/query";
import { ChevronDown } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api, ApiError } from "./api/client";
import type { Agent, HostPersona, Role, RolePatch } from "./api/types.gen";
import { MainKey } from "./card";
import { defaultFirst } from "./chat/channel/pick";
import { choicesQ } from "./chat/choices";
import { validNick } from "./chat/channel/lib";
import { ModelList, modelView, presetName } from "./chat/meters";
import { Choose } from "./choose";
import { asDataURL, FacePick } from "./facepick";
import { Crop } from "./profile";
import { t, useLang } from "./i18n";
import { agentsQ, useMachines } from "./data";
import { HostPersonas, PersonaEditor, personaHead } from "./persona";
import { RoleFace, roleModel } from "./roles";
import { Field, Unfold } from "./rows";
import { errText, toast } from "./toast";
import { cn, MenuPortal } from "./ui";

/** A value typed in a line's right: no box till pointed at or typed in. */
const TYPE_IN = "no-ring h-8 w-44 min-w-0 rounded-md bg-transparent px-2 text-right text-sm outline-none placeholder:text-ink-3 hover:bg-wash focus:bg-fill";
const POPUP = "g-unfold glass thick float rounded-lg p-1.5 text-ink outline-none";

/** The agents a role may be at machine (""; the host): those it runs and has on, the host's
 *  installed ones. */
function useKinds(machine: string): Agent[] {
  const host = useQuery(agentsQ).data;
  const ms = useMachines(true);
  if (!machine) return (host ?? []).filter((a) => !a.missing).map((a) => a.agent);
  return (ms.find((m) => m.id === machine)?.agents ?? []).map((a) => a.agent as Agent);
}

/** A new role for agent: its name, where it runs (with more than one computer), a host's persona
 *  taken (its name where none is typed, and its persona). Made, made is told it. */
export function NewRoleFold({ agent, made }: { agent: Agent; made: (r: Role) => void }) {
  useLang();
  const qc = useQueryClient();
  const ms = useMachines(true);
  const [machine, setMachine] = useState("");
  const kinds = useKinds(machine);
  const kind = kinds.includes(agent) ? agent : "";
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
      const r = await api.newRole({ name: name.trim(), agent: kind, ...(machine ? { machine } : {}), ...(took ? { persona: took.text } : {}) });
      qc.setQueriesData<Role[]>({ queryKey: ["roles"] }, (old) => (old ? [...old.filter((x) => x.id !== r.id), r] : old));
      made(r);
    } catch (e) {
      toast(t("err.action"), { error: true, description: refused(e) });
      setBusy(false);
    }
  };
  return (
    <div className="flex flex-col">
      <Field label={t("channel.nick")}>
        <input
          autoFocus
          aria-label={t("channel.nick")}
          value={name}
          maxLength={32}
          placeholder={took?.name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => !e.nativeEvent.isComposing && e.keyCode !== 229 && e.key === "Enter" && void create()}
          className={cn(TYPE_IN, "bg-fill", bad && "text-err")}
        />
      </Field>
      {ms.length > 1 && (
        <Field label={t("role.computer")}>
          <Choose label={t("role.computer")} value={machine} onChange={(m) => setMachine(m)} options={ms.map((m) => ({ value: m.id, label: m.name }))} />
        </Field>
      )}
      {took && (
        <Field label={t("profile.persona")}>
          <span className="truncate">{took.name}</span>
        </Field>
      )}
      <HostPersonas take={take} />
      <div className="flex justify-end pt-2">
        <MainKey busy={busy} disabled={!ok} onClick={() => void create()}>
          {t("role.create")}
        </MainKey>
      </div>
    </div>
  );
}

/** Where a role's agent runs: its machine, else here. */
export const placeOf = (r: Role) => (r.machine ? { machine: r.machine } : NO_PLACE);
const NO_PLACE = {};

/** What a refused change says: a name taken or not one, else the server's words. */
const refused = (e: unknown) => (e instanceof ApiError && e.status === 409 ? t("channel.nickTaken") : e instanceof ApiError && e.status === 400 && /^name:/.test(e.message) ? t("channel.nickBad") : errText(e));

/** One role, as its row unfolds it; gone: it was deleted. */
export function RoleFold({ role, gone }: { role: Role; gone: () => void }) {
  useLang();
  const qc = useQueryClient();
  const [open, setOpen] = useState<"" | "persona" | "crop">("");
  // A picture chosen for the face, cropped round under its line as 编辑资料's is.
  const [picture, setPicture] = useState<string | null>(null);
  // The models' menu opens to the side with more room (otae, 2026-11-22: cut to what was left below).
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
    try {
      await api.deleteRole(role.id);
      qc.setQueriesData<Role[]>({ queryKey: ["roles"] }, (old) => old?.filter((r) => r.id !== role.id));
      gone();
    } catch (e) {
      toast(t("err.action"), { error: true, description: errText(e) });
    }
  };
  const asked = useQuery(choicesQ(role.agent, placeOf(role))).data;
  const v = modelView({ agent: role.agent, models: asked?.models.length ? asked.models : undefined, efforts: asked?.efforts, pick: role.model, model: role.model, ran: asked?.ran, effortOwn: asked?.effort });
  const presets = asked?.presets?.presets ?? [];
  const head = personaHead(role.persona);
  const drop = () => (setPicture((old) => (old && URL.revokeObjectURL(old), null)), setOpen(""));
  return (
    <div className="flex flex-col">
      <Field label={t("channel.nick")}>
        <TypeIn key={role.name} value={role.name} label={t("channel.nick")} save={(name) => patch({ name })} />
      </Field>
      <Field label={t("profile.persona")} onClick={() => setOpen(open === "persona" ? "" : "persona")} open={open === "persona"}>
        <span className={cn("truncate", !head && "text-ink-3")}>{head || t("profile.personaNone")}</span>
      </Field>
      <Unfold open={open === "persona"}>
        <PersonaEditor value={role.persona} save={(v) => patch({ persona: v }).then((ok) => ok || Promise.reject(new Error(t("err.action"))))} />
      </Unfold>
      <FacePick label={t("profile.avatar")} preview={<RoleFace role={role} size={24} />} set={(avatar) => patch({ avatar })} upload={(f) => (setPicture(URL.createObjectURL(f)), setOpen("crop"))} />
      <Unfold open={open === "crop" && !!picture}>
        {picture && (
          <div className="flex justify-center">
            <Crop
              src={picture}
              onCancel={drop}
              onDone={(b) =>
                void asDataURL(b)
                  .then((avatar) => patch({ avatar }))
                  .then((ok) => ok && drop())
              }
            />
          </div>
        )}
      </Unfold>
      {v.models.length > 0 && (
        <Field label={t("composer.model")}>
          <Menu.Root
            onOpenChange={(o) => {
              const r = modelKey.current?.getBoundingClientRect();
              if (o && r) setUp(r.top + r.height / 2 > window.innerHeight / 2);
            }}
          >
            <Menu.Trigger ref={modelKey} aria-label={t("composer.model")} className="g-press -mr-1 flex h-8 min-w-0 cursor-default items-center gap-1 rounded-md px-2 text-sm text-ink-2 outline-none hover:bg-wash data-popup-open:bg-wash">
              <span className="min-w-0 truncate">{v.name || roleModel(role, asked?.models)}</span>
              <ChevronDown size={14} className="shrink-0 text-ink-4" />
            </Menu.Trigger>
            <MenuPortal>
              <Menu.Positioner side={up ? "top" : "bottom"} align="end" sideOffset={6} collisionPadding={8} className="z-50">
                <Menu.Popup finalFocus={false} className={cn(POPUP, "flex max-h-[min(560px,var(--available-height))] w-[min(280px,calc(100vw-16px))] flex-col")}>
                  {/* The models alone: the effort is chosen at each start. */}
                  <ModelList models={defaultFirst(v.models, v.def)} value={v.value} pickModel={(model) => void patch({ model })} levels={[]} level="" setEffort={async () => false} />
                </Menu.Popup>
              </Menu.Positioner>
            </MenuPortal>
          </Menu.Root>
        </Field>
      )}
      {role.agent === "dsh" && presets.length > 1 && (
        <Field label={t("composer.preset")}>
          <Choose label={t("composer.preset")} value={role.preset || asked?.presets?.default || presets[0].id} onChange={(preset) => void patch({ preset })} options={presets.map((p) => ({ value: p.id, label: presetName(p.id, p) }))} />
        </Field>
      )}
      <Field label={t("profile.memory")}>
        <span className="tabular-nums">{role.memories ?? 0}</span>
      </Field>
      <div className="flex justify-end pt-1">
        <SureText label={t("role.delete")} sure={t("role.deleteSure")} run={() => void remove()} />
      </div>
    </div>
  );
}

/** A word key that does something for good: pressed once it turns red and says so, pressed again
 *  it does it (docs/前端设计规范.md "确认在原处"); left alone, it comes back. */
function SureText({ label, sure, run }: { label: string; sure: string; run: () => void }) {
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const timer = setTimeout(() => setArmed(false), 4000);
    return () => clearTimeout(timer);
  }, [armed]);
  return (
    <button type="button" onClick={() => (armed ? (setArmed(false), run()) : setArmed(true))} onBlur={() => setArmed(false)} className={cn("g-press h-8 rounded-md px-2.5 text-sm pointer-coarse:h-10", armed ? "bg-fill text-err" : "text-ink-3 hover:bg-wash hover:text-ink-2")}>
      {armed ? sure : label}
    </button>
  );
}

/** A value typed in place: saved as it is left or Enter is pressed, if changed; one refused goes
 *  back to what it was. */
function TypeIn({ value, save, label }: { value: string; save: (v: string) => Promise<boolean>; label: string }) {
  const [v, setV] = useState(value);
  const commit = () => {
    const next = v.trim();
    if (next === value) return setV(value);
    void save(next).then((ok) => ok || setV(value));
  };
  return <input aria-label={label} value={v} onBlur={commit} onChange={(e) => setV(e.target.value)} onKeyDown={(e) => !e.nativeEvent.isComposing && e.keyCode !== 229 && e.key === "Enter" && e.currentTarget.blur()} className={TYPE_IN} />;
}
