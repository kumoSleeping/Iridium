// dsh-remote-iridium, browser half: a page of its own in dsh's settings (a settings.section, beside
// 通用 and 模型, not under 插件; the user, 2026-11-08), the same in dsh web and DSH Desktop, which
// share this settings shell. On it: whether it connects to the Iridium on this computer (the
// entry's volatile `local`), another Iridium's address (dsh's own settings form over the entry's
// volatile `url`), and whether the sessions begun here show in the Iridium (`syncNewSessions`);
// the switches written at once. Plain JS in the shape dsh's module loader takes; React and the form
// parts are the page's own.
window.__ModuleLoader__.load({
  id: 'dsh-remote-iridium',
  factory: (require) => {
    const { jsx, jsxs } = require('react/jsx-runtime')
    const { useEffect, useRef, useState } = require('react')
    const ui = require('@deepseek-ai/dsh-client-ui-primitives')

    const NS = 'remote-iridium'
    const ENTRY = 'remote-iridium' // the profile entry cordis.patch.yml inserts
    const SECTION = 'remote-iridium' // its page among the settings' sections

    const zh = {
      title: 'Iridium',
      connection: '连接',
      local: '本机 Iridium',
      sync: '同步新会话',
      url: '其他 Iridium 地址',
      urlHint: '例如 https://ir.example.com。保存后在 Iridium 网页上批准；配对码和连接状态用 /iridium 查看。',
      overridden: '已覆盖',
      reset: '恢复默认',
      invalid: '请填写 http(s) 地址。',
      readOnly: '本部署的设置为只读。',
      unavailable: '该插件当前未加载，暂时无法配置。',
      save: '保存',
      saving: '保存中…',
      saveFailed: '未能保存设置。',
      switchFailed: '未能保存，请重试。',
    }
    const en = {
      title: 'Iridium',
      connection: 'Connection',
      local: 'Iridium on this computer',
      sync: 'Sync new sessions',
      url: 'Another Iridium',
      urlHint: 'For example https://ir.example.com. Once saved, allow the request on the Iridium page; /iridium shows the pairing code and the connection.',
      overridden: 'Overridden',
      reset: 'Reset to default',
      invalid: 'Enter an http(s) address.',
      readOnly: 'This deployment stores settings read-only.',
      unavailable: 'This plugin is not loaded, so it cannot be configured right now.',
      save: 'Save',
      saving: 'Saving…',
      saveFailed: 'The settings were not saved.',
      switchFailed: 'Not saved. Try again.',
    }

    // dsh's own look (its settings rows: --dsw-alias-* tokens, 14/12px type, hairline rules).
    const st = {
      page: { display: 'flex', flexDirection: 'column', width: '100%', gap: 24 },
      heading: { margin: 0, color: 'var(--dsw-alias-label-primary)', fontSize: 14, lineHeight: '20px', fontWeight: 500 },
      hint: { margin: '4px 0 0', color: 'var(--dsw-alias-label-secondary)', fontSize: 12, lineHeight: '18px' },
      row: { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 24, padding: '12px 0', borderBottom: '.5px solid var(--dsw-alias-border-l2)' },
      text: { minWidth: 0 },
      name: { color: 'var(--dsw-alias-label-primary)', fontSize: 14, lineHeight: '20px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
      path: { marginTop: 2, color: 'var(--dsw-alias-label-tertiary)', fontSize: 12, lineHeight: '18px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
      note: { padding: '12px 0', color: 'var(--dsw-alias-label-tertiary)', fontSize: 12, lineHeight: '18px' },
      error: { marginTop: 4, color: 'var(--dsw-alias-state-error-primary)', fontSize: 12, lineHeight: '18px' },
    }


    // The address and its 保存 on one line (the user, 2026-11-08): dsh's own field, its parts laid
    // in a grid with the button beside the input. By element only: dsh's class names are hashes
    // that differ from build to build (dsh web, DSH Desktop).
    const CSS = `
.remote-iridium-url{display:grid;grid-template-columns:minmax(0,1fr) auto;column-gap:8px;row-gap:6px;padding:12px 0}
.remote-iridium-url>div:first-child{display:contents}
.remote-iridium-url>div:first-child>*{grid-column:1/-1}
.remote-iridium-url>div:first-child>input{grid-column:1;grid-row:2;min-width:0}
.remote-iridium-url>button{grid-column:2;grid-row:2;height:34px;appearance:none;border:1px solid transparent;border-radius:var(--dsw-radius-md);padding:0 14px;font:inherit;font-size:13px;line-height:1.5;cursor:pointer;background:var(--dsw-alias-label-primary);color:var(--dsw-alias-bg-layer-3)}
.remote-iridium-url>button:disabled{opacity:.4;cursor:default}`

    function Connection(props) {
      const { t } = props
      const state = props.useIridiumForm((s) => s)
      // As dsh's SettingsForm: a draft left unsaved is dropped with the page.
      const discard = useRef(props.discard)
      discard.current = props.discard
      useEffect(() => () => discard.current(), [])
      if (!state.available) return jsx('p', { style: st.note, role: 'status', children: t('unavailable') })
      return jsxs('div', {
        children: [
          !state.writable && jsx('p', { style: st.note, role: 'status', children: t('readOnly') }),
          jsxs('div', {
            className: 'remote-iridium-url',
            children: [
              jsx(ui.SettingsValueField, {
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
              }),
              jsx('button', { type: 'button', disabled: !state.dirty || state.invalid || state.saving, onClick: props.save, children: state.saving ? t('saving') : t('save') }),
            ],
          }),
          state.failed && jsx('p', { style: st.error, role: 'status', children: t('saveFailed') }),
        ],
      })
    }

    /** A switch of the entry's, written at once: key, its default. */
    function Switch(props) {
      const { t, k, def } = props
      const config = props.useIridiumConfig((s) => s)
      const [busy, setBusy] = useState(false)
      const [failed, setFailed] = useState(false)
      const on = config.value?.[k] ?? def
      const flip = (next) => {
        setBusy(true)
        setFailed(false)
        Promise.resolve(props.setSwitch(k, next))
          .then((ok) => ok === false && setFailed(true), () => setFailed(true))
          .finally(() => setBusy(false))
      }
      return jsxs('div', {
        style: st.row,
        children: [
          jsxs('div', {
            style: st.text,
            children: [
              jsx('div', { style: st.name, children: t(props.label) }),
              failed && jsx('div', { style: st.error, role: 'alert', children: t('switchFailed') }),
            ],
          }),
          jsx(ui.Switch, { checked: on, label: t(props.label), disabled: !config.writable || busy, onChange: flip }),
        ],
      })
    }

    function Page(props) {
      return jsxs('div', {
        style: st.page,
        children: [
          jsxs('section', {
            children: [
              jsx('h3', { style: st.heading, children: props.t('connection') }),
              jsx(Switch, { ...props, k: 'local', def: true, label: 'local' }),
              jsx(Connection, props),
              jsx(Switch, { ...props, k: 'syncNewSessions', def: false, label: 'sync' }),
            ],
          }),
        ],
      })
    }

    /** Its mark in the settings' list: two links, as it joins this machine to an Iridium. */
    function Icon({ size }) {
      return jsxs('svg', {
        width: size,
        height: size,
        viewBox: '0 0 24 24',
        fill: 'none',
        stroke: 'currentColor',
        strokeWidth: 1.8,
        strokeLinecap: 'round',
        strokeLinejoin: 'round',
        'aria-hidden': true,
        children: [jsx('path', { d: 'M10 13a5 5 0 0 0 7.07 0l3-3a5 5 0 0 0-7.07-7.07l-1.5 1.5' }), jsx('path', { d: 'M14 11a5 5 0 0 0-7.07 0l-3 3a5 5 0 0 0 7.07 7.07l1.5-1.5' })],
      })
    }

    const inject = ['slots', 'locale', 'configForms']

    function apply(ctx) {
      ctx.effect(() => ctx.locale.register(NS, { zh, en }), 'remote-iridium: dictionaries')
      ctx.effect(() => {
        const tag = document.createElement('style')
        tag.dataset.pluginCss = 'remote-iridium'
        tag.textContent = CSS
        document.head.appendChild(tag)
        return () => tag.remove()
      }, 'remote-iridium: styles')
      const t = ctx.locale.bind(NS)
      const scope = ctx.configForms.get(ENTRY)
      const form = new ui.SettingsFormModel(scope, [ui.settingsTextField('url')])
      const store = form.bind(() => ({ ...form.shell(), url: form.field('url') }))
      ctx.effect(() => () => form.dispose(), 'remote-iridium: form')
      ctx.effect(
        () =>
          ctx.slots.inject('settings.section', () =>
            ctx.slots.register(
              {
                name: 'settings.section',
                id: SECTION,
                order: 50,
                label: () => t('title'),
                locale: NS,
                inject: () => ({ hooks: { iridiumForm: store, iridiumConfig: scope }, ...form.actions(), setSwitch: (k, on) => scope.set(k, on) }),
              },
              Page,
            ),
          ),
        'remote-iridium: page',
      )
      ctx.effect(
        () => ctx.slots.inject('settings.section.icon', () => ctx.slots.register({ name: 'settings.section.icon', id: SECTION }, Icon)),
        'remote-iridium: icon',
      )
    }

    const exports = { apply, inject, NS }
    Object.defineProperty(exports, Symbol.toStringTag, { value: 'Module' })
    return exports
  },
})
