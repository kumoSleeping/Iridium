// Uploads (plan "文件传输"): create the task, PUT chunks at their offsets, ask what came to go on
// after a break, and finish with the whole file's sha256. Chunks start at 8MB and are sized from the
// speed seen to take some 15 s each, up to 50MB. The task is kept by file in localStorage, so the
// same file picked again after a reload goes on from where it stopped.
import type { UploadDone } from "../api/assumed";
import { api, ApiError } from "../api/client";
import { keep, kept } from "../ui";
import { Sha256 } from "./sha256";

const MB = 1 << 20;
export const CHUNK = { first: 8 * MB, least: 1 * MB, most: 50 * MB, seconds: 15 };

/** The next chunk's size from the last one's: bytes over ms, aiming at CHUNK.seconds. */
export const nextChunk = (bytes: number, ms: number) => Math.round(Math.min(CHUNK.most, Math.max(CHUNK.least, (bytes / Math.max(ms, 1)) * 1000 * CHUNK.seconds)) / 65536) * 65536;

const taskKey = (session: string, f: File) => `upload:${session}:${f.name}:${f.size}:${f.lastModified}`;

async function hashInto(h: Sha256, f: File, from: number, to: number, signal?: AbortSignal) {
  for (let at = from; at < to; at += 8 * MB) {
    signal?.throwIfAborted();
    h.update(new Uint8Array(await f.slice(at, Math.min(to, at + 8 * MB)).arrayBuffer()));
  }
}

const pause = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Uploads f for session; progress(sent, size) as chunks land. Resumes a task kept for the same
 *  file. Throws on a refusal, or after three network errors in a row. */
export async function upload(session: string, f: File, progress: (sent: number, size: number) => void, signal?: AbortSignal): Promise<UploadDone> {
  const key = taskKey(session, f);
  let id = kept(key);
  let at = 0;
  if (id) {
    try {
      at = (await api.uploadAt(id)).received;
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 404)) throw e;
      id = null;
    }
  }
  if (!id) {
    id = (await api.upload(session, f.name, f.size, f.type || "application/octet-stream")).id;
    keep(key, id);
    at = 0;
  }
  const h = new Sha256();
  await hashInto(h, f, 0, at, signal);
  progress(at, f.size);
  let chunk = CHUNK.first;
  let fails = 0;
  while (at < f.size) {
    signal?.throwIfAborted();
    const end = Math.min(f.size, at + chunk);
    const piece = f.slice(at, end);
    const t0 = performance.now();
    try {
      const r = await api.uploadChunk(id, at, piece, signal);
      // The server says how far it has it; a chunk that landed short goes again from there.
      const got = Math.min(r.received, end);
      await hashInto(h, f, at, got, signal);
      chunk = nextChunk(got - at, performance.now() - t0);
      at = got;
      fails = 0;
      progress(at, f.size);
    } catch (e) {
      if (signal?.aborted || (e instanceof ApiError && e.status !== 409 && e.status < 500)) throw e;
      if (++fails >= 3) throw e;
      await pause(1000 * fails);
      // Where the server is now: the chunk may have landed with its answer lost.
      try {
        const now = (await api.uploadAt(id)).received;
        if (now > at) {
          await hashInto(h, f, at, now, signal);
          at = now;
          progress(at, f.size);
        }
      } catch {}
      chunk = Math.max(CHUNK.least, Math.round(chunk / 2));
    }
  }
  const done = await api.uploadDone(id, h.hex());
  keep(key, null);
  return done;
}
