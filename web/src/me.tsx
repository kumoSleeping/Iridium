// This panel's identity at the sidebar's bottom-left (plan "网页"): a terminal, not a person, shown
// like a signed-in account. Its popover: avatar (upload, cropped round, stored at 128px by the gate),
// rename, the read-only permissions, the device it runs on (its messages' badge), appearance and
// language, sign out. Local-only with no Passkey,
// it is 本机, which has nothing to rename or sign out of.
import { Popover } from "@base-ui/react/popover";
import { Slider } from "@base-ui/react/slider";
import { useQueryClient } from "@tanstack/react-query";
import { Camera, Check, ChevronLeft, ChevronRight, EyeOff, Laptop, LogOut, Minus, Monitor, Moon, PencilLine, Settings, ShieldCheck, Smartphone, Sun } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type { Device, Me, Rules } from "./api/types.gen";
import { LOCAL_ME, useMe, useWorkspaces } from "./data";
import { setLang, t, useLang, type Lang } from "./i18n";
import { errText, toast } from "./toast";
import { AGENTS, Avatar, cn, IconKey, keep, kept, Logo, PopSurface, tilde, Tip } from "./ui";

export type ThemePref = "light" | "dark" | "auto";

/** The devices a terminal may say it runs on, in the picker's order; none: no badge shown. */
const DEVICES = ["computer", "laptop", "phone", "none"] as const satisfies readonly Device[];
const DEVICE_ICON: Record<Device, (size: number) => ReactNode> = {
  computer: (n) => <Monitor size={n} />,
  laptop: (n) => <Laptop size={n} />,
  phone: (n) => <Smartphone size={n} />,
  none: (n) => <EyeOff size={n} />,
};
type Page = "main" | "perms" | "crop";

export function initialTheme(): ThemePref {
  const q = new URLSearchParams(location.search).get("theme");
  if (q === "light" || q === "dark" || q === "auto") return q;
  const s = kept("theme");
  return s === "light" || s === "dark" ? s : "auto";
}

const nameError = (e: unknown) => {
  if (e instanceof ApiError && e.status === 409) {
    const code = (e.body as { nameError?: string } | undefined)?.nameError;
    return code === "taken" ? t("me.nameTaken") : code ? t("me.nameBad") : t("me.localOnly");
  }
  return errText(e);
};

export function MeChip({ theme, setTheme, initialPage = "main", defaultOpen = false }: { theme: ThemePref; setTheme: (t: ThemePref) => void; initialPage?: Page; defaultOpen?: boolean }) {
  const { data } = useMe();
  const me: Me = data ?? LOCAL_ME;
  const name = me.local ? t("me.local") : me.name;
  const [open, setOpen] = useState(defaultOpen);
  // Each time it is put away, its body is drawn afresh next time: its first page, not renaming.
  const [round, setRound] = useState(0);
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) setTimeout(() => setRound((n) => n + 1), 150);
      }}
    >
      <Tip label={t("me.title")} align="start">
        <Popover.Trigger
          render={
            <button type="button" className="g-press flex h-11 w-full min-w-0 items-center gap-2.5 rounded-full py-1 pr-3 pl-1 hover:bg-fill data-popup-open:bg-fill pointer-coarse:h-12">
              {me.local ? <LocalMark size={32} /> : <Avatar src={me.avatar} name={name} size={32} />}
              <span className="truncate text-md text-ink">{name}</span>
            </button>
          }
        />
      </Tip>
      <PopSurface side="top" align="start">
        <MePanel key={round} theme={theme} setTheme={setTheme} initialPage={initialPage} />
      </PopSurface>
    </Popover.Root>
  );
}

/** The me popover's body. On a phone with a chat open it is a sheet of its own (a swipe up from the
 *  composer's left quarter, or 个人 in the chat bar's ⋯), the window's width, the settings key a row
 *  in it, not the sidebar's foot (the user, 2026-11-06). */
