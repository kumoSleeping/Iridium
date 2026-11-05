// This panel's identity, at the sidebar's bottom-right: a web-panel terminal (not a person), shown like
// a signed-in account. Its popover: avatar (upload, cropped round), rename, read-only permissions,
// appearance and language, sign out.
import { Popover } from "@base-ui/react/popover";
import { Slider } from "@base-ui/react/slider";
import { Camera, Check, ChevronLeft, ChevronRight, LogOut, Minus, Monitor, Moon, PencilLine, ShieldCheck, Sun } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { setLang, t, type Lang } from "./i18n";
import { Avatar, cn, IconKey, Logo, PopSurface, Tip } from "./ui";

export type ThemePref = "light" | "dark" | "auto";
type Page = "main" | "perms" | "crop";

export function MeChip({
  me,
  onMe,
  theme,
  setTheme,
  lang,
  open,
  onOpenChange,
  initialPage = "main",
}: {
  me: { name: string; avatar: string };
  onMe: (m: { name: string; avatar: string }) => void;
  theme: ThemePref;
  setTheme: (t: ThemePref) => void;
  lang: Lang;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  initialPage?: Page;
}) {
  const [page, setPage] = useState<Page>(initialPage);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(me.name);
  const [img, setImg] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) setTimeout(() => (setPage("main"), setEditing(false)), 150);
      }}
    >
      <Tip label={t("me.title")}>
        <Popover.Trigger
          render={
            <button type="button" className="g-press flex h-9 min-w-0 items-center gap-2 rounded-full py-1 pr-2.5 pl-1 hover:bg-fill pointer-coarse:h-11">
              <Avatar src={me.avatar} size={24} />
              <span className="truncate text-sm text-ink-2">{me.name}</span>
            </button>
          }
        />
      </Tip>
      <PopSurface side="top" align="end">
        <input
          ref={file}
          type="file"
          accept="image/*"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (!f) return;
            setImg(URL.createObjectURL(f));
            setPage("crop");
            e.target.value = "";
          }}
        />
        <div key={page} className={cn("w-[min(288px,calc(100vw-16px))]", page !== "main" && "g-slide")}>
          {page === "main" && (
            <div className="flex flex-col p-1.5">
              <div className="flex items-center gap-3 px-1.5 pt-1.5 pb-2.5">
                <Tip label={t("me.avatar")}>
                  <button type="button" aria-label={t("me.avatar")} onClick={() => file.current?.click()} className="group g-press relative shrink-0 rounded-full">
                    <Avatar src={me.avatar} size={44} />
                    <span className="absolute inset-0 grid place-items-center rounded-full bg-ink/35 text-paper opacity-0 transition-opacity duration-(--dur-1) group-hover:opacity-100">
                      <Camera size={16} />
                    </span>
                  </button>
                </Tip>
                {editing ? (
                  <input
                    autoFocus
                    aria-label={t("me.rename")}
                    value={draft}
                    maxLength={12}
                    onChange={(e) => setDraft(e.target.value)}
                    onBlur={() => setEditing(false)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && draft.trim()) {
                        onMe({ ...me, name: draft.trim() });
                        setEditing(false);
                      }
                      if (e.key === "Escape") {
                        e.stopPropagation();
                        setEditing(false);
                      }
                    }}
                    className="no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2 text-base font-semibold"
                  />
                ) : (
                  <span className="min-w-0 flex-1 truncate text-base font-semibold">{me.name}</span>
                )}
                {!editing && (
                  <IconKey label={t("me.rename")} onClick={() => (setDraft(me.name), setEditing(true))} className="size-7">
                    <PencilLine size={15} />
                  </IconKey>
                )}
              </div>
              <Row icon={<ShieldCheck size={16} />} onClick={() => setPage("perms")} end={<ChevronRight size={14} className="text-ink-4" />}>
                {t("me.perms")}
              </Row>
              <div className="flex items-center justify-between gap-2 px-1.5 py-1.5">
                <Segmented
                  label={t("me.theme")}
                  value={theme}
                  onChange={(v) => setTheme(v as ThemePref)}
                  items={[
                    { v: "light", label: t("me.light"), icon: <Sun size={15} /> },
                    { v: "dark", label: t("me.dark"), icon: <Moon size={15} /> },
                    { v: "auto", label: t("me.auto"), icon: <Monitor size={15} /> },
                  ]}
                />
                <Segmented
                  label={t("me.lang")}
                  value={lang}
                  onChange={(v) => setLang(v as Lang)}
                  items={[
                    { v: "zh", label: "中文", icon: <span className="text-[13px]">中</span> },
                    { v: "en", label: "English", icon: <span className="text-[12px] font-medium">EN</span> },
                  ]}
                />
              </div>
              <Row icon={<LogOut size={16} />} onClick={() => onOpenChange(false)} muted>
                {t("me.logout")}
              </Row>
            </div>
          )}
          {page === "perms" && <Perms onBack={() => setPage("main")} />}
          {page === "crop" && img && (
            <Crop
              src={img}
              onCancel={() => setPage("main")}
              onDone={(url) => {
                onMe({ ...me, avatar: url });
                setPage("main");
              }}
            />
          )}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}

