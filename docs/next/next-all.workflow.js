export const meta = {
  name: 'next-all',
  description: 'Run the remaining 0.2 refactor steps: S1 sequential, then S2-S4a and Satori in parallel lanes, merge, S5',
  phases: [
    { title: 'S1' },
    { title: 'Lane A' },
    { title: 'Lane B' },
    { title: 'Merge' },
    { title: 'S5' },
  ],
}

const MAIN = '/home/iridium/kumo/Iridium-next'
const SAT = '/home/iridium/kumo/Iridium-satori'
const rules = wt => `You work ONLY in the git worktree ${wt}. Never edit, build into, or run git commands that change ~/kumo/Iridium (the main tree: other sessions work there)${wt === MAIN ? '' : ` or ${MAIN}`}. ` +
  `Go is at ~/.local/bin/go (export PATH=$HOME/.local/bin:$PATH). Frontend: cd ${wt}/web && npm run check (run npm ci there first if node_modules is missing). ` +
  `First read ${wt}/docs/next/decisions.md (binding decisions; numbers refer to prep.md §3), ${wt}/docs/next/progress.md (what earlier steps did), and the parts of ${wt}/docs/next/prep.md, draft.md, k_glossary.md relevant to your step. k_bot.md is an OLD doc (inventory of current behaviour to delete). There is no "Bot" concept in 0.2: all apps are equal; a type name is display only. ` +
  `QQ bridge (cmd/iridium-qq, internal/qqbridge) and the dsh plugin (plugins/dsh-remote-iridium) are deferred: only minimal changes to keep them compiling and their tests passing (deleting a qqbridge test that only exercised a removed daemon API is fine; note it). ` +
  `One switch, no old interfaces kept, except the exec-link compatibility listed in decisions.md. Tests use temp dirs and fake agents; never touch ~/.claude ~/.codex ~/.pi ~/.config, the running Iridium instances (ports 7810/7811), or processes you did not start. ` +
  `UI copy: written register, short, Chinese and English both (no chatty narration). Layout changes in the web must animate (glide/pop), never jump. Don't push to any remote. Work fast: no exploratory detours beyond what the step needs.`

const RESULT = {
  type: 'object',
  properties: {
    base: { type: 'string' }, head: { type: 'string' },
    checks_green: { type: 'boolean', description: 'go vet ./..., go test ./..., npm run check all pass at head' },
    summary: { type: 'string' }, deviations: { type: 'string' }, left_for_later: { type: 'string' },
  },
  required: ['base', 'head', 'checks_green', 'summary', 'deviations', 'left_for_later'],
}
const FINDINGS = {
  type: 'object',
  properties: { findings: { type: 'array', items: { type: 'object', properties: {
    severity: { type: 'string', enum: ['high', 'medium', 'low'] }, file: { type: 'string' }, line: { type: 'number' },
    problem: { type: 'string' }, evidence: { type: 'string' }, fix: { type: 'string' },
  }, required: ['severity', 'file', 'problem', 'evidence', 'fix'] } } },
  required: ['findings'],
}

