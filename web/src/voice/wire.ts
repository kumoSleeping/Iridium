// A voice frame's head and the numbers measured on the way (docs/voice.md): no audio here, so
// the tests run without a browser's.

/** A frame as a member sends it: kind, flags, seq (2), the room's clock at its first sample (4,
 *  ms, wrapping), then Opus. The room puts the sender's number (2) before it. */
export const HEADER = 8;
export const KIND_OPUS = 1;

export function pack(seq: number, clock: number, opus: Uint8Array): Uint8Array<ArrayBuffer> {
  const b = new Uint8Array(HEADER + opus.length);
  const v = new DataView(b.buffer);
  v.setUint8(0, KIND_OPUS);
  v.setUint16(2, seq & 0xffff);
  v.setUint32(4, low32(clock));
  b.set(opus, HEADER);
  return b;
}

export type Heard = { from: number; seq: number; clock: number; opus: Uint8Array };

/** A frame as the room passes it on; null: not one. */
export function unpack(buf: ArrayBuffer): Heard | null {
  if (buf.byteLength <= 2 + HEADER) return null;
  const v = new DataView(buf);
  if (v.getUint8(2) !== KIND_OPUS) return null;
  return { from: v.getUint16(0), seq: v.getUint16(4), clock: v.getUint32(6), opus: new Uint8Array(buf, 2 + HEADER) };
}

/** A clock's low 32 bits of its milliseconds, as a frame carries them. */
export const low32 = (ms: number) => Math.floor(ms) % 2 ** 32 >>> 0;
/** now − then, both low 32 bits of the same clock: right across the wrap. */
export const since = (now: number, then: number) => (now - then) | 0;

/** The p-th percentile (0–1) of xs; null with none. */
export function pct(xs: number[], p: number): number | null {
  if (!xs.length) return null;
  const s = [...xs].sort((a, b) => a - b);
  return s[Math.min(s.length - 1, Math.floor(p * s.length))]!;
}

/** The room's clock from this page's: pings give round trips, and the quickest of the last few
 *  is the truest (the least of it spent waiting somewhere). */
export class Clock {
  private seen: { rtt: number; off: number }[] = [];
  /** A pong: sent at t0 and back at t1 (this page's clock), the room's clock then `now`. */
  pong(t0: number, t1: number, now: number) {
    this.seen.push({ rtt: t1 - t0, off: now - (t0 + t1) / 2 });
    if (this.seen.length > 10) this.seen.shift();
  }
  get ready() {
    return this.seen.length > 0;
  }
  /** The room's clock is this page's plus this. */
  get offset() {
    return this.seen.reduce((a, b) => (b.rtt < a.rtt ? b : a)).off;
  }
  /** The round trip, the median of the last few; null before the first. */
  get rtt() {
    return pct(this.seen.map((s) => s.rtt), 0.5);
  }
}

/** How much of a speaker's words wait before they play, and when the queue counts as behind, in
 *  ms: the spread of how long its frames took, nearly the worst of them (a frame later than what
 *  waits is a gap in the middle of a word, heard far more than the wait is), a frame and a little
 *  over it. */
export function playout(delays: number[], frame: number): { target: number; max: number } {
  const spread = delays.length >= 8 ? pct(delays, 0.98)! - pct(delays, 0.05)! : 60;
  const target = Math.round(Math.min(800, Math.max(60, spread + frame + 20)));
  return { target, max: target + Math.max(240, target) };
}