function Row({ icon, children, onClick, end, muted }: { icon: ReactNode; children: ReactNode; onClick: () => void; end?: ReactNode; muted?: boolean }) {
  return (
    <button type="button" onClick={onClick} className={cn("flex h-9 items-center gap-2.5 rounded-md px-2 text-left text-base hover:bg-fill pointer-coarse:h-11", muted && "text-ink-2 hover:text-ink")}>
      <span className="text-ink-2">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {end}
    </button>
  );
}

function Segmented({ label, value, onChange, items }: { label: string; value: string; onChange: (v: string) => void; items: { v: string; label: string; icon: ReactNode }[] }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex rounded-md bg-fill p-0.5">
      {items.map((it) => (
        <Tip key={it.v} label={it.label}>
          <button
            type="button"
            role="radio"
            aria-checked={value === it.v}
            aria-label={it.label}
            onClick={() => onChange(it.v)}
            className={cn("g-press grid h-7 min-w-8 place-items-center rounded-[5px] px-1.5 pointer-coarse:h-9", value === it.v ? "bg-paper text-ink shadow-[0_0_0_0.5px_var(--edge),0_1px_2px_var(--edge)]" : "text-ink-3 hover:text-ink")}
          >
            {it.icon}
          </button>
        </Tip>
      ))}
    </div>
  );
}

function Perms({ onBack }: { onBack: () => void }) {
  const yes = <Check size={15} aria-label={t("perm.yes")} className="text-ink" />;
  const no = <Minus size={15} aria-label={t("perm.no")} className="text-ink-4" />;
  const rows: [string, ReactNode][] = [
    [
      t("perm.agents"),
      <span className="flex gap-1.5">
        <Logo agent="claude" size={14} />
        <Logo agent="codex" size={14} />
        <Logo agent="pi" size={14} />
        <Logo agent="dsh" size={14} />
      </span>,
    ],
    [t("perm.workspaces"), <span className="text-sm text-ink-2">{t("perm.all")}</span>],
    [t("perm.others"), yes],
    [t("perm.copyOut"), yes],
    [t("perm.copyIn"), yes],
    [t("perm.answer"), yes],
    [t("perm.mustApprove"), no],
    [t("perm.daily"), <span className="text-sm text-ink-2">{t("perm.unlimited")}</span>],
  ];
  return (
    <div className="flex flex-col p-1.5">
      <div className="flex h-9 items-center gap-1">
        <IconKey label={t("nav.back")} onClick={onBack} className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <span className="text-sm font-semibold">{t("me.perms")}</span>
      </div>
      <dl className="flex flex-col px-2 pb-1">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-h-8 items-center justify-between gap-3 py-1">
            <dt className="text-sm text-ink-2">{k}</dt>
            <dd className="m-0 shrink-0">{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

/** Round crop: drag to place, slide to zoom. */
function Crop({ src, onCancel, onDone }: { src: string; onCancel: () => void; onDone: (url: string) => void }) {
  const [zoom, setZoom] = useState(1);
  const [pos, setPos] = useState({ x: 0, y: 0 });
  const drag = useRef<{ x: number; y: number } | null>(null);
  const im = useRef<HTMLImageElement>(null);
  const S = 176;
  const done = () => {
    const img = im.current!;
    const c = document.createElement("canvas");
    c.width = c.height = 256;
    const g = c.getContext("2d")!;
    const k = 256 / S;
    const cover = Math.max(S / img.naturalWidth, S / img.naturalHeight) * zoom;
    const w = img.naturalWidth * cover;
    const h = img.naturalHeight * cover;
    g.drawImage(img, ((S - w) / 2 + pos.x) * k, ((S - h) / 2 + pos.y) * k, w * k, h * k);
    onDone(c.toDataURL("image/png"));
  };
  return (
    <div className="flex flex-col items-center gap-3 p-3">
      <div
        className="relative cursor-grab touch-none overflow-hidden rounded-full bg-fill active:cursor-grabbing"
        style={{ width: S, height: S }}
        onPointerDown={(e) => {
          (e.target as HTMLElement).setPointerCapture(e.pointerId);
          drag.current = { x: e.clientX - pos.x, y: e.clientY - pos.y };
        }}
        onPointerMove={(e) => drag.current && setPos({ x: e.clientX - drag.current.x, y: e.clientY - drag.current.y })}
        onPointerUp={() => (drag.current = null)}
      >
        <img ref={im} src={src} alt="" draggable={false} className="absolute inset-0 size-full object-cover select-none" style={{ transform: `translate(${pos.x}px, ${pos.y}px) scale(${zoom})` }} />
      </div>
      <Slider.Root value={zoom} min={1} max={3} step={0.01} onValueChange={(v) => setZoom(v as number)} aria-label={t("me.zoom")} className="w-44">
        <Slider.Control className="flex h-6 items-center">
          <Slider.Track className="h-1 w-full rounded-full bg-fill-2">
            <Slider.Indicator className="rounded-full bg-ink-3" />
            <Slider.Thumb className="size-4 rounded-full bg-paper shadow-[0_0_0_0.5px_var(--edge),0_1px_3px_oklch(0_0_0/0.25)]" />
          </Slider.Track>
        </Slider.Control>
      </Slider.Root>
      <div className="flex w-full justify-end gap-2">
        <button type="button" onClick={onCancel} className="g-press h-8 rounded-md bg-fill px-3 text-sm">
          {t("me.cancel")}
        </button>
        <button type="button" onClick={done} className="g-press h-8 rounded-md bg-ink px-3 text-sm font-medium text-on-ink">
          {t("me.done")}
        </button>
      </div>
    </div>
  );
}