export function MePanel({ theme, setTheme, initialPage = "main", wide, onSettings, updateDot }: { theme: ThemePref; setTheme: (t: ThemePref) => void; initialPage?: Page; wide?: boolean; onSettings?: () => void; updateDot?: boolean }) {
  const lang = useLang();
  const qc = useQueryClient();
  const { data } = useMe();
  const me: Me = data ?? LOCAL_ME;
  const local = !!me.local;
  const name = me.local ? t("me.local") : me.name;
  const [page, setPage] = useState<Page>(initialPage);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(me.name);
  const [img, setImg] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const save = async (p: { name?: string; avatar?: Blob }) => {
    try {
      const next = await api.profile(p);
      qc.setQueryData(["me"], { ...me, name: next.name, avatar: next.avatar } satisfies Me);
      void qc.invalidateQueries({ queryKey: ["terminals"] });
      return true;
    } catch (e) {
      toast(p.name ? t("me.rename") : t("me.avatar"), { error: true, description: nameError(e) });
      return false;
    }
  };
  // The device this terminal runs on, for every panel to see: the badge on its messages' avatar.
  const device: Device = me.device ?? "computer";
  const setDevice = async (d: Device) => {
    if (d === device) return;
    try {
      const next = await api.device(d);
      qc.setQueryData(["me"], { ...me, device: next.device } satisfies Me);
      void qc.invalidateQueries({ queryKey: ["terminals"] });
    } catch (e) {
      toast(t("me.device"), { error: true, description: errText(e) });
    }
  };
  return (
    <>
      <input
        ref={file}
        type="file"
        accept="image/png,image/jpeg,image/webp"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (!f) return;
          setImg(URL.createObjectURL(f));
          setPage("crop");
          e.target.value = "";
        }}
      />
      <div key={page} className={cn(wide ? "w-full" : "w-[min(288px,calc(100vw-16px))]", page !== "main" && "g-slide")}>
        {page === "main" && (
          <div className="flex flex-col p-1.5">
            <div className="flex items-center gap-3 px-1.5 pt-1.5 pb-2.5">
              {me.local ? (
                <LocalMark size={44} />
              ) : (
                <Tip label={t("me.avatar")}>
                  <button type="button" aria-label={t("me.avatar")} onClick={() => file.current?.click()} className="group g-press relative shrink-0 rounded-full">
                    <Avatar src={me.avatar} name={name} size={44} />
                    <span className="absolute inset-0 grid place-items-center rounded-full bg-ink/35 text-paper opacity-0 transition-opacity duration-(--dur-1) group-hover:opacity-100">
                      <Camera size={16} />
                    </span>
                  </button>
                </Tip>
              )}
              {editing ? (
                <input
                  autoFocus
                  aria-label={t("me.rename")}
                  value={draft}
                  maxLength={12}
                  onChange={(e) => setDraft(e.target.value)}
                  onBlur={() => setEditing(false)}
                  onKeyDown={(e) => {
                    if (e.nativeEvent.isComposing) return;
                    if (e.key === "Enter" && draft.trim()) void save({ name: draft.trim() }).then((ok) => ok && setEditing(false));
                    if (e.key === "Escape") {
                      e.stopPropagation();
                      setEditing(false);
                    }
                  }}
                  className="no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2 text-base font-semibold"
                />
              ) : (
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-base font-semibold">{name}</span>
                  {me.local && <span className="block truncate text-xs text-ink-3">{t("me.localHint")}</span>}
                </span>
              )}
              {!editing && !me.local && (
                <IconKey label={t("me.rename")} onClick={() => (setDraft(me.name), setEditing(true))} className="size-7">
                  <PencilLine size={15} />
                </IconKey>
              )}
            </div>
            <Row icon={<ShieldCheck size={16} />} onClick={() => setPage("perms")} end={<ChevronRight size={14} className="text-ink-4" />}>
              {t("me.perms")}
            </Row>
            {onSettings && (
              <Row
                icon={<Settings size={16} />}
                onClick={onSettings}
                end={
                  <span className="flex items-center gap-1.5">
                    {updateDot && <span className="g-pop size-2 rounded-full bg-err" aria-label={t("settings.updateReady")} />}
                    <ChevronRight size={14} className="text-ink-4" />
                  </span>
                }
              >
                {t("nav.settings")}
              </Row>
            )}
            <div className="flex items-center justify-between gap-2 py-1.5 pr-1.5 pl-2">
              <span className="flex min-w-0 items-center gap-2.5 text-base">
                <span className="text-ink-2">{DEVICE_ICON[device]?.(16) ?? <Monitor size={16} />}</span>
                <span className="truncate">{t("me.device")}</span>
              </span>
              <Segmented
                label={t("me.device")}
                value={device}
                onChange={(v) => void setDevice(v as Device)}
                items={[
                  ...DEVICES.map((d) => ({ v: d, label: t(`device.${d}`), icon: DEVICE_ICON[d](15) })),
                ]}
              />
            </div>
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
              <LangSwitch lang={lang} />
            </div>
            {!me.local && (
              <Row icon={<LogOut size={16} />} onClick={() => void api.logout().then(() => location.assign("/_ir/login"), (e) => toast(t("me.logout"), { error: true, description: errText(e) }))} muted>
                {t("me.logout")}
              </Row>
            )}
          </div>
        )}
        {page === "perms" && <Perms p={me.rules} local={local} onBack={() => setPage("main")} />}
        {page === "crop" && img && (
          <Crop
            src={img}
            onCancel={() => setPage("main")}
            onDone={(blob) => void save({ avatar: blob }).then((ok) => ok && setPage("main"))}
          />
        )}
      </div>
    </>
  );
}

