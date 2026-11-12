// The voice this page is in (plan "语音"), at the sidebar's foot beside me (on a phone, in the
// chat's bar): the microphone's key, and a key to a popover of who is there, the way out, this
// member's own settings and what is measured of the link. Nothing while in no room: a room is
// joined from its folder group's "+" (dock.tsx). Loaded as a room is first joined.
import { Popover } from "@base-ui/react/popover";
import { AudioLines, Mic, MicOff, PhoneOff } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useWorkspaces } from "../data";
import { t, useLang } from "../i18n";
import { useWhoList } from "../data";
import { Avatar, cn, PopSurface, Switch, Tip } from "../ui";
import { GATE_MAX, GATE_MIN, leave, micLevels, mute, NOISES, QUALITIES, setGain, setGate, setNoise, setQuality, useVoice, type Numbers } from "./state";

/** Bytes, short: 512 B, 38 KB, 1.4 MB. */
const size = (n: number) => (n < 1024 ? `${n} B` : n < 1024 ** 2 ? `${Math.round(n / 1024)} KB` : `${(n / 1024 ** 2).toFixed(1)} MB`);
const ms = (n: number | null) => (n === null ? "—" : `${n} ms`);
/** The rest of what is measured, for the line's tip. */
const detail = (n: Numbers) => t("voice.detail", { net: ms(n.net), p95: ms(n.net95), buffer: ms(n.buffer), rtt: ms(n.rtt), late: n.late, unsent: n.unsent, stalls: n.stalls });
/** Mouth to ear, about: the way here, the wait in the buffer, the way out of the device. */
const whole = (n: Numbers) => (n.net === null ? null : n.net + (n.buffer ?? 0) + n.out);

/** One of a few, in a row: the one chosen raised. */
function Choice<T extends string>({ label, values, value, name, onChange }: { label: string; values: readonly T[]; value: T; name: (v: T) => string; onChange: (v: T) => void }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex rounded-md bg-fill p-0.5">
      {values.map((x) => (
        <button key={x} type="button" role="radio" aria-checked={value === x} onClick={() => onChange(x)} className={cn("g-press h-7 flex-1 rounded-[5px] text-xs pointer-coarse:h-9", value === x ? "bg-paper font-medium text-ink shadow-[0_0_0_0.5px_var(--edge)]" : "text-ink-3 hover:text-ink-2")}>
          {name(x)}
        </button>
      ))}
    </div>
  );
}

/** The gate's bar: how loud the microphone is now (the fill, green while the gate is open) and
 *  the level it must reach (the handle), which stays where it is dragged. The fill is drawn by
 *  hand, some times a second, the popover not drawn again. */
function Gate({ value }: { value: number }) {
  const bar = useRef<HTMLDivElement>(null);
  const fill = useRef<HTMLDivElement>(null);
  const at = (db: number) => `${(Math.min(1, Math.max(0, (db - GATE_MIN) / (GATE_MAX - GATE_MIN))) * 100).toFixed(1)}%`;
  useEffect(() => {
    const draw = (db: number, open: boolean) => {
      if (!fill.current) return;
      fill.current.style.width = at(db);
      fill.current.style.background = open ? "var(--run)" : "var(--ink-4)";
    };
    micLevels.add(draw);
    return () => void micLevels.delete(draw);
  }, []);
  const drag = (e: React.PointerEvent) => {
    const r = bar.current!.getBoundingClientRect();
    setGate(GATE_MIN + Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)) * (GATE_MAX - GATE_MIN));
  };
  return (
    <div
      ref={bar}
      role="slider"
      tabIndex={0}
      aria-label={t("voice.gate")}
      aria-valuemin={GATE_MIN}
      aria-valuemax={GATE_MAX}
      aria-valuenow={value}
      aria-valuetext={`${value} dB`}
      onPointerDown={(e) => (e.currentTarget.setPointerCapture(e.pointerId), drag(e))}
      onPointerMove={(e) => e.buttons && drag(e)}
      onKeyDown={(e) => {
        const step = e.key === "ArrowRight" || e.key === "ArrowUp" ? 2 : e.key === "ArrowLeft" || e.key === "ArrowDown" ? -2 : 0;
        if (step) setGate(value + step);
      }}
      className="relative h-7 min-w-0 cursor-pointer touch-none pointer-coarse:h-9"
    >
      <div className="absolute inset-x-0 top-1/2 h-1.5 -translate-y-1/2 overflow-hidden rounded-full bg-fill-2">
        <div ref={fill} className="h-full w-0 rounded-full" />
      </div>
      <div style={{ left: at(value) }} className="absolute top-1/2 h-4 w-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-ink shadow-[0_0_0_0.5px_var(--edge)]" />
    </div>
  );
}

