// The members an @ may name in a channel (agents, people, apps: peers, the user, 2026-11-13), over the composer as what an @ names is
// elsewhere (composer.tsx AtList): a row each, its face (an agent's own, as beside its words; a person's avatar), its
// name, what it runs as (a person's app) faint at the end, its lamp. ↑↓ and Enter or Tab on a keyboard, a tap on a phone.
import { Bot, Users } from "lucide-react";
import type { CSSProperties } from "react";
import { t } from "../../i18n";
import { Avatar, cn, Lamp } from "../../ui";
import { Face } from "./face";
import type { AgentRow } from "./lib";

export function NickList({ rows, at, pick, point }: { rows: AgentRow[]; at: number; pick: (r: AgentRow) => void; point: (i: number) => void }) {
  return (
    <div role="listbox" aria-label={t("channel.members")} className="g-pop glass thick float absolute bottom-full left-2 z-10 mb-2 max-h-[min(360px,50dvh)] w-80 max-w-[calc(100%-16px)] origin-bottom-left no-bar overflow-y-auto overscroll-contain rounded-lg p-1.5 text-ink">
      <div className="g-stagger flex flex-col">
        {rows.map((r, i) => (
          <button
            key={r.session || r.member}
            type="button"
            role="option"
            aria-selected={i === at}
            ref={(el) => void (i === at && el?.scrollIntoView({ block: "nearest" }))}
            onPointerDown={(e) => e.preventDefault()}
            onPointerMove={() => i === at || point(i)}
            onClick={() => pick(r)}
            style={{ "--i": i } as CSSProperties}
            className={cn("flex h-8 w-full shrink-0 cursor-default items-center gap-2.5 rounded-md px-2.5 text-left text-base outline-none select-none pointer-coarse:h-11", i === at && "bg-fill")}
          >
            {/* The names that call more than one: a mark of their own, no logo (the user, 2026-11-13). */}
            {r.member === "*" ? <Users size={14} className="shrink-0 text-ink-2" /> : r.member === "agent:*" ? <Bot size={14} className="shrink-0 text-ink-2" /> : r.person ? <Avatar src={r.avatar} name={r.nick} size={14} /> : <Face id={r.session} agent={r.agent} size={14} />}
            <span className="shrink-0">{r.nick}</span>
            <span className="min-w-0 flex-1 truncate text-right text-xs text-ink-3">{r.name}</span>
            <Lamp status={r.status} />
          </button>
        ))}
      </div>
    </div>
  );
}
