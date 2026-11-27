// The name of the Iridium whose chat is shown beside another's list in the desktop app
// (crossfav.ts, the app's assemble.go): its face and its name by the chat's title, lest one write
// to the wrong one. Pressed, that Iridium is shown whole, its own list beside its chat.
import { assembledFull, usePart } from "./crossfav";
import { t } from "./i18n";
import { Tip } from "./ui";

export function AssembledChip() {
  const p = usePart();
  if (p.part !== "chat" || !p.title) return null;
  return (
    <Tip label={t("app.assembledWhole")} side="bottom">
      <button
        type="button"
        onClick={assembledFull}
        className="g-press flex h-6 max-w-40 shrink-0 items-center gap-1.5 rounded-full bg-fill pr-2.5 pl-1 text-xs text-ink-2 hover:bg-fill-2 hover:text-ink"
      >
        {p.face ? (
          <img src={p.face} alt="" width={15} height={15} className="size-[15px] shrink-0 rounded-full object-cover" />
        ) : (
          <span className="grid size-[15px] shrink-0 place-items-center rounded-full bg-fill-2 text-[9px] font-semibold">{[...p.title][0]?.toUpperCase()}</span>
        )}
        <span className="truncate">{p.title}</span>
      </button>
    </Tip>
  );
}
