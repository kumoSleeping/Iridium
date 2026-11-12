// A room's audio in this page (docs/voice.md). Up: the microphone (the browser's echo cancelling;
// the noise taken out as chosen: the browser's, or RNNoise and a gate) → 20 ms blocks
// (capture.worklet.js) → Opus at the quality's rate → the room's WebSocket. Down: a frame → its
// speaker's decoder → the speaker's queue (play.worklet.js) → mixed and played.
//
// The way is TCP: a frame is late, never lost. So nothing here repairs; it measures how late (the
// room's clock, by pings) and keeps what waits short: a frame is not sent while the socket still
// holds older ones, and a queue too far behind lets go of its oldest.
//
// Nothing is cut at full level, on either side: a cut is a click. Whatever would cut what is sent
// (muting, another microphone, the encoder set again) is done with the capture hushed first.
import { loadRnnoise, RnnoiseWorkletNode } from "@sapphi-red/web-noise-suppressor";
import rnnoiseWorkletURL from "@sapphi-red/web-noise-suppressor/rnnoiseWorklet.js?url";
import rnnoiseURL from "@sapphi-red/web-noise-suppressor/rnnoise.wasm?url";
import rnnoiseSimdURL from "@sapphi-red/web-noise-suppressor/rnnoise_simd.wasm?url";
import captureURL from "./capture.worklet.js?url&no-inline";
import playURL from "./play.worklet.js?url&no-inline";
import { getVoice, micLevels, NONE, setVoice, type Member } from "./state";
import { Clock, pack, pct, playout, since, low32, unpack } from "./wire";

const RATE = 48000;
/** The qualities, low to high: the rate, a frame's length, and whether nothing is sent while
 *  silent. The lowest saves what it can; the others send all the time (the starts of words are
 *  never cut, and the listener's queue stays even), the highest in frames half as long. */
const LEVELS = [
  { bitrate: 12_000, frame: 40, dtx: true },
  { bitrate: 24_000, frame: 40, dtx: false },
  { bitrate: 40_000, frame: 20, dtx: false },
];
const LEVEL = { low: 0, mid: 1, high: 2 } as const;
/** How much may wait in the socket, in seconds of the voice at its rate: past FULL a new frame
 *  would only be later, and is not sent; past SLOW, auto goes down a level, not twice within HOLD,
 *  and up one once the way has been clear for CLEAR. */
const FULL = 1;
const SLOW = 0.4;
const HOLD = 3000;
const CLEAR = 20_000;
/** How long the capture takes to fall silent or come back (capture.worklet.js ATTACK), and a block more. */
const HUSH = 40;
const WINDOW = 30_000; // what the shown numbers cover
const SPEAKING = 350; // heard this long ago, still speaking
/** A frame this much later after the one before it than it was caught after it: the link stalled. */
const STALL = 300;
/** A decoded chunk with a sample over this is a voice (not the hiss of a silent microphone). */
const VOICED = 0.02;

type Session = {
  room: string;
  over: boolean;
  ctx: AudioContext;
  mic: MediaStream;
  /** The microphone's nodes, into capture: its source, and RNNoise between when it is on. */
  source: MediaStreamAudioSourceNode;
  rnnoise: RnnoiseWorkletNode | null;
  capture: AudioWorkletNode;
  /** Which opening of the microphone is the last asked for. */
  micAsked: number;
  play: AudioWorkletNode;
  encoder: AudioEncoder;
  ws: WebSocket | null;
  clock: Clock;
  seq: number;
  /** The level (LEVELS) the encoder runs at, -1 before the first; the one being set. */
  level: number;
  setting: Promise<void>;
  /** Nothing goes to the encoder: muted, or being set again. */
  held: boolean;
  /** Auto: when the way up was last slow, when the level last changed. */
  slowAt: number;
  levelAt: number;
  /** The context's time to this page's clock (ms): the least seen lately of when a block came
   *  minus when it ended, as a block is posted late at times, never early. Lately (the least of
   *  each of the last ten seconds, ctxMins): the two clocks drift apart over a long call. */
  ctxAt: number;
  ctxMins: number[];
  ctxSecond: number;
  /** When the last block given to the encoder ended, this page's clock. */
  fedEnd: number;
  /** When this member's voice was last loud enough to be one. */
  spokeAt: number;
  speakers: Map<number, { decoder: AudioDecoder; clock: number; at: number; heard: number; delays: number[]; due: { target: number; max: number } }>;
  nets: { at: number; ms: number }[];
  buffer: number | null;
  total: { frames: number; late: number; gaps: number; skipped: number; unsent: number; stalls: number; up: number; down: number; nets: number[]; since: number };
  timers: ReturnType<typeof setInterval>[];
  retry?: ReturnType<typeof setTimeout>;
};

