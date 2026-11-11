// Iridium: binds each session of this process to the agent preset of its config.
export const name = 'iridium-preset'
export const inject = ['agents', 'agentPresets']
export function apply(ctx, config) {
  ctx.on('agent/created', async ({ agent }) => {
    if (ctx.agentPresets.composedPreset(agent.ctx) !== undefined) return
    const { id } = await ctx.agentPresets.recompose(agent.ctx, config.preset)
    if (ctx.agentPresets.presetForSession(agent.session) !== id) agent.session.append('agent-preset/selected', { agentPreset: id })
  })
}
