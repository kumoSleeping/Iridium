// The page's voice, as its keys show it (key.tsx): one room at a time, whichever chat is open.
// The audio itself is engine.ts, loaded as a room is first joined.
import { useSyncExternalStore } from "react";

export type Member = { id: number; app: string; login?: string; name: string; muted: boolean };
/** What is measured, in ms: the round trip to the room; from a frame's first sample caught to its
 *  arrival here (the middle one and the 95th percentile of the last half minute); its wait in the
 *  buffer; the device's way out. Counts since joining: frames dropped here as too far behind,
 *  frames not sent as the way up was full, gaps over half a second within someone's speech; the
 *  bytes sent and received on the room's link (kept nowhere: gone with the page, and as the room
 *  is left); the rate the voice is sent at now. */
export type Numbers = { rtt: number | null; net: number | null; net95: number | null; buffer: number | null; out: number; late: number; unsent: number; stalls: number; up: number; down: number; kbps: number };
/** How good this member's own voice is sent: a level, or auto, which goes down a level as the way
 *  up fills and back up once it has been clear a while. */
export type Quality = "auto" | "high" | "mid" | "low";
export const QUALITIES: Quality[] = ["auto", "high", "mid", "low"];
/** What takes the noise out of this member's own voice: nothing; the browser's own (steady noise:
 *  a fan, hum); strong: RNNoise, which knows a keyboard and a mouse from a voice, and a gate shut
 *  while nobody speaks close to the microphone (voices across the room). */
export type Noise = "off" | "browser" | "strong";
export const NOISES: Noise[] = ["off", "browser", "strong"];
export type Voice = {
  room: string | null;
  /** joining: the microphone, the room; on; down: the link dropped, coming back. */
  status: "off" | "joining" | "on" | "down";
  error: "" | "mic" | "unsupported" | "full" | "failed";
  self: number;
  members: Member[];
  muted: boolean;
  quality: Quality;
  noise: Noise;
  /** The browser evens out how loud the microphone is. */
  gain: boolean;
  /** The level (dBFS) the microphone must reach for this member's voice to go out; null: found
   *  by itself, from how loud this member speaks and how loud the room is. */
  gate: number | null;
  /** The members heard (or, self, sending) in the last moments. */
  speaking: number[];
  /** How late each of the others is heard here, mouth to ear about, ms, by member. */
  delays: Record<number, number>;
  numbers: Numbers;
};

export const NONE: Numbers = { rtt: null, net: null, net95: null, buffer: null, out: 0, late: 0, unsent: 0, stalls: 0, up: 0, down: 0, kbps: 0 };
/** What was last chosen on this device of a setting, one of its values; else its default. */
function chosen<T extends string>(key: string, values: readonly T[], or: T): T {
  try {
    const v = localStorage.getItem(`voice.${key}`) as T | null;
    if (v && values.includes(v)) return v;
  } catch {}
  return or;
}
function choose(key: string, v: string) {
  try {
    localStorage.setItem(`voice.${key}`, v);
  } catch {}
}
/** The gate's level as last dragged to; null: by itself. */
function gateChosen(): number | null {
  try {
    const v = Number(localStorage.getItem("voice.gate") ?? NaN);
    if (v >= GATE_MIN && v <= GATE_MAX) return v;
  } catch {}
  return null;
}
const off = (): Voice => ({ room: null, status: "off", error: "", self: 0, members: [], muted: false, quality: chosen("quality", QUALITIES, "auto"), noise: chosen("noise", NOISES, "strong"), gain: chosen("gain", ["on", "off"], "on") === "on", gate: gateChosen(), speaking: [], delays: {}, numbers: NONE });

let voice = off();
const subs = new Set<() => void>();
export const getVoice = () => voice;
export function setVoice(v: Partial<Voice> | null) {
  voice = v ? { ...voice, ...v } : off();
  subs.forEach((f) => f());
}
export const useVoice = () =>
  useSyncExternalStore(
    (f) => (subs.add(f), () => void subs.delete(f)),
    getVoice,
  );

let engine: Promise<typeof import("./engine")> | undefined;
const load = () => (engine ??= import("./engine"));
/** Into room's voice, out of any other first. From a press: the microphone is asked for. */
export const join = (room: string) => void load().then((e) => e.join(room));
export const leave = () => void load().then((e) => e.leave());
export const mute = (on: boolean) => void load().then((e) => e.mute(on));
/** The quality this member's voice is sent at, from now and the next time too. */
export function setQuality(q: Quality) {
  choose("quality", q);
  setVoice({ quality: q });
  void load().then((e) => e.quality());
}
/** What takes the noise out, and whether the browser evens out the level: the microphone is
 *  opened again with them. */
export function setNoise(n: Noise) {
  choose("noise", n);
  setVoice({ noise: n });
  void load().then((e) => e.microphone());
}
export function setGain(on: boolean) {
  choose("gain", on ? "on" : "off");
  setVoice({ gain: on });
  void load().then((e) => e.microphone());
}

/** The levels the gate's bar runs between, dBFS. */
export const GATE_MIN = -70;
export const GATE_MAX = -10;
/** The gate's level, dragged to (dBFS), or null: by itself again. */
export function setGate(db: number | null) {
  choose("gate", db === null ? "auto" : String(Math.round(db)));
  setVoice({ gate: db === null ? null : Math.round(db) });
  void load().then((e) => e.gate());
}
/** Told how loud the microphone is now (dBFS, before the gate), the gate's level in use and
 *  whether it is open, some times a second while in a room: the gate's bar draws them, without the
 *  page drawn again. */
export const micLevels = new Set<(db: number, mark: number, open: boolean) => void>();

/** Who is in each channel's voice, by channel: the state "voice", for those not in it too. */
export type Rooms = Record<string, Member[]>;