let cur: Session | null = null;
/** Which join or leave is the last asked for: one still waiting on the microphone gives way. */
let asked = 0;
/** RNNoise's code, fetched once. */
let rnnoiseBinary: Promise<ArrayBuffer> | undefined;

export function supported() {
  return typeof AudioEncoder !== "undefined" && typeof AudioDecoder !== "undefined" && typeof AudioWorkletNode !== "undefined" && !!navigator.mediaDevices?.getUserMedia;
}

const url = (room: string) => `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/v1/internal/sessions/${encodeURIComponent(room)}/voice`;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** The microphone as the settings say: the echo always cancelled; the browser's noise suppression
 *  only when it is the one chosen (RNNoise works on the sound as it is). */
const listen = () => {
  const v = getVoice();
  return navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: v.noise === "browser", autoGainControl: v.gain, channelCount: 1 } });
};

/** RNNoise's node in ctx; null where it cannot be had (no WebAssembly, an old page's CSP): the
 *  browser's noise suppression is fallen back to. */
async function denoiser(ctx: AudioContext): Promise<RnnoiseWorkletNode | null> {
  try {
    rnnoiseBinary ??= loadRnnoise({ url: rnnoiseURL, simdUrl: rnnoiseSimdURL });
    const [binary] = await Promise.all([rnnoiseBinary, ctx.audioWorklet.addModule(rnnoiseWorkletURL)]);
    return new RnnoiseWorkletNode(ctx, { maxChannels: 1, wasmBinary: binary });
  } catch {
    rnnoiseBinary = undefined;
    return null;
  }
}

