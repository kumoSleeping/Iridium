// dsh-remote-iridium, browser half: its page among dsh web's plugins, where the Iridium's address
// is typed and asked (dsh's own settings form over the entry's volatile `url`). Plain JS in the
// shape dsh's module loader takes; React and the form parts are the page's own.
window.__ModuleLoader__.load({
  id: 'dsh-remote-iridium',
  factory: (require) => {
    const { jsx, jsxs } = require('react/jsx-runtime')
    const ui = require('@deepseek-ai/dsh-client-ui-primitives')

    const NS = 'remote-iridium'
    const ENTRY = 'remote-iridium' // the profile entry cordis.patch.yml inserts
    const PACKAGE = 'dsh-remote-iridium'

    const zh = {
      title: 'Iridium',
      description: '由 Iridium 在本机运行 dsh 会话。',
      url: 'Iridium 地址',
      urlHint: '例如 https://ir.example.com。申请后在 Iridium 网页上批准；配对码和连接状态用 /iridium 查看。',
      overridden: '已覆盖',
      reset: '恢复默认',
      invalid: '请填写 http(s) 地址。',
      readOnly: '本部署的设置为只读。',
      unavailable: '该插件当前未加载，暂时无法配置。',
      save: '申请连接',
      saving: '申请中…',
      saveFailed: '未能保存地址。',
    }
    const en = {
      title: 'Iridium',
      description: 'Lets an Iridium run dsh sessions on this machine.',
      url: 'Iridium address',
      urlHint: 'For example https://ir.example.com. Allow the request on the Iridium page; /iridium shows the pairing code and the connection.',
      overridden: 'Overridden',
      reset: 'Reset to default',
      invalid: 'Enter an http(s) address.',
      readOnly: 'This deployment stores settings read-only.',
      unavailable: 'This plugin is not loaded, so it cannot be configured right now.',
      save: 'Request access',
      saving: 'Requesting…',
      saveFailed: 'The address was not saved.',
    }

    function Page(props) {
      const { t } = props
      const state = props.useIridiumForm((s) => s)
      if (props.view === 'summary') return t('description')
      return jsxs(ui.SettingsForm, {
        labels: { unavailable: t('unavailable'), readOnly: t('readOnly'), saveFailed: t('saveFailed'), save: t('save'), saving: t('saving') },
        state,
        onSave: props.save,
        onDiscard: props.discard,
        children: [
          jsx(
            ui.SettingsValueField,
            {
              id: 'plugin-config-remote-iridium-url',
              label: t('url'),
              hint: t('urlHint'),
              overriddenLabel: t('overridden'),
              resetLabel: t('reset'),
              invalidLabel: t('invalid'),
              disabled: !state.writable,
              ...state.url,
              onEdit: (text) => props.edit('url', text),
              onReset: () => props.resetField('url'),
            },
            'url',
          ),
        ],
      })
    }

    const inject = ['slots', 'locale', 'configForms']

    function apply(ctx) {
      ctx.effect(() => ctx.locale.register(NS, { zh, en }), 'remote-iridium: dictionaries')
      const form = new ui.SettingsFormModel(ctx.configForms.get(ENTRY), [ui.settingsTextField('url')])
      const store = form.bind(() => ({ ...form.shell(), url: form.field('url') }))
      ctx.effect(() => () => form.dispose(), 'remote-iridium: form')
      ctx.effect(
        () =>
          ctx.slots.inject('plugins.bundle.config', () =>
            ctx.slots.register(
              {
                name: 'plugins.bundle.config',
                key: PACKAGE,
                locale: NS,
                inject: () => ({ hooks: { iridiumForm: store }, ...form.actions() }),
              },
              Page,
            ),
          ),
        'remote-iridium: page',
      )
    }

    const exports = { apply, inject, NS }
    Object.defineProperty(exports, Symbol.toStringTag, { value: 'Module' })
    return exports
  },
})
