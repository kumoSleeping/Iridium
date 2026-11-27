// The model menu of an agent before it is started (invite.tsx): its kind's models and efforts,
// and dsh's presets. They pop at once (kumo, 2026-11-15: not looked up as the menu opens): asked
// for as the members' menu opens (prefetchChoices), kept in this browser for the next visit, and
// the server answers with what it kept (../choices.ts).
import { useQuery } from "../../api/query";
import { Check } from "lucide-react";
import type { Agent, Model } from "../../api/types.gen";
import { t, useLang } from "../../i18n";
import { choicesQ, type At } from "../choices";
import { featKeys, ModelList, modelView, nextPick, presetName, startKeys } from "../meters";

export { choicesQ, draftDefault, prefetchChoices } from "../choices";

/** models with def first; listed in groups (dsh's providers), its group first, def at its head. */
export function defaultFirst(models: Model[], def?: Model): Model[] {
  if (!def || !models.includes(def)) return models;
  const mates = models.filter((m) => m !== def && !!def.group && m.group === def.group);
  return [def, ...mates, ...models.filter((m) => m !== def && !mates.includes(m))];
}

/** What a draft's menu sets: its model, effort, preset, and the key it starts with pressed
 *  (attach: one button's id of AgentChoices.attached, "" none). */
export type Draft = { model?: string; effort?: string; preset?: string; attach?: string };

/** The model menu for an agent not yet started: its kind's models where it would run, else those
 *  the page knows; the one it runs when given none first, ticked while model is "". Picking one
 *  adds it (set; the agent's own default as ""). dsh: its presets at the foot, picked in place.
 *  At the effort track's right the key it may start with (Claude's UltraCode, OMD's Ultra), as a
 *  session's: a press the next of its modes (attach). */
export function DraftModels({ agent, at, model, effort, preset, attach, set }: { agent: Agent; at: At; model: string; effort: string; preset?: string; attach: string; set: (p: Draft) => void }) {
  const lang = useLang();
  const asked = useQuery(choicesQ(agent, at)).data;
  const known = asked?.models.length ? asked.models : undefined;
  const v = modelView({ agent, models: known, efforts: asked?.efforts, pick: model, model, effort, ran: asked?.ran, effortOwn: asked?.effort });
  const presets = asked?.presets?.presets ?? [];
  const now = preset || asked?.presets?.default || presets[0]?.id || "";
  // The key's buttons pressed by their ids: a key for some presets alone, off under another.
  const start = (p: string) => startKeys(asked?.attached, "effort", lang, attach, p, (id) => id);
  const { keys } = start(preset || asked?.presets?.default || "");
  const { feat, name, featKey, featTag } = featKeys(keys, () => {
    const next = nextPick(keys);
    if (next !== undefined) set({ attach: next });
  });
  if (!v.models.length) return null;
  // A preset its pick is not for: the pick goes with it.
  const setPreset = (p: string) => set({ preset: p === asked?.presets?.default ? "" : p, ...(attach && !start(p).fits ? { attach: "" } : {}) });
  // Under the models, its own block (ModelList's foot is the effort's).
  const foot =
    presets.length > 1 ? (
      <div className="mt-1 flex shrink-0 flex-col border-t border-(--edge) pt-1">
        <div className="truncate px-2.5 pb-0.5 text-xs text-ink-3 select-none">{t("composer.preset")}</div>
        {presets.map((p) => (
          <button key={p.id} type="button" onClick={() => setPreset(p.id)} className="flex h-8 w-full cursor-default items-center gap-2.5 rounded-md px-2.5 text-left text-base outline-none select-none hover:bg-fill focus-visible:bg-fill pointer-coarse:h-9">
            <span className="min-w-0 flex-1 truncate">{presetName(p.id, p)}</span>
            {p.id === now && <Check size={14} className="text-ink" />}
          </button>
        ))}
      </div>
    ) : undefined;
  return (
    <>
      <ModelList models={defaultFirst(v.models, v.def)} value={v.value} pickModel={(x) => set({ model: v.def?.default && x === v.def.value ? "" : x })} levels={v.levels} level={v.level} setEffort={async (e) => (set({ effort: e }), true)} featKey={featKey} featTag={featTag} foot={!!feat} name={name} />
      {foot}
    </>
  );
}