export async function join(room: string) {
  if (cur?.room === room) return;
  leave();
  const mine = ++asked;
  if (!supported()) return void setVoice({ room, status: "off", error: "unsupported" });
  setVoice({ room, status: "joining", error: "", members: [], speaking: [], delays: {}, numbers: NONE, muted: false });
  // The context first, still within the press: a browser lets it sound only then.
  const ctx = new AudioContext({ sampleRate: RATE, latencyHint: "interactive" });
  let mic: MediaStream;
  try {
    mic = await listen();
  } catch {
    void ctx.close();
    return void setVoice({ status: "off", error: "mic" });
  }
  const stop = (error?: "unsupported" | "failed") => {
    mic.getTracks().forEach((t) => t.stop());
    void ctx.close();
    if (error && asked === mine) setVoice({ status: "off", error });
  };
  if (asked !== mine) return stop();
  let rnnoise: RnnoiseWorkletNode | null = null;
  try {
    await ctx.resume();
    await Promise.all([ctx.audioWorklet.addModule(captureURL), ctx.audioWorklet.addModule(playURL)]);
    if (getVoice().noise === "strong") rnnoise = await denoiser(ctx);
  } catch {
    return stop("failed");
  }
  if (asked !== mine) return stop();
  if (ctx.sampleRate !== RATE) return stop("unsupported");
  const s: Session = {
    room,
    over: false,
    ctx,
    mic,
    source: ctx.createMediaStreamSource(mic),
    rnnoise,
    capture: new AudioWorkletNode(ctx, "ir-voice-capture", { numberOfInputs: 1, numberOfOutputs: 0 }),
    micAsked: 0,
    play: new AudioWorkletNode(ctx, "ir-voice-play", { numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [1] }),
    encoder: new AudioEncoder({ output: (c) => send(s, c), error: () => {} }),
    ws: null,
    clock: new Clock(),
    seq: 0,
    level: -1,
    setting: Promise.resolve(),
    held: false,
    slowAt: 0,
    levelAt: 0,
    ctxAt: Infinity,
    ctxMins: [],
    ctxSecond: 0,
    fedEnd: 0,
    spokeAt: 0,
    speakers: new Map(),
    nets: [],
    buffer: null,
    total: { frames: 0, late: 0, gaps: 0, skipped: 0, unsent: 0, stalls: 0, up: 0, down: 0, nets: [], since: performance.now() },
    timers: [],
  };
  cur = s;
  quality();
  s.play.connect(ctx.destination);
  // What is played, to a test that listens for clicks in it (scripts/voicepop.mjs).
  const tap = (globalThis as { __irVoiceTap?: (pcm: Float32Array) => void }).__irVoiceTap;
  if (tap) s.play.port.postMessage({ tap: true });
  s.play.port.onmessage = (e: MessageEvent<{ waited: number | null; late: number; gaps: number; skipped: number; played?: Float32Array }>) => {
    if (e.data.played) return tap?.(e.data.played);
    if (e.data.waited !== null) s.buffer = e.data.waited;
    s.total.late += e.data.late;
    s.total.gaps += e.data.gaps;
    s.total.skipped += e.data.skipped;
  };
  wire(s);
  let told = 0;
  s.capture.port.onmessage = (e: MessageEvent<{ pcm: Float32Array<ArrayBuffer>; t: number; level: number; mark: number; open: boolean }>) => {
    const { pcm, t } = e.data;
    // How loud, to the gate's bar: every third block.
    const db = (x: number) => 20 * Math.log10(Math.max(x, 1e-5));
    if (micLevels.size && ++told % 3 === 0) micLevels.forEach((f) => f(db(e.data.level), db(e.data.mark), e.data.open && !s.held));
    const end = (t + pcm.length / RATE) * 1000;
    const second = Math.floor(t);
    if (second !== s.ctxSecond) {
      s.ctxSecond = second;
      s.ctxMins.unshift(Infinity);
      s.ctxMins.length = Math.min(s.ctxMins.length, 10);
    }
    s.ctxMins[0] = Math.min(s.ctxMins[0] ?? Infinity, performance.now() - end);
    s.ctxAt = Math.min(...s.ctxMins);
    if (s.over || s.held || s.level < 0 || s.encoder.state !== "configured" || s.ws?.readyState !== WebSocket.OPEN || !s.clock.ready) return;
    for (let i = 0; i < pcm.length; i += 8) {
      if (Math.abs(pcm[i]!) > VOICED) {
        s.spokeAt = performance.now();
        break;
      }
    }
    const data = new AudioData({ format: "f32-planar", sampleRate: RATE, numberOfFrames: pcm.length, numberOfChannels: 1, timestamp: Math.round(t * 1e6), data: pcm });
    s.fedEnd = end + s.ctxAt;
    s.encoder.encode(data);
    data.close();
  };
  s.timers.push(setInterval(() => show(s), 250), setInterval(() => ping(s), 2000), setInterval(() => report(s), 30_000));
  connect(s);
}

/** The microphone's nodes joined up: through RNNoise when it is there, the gate on with it. */
function wire(s: Session) {
  if (s.rnnoise) s.source.connect(s.rnnoise).connect(s.capture);
  else s.source.connect(s.capture);
  gate();
  // A microphone muted from outside the page (a headset's key, the system's) is muted to the others too.
  for (const t of s.mic.getAudioTracks()) t.onmute = t.onunmute = () => tell(s);
  // RNNoise asked for and not to be had: the browser's own, shown as what is on.
  if (getVoice().noise === "strong" && !s.rnnoise) setVoice({ noise: "browser" });
}

/** The gate as the settings say now: at the level dragged to, else found by itself. */
export function gate() {
  const s = cur;
  if (!s) return;
  const db = getVoice().gate;
  s.capture.port.postMessage({ gate: true, mark: db === null ? null : 10 ** (db / 20) });
}

/** Whether this member is muted, to the room: by its key here, or the microphone from outside. */
function tell(s: Session) {
  say(s, { op: "mute", muted: getVoice().muted || s.mic.getAudioTracks().some((t) => t.muted) });
}

/** The microphone opened again as the settings say now (state.ts setNoise, setGain), the capture
 *  hushed while one gives way to the other. */