/** 本机's mark in place of an avatar: the machine itself. */
export function LocalMark({ size }: { size: number }) {
  return (
    <span className="grid shrink-0 place-items-center rounded-full bg-fill-2 text-ink-2" style={{ width: size, height: size }} aria-hidden>
      <Laptop size={Math.round(size * 0.55)} strokeWidth={1.75} />
    </span>
  );
}

export function LangSwitch({ lang }: { lang: Lang }) {
  return (
    <Segmented
      label={t("me.lang")}
      value={lang}
      onChange={(v) => setLang(v as Lang)}
      items={[
        { v: "zh", label: "中文", icon: <span className="text-[13px]">中</span> },
        { v: "en", label: "English", icon: <span className="text-[12px] font-medium">EN</span> },
      ]}
    />
  );
}

export function applyTheme(v: ThemePref, systemDark: boolean) {
  document.documentElement.dataset.theme = v === "auto" ? (systemDark ? "dark" : "light") : v;
}
export const keepTheme = (v: ThemePref) => keep("theme", v === "auto" ? null : v);

function Row({ icon, children, onClick, end, muted }: { icon: ReactNode; children: ReactNode; onClick: () => void; end?: ReactNode; muted?: boolean }) {
  return (
    <button type="button" onClick={onClick} className={cn("flex h-9 items-center gap-2.5 rounded-md px-2 text-left text-base hover:bg-fill pointer-coarse:h-11", muted && "text-ink-2 hover:text-ink")}>
      <span className="text-ink-2">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {end}
    </button>
  );
}

