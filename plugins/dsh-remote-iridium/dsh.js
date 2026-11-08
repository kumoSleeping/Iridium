// Requests about the dsh this plugin runs in, beside the executor's own. Apart from index.js so
// that it loads without dsh's packages (the tests' run.mjs).
import path from 'node:path'
import { RemoteError } from './executor.js'

/**
 * What the dsh this plugin runs in knows, for the Iridium (its docs/exec.md "远端 DSH"):
 * dsh.open, the sessions open in this process now (DSH Desktop's: one the Iridium resumes is refused
 * while it is), and what this plugin's settings keep from it (kept(): unsynced, the workspaces,
 * by folder or name; ungrouped false, the sessions in none of them); dsh.attach {session, cwd}, the session filed under the workspace of its folder, made
 * if there is none (a session the Iridium started here, in DSH Desktop's sidebar); dsh.prompt
 * {session, text, steer}, a message for a session this dsh holds, given to its agent here.
 */
export function dshMethods(ctx, kept = () => ({ unsynced: [], ungrouped: true })) {
  return {
    'dsh.open': async () => {
      const k = kept()
      return { sessions: (ctx.get('sessions')?.list() ?? []).map((s) => s.id), unsynced: k.unsynced, ...(k.ungrouped === false ? { unsyncedUngrouped: true } : {}) }
    },
    // A message for a session this dsh holds (open in DSH Desktop, its writer lock here): the
    // Iridium cannot resume it, so its agent here takes the message, as a next turn or (steer)
    // into the running one; not_found if no agent of it lives here.
    'dsh.prompt': async (p) => {
      if (typeof p.session !== 'string' || !p.session || typeof p.text !== 'string' || !p.text.trim())
        throw new RemoteError('bad_request', 'dsh.prompt: session and text')
      const a = ctx.get('agents')?.get(p.session)
      if (!a) throw new RemoteError('not_found', `no agent of ${p.session} here`)
      const input = { content: [{ type: 'text', text: p.text }], source: { kind: 'user' } } // yours, through the Iridium (its tag before the words)
      try {
        await (p.steer ? a.steer(input) : a.followup(input))
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
      return {}
    },
    'dsh.attach': async (p) => {
      if (typeof p.session !== 'string' || !p.session || typeof p.cwd !== 'string' || !path.isAbsolute(p.cwd))
        throw new RemoteError('bad_request', 'dsh.attach: session and an absolute cwd')
      const reg = ctx.get('workspaceRegistry')
      if (!reg) throw new RemoteError('unsupported', 'no workspace registry in this dsh')
      try {
        const w = (await reg.resolveByPath(p.cwd)) ?? (await reg.create(p.cwd))
        await w.attachSession(p.session)
        return { workspace: w.id }
      } catch (e) {
        throw new RemoteError('failed', e.message)
      }
    },
  }
}