export async function microphone() {
  const s = cur;
  if (!s) return;
  const mine = ++s.micAsked;
  s.capture.port.postMessage({ hush: true });
  await sleep(HUSH);
  let mic: MediaStream | null = null;
  let rnnoise: RnnoiseWorkletNode | null = null;
  try {
    if (getVoice().noise === "strong") rnnoise = s.rnnoise ?? (await denoiser(s.ctx));
    // RNNoise not to be had: the browser's, asked for in its place.
    if (getVoice().noise === "strong" && !rnnoise) setVoice({ noise: "browser" });
    mic = await listen();
  } catch {}
  if (s.over || mine !== s.micAsked) return void mic?.getTracks().forEach((t) => t.stop());
  if (mic) {
    s.source.disconnect();
    s.rnnoise?.disconnect();
    if (s.rnnoise && s.rnnoise !== rnnoise) s.rnnoise.destroy();
    s.mic.getTracks().forEach((t) => t.stop());
    s.mic = mic;
    mic.getAudioTracks().forEach((t) => (t.enabled = !getVoice().muted));
    s.source = s.ctx.createMediaStreamSource(mic);
    s.rnnoise = rnnoise;
    wire(s);
  }
  if (!getVoice().muted) s.capture.port.postMessage({ hush: false });
}

function connect(s: Session) {
  const ws = new WebSocket(url(s.room));
  ws.binaryType = "arraybuffer";
  s.ws = ws;
  ws.onopen = () => {
    // The clock at once, a few times over: the first frames are measured by it.
    for (let i = 0; i < 4; i++) setTimeout(() => ping(s), i * 150);
    tell(s);
  };
  ws.onmessage = (m) => {
    if (s.ws !== ws) return;
    if (typeof m.data !== "string") return hear(s, m.data as ArrayBuffer);
    s.total.down += m.data.length;
    const sig = JSON.parse(m.data) as { op: string; t?: number; now?: number; self?: number; members?: Member[] };
    if (sig.op === "pong" && sig.t !== undefined && sig.now !== undefined) s.clock.pong(sig.t, performance.now(), sig.now);
    else if (sig.op === "hello" || sig.op === "members") {
      const members = sig.members ?? [];
      for (const [id, sp] of s.speakers) {
        if (members.some((x) => x.id === id)) continue;
        sp.decoder.close();
        s.speakers.delete(id);
        s.play.port.postMessage({ drop: id });
      }
      setVoice({ status: "on", members, ...(sig.op === "hello" ? { self: sig.self ?? 0 } : {}) });
    }
  };
  ws.onclose = (e) => {
    if (s.ws !== ws || s.over) return;
    s.ws = null;
    if (e.code === 4008) return leave("full");
    // Refused before it opened (the session gone, the login out): not tried again for ever.
    if (getVoice().status === "joining") return leave("failed");
    setVoice({ status: "down", speaking: [] });
    s.retry = setTimeout(() => connect(s), 1500);
  };
}

/** A signal up, counted. */
function say(s: Session, sig: object) {
  if (s.ws?.readyState !== WebSocket.OPEN) return;
  const text = JSON.stringify(sig);
  s.total.up += text.length;
  s.ws.send(text);
}

function ping(s: Session) {
  say(s, { op: "ping", t: performance.now() });
}

/** The encoder set to level (LEVELS), one setting after another: the capture hushed first, as an
 *  encoder set again starts from nothing, which a voice running through it would click at. */
function level(s: Session, to: number) {
  if (to === s.level) return;
  const first = s.level < 0;
  s.level = to;
  s.levelAt = performance.now();
  s.setting = s.setting.then(async () => {
    if (s.over || s.level !== to) return;
    const l = LEVELS[to]!;
    const config: AudioEncoderConfig = { codec: "opus", sampleRate: RATE, numberOfChannels: 1, bitrate: l.bitrate };
    // Speech, the level's frames, nothing but a mark while silent where it says so; a browser
    // that knows only some of these still encodes.
    const opus = { frameDuration: l.frame * 1000, usedtx: l.dtx };
    let use = config;
    for (const c of [{ ...config, opus: { ...opus, application: "voip", signal: "voice" } }, { ...config, opus }] as AudioEncoderConfig[]) {
      if ((await AudioEncoder.isConfigSupported(c).catch(() => null))?.supported) {
        use = c;
        break;
      }
    }
    if (s.over || s.encoder.state === "closed") return;
    if (!first) {
      s.capture.port.postMessage({ hush: true });
      await sleep(HUSH + l.frame);
      if (s.over) return;
    }
    s.encoder.configure(use);
    if (!first && !getVoice().muted) s.capture.port.postMessage({ hush: false });
  });
}

/** The quality chosen (state.ts), taken up: a level at once; auto from the middle one, or from
 *  where it is. */
export function quality() {
  const s = cur;
  if (!s) return;
  const q = getVoice().quality;
  level(s, q === "auto" ? (s.level < 0 ? LEVEL.mid : s.level) : LEVEL[q]);
}

