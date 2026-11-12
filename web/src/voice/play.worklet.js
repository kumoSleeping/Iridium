// What the others say, played: a queue a speaker, mixed. A speaker's words start once `target`
// samples of them wait (the network's jitter, engine.ts measures it), and play on till the queue
// runs dry, the end of that stretch of speech (a speaker sends nothing while silent). A queue
// past `max` is behind (the link stalled, then gave all at once): its oldest go, down to target,
// so what is heard is never far behind what is said; not yet playing, it is cut as it starts.
//
// Nothing starts or stops at full level: the gain rises over FADE as words begin (or go on past a
// cut), and falls over the queue's last FADE as it runs dry, so a gap is a short silence, not a
// click. A frame that comes alone is not played: a silent speaker's encoder gives one out every
// 400 ms (its background noise), which heard by itself is a tick.
//
// A speaker that sends all the time has no end of speech to catch up at: its queue, longer than
// target by SLACK, lets go of the quiet chunks at its head (the pauses between words), which
// nobody hears go. What is mixed is bent under full level, not clipped at it.
const FADE = Math.round(sampleRate * 0.012);
// Never plain silence out: a phone's amplifier rests on it and wakes with a pop at the next word
// (the user, 2026-11-12: 吧嗒吧嗒 from a phone's speaker). A hiss far under hearing keeps it awake.
const HISS = 0.0002; // -74 dBFS
const LONE = Math.round(sampleRate * 0.05); // one frame, and a little
const SLACK = Math.round(sampleRate * 0.03);
const QUIET = 0.006; // a chunk with no sample over this is a pause: -44 dBFS
const KNEE = 0.85; // the mix is as it is under this, bent towards 1 over it
class Play extends AudioWorkletProcessor {
  constructor() {
    super();
    this.speakers = new Map();
    this.waited = 0; // samples, summed over the chunks begun
    this.begun = 0;
    this.late = 0; // chunks dropped
    this.gaps = 0; // queues that ran dry
    this.skipped = 0; // quiet chunks let go
    this.port.onmessage = (e) => {
      const m = e.data;
      if (m.drop !== undefined) return void this.speakers.delete(m.drop);
      // The tests listen to what is played (scripts/voicepop.mjs).
      if (m.tap !== undefined) return void (this.tap = m.tap);
      let s = this.speakers.get(m.id);
      if (!s) this.speakers.set(m.id, (s = { q: [], at: 0, n: 0, on: false, idle: 0, gain: 0, cut: false, target: 2880, max: 19200 }));
      if (m.target) {
        s.target = m.target;
        s.max = m.max;
      }
      if (!m.pcm) return;
      // How long this chunk waits: what is ahead of it, and the start's wait if not playing yet.
      this.waited += s.n + (s.on ? 0 : Math.max(0, s.target - s.n - m.pcm.length));
      this.begun++;
      let peak = 0;
      for (let i = 0; i < m.pcm.length; i++) {
        const a = Math.abs(m.pcm[i]);
        if (a > peak) peak = a;
      }
      m.pcm.quiet = peak < QUIET;
      s.q.push(m.pcm);
      s.idle = 0;
      s.n += m.pcm.length;
      // Behind: cut back to target, once what plays now has faded out (process).
      if (s.n > s.max) s.cut = true;
      if (!s.on && s.n >= s.target) s.on = true;
    };
    this.ticks = 0;
  }
  process(_, outputs) {
    const out = outputs[0][0];
    if (!out) return true;
    for (const s of this.speakers.values()) {
      // Nothing more has come for as long as words wait: short words ("嗯") are played; a frame
      // alone is dropped.
      if (!s.on && s.n && (s.idle += out.length) >= s.target) {
        if (s.n > LONE) s.on = true;
        else {
          s.q.length = s.at = s.n = 0;
        }
      }
      if (!s.on) continue;
      for (let i = 0; i < out.length && s.q.length; i++) {
        // Up to full over FADE; down with what is left of the queue, or before a cut.
        const want = s.cut ? 0 : Math.min(1, s.n / FADE);
        s.gain = want < s.gain ? Math.max(want, s.gain - 1 / FADE) : Math.min(want, s.gain + 1 / FADE);
        if (s.cut && s.gain === 0) {
          s.cut = false;
          if (!s.on || s.n <= s.target) continue;
          // The oldest go, whole chunks of them, down to target.
          while (s.q.length > 1 && s.n - (s.q[0].length - s.at) >= s.target) {
            s.n -= s.q[0].length - s.at;
            s.q.shift();
            s.at = 0;
            this.late++;
          }
        }
        // A pause at the head of a queue that is long: let go, whole.
        while (s.at === 0 && s.q.length > 1 && s.q[0].quiet && s.n - s.q[0].length >= s.target + SLACK) {
          s.n -= s.q[0].length;
          s.q.shift();
          this.skipped++;
        }
        const c = s.q[0];
        out[i] += c[s.at] * s.gain;
        s.n--;
        if (++s.at === c.length) {
          s.q.shift();
          s.at = 0;
        }
      }
      if (!s.q.length) {
        s.on = false;
        s.gain = 0;
        this.gaps++;
      }
    }
    for (let i = 0; i < out.length; i++) {
      out[i] += (Math.random() * 2 - 1) * HISS;
      const a = Math.abs(out[i]);
      if (a > KNEE) out[i] = Math.sign(out[i]) * (KNEE + (1 - KNEE) * Math.tanh((a - KNEE) / (1 - KNEE)));
    }
    for (let c = 1; c < outputs[0].length; c++) outputs[0][c].set(out);
    if (this.tap) this.port.postMessage({ played: out.slice() });
    // About once a second: what was measured since the last.
    if (++this.ticks >= Math.round(sampleRate / 128)) {
      this.ticks = 0;
      this.port.postMessage({ waited: this.begun ? (this.waited / this.begun / sampleRate) * 1000 : null, late: this.late, gaps: this.gaps, skipped: this.skipped });
      this.waited = this.begun = this.late = this.gaps = this.skipped = 0;
    }
    return true;
  }
}
registerProcessor("ir-voice-play", Play);
