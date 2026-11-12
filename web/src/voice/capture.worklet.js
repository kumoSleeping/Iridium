// The microphone's samples, out of the audio thread in blocks of 20 ms, each with the context's
// time of its first sample (engine.ts turns it into the room's clock). Plain JS: loaded as a file
// of its own by audioWorklet.addModule (the page's CSP takes no blob).
//
// Two gains on the way, both moved a little a sample, never stepped (a step is a click, sent to
// everyone):
//   hush: down to nothing and back, around what would cut the sound: muting, another microphone,
//     the encoder set again.
//   gate: shut while nobody speaks close to the microphone. Open: a block as loud as the mark, or
//     with a peak well over it; shut again once it has stayed under a lower mark for HOLD. The
//     mark is the one set (the level a person dragged it to), else found by itself, the higher of
//     two: a part (OPEN) of this speaker's own speech lately (voices across the room, a keyboard,
//     a mouse are far quieter here than the one at the microphone), and well over (OVER) the
//     room's noise, which is the quietest the microphone has been lately (it follows a block
//     quieter than it at once, and creeps up otherwise: speech never stays as low as the room).
// Each block goes with how loud it was before the gains (its RMS), the mark, and whether the gate
// was open.
const ATTACK = Math.round(sampleRate * 0.005);
const RELEASE = Math.round(sampleRate * 0.06);
const HOLD = 15; // blocks: 300 ms
const OPEN = 0.12; // of the speaker's level: 18 dB under it
const CLOSE = 0.85; // of the mark to open
const FLOOR = 0.004; // never open under this: -48 dBFS
const DECAY = 0.9986; // the speaker's level halves in 10 s of speech quieter than it
const OVER = 4; // of the room's noise: 12 dB over it
const CREEP = 1.01; // the room's noise doubles in 1.4 s of nothing quieter

class Capture extends AudioWorkletProcessor {
  constructor() {
    super();
    this.size = Math.round(sampleRate / 50);
    this.buf = new Float32Array(this.size);
    this.n = 0;
    this.hush = false;
    this.hushGain = 1;
    this.gate = false;
    this.open = false;
    this.held = 0;
    this.gateGain = 1;
    this.loud = 0.05;
    this.mark = null; // the mark set; null: found by itself
    this.noise = 0.001; // the room's
    this.now = FLOOR; // the mark in use
    this.level = 0;
    this.port.onmessage = (e) => {
      if (e.data.hush !== undefined) this.hush = e.data.hush;
      if (e.data.gate !== undefined) {
        if (e.data.gate !== this.gate) this.held = 0;
        this.gate = e.data.gate;
        this.mark = e.data.mark ?? null;
      }
    };
  }
  /** The block, whole: the gate decided on all of it, then the gains run over it. */
  block(pcm) {
    let sum = 0;
    let peak = 0;
    for (let i = 0; i < pcm.length; i++) {
      const a = Math.abs(pcm[i]);
      sum += a * a;
      if (a > peak) peak = a;
    }
    const rms = (this.level = Math.sqrt(sum / pcm.length));
    this.noise = rms < this.noise ? Math.max(rms, 0.0001) : Math.min(this.noise * CREEP, 0.02);
    const mark = (this.now = this.mark ?? Math.max(FLOOR, this.loud * OPEN, this.noise * OVER));
    if (this.gate) {
      if (rms > mark || peak > mark * 4) {
        this.open = true;
        this.held = 0;
      } else if (this.open && rms < mark * CLOSE && ++this.held > HOLD) this.open = false;
      if (this.open) this.loud = Math.min(0.5, Math.max(rms, this.loud * DECAY, 0.02));
    }
    const gate = !this.gate || this.open ? 1 : 0;
    const hush = this.hush ? 0 : 1;
    for (let i = 0; i < pcm.length; i++) {
      this.gateGain = gate > this.gateGain ? Math.min(1, this.gateGain + 1 / ATTACK) : Math.max(gate, this.gateGain - 1 / RELEASE);
      this.hushGain = hush > this.hushGain ? Math.min(1, this.hushGain + 1 / ATTACK) : Math.max(hush, this.hushGain - 1 / ATTACK);
      pcm[i] *= this.gateGain * this.hushGain;
    }
  }
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (!ch) return true;
    let i = 0;
    while (i < ch.length) {
      const take = Math.min(ch.length - i, this.size - this.n);
      this.buf.set(ch.subarray(i, i + take), this.n);
      this.n += take;
      i += take;
      if (this.n === this.size) {
        const pcm = this.buf;
        this.block(pcm);
        // The block's first sample, by the context's clock now: not counted up from the start, as
        // the node is not run while nothing is joined to it (another microphone coming).
        this.port.postMessage({ pcm, t: (currentFrame + i - this.size) / sampleRate, level: this.level, mark: this.now, open: !this.gate || this.open }, [pcm.buffer]);
        this.buf = new Float32Array(this.size);
        this.n = 0;
      }
    }
    return true;
  }
}
registerProcessor("ir-voice-capture", Capture);
