// Uploads (plan "文件传输", docs/api.md "文件和上传"): create the task, PUT chunks at their offsets,
// ask what came to go on after a break, and complete with the whole file's sha256. Chunks start at
// 8MB and are sized from the speed seen to take some 15 s each, between 8MB and 50MB, less only on
// a link too slow for 8MB in a minute. The task is kept by file in localStorage, so the same file
// picked again after a reload goes on from where it stopped.
import { api, ApiError, notThere } from "../api/client";
import type { FileRef, Upload } from "../api/types.gen";
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
/** What the server has, from a 409's body (an offset that was not its own). */
const received = (e: unknown) => (e instanceof ApiError && e.status === 409 ? (e.body as Partial<Upload> | undefined)?.received : undefined);

/** Uploads f for session; progress(sent, size) as chunks land. Resumes a task kept for the same
 *  file. Throws on a refusal, or after three failures in a row. */
export async function upload(session: string, f: File, progress: (sent: number, size: number) => void, signal?: AbortSignal, again = true): Promise<FileRef> {
  const key = taskKey(session, f);
  let id = kept(key);
  let at = 0;
  if (id) {
    try {
      const u = await api.uploadAt(id);
      if (u.done && u.file) return (keep(key, null), u.file);
      at = u.received;
    } catch (e) {
      if (!notThere(e)) throw e;
      id = null;
    }
  }
  if (!id) {
    id = (await api.upload(session, { name: f.name, size: f.size })).id;
    keep(key, id);
    at = 0;
  }
  let h = new Sha256();
  await hashInto(h, f, 0, at, signal);
  progress(at, f.size);
  let chunk = CHUNK.first;
  let fails = 0;
  const goTo = async (now: number) => {
    if (now > at) await hashInto(h, f, at, now, signal);
    else if (now < at) {
      // The server has less than was hashed: hash again from the start up to it.
      h = new Sha256();
      await hashInto(h, f, 0, now, signal);
    }
    at = now;
    progress(at, f.size);
  };
  while (at < f.size) {
    signal?.throwIfAborted();
    const end = Math.min(f.size, at + chunk);
    const t0 = performance.now();
    try {
      const r = await api.uploadChunk(id, at, f.slice(at, end), signal);
      const got = Math.min(r.received, f.size);
      chunk = nextChunk(got - at, performance.now() - t0);
      await goTo(got);
      fails = 0;
    } catch (e) {
      if (signal?.aborted) throw e;
      const theirs = received(e);
      if (theirs !== undefined) {
        await goTo(theirs);
        continue;
      }
      if ((e instanceof ApiError && e.status < 500) || ++fails >= 3) throw e;
      await pause(1000 * fails);
      // Where the server is now: the chunk may have landed, or part of it, its answer lost.
      try {
        await goTo((await api.uploadAt(id)).received);
      } catch {}
      chunk = Math.max(CHUNK.least, Math.round(chunk / 2));
    }
  }
  try {
    const done = await api.uploadDone(id, h.hex());
    keep(key, null);
    return done.file!;
  } catch (e) {
    // Not the file it was: what came is dropped; once more from the start.
    keep(key, null);
    if (again && e instanceof ApiError && e.status === 422) return upload(session, f, progress, signal, false);
    throw e;
  }
}
