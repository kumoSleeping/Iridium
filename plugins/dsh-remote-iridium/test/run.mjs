// Runs the executor for Iridium's tests (internal/remote): node run.mjs <url> <token> <state>.
// SIGUSR1 drops the connection (it connects again); SIGTERM stops it and its processes.
import { Executor } from '../executor.js'

const [url, token, state] = process.argv.slice(2)
const ex = new Executor({ url, token, state, log: (m) => console.error(`executor: ${m}`) })
process.on('SIGUSR1', () => ex.drop())
process.on('SIGTERM', () => {
  ex.stop()
  setTimeout(() => process.exit(0), 100)
})
ex.run()