export function Segmented({ label, value, onChange, items }: { label: string; value: string; onChange: (v: string) => void; items: { v: string; label: string; icon: ReactNode }[] }) {
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

/** Where a permission table lets a terminal work: everywhere, or its workspaces and folders. */
export function placesOf(q: Rules, ws: { id: number; name: string }[] | null | undefined): string {
  if (q.allDirs) return t("perm.all");
  const names = [...(q.workspaces ?? []).map((id) => ws?.find((w) => w.id === id)?.name ?? `#${id}`), ...(q.dirs ?? []).map((d) => tilde(d))];
  return names.length ? names.join(t("msg.sep")) : t("perm.none");
}

/** The permissions (GET /api/me's rules), read only. 本机's are all. */
function Perms({ p, local, onBack }: { p?: Rules; local: boolean; onBack: () => void }) {
  const ws = useWorkspaces().data;
  const q = local ? LOCAL_ME.rules : p;
  const yes = <Check size={15} aria-label={t("perm.yes")} className="text-ink" />;
  const no = <Minus size={15} aria-label={t("perm.no")} className="text-ink-4" />;
  const yn = (v: boolean | undefined) => (v ? yes : no);
  const rows: [string, ReactNode][] = q
    ? [
        [
          t("perm.agents"),
          <span className="flex gap-1.5">
            {AGENTS.filter((a) => q.agents?.includes(a)).map((a) => (
              <Logo key={a} agent={a} size={14} />
            ))}
          </span>,
        ],
        [t("perm.workspaces"), <span className="block max-w-40 truncate text-sm text-ink-2">{placesOf(q, ws)}</span>],
        [t("perm.others"), yn(q.seeOthers)],
        [t("perm.copyOut"), yn(q.filesOut)],
        [t("perm.copyIn"), yn(q.filesIn)],
        [t("perm.answer"), yn(q.answer)],
        [t("perm.mustApprove"), yn(q.mustApprove)],
        [t("perm.commands"), <span className="block max-w-40 truncate text-sm text-ink-2">{q.allCommands ? t("perm.all") : (q.commands ?? []).join(" ") || t("perm.none")}</span>],
        [t("perm.daily"), <span className="text-sm text-ink-2 tabular-nums">{q.dailyTokens ? `${Math.round(q.dailyTokens / 1000)}k` : t("perm.unlimited")}</span>],
      ]
    : [];
  return (
    <div className="flex flex-col p-1.5">
      <div className="flex h-9 items-center gap-1">
        <IconKey label={t("nav.back")} onClick={onBack} className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <span className="text-sm font-semibold">{t("me.perms")}</span>
      </div>
      <dl className="m-0 flex flex-col px-2 pb-1">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-h-8 items-center justify-between gap-3 py-1">
            <dt className="text-sm text-ink-2">{k}</dt>
            <dd className="m-0 shrink-0">{v}</dd>
          </div>
        ))}
        {!q && <p className="m-0 py-2 text-sm text-ink-3">{t("perm.unknown")}</p>}
      </dl>
    </div>
  );
}

/** Round crop: drag to place, slide to zoom. Sent as a 256px PNG; the gate stores 128px. The picture
 *  always covers the circle: it cannot be dragged, nor zoomed back, to leave a gap (the user,
 *  2026-11-06). The preview is the picture whole, as drawn into the PNG. */
function Crop({ src, onCancel, onDone }: { src: string; onCancel: () => void; onDone: (b: Blob) => void }) {
  const [zoom, setZoom] = useState(1);
  const [pos, setPos] = useState({ x: 0, y: 0 });
  const [nat, setNat] = useState<{ w: number; h: number } | null>(null);
  const drag = useRef<{ x: number; y: number } | null>(null);
  const im = useRef<HTMLImageElement>(null);
  const S = 176;
  // Its size at zoom 1, covering the circle; how far it may go each way at a zoom.
  const base = nat ? Math.max(S / nat.w, S / nat.h) : 1;
  const w0 = nat ? nat.w * base : S;
  const h0 = nat ? nat.h * base : S;
  const clamp = (p: { x: number; y: number }, z = zoom) => {
    const mx = Math.max(0, (w0 * z - S) / 2);
    const my = Math.max(0, (h0 * z - S) / 2);
    return { x: Math.min(mx, Math.max(-mx, p.x)), y: Math.min(my, Math.max(-my, p.y)) };
  };
  const done = () => {
    const img = im.current!;
    const c = document.createElement("canvas");
    c.width = c.height = 256;
    const g = c.getContext("2d")!;
    const k = 256 / S;
    const w = w0 * zoom;
    const h = h0 * zoom;
    g.drawImage(img, ((S - w) / 2 + pos.x) * k, ((S - h) / 2 + pos.y) * k, w * k, h * k);
    c.toBlob((b) => b && onDone(b), "image/png");
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
        onPointerMove={(e) => drag.current && setPos(clamp({ x: e.clientX - drag.current.x, y: e.clientY - drag.current.y }))}
        onPointerUp={() => (drag.current = null)}
      >
        <img
          ref={im}
          src={src}
          alt=""
          draggable={false}
          onLoad={(e) => setNat({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })}
          className="absolute top-1/2 left-1/2 max-w-none select-none"
          style={{ width: w0, height: h0, transform: `translate(-50%, -50%) translate(${pos.x}px, ${pos.y}px) scale(${zoom})` }}
        />
      </div>
      <Slider.Root value={zoom} min={1} max={3} step={0.01} onValueChange={(v) => (setZoom(v as number), setPos((p) => clamp(p, v as number)))} aria-label={t("me.zoom")} className="w-44">
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