export function VoiceDock({ side = "top", className }: { side?: "top" | "bottom"; className?: string }) {
  useLang();
  const v = useVoice();
  const who = useWhoList();
  const group = useWorkspaces().data?.find((w) => String(w.id) === v.room);
  const [open, setOpen] = useState(false);
  if (v.status === "off") return null;
  const n = v.numbers;
  const state = v.status === "down" ? t("top.offline") : v.status === "joining" ? t("top.connecting") : "";
  const late = whole(n);
  return (
    <span className={cn("g-pop flex shrink-0 items-center", className)}>
      <Tip label={v.muted ? t("voice.unmute") : t("voice.mute")} side={side}>
        <button type="button" aria-label={v.muted ? t("voice.unmute") : t("voice.mute")} aria-pressed={v.muted} onClick={() => mute(!v.muted)} className={cn("g-press grid size-8 place-items-center rounded-md hover:bg-fill pointer-coarse:size-10", v.muted ? "text-err" : "text-ink-2 hover:text-ink")}>
          {v.muted ? <MicOff size={17} /> : <Mic size={17} />}
        </button>
      </Tip>
      <Popover.Root open={open} onOpenChange={setOpen}>
        <Tip label={group ? t("voice.inGroup", { name: group.name }) : t("voice.in")} side={side}>
          <Popover.Trigger
            render={
              <button type="button" aria-label={t("voice.in")} className="g-press relative grid size-8 place-items-center rounded-md text-ink hover:bg-fill data-popup-open:bg-fill pointer-coarse:size-10">
                <AudioLines size={17} />
                <span aria-hidden className={cn("absolute top-1.5 right-1.5 size-1.5 rounded-full pointer-coarse:top-2.5 pointer-coarse:right-2.5", v.status === "on" ? "bg-run" : "bg-need")} />
              </button>
            }
          />
        </Tip>
        <PopSurface side={side} align="end" className="w-[min(280px,calc(100vw-16px))] p-3">
          <div className="mb-1.5 flex items-baseline justify-between gap-2">
            <span className="min-w-0 truncate text-sm font-semibold">{group?.name ?? t("voice.join")}</span>
            <span className="shrink-0 text-xs text-ink-3">{state}</span>
          </div>
          <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
            {v.members.map((m) => (
              <li key={m.id} className={cn("flex h-8 items-center gap-2 text-sm", m.muted && "text-ink-3")}>
                <span className="relative grid">
                  <Avatar src={who.find((w) => w.id === (m.login || m.app))?.avatar} name={m.name} size={22} />
                  {/* Speaking: a dot on the corner. */}
                  {v.speaking.includes(m.id) && <span role="img" aria-label={t("voice.speaking")} className="g-pop absolute -top-0.5 -right-0.5 size-2 rounded-full bg-run shadow-[0_0_0_1.5px_var(--glass-thick-solid)]" />}
                </span>
                <span className="min-w-0 flex-1 truncate">{m.name}</span>
                {/* How late this one is heard here. */}
                {v.delays[m.id] !== undefined && <span className="shrink-0 text-xs text-ink-3 tabular-nums">{v.delays[m.id]} ms</span>}
                {m.muted && <MicOff size={14} className="shrink-0 text-err" aria-label={t("voice.muted")} />}
              </li>
            ))}
          </ul>
          <button type="button" onClick={() => (leave(), setOpen(false))} className="g-press mt-2 flex h-8 w-full items-center justify-center gap-1.5 rounded-md bg-fill text-sm text-err hover:bg-fill-2 pointer-coarse:h-10">
            <PhoneOff size={15} />
            {t("voice.leave")}
          </button>
          {/* This member's own voice: how good it is sent, what takes the noise out of it, the level
              it must reach to go out, and whether the browser evens out its level. */}
          <div className="mt-2.5 grid grid-cols-[auto_1fr] items-center gap-x-2.5 gap-y-1.5 text-xs text-ink-3">
            <span>{t("voice.quality")}</span>
            <Choice label={t("voice.quality")} values={QUALITIES} value={v.quality} name={(q) => t(`voice.quality.${q}`)} onChange={setQuality} />
            <span>{t("voice.noise")}</span>
            <Choice label={t("voice.noise")} values={NOISES} value={v.noise} name={(x) => t(`voice.noise.${x}`)} onChange={setNoise} />
            <span>{t("voice.gate")}</span>
            <Gate value={v.gate} />
            <span>{t("voice.gain")}</span>
            <Switch on={v.gain} onChange={setGain} label={t("voice.gain")} className="justify-self-end" />
          </div>
          {/* The link in one line: how late the others are heard (once someone has been), the rate
              sent at, the bytes up and down. The rest of what is measured is in its tip (and the
              room's log). */}
          <Tip label={detail(n)} side="bottom">
            <div data-voice={JSON.stringify(n)} className="mt-2.5 flex justify-between gap-2 text-xs text-ink-3 tabular-nums">
              <span>{late !== null && t("voice.late", { ms: late })}</span>
              <span>
                {n.kbps} kbps · ↑ {size(n.up)} ↓ {size(n.down)}
              </span>
            </div>
          </Tip>
        </PopSurface>
      </Popover.Root>
    </span>
  );
}
