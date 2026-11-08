// ir for an agent the Iridium hosts at this machine (Iridium's docs/exec.md "远端的 ir"): its
// arguments and folder go to the Iridium's POST /api/cli with the session's own TOKEN
// (IRIDIUM_URL, IRIDIUM_TOKEN, set as the executor started it), and the Iridium runs them as
// this machine's Bot, for that session; what it writes comes back a line at a time. The executor
// puts this on the agent's PATH as `ir` (ir, ir.cmd beside it).
const url = process.env.IRIDIUM_URL
const token = process.env.IRIDIUM_TOKEN
if (!url || !token) {
  process.stderr.write('ir: no Iridium here (IRIDIUM_URL, IRIDIUM_TOKEN)\n')
  process.exit(1)
}

/** Writes s to a stream, waiting while it is full. */
const put = (stream, s) => (s ? new Promise((done) => (stream.write(s) ? done() : stream.once('drain', done))) : Promise.resolve())

async function main() {
  let res
  try {
    res = await fetch(url.replace(/\/+$/, '') + '/api/cli', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
      body: JSON.stringify({ args: process.argv.slice(2), cwd: process.cwd() }),
    })
  } catch (e) {
    await put(process.stderr, `ir: the Iridium at ${url} does not answer: ${e.cause?.message ?? e.message}\n`)
    return 1
  }
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    let msg = text.trim()
    try {
      msg = JSON.parse(text).error || msg
    } catch {}
    await put(process.stderr, `ir: ${msg || res.status}\n`)
    return 1
  }
  const dec = new TextDecoder()
  let buf = ''
  for await (const chunk of res.body) {
    buf += dec.decode(chunk, { stream: true })
    let i
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i)
      buf = buf.slice(i + 1)
      let o
      try {
        o = JSON.parse(line)
      } catch {
        continue
      }
      await put(process.stdout, o.out)
      await put(process.stderr, o.err)
      if (typeof o.exit === 'number') return o.exit
    }
  }
  await put(process.stderr, 'ir: the answer stopped short\n')
  return 1
}

main().then((code) => process.exit(code))