/** A second of the voice at its rate now, in bytes. */
const second = (s: Session) => LEVELS[Math.max(0, s.level)]!.bitrate / 8;

/** Auto, looked at as the numbers are shown: the way up slow, a level down; long clear, one up. */
function adapt(s: Session, now: number) {
  if ((s.ws?.bufferedAmount ?? 0) > second(s) * SLOW) s.slowAt = now;
  if (getVoice().quality !== "auto") return;
  if (now - s.slowAt < 500 && now - s.levelAt > HOLD && s.level > 0) level(s, s.level - 1);
  else if (now - Math.max(s.slowAt, s.levelAt) > CLEAR && s.level < LEVELS.length - 1) level(s, s.level + 1);
}

/** An encoded frame, up: not the mark of silence, and not while older ones still wait to go. */
function send(s: Session, c: EncodedAudioChunk) {
  const ws = s.ws;
  if (s.over || ws?.readyState !== WebSocket.OPEN || c.byteLength <= 2) return;
  if (ws.bufferedAmount > second(s) * FULL) {
    s.slowAt = performance.now();
    return void s.total.unsent++;
  }
  const opus = new Uint8Array(c.byteLength);
  c.copyTo(opus);
  // When its first sample was caught, in the room's clock: a frame comes out as the block that
  // completes it goes in. Not the chunk's own timestamp: the encoder counts only the frames it
  // gives out, so past each silence it falls behind by as long as the silence was.
  const caught = s.fedEnd - (c.duration ?? LEVELS[s.level]!.frame * 1000) / 1000 + s.clock.offset;
  const frame = pack(s.seq++, caught, opus);
  s.total.up += frame.length;
  ws.send(frame);
}

/** A frame from the room: how long it took, then to its speaker's decoder and queue. */
function hear(s: Session, buf: ArrayBuffer) {
  const f = unpack(buf);
  if (!f || s.over) return;
  s.total.down += buf.byteLength;
  const now = performance.now();
  let sp = s.speakers.get(f.from);
  if (!sp) {
    const id = f.from;
    const decoder = new AudioDecoder({
      output: (a) => {
        const pcm = new Float32Array(a.numberOfFrames);
        a.copyTo(pcm, { planeIndex: 0, format: "f32-planar" });
        a.close();
        const me = s.speakers.get(id);
        if (!me) return;
        // Speaking: a voice in what came, not the frames themselves (a member may send all the time).
        for (let i = 0; i < pcm.length; i += 8) {
          if (Math.abs(pcm[i]!) > VOICED) {
            me.heard = performance.now();
            break;
          }
        }
        const samples = (ms: number) => Math.round((ms * RATE) / 1000);
        s.play.port.postMessage({ id, pcm, target: samples(me.due.target), max: samples(me.due.max) }, [pcm.buffer]);
      },
      error: () => {},
    });
    decoder.configure({ codec: "opus", sampleRate: RATE, numberOfChannels: 1 });
    s.speakers.set(id, (sp = { decoder, clock: 0, at: 0, heard: 0, delays: [], due: playout([], 40) }));
  }
  if (s.clock.ready) {
    const ms = since(low32(now + s.clock.offset), f.clock);
    // A clock set before the first good ping can be seconds off: not counted.
    if (ms > -200 && ms < 60_000) {
      s.nets.push({ at: now, ms });
      s.total.nets.push(ms);
      if (s.total.nets.length > 20_000) s.total.nets.splice(0, 10_000);
      sp.delays.push(ms);
      if (sp.delays.length > 150) sp.delays.shift();
      sp.due = playout(sp.delays, 40);
    }
  }
  if (sp.at && now - sp.at - since(f.clock, sp.clock) > STALL) s.total.stalls++;
  sp.clock = f.clock;
  sp.at = now;
  s.total.frames++;
  if (sp.decoder.state === "configured") sp.decoder.decode(new EncodedAudioChunk({ type: "key", timestamp: s.total.frames * 20_000, data: f.opus }));
}

