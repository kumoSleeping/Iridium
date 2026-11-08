// What of this machine stays on it (Iridium docs/exec.md): secrets in the environment, and files
// with logins and keys. As the Go executor's (internal/remote/executor/secrets.go).
import os from 'node:os'
import path from 'node:path'
import fs from 'node:fs'

/**
 * What the hello says a secret's value is: the value stays here, and a hold.start that passes it
 * back gets the real one. The Iridium bases its spawns on the hello's env and keeps them on its
 * disk, so a key never leaves this machine; old Iridiums pass the env back as they got it.
 */
export const SECRET = '(secret kept at the executor)'

const SECRET_NAME = /KEY|TOKEN|SECRET|PASSW|CREDENTIAL|COOKIE|AUTH|SESSION|PRIVATE|^(DEEPSEEK|ANTHROPIC|OPENAI|AWS|AZURE|GOOGLE|GH|GITHUB|NPM|HF)_/i
// The variables that point an ir at an Iridium or say whose session it runs in: this machine's are
// for an Iridium here, not the one it connects to (that one sets its own).
const OWN_NAME = /^IRIDIUM_(SESSION|SOCKET|CONFIG|URL|TOKEN)$/

/** env ({name: value}) as the hello gives it: secrets' values as SECRET, the Iridium's own left out. */
export function helloEnv(env) {
  const out = []
  for (const [k, v] of Object.entries(env)) {
    if (v === undefined || OWN_NAME.test(k)) continue
    out.push(`${k}=${SECRET_NAME.test(k) ? SECRET : v}`)
  }
  return out
}

/** A hold.start's env (["k=v"]) as an object, each SECRET its value in own (left out if none). */
export function startEnv(list, own) {
  const env = {}
  for (const kv of list ?? []) {
    const i = kv.indexOf('=')
    if (i <= 0) continue
    const k = kv.slice(0, i)
    const v = kv.slice(i + 1)
    if (v !== SECRET) env[k] = v
    else if (own[k] !== undefined) env[k] = own[k]
  }
  return env
}

/**
 * The paths never served: its TOKENs (extra, the plugin's), ~/.ssh, and the agents' logins and keys
 * (dsh's credentials, .env and profiles with this plugin's settings; Claude's, Codex's and pi's
 * logins; cloud, gh, npm and git credentials; GnuPG; .netrc). Each also with its links resolved.
 */
export function deniedPaths(env, agents, extra) {
  const home = os.homedir()
  const at = (...p) => path.join(home, ...p)
  const d = [
    at('.ssh'),
    path.join(env.CLAUDE_CONFIG_DIR || at('.claude'), '.credentials.json'),
    path.join(env.CODEX_HOME || at('.codex'), 'auth.json'),
    at('.pi', 'agent', 'auth.json'),
    at('.aws'), at('.config', 'gh'), at('.gnupg'), at('.netrc'), at('.npmrc'), at('.git-credentials'),
    at('.docker', 'config.json'),
  ]
  const dshHomes = [env.DSH_HOME || at('.dsh'), ...agents.filter((a) => a.agent === 'dsh' && a.home).map((a) => a.home)]
  for (const h of dshHomes) d.push(path.join(h, '.credentials.yaml'), path.join(h, '.env'), path.join(h, 'profiles'))
  d.push(...extra)
  for (const p of [...d]) {
    try {
      const r = fs.realpathSync(p)
      if (r !== p) d.push(r)
    } catch {}
  }
  return d
}
