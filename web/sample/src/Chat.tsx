// The chat page: a frosted top bar, the messages by source, and the bottom stack (Composer.tsx).
import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { ArrowUpRight, ChevronLeft, Copy, Download, Ellipsis, FileCode2, FileText, FolderInput, FolderOpen, Pencil, Pin, RotateCcw, Archive, Square, SquareTerminal } from "lucide-react";
import { forwardRef, type ReactNode } from "react";
import { pick, terminals, type Block, type Message, type Session } from "./data";
import { t, useLang } from "./i18n";
import { agentName, Avatar, cn, IconKey, Logo, LogoLamp, statusText, Tip } from "./ui";

export function TopBar({ s, folder, phone, onBack, menuOpen, setMenuOpen }: { s: Session; folder: string; phone: boolean; onBack: () => void; menuOpen: boolean; setMenuOpen: (o: boolean) => void }) {
  const lang = useLang();
  const items: [ReactNode, string][] = [
    [<Pencil size={15} />, t("menu.rename")],
    [<Pin size={15} />, t("menu.pin")],
    [<FolderInput size={15} />, t("menu.move")],
    [<Copy size={15} />, t("menu.copyId")],
    [<Archive size={15} />, t("menu.archive")],
    [<Square size={14} />, t("menu.stop")],
  ];
  return (
    <header className="glass absolute inset-x-0 top-0 z-20 flex h-12 items-center gap-1 pr-2 pl-2 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13">
      {phone && (
        <IconKey label={t("nav.back")} onClick={onBack} tipSide="bottom" className="-mr-1">
          <ChevronLeft size={20} />
        </IconKey>
      )}
      <div className={cn("flex min-w-0 flex-1 items-center gap-2.5", !phone && "pl-2")}>
        <Tip label={`${agentName(s.agent)} · ${statusText(s.status)}`} side="bottom">
          <span className="grid">
            <LogoLamp agent={s.agent} status={s.status} size={18} />
          </span>
        </Tip>
        <h1 className="m-0 min-w-0 truncate text-base font-semibold">{pick(s.title, lang)}</h1>
        {!phone && <span className="min-w-0 shrink-[2] truncate font-mono text-xs text-ink-3">{folder}</span>}
      </div>
      <Tip label={t("top.online", { ms: 38 })} side="bottom">
        <span className="mx-1.5 flex items-center gap-1.5 text-xs text-ink-3 tabular-nums" aria-label={t("top.online", { ms: 38 })}>
          <span className="size-1.5 rounded-full bg-ink-3" />
          {!phone && "38 ms"}
        </span>
      </Tip>
      <IconKey label={t("top.files")} tipSide="bottom">
        <FolderOpen size={16} />
      </IconKey>
      <Menu.Root open={menuOpen} onOpenChange={setMenuOpen}>
        <Tip label={t("top.more")} side="bottom">
          <Menu.Trigger
            render={
              <button type="button" aria-label={t("top.more")} className="g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10">
                <Ellipsis size={17} />
              </button>
            }
          />
        </Tip>
        <Menu.Portal>
          <Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
            <Menu.Popup className="g-unfold glass thick float min-w-48 rounded-lg p-1.5 text-ink outline-none">
              <div className="g-stagger flex flex-col">
                {items.map(([icon, label], i) => (
                  <Menu.Item key={label} style={{ "--i": i } as React.CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11">
                    <span className="text-ink-2">{icon}</span>
                    {label}
                  </Menu.Item>
                ))}
              </div>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </header>
  );
}

/** `code` spans in plain text. */
function Inline({ text }: { text: string }) {
  const parts = text.split(/(`[^`]+`)/g);
  return <>{parts.map((p, i) => (p.startsWith("`") ? <code key={i}>{p.slice(1, -1)}</code> : p))}</>;
}

const TOOL_ICON: Record<string, ReactNode> = {
  Read: <FileText size={14} />,
  Edit: <Pencil size={14} />,
  Bash: <SquareTerminal size={14} />,
};

function BlockView({ b, onFile }: { b: Block; onFile: (name: string) => void }) {
  const lang = useLang();
  if (b.t === "text")
    return (
      <div className="prose-ir text-md">
        <p>
          <Inline text={pick(b.text, lang)} />
        </p>
      </div>
    );
  if (b.t === "tool")
    return (
      <div className="flex h-6 min-w-0 items-center gap-2 text-sm text-ink-3" aria-label={`${t("msg.tool")}: ${b.name} ${b.arg}`}>
        <span className="shrink-0 text-ink-3">{TOOL_ICON[b.name]}</span>
        <span className="shrink-0 text-ink-2">{b.name}</span>
        <span className="min-w-0 truncate font-mono text-xs">{b.arg}</span>
        {b.stat && <span className="shrink-0 font-mono text-xs tabular-nums">{b.stat}</span>}
      </div>
    );
  if (b.t === "image")
    return (
      <Dialog.Root>
        <Tip label={t("msg.openImage")}>
          <Dialog.Trigger
            render={
              <button type="button" aria-label={`${b.name} · ${t("msg.openImage")}`} className="g-press block w-full max-w-[340px] overflow-hidden rounded-lg shadow-[0_0_0_0.5px_var(--edge)]">
                <img src={b.src} alt={b.name} className="block aspect-[16/10] w-full object-cover" />
              </button>
            }
          />
        </Tip>
        <Dialog.Portal>
          <Dialog.Backdrop className="g-fade fixed inset-0 z-50 bg-paper/70 backdrop-blur-sm transition-opacity duration-(--dur-2) data-ending-style:opacity-0 data-starting-style:opacity-0" />
          <Dialog.Popup className="g-unfold fixed top-1/2 left-1/2 z-50 w-[min(1024px,92vw)] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-xl shadow-(--shadow-float) outline-none">
            <Dialog.Close className="block w-full">
              <img src={b.src} alt={b.name} className="block w-full" />
            </Dialog.Close>
          </Dialog.Popup>
        </Dialog.Portal>
      </Dialog.Root>
    );
  return (
    <div className="flex w-full max-w-[340px] items-center gap-3 rounded-lg py-2 pr-1.5 pl-3 shadow-[0_0_0_0.5px_var(--edge),0_0_0_1px_var(--edge)]">
      <FileCode2 size={20} className="shrink-0 text-ink-3" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm">{b.name}</div>
        <div className="text-xs text-ink-3 tabular-nums">{b.size}</div>
      </div>
      <IconKey label={t("msg.download")} onClick={() => onFile(b.name)}>
        <Download size={16} />
      </IconKey>
    </div>
  );
}

/** Your words: a bubble; from another panel, that panel's avatar on its corner. */
export const YouBubble = forwardRef<HTMLDivElement, { text: string; panel?: keyof typeof terminals; flip?: string }>(function YouBubble({ text, panel, flip }, ref) {
  const term = panel ? terminals[panel] : null;
  return (
    <div className="flex justify-end pl-10">
      <div ref={ref} data-flip={flip} className="relative max-w-[min(560px,100%)] px-3.5 py-2">
        <span data-bubble-bg className="absolute inset-0 rounded-xl bg-fill" aria-hidden />
        <span className="relative block text-md whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</span>
        {term && (
          <Tip label={t("msg.fromPanel", { name: term.name })}>
            <span className="absolute -top-1.5 -left-1.5 grid rounded-full" style={{ boxShadow: "0 0 0 2px var(--paper)" }} aria-label={t("msg.fromPanel", { name: term.name })}>
              <Avatar src={term.avatar} size={18} />
            </span>
          </Tip>
        )}
      </div>
    </div>
  );
});

function Letter({ m, onJump }: { m: Extract<Message, { kind: "letter" }>; onJump: (id: string) => void }) {
  const lang = useLang();
  const name = pick(m.from.name, lang);
  const via = { ir: t("msg.viaIr"), plugin: t("msg.viaPlugin"), peer: t("msg.viaPeer") }[m.via];
  const head = (
    <>
      {m.from.agent ? <Logo agent={m.from.agent} size={15} /> : m.from.avatar ? <Avatar src={m.from.avatar} size={18} channel /> : null}
      <span className="min-w-0 truncate text-sm font-medium text-ink-2">{name}</span>
    </>
  );
  return (
    <div className="flex pr-10">
      <div className="max-w-[min(560px,100%)] rounded-lg px-3.5 pt-2.5 pb-2.5 shadow-[0_0_0_1px_var(--edge)]">
        <Tip label={m.from.sessionId ? t("msg.jump", { name }) : `${name} · ${via}`}>
          {m.from.sessionId ? (
            <button type="button" onClick={() => onJump(m.from.sessionId!)} className="group -mx-1 flex max-w-full items-center gap-2 rounded-sm px-1 hover:bg-fill">
              {head}
              <ArrowUpRight size={13} className="shrink-0 text-ink-4 group-hover:text-ink-2" />
            </button>
          ) : (
            <span className="flex max-w-full items-center gap-2">{head}</span>
          )}
        </Tip>
        <p className="m-0 mt-1.5 text-md">{pick(m.text, lang)}</p>
      </div>
    </div>
  );
}

export function MessageView({ m, onFile, onJump, flyRef }: { m: Message; onFile: (n: string) => void; onJump: (id: string) => void; flyRef?: React.Ref<HTMLDivElement> }) {
  const lang = useLang();
  if (m.kind === "system")
    return (
      <div className="flex items-center justify-center gap-1.5 py-1 text-xs text-ink-3">
        <RotateCcw size={12} />
        <span>{pick(m.text, lang)}</span>
        <span className="tabular-nums">· {m.time}</span>
      </div>
    );
  if (m.kind === "you") return <YouBubble ref={flyRef} text={pick(m.text, lang)} panel={m.panel} />;
  if (m.kind === "letter") return <Letter m={m} onJump={onJump} />;
  return (
    <div className="flex flex-col items-start gap-2 pr-6">
      {m.blocks.map((b, i) => (
        <BlockView key={i} b={b} onFile={onFile} />
      ))}
    </div>
  );
}