/** The numbers and who speaks, to the keys. */
function show(s: Session) {
  if (s.over) return;
  const now = performance.now();
  adapt(s, now);
  while (s.nets.length && now - s.nets[0]!.at > WINDOW) s.nets.shift();
  const nets = s.nets.map((n) => n.ms);
  const speaking = [...s.speakers].filter(([, sp]) => now - sp.heard < SPEAKING).map(([id]) => id);
  if (now - s.spokeAt < SPEAKING && !s.held && getVoice().self) speaking.push(getVoice().self);
  const round = (x: number | null) => (x === null ? null : Math.round(x));
  const out = Math.round(((s.ctx.outputLatency || 0) + (s.ctx.baseLatency || 0)) * 1000);
  const prev = getVoice();
  const numbers = { rtt: round(s.clock.rtt), net: round(pct(nets, 0.5)), net95: round(pct(nets, 0.95)), buffer: round(s.buffer), out, late: s.total.late, unsent: s.total.unsent, stalls: s.total.stalls, up: s.total.up, down: s.total.down, kbps: LEVELS[Math.max(0, s.level)]!.bitrate / 1000 };
  // How late each of the others is heard: the way here (the middle of its last frames), the wait
  // in the buffer, the way out of the device.
  const delays: Record<number, number> = {};
  for (const [id, sp] of s.speakers) if (sp.delays.length) delays[id] = Math.round(pct(sp.delays, 0.5)! + (s.buffer ?? 0) + out);
  if (JSON.stringify(numbers) === JSON.stringify(prev.numbers) && speaking.join() === prev.speaking.join() && JSON.stringify(delays) === JSON.stringify(prev.delays)) return;
  setVoice({ numbers, speaking, delays });
}

/** What was measured since joining, to the room's log: the tests on real networks read it there. */
function report(s: Session) {
  const t = s.total;
  const v = getVoice();
  const l = LEVELS[Math.max(0, s.level)]!;
  const ua = navigator.userAgent;
  const platform = `${/iPhone|iPad/.test(ua) ? "iOS" : /Android/.test(ua) ? "Android" : /Mac/.test(ua) ? "Mac" : /Windows/.test(ua) ? "Windows" : "Linux"} ${/Firefox\/\d+/.exec(ua)?.[0] ?? /Edg\/\d+/.exec(ua)?.[0] ?? /Chrome\/\d+/.exec(ua)?.[0] ?? /Version\/\d+/.exec(ua)?.[0] ?? ""}`;
  const stats = { rtt: s.clock.rtt ?? 0, netP50: pct(t.nets, 0.5) ?? 0, netP95: pct(t.nets, 0.95) ?? 0, netMax: t.nets.length ? Math.max(...t.nets) : 0, buffer: s.buffer ?? 0, frames: t.frames, late: t.late, gaps: t.gaps, skipped: t.skipped, unsent: t.unsent, stalls: t.stalls, seconds: (performance.now() - t.since) / 1000, path: "ws", codec: `opus@${l.bitrate}/${l.frame}${v.quality === "auto" ? " auto" : ""} ns=${s.rnnoise ? "rnnoise" : v.noise} agc=${v.gain ? 1 : 0}`, platform };
  say(s, { op: "stats", stats });
}

export function leave(error: "" | "full" | "failed" = "") {
  const s = cur;
  cur = null;
  asked++;
  if (!s) return void setVoice(error ? { status: "off", error } : null);
  report(s);
  s.over = true;
  s.timers.forEach(clearInterval);
  clearTimeout(s.retry);
  s.ws?.close();
  s.mic.getTracks().forEach((t) => t.stop());
  s.rnnoise?.destroy();
  if (s.encoder.state !== "closed") s.encoder.close();
  for (const sp of s.speakers.values()) if (sp.decoder.state !== "closed") sp.decoder.close();
  void s.ctx.close();
  setVoice(error ? { status: "off", error, members: [], speaking: [] } : null);
}

/** Muted: the capture falls silent first, then nothing goes up and the microphone rests; back the
 *  other way round. */
export function mute(on: boolean) {
  const s = cur;
  if (!s || getVoice().muted === on) return;
  setVoice({ muted: on });
  tell(s);
  if (!on) {
    s.mic.getAudioTracks().forEach((t) => (t.enabled = true));
    s.held = false;
    s.capture.port.postMessage({ hush: false });
    return;
  }
  s.capture.port.postMessage({ hush: true });
  setTimeout(() => {
    if (s.over || !getVoice().muted) return;
    s.held = true;
    s.mic.getAudioTracks().forEach((t) => (t.enabled = false));
  }, HUSH + 40);
}

// A page that goes says so: the others see it leave at once, not after the room gives up on it.
addEventListener("pagehide", () => leave());