async function runStep(step, wt, ph) {
  const impl = await agent(`${rules(wt)}\n\nImplement step "${step.key}: ${step.title}" of the 0.2 refactor.\n\nScope:\n${step.scope}\n\nRead the code before changing it; several focused commits are fine. Tests for new behaviour. At the end go vet ./..., go test ./..., (cd web && npm run check) must pass; if API types changed regenerate web/src/api/types.gen.ts the way the repo does (tygo.yaml / scripts). Append a short section to docs/next/progress.md (Chinese: 做了什么、和计划不一样的地方、留给后面的) and commit. Commit messages in English, repo style, ending with: Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>. Record HEAD before you start as base.`, { label: `impl:${step.key}`, phase: ph, schema: RESULT })
  if (!impl) throw new Error(`${step.key}: implementer died`)
  log(`${step.key}: ${impl.base.slice(0, 7)}..${impl.head.slice(0, 7)} green=${impl.checks_green}`)
  const rev = await agent(`${rules(wt)}\n\nREAD-ONLY review (no edits, no commits) of step "${step.key}: ${step.title}" = git -C ${wt} diff ${impl.base}..${impl.head}.\nScope:\n${step.scope}\nImplementer's notes: ${JSON.stringify({ summary: impl.summary, deviations: impl.deviations, left_for_later: impl.left_for_later })}\n\nFind verified real issues only: bugs, security/permission holes, broken existing behaviour, data loss in migrations, vacuous tests, AND scope items not done or decisions.md violated without a recorded reason. file:line + evidence. No style nits.`, { label: `review:${step.key}`, phase: ph, schema: FINDINGS })
  const findings = (rev && rev.findings) || []
  const serious = findings.filter(f => f.severity !== 'low')
  log(`${step.key}: ${findings.length} findings, ${serious.length} medium/high`)
  let fix = null
  if (serious.length || !impl.checks_green) {
    fix = await agent(`${rules(wt)}\n\nStep "${step.key}: ${step.title}" (${impl.base}..${impl.head}) was reviewed. Scope:\n${step.scope}\nChecks green after implementation: ${impl.checks_green}.\nFindings:\n${JSON.stringify(findings)}\n\nVerify each against the code; fix the real ones (test where sensible; low ones only if trivial); skip wrong/out-of-scope ones with a reason, putting real-but-later items into docs/next/progress.md 留给后面的. Make go vet, go test ./..., npm run check pass. Commit. Return base=${impl.head}.`, { label: `fix:${step.key}`, phase: ph, schema: RESULT })
  }
  const green = fix ? fix.checks_green : impl.checks_green
  if (!green) throw new Error(`${step.key}: checks not green`)
  return { step: step.key, impl, findings, fix }
}

const S = args.steps
const byKey = k => S.find(s => s.key === k)
const done = []

phase('S1')
for (const k of ['S1a', 'S1b', 'S1c']) done.push(await runStep(byKey(k), MAIN, 'S1'))

const laneA = async () => {
  const r = []
  for (const k of ['S2', 'S3', 'S4a']) r.push(await runStep(byKey(k), MAIN, 'Lane A'))
  return r
}
const laneB = async () => {
  const setup = await agent(`Create a git worktree for a parallel lane: run exactly: git -C ${MAIN} worktree add ${SAT} -b next-satori HEAD  — then (cd ${SAT}/web && npm ci --no-audit --no-fund). Do nothing else. Return the new HEAD sha.`, { label: 'laneB:setup', phase: 'Lane B' })
  log(`lane B worktree: ${String(setup).slice(0, 80)}`)
  return [await runStep(byKey('S4b'), SAT, 'Lane B')]
}
const [a, b] = await Promise.all([laneA(), laneB()])
done.push(...a, ...b)

phase('Merge')
const merge = await agent(`${rules(MAIN)}\n\nMerge the parallel lane: branch next-satori (worktree ${SAT}, step S4b: Satori with ir) into next in ${MAIN} (git -C ${MAIN} merge next-satori). Lane A meanwhile did S2 permissions, S3 agents/places/run, S4a event production/needs/send/share and deleted the chat layer. Resolve conflicts, then WIRE them together: Satori's event stream must be fed by S4a's per-login event production (originating login, visibility by permissions), Satori HTTP methods must go through S2's permission checks and S1's login model, message.create into a channel = send into a session, resource proxy respects scope. Remove any temporary shims either lane left for the other (see both lanes' notes in docs/next/progress.md). go vet, go test ./..., npm run check green. Commit. Then remove the lane worktree: git -C ${MAIN} worktree remove ${SAT} (keep the branch).`, { label: 'merge', phase: 'Merge', schema: RESULT })
if (!merge || !merge.checks_green) throw new Error('merge not green')
done.push({ step: 'merge', impl: merge })

phase('S5')
done.push(await runStep(byKey('S5'), MAIN, 'S5'))
return done.map(d => ({ step: d.step, head: (d.fix || d.impl).head, summary: d.impl.summary, deviations: d.impl.deviations, left: (d.fix || d.impl).left_for_later, findings: (d.findings || []).length }))
