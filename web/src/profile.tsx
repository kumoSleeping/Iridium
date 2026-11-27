// This terminal's own, at the top of the 终端 panel (settings.tsx): its avatar (upload, cropped
// round, stored at 128px by the gate), its name to change, today's tokens, the device it runs on
// (its messages' badge), all in one line. Local-only with no Passkey it is 本机, which has
// nothing to rename. Comes with the panel, not the first screen.
import { Slider } from "@base-ui/react/slider";
import { useQueryClient } from "@tanstack/react-query";
import { EyeOff, Laptop, Monitor, PencilLine, Smartphone } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import { type Device, type Me } from "./api/types.gen";
import { LOCAL_ME, useMe } from "./data";
import { t } from "./i18n";
import { FaceGlow } from "./glow";
import { LocalMark } from "./me";
import { errText, toast } from "./toast";
import { Avatar, AvatarKey, cn, FaceChangeOver, IconKey, Tip, tokens } from "./ui";

/** The devices a terminal may say it runs on, in the picker's order; none: no badge shown. */
const DEVICES = ["computer", "laptop", "phone", "none"] as const satisfies readonly Device[];
const DEVICE_ICON: Record<Device, (size: number) => ReactNode> = {
  computer: (n) => <Monitor size={n} />,
  laptop: (n) => <Laptop size={n} />,
  phone: (n) => <Smartphone size={n} />,
  none: (n) => <EyeOff size={n} />,
};

const nameError = (e: unknown) => {
  if (e instanceof ApiError) {
    const code = (e.body as { nameError?: string } | undefined)?.nameError;
    if (code) return code === "taken" ? t("me.nameTaken") : t("me.nameBad");
    if ((e.body as { code?: string } | undefined)?.code === "built_in") return t("me.localOnly");
  }
  return errText(e);
};

/** Its profile saved: the name or the avatar; false if it failed (said in a toast). */
function useSave() {
  const qc = useQueryClient();
  const me: Me = useMe().data ?? LOCAL_ME;
  return async (p: { name?: string; avatar?: Blob }) => {
    try {
      const next = await api.profile(p);
      qc.setQueryData(["me"], { ...me, name: next.name, avatar: next.avatar, ...(me.host ? { named: true } : {}) } satisfies Me);
      void qc.invalidateQueries({ queryKey: ["apps"] });
      return true;
    } catch (e) {
      toast(p.name ? t("me.rename") : t("me.avatar"), { error: true, description: nameError(e) });
      return false;
    }
  };
}

/** Its avatar, name and today's tokens. onPicture: a picture chosen for the avatar, to be cropped. */
export function Profile({ onPicture }: { onPicture: (url: string) => void }) {
  const me: Me = useMe().data ?? LOCAL_ME;
  // The host as Iridium till it has a name and face of its own (Me.named); either way it may
  // take one, as a login does (kumo, 2026-11-22).
  const local = me.host && !me.named;
  const name = local ? t("me.local") : me.name;
  const save = useSave();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(me.name);
  const file = useRef<HTMLInputElement>(null);
  const glow = local ? <LocalMark size={20} /> : <Avatar src={me.avatar} name={name} size={20} pattern={me.id} />;
  return (
    <div className="flex flex-col">
      {/* A band over it, the face blurred and spread across, as a member's card has (kumo,
          2026-11-27); the face on its lower edge, ringed in the panel's ground. */}
      <div className="relative -mx-3 -mt-2.5 h-16 shrink-0 overflow-hidden bg-fill-2">
        <FaceGlow>{glow}</FaceGlow>
      </div>
      <input
        ref={file}
        type="file"
        accept="image/png,image/jpeg,image/webp"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) onPicture(URL.createObjectURL(f));
          e.target.value = "";
        }}
      />
      {/* The face: a press changes it. On its corner the device it runs on, a key of its own: a
          press goes on to the next (the user, 2026-11-08). */}
      <span className={cn("relative -mt-8 grid shrink-0 self-start bg-paper p-[3px] shadow-[0_1px_3px_var(--edge)]", local ? "rounded-[30%]" : "rounded-full")}>
        {local ? (
          <Tip label={t("me.avatar")}>
            <button type="button" aria-label={t("me.avatar")} onClick={() => file.current?.click()} className="group g-press relative grid shrink-0 rounded-full">
              <LocalMark size={52} />
              <FaceChangeOver size={52} />
            </button>
          </Tip>
        ) : (
          <AvatarKey label={t("me.avatar")} onClick={() => file.current?.click()} src={me.avatar} name={name} size={52} pattern={me.id} />
        )}
        <DeviceKey />
      </span>
      <div className="flex items-center gap-3 py-1">
      {editing ? (
        <input
          autoFocus
          aria-label={t("me.rename")}
          value={draft}
          maxLength={12}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => setEditing(false)}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing || e.keyCode === 229) return;
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
          <span className="block truncate text-xs text-ink-3 tabular-nums">{local ? t("me.localHint") : t("term.today", { n: tokens(me.usedToday) })}</span>
        </span>
      )}
      {!editing && (
        <IconKey label={t("me.rename")} onClick={() => (setDraft(local ? "" : me.name), setEditing(true))} className="size-7">
          <PencilLine size={15} />
        </IconKey>
      )}
      </div>
    </div>
  );
}

