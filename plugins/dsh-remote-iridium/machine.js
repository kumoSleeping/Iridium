// This computer's ID, hashed as Iridium's machineid.Hash() does it: the hello says it, so an
// Iridium on this same computer knows this app is on it and asks it for a local dsh launch's
// presets and patches (agent.presets, agent.prepare). It guards against a mistake, not an
// attacker: any executor may say any hash.
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import fs from 'node:fs'

/** id hashed as Iridium's machineid.Of() does it (sha256 of "iridium machine " + id, its first 16
 *  bytes hex); "" for none. */
export function hashOf(id) {
  id = String(id ?? '').trim()
  if (!id || id === 'none') return ''
  return createHash('sha256').update('iridium machine ' + id).digest('hex').slice(0, 32)
}

/** This computer's ID hashed; "" if it has none to be read. IRIDIUM_MACHINE_ID overrides it, as
 *  Iridium's own machineid.Hash() does. */
export function machineID() {
  return hashOf(read())
}

function read() {
  if (process.env.IRIDIUM_MACHINE_ID !== undefined) return process.env.IRIDIUM_MACHINE_ID
  if (process.platform === 'linux') {
    for (const p of ['/etc/machine-id', '/var/lib/dbus/machine-id']) {
      try {
        return fs.readFileSync(p, 'utf8')
      } catch {}
    }
    return ''
  }
  if (process.platform === 'darwin') {
    try {
      const out = execFileSync('ioreg', ['-rd1', '-c', 'IOPlatformExpertDevice'], { encoding: 'utf8' })
      return /"IOPlatformUUID" = "([^"]+)"/.exec(out)?.[1] ?? ''
    } catch {
      return ''
    }
  }
  if (process.platform === 'win32') {
    try {
      const out = execFileSync('reg', ['query', 'HKLM\\SOFTWARE\\Microsoft\\Cryptography', '/v', 'MachineGuid'], { encoding: 'utf8' })
      return /MachineGuid\s+REG_SZ\s+(\S+)/.exec(out)?.[1] ?? ''
    } catch {
      return ''
    }
  }
  return ''
}