/** The device this terminal runs on, for every panel to see: the badge on its messages' avatar. A
 *  key on the face's corner, as that badge is, its icon the device now; a press goes on to the next
 *  (the user, 2026-11-08). Under a pointer it inverts: a key, not a mark (the
 *  user, 2026-11-12). */
function DeviceKey() {
  const qc = useQueryClient();
  const me: Me = useMe().data ?? LOCAL_ME;
  const device: Device = me.device ?? "computer";
  const next = DEVICES[(DEVICES.indexOf(device) + 1) % DEVICES.length]!;
  const set = async (d: Device) => {
    // At once in the page; back as it was if the daemon says no.
    qc.setQueryData(["me"], { ...me, device: d } satisfies Me);
    try {
      const got = await api.device(d);
      qc.setQueryData(["me"], (m: Me | undefined) => m && { ...m, device: got.device });
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      qc.setQueryData(["me"], (m: Me | undefined) => m && { ...m, device });
      toast(t("me.device"), { error: true, description: errText(e) });
    }
  };
  // Ringed in the ground under it, as a badge on a face is (ui.tsx Badge); its hit wider than it.
  return (
    <Tip label={`${t("me.device")} · ${t(`device.${device}`)}`}>
      <button
        type="button"
        aria-label={`${t("me.device")} · ${t(`device.${device}`)}`}
        onClick={() => void set(next)}
        className="g-press absolute -right-[3px] -bottom-[3px] grid size-[20px] place-items-center rounded-full bg-fill-2 text-ink-2 shadow-[0_0_0_1.5px_var(--paper)] before:absolute before:-inset-2 before:content-[''] hover:bg-ink hover:text-on-ink focus-visible:bg-ink focus-visible:text-on-ink"
      >
        <span key={device} className="g-pop grid place-items-center">
          {DEVICE_ICON[device](12)}
        </span>
      </button>
    </Tip>
  );
}

/** The crop, its picture saved as the avatar: back once saved, or cancelled. */
export function AvatarCrop({ src, onBack }: { src: string; onBack: () => void }) {
  const save = useSave();
  return <Crop src={src} onCancel={onBack} onDone={(blob) => void save({ avatar: blob }).then((ok) => ok && onBack())} />;
}

/** Round crop: drag to place, slide to zoom. Sent as a 256px PNG; the gate stores 128px. The picture
 *  always covers the circle: it cannot be dragged, nor zoomed back, to leave a gap (the user,
 *  2026-11-06). The preview is the picture whole, as drawn into the PNG. */
export function Crop({ src, onCancel, onDone }: { src: string; onCancel: () => void; onDone: (b: Blob) => void }) {
  const [zoom, setZoom] = useState(1);
  const [pos, setPos] = useState({ x: 0, y: 0 });
  const [nat, setNat] = useState<{ w: number; h: number } | null>(null);
  const drag = useRef<{ x: number; y: number } | null>(null);
  const im = useRef<HTMLImageElement>(null);
  // The frame on the page, and what is sent: a circle.
  const W = 176;
  const H = 176;
  const OW = 256;
  const OH = 256;
  // Its size at zoom 1, covering the frame; how far it may go each way at a zoom.
  const base = nat ? Math.max(W / nat.w, H / nat.h) : 1;
  const w0 = nat ? nat.w * base : W;
  const h0 = nat ? nat.h * base : H;
  const clamp = (p: { x: number; y: number }, z = zoom) => {
    const mx = Math.max(0, (w0 * z - W) / 2);
    const my = Math.max(0, (h0 * z - H) / 2);
    return { x: Math.min(mx, Math.max(-mx, p.x)), y: Math.min(my, Math.max(-my, p.y)) };
  };
  const done = () => {
    const img = im.current!;
    const c = document.createElement("canvas");
    c.width = OW;
    c.height = OH;
    const g = c.getContext("2d")!;
    const k = OW / W;
    const w = w0 * zoom;
    const h = h0 * zoom;
    g.drawImage(img, ((W - w) / 2 + pos.x) * k, ((H - h) / 2 + pos.y) * k, w * k, h * k);
    // A face as small as it goes
    // (kumo, 2026-11-23: every upload made small; the daemon keeps 128 px of it, gate.MakeAvatar):
    // a JPEG where nothing shows through, else WebP (PNG where the browser makes no WebP: Safari).
    const opaque = g.getImageData(0, 0, OW, OH).data.every((x, i) => i % 4 !== 3 || x === 255);
    if (opaque) c.toBlob((b) => b && onDone(b), "image/jpeg", 0.88);
    else c.toBlob((b) => b && onDone(b), "image/webp", 0.9);
  };
  return (
    <div className="flex flex-col items-center gap-3 p-3">
      <div
        className={cn("relative cursor-grab touch-none overflow-hidden bg-fill active:cursor-grabbing", "rounded-full")}
        style={{ width: W, height: H }}
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
