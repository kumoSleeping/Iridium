// dsh-remote-iridium, browser half: a page of its own in dsh's settings (a settings.section, beside
// 通用 and 模型, not under 插件; the user, 2026-11-08), the same in dsh web and DSH Desktop, which
// share this settings shell. On it: the Iridium's address (dsh's own settings form over the
// entry's volatile `url`), and a switch for each of dsh's workspaces, whether the Iridium lists it
// (the entry's volatile `unsynced`, their folders; written at once), and one for each feature it
// offers the Iridium's page (the entry's volatile `unfeatured`, the ids switched off). Plain JS in
// the shape dsh's module loader takes; React and the form parts are the page's own.
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
      url: 'Iridium 地址',
      urlHint: '例如 https://ir.example.com。保存后在 Iridium 网页上批准；配对码和连接状态用 /iridium 查看。',
      overridden: '已覆盖',
      reset: '恢复默认',
      invalid: '请填写 http(s) 地址。',
      readOnly: '本部署的设置为只读。',
      unavailable: '该插件当前未加载，暂时无法配置。',
      save: '保存',
      saving: '保存中…',
      saveFailed: '未能保存设置。',
      workspaces: '同步到 Iridium 的工作区',
      workspacesHint: '关闭的工作区，其会话不在 Iridium 中列出，在 Iridium 新建会话时也不可选。',
      loading: '加载中…',
      ungrouped: '未分组',
      ungroupedHint: '不在任何工作区里的会话',
      switchFailed: '未能保存，请重试。',
      features: '提供给 Iridium 的附加功能',
      featuresHint: '关闭的功能不在 Iridium 网页上出现。',
      omdUltra: 'Ultra（OMD）',
      omdUltraHint: 'OMD 的协作模式，装有 OMD 时可用',
    }
    const en = {
      title: 'Iridium',
      connection: 'Connection',
      url: 'Iridium address',
      urlHint: 'For example https://ir.example.com. Once saved, allow the request on the Iridium page; /iridium shows the pairing code and the connection.',
      overridden: 'Overridden',
      reset: 'Reset to default',
      invalid: 'Enter an http(s) address.',
      readOnly: 'This deployment stores settings read-only.',
      unavailable: 'This plugin is not loaded, so it cannot be configured right now.',
      save: 'Save',
      saving: 'Saving…',
      saveFailed: 'The settings were not saved.',
      workspaces: 'Workspaces synced to Iridium',
      workspacesHint: 'Sessions in a workspace switched off are not listed in Iridium, and new sessions there cannot pick it.',
      loading: 'Loading…',
      ungrouped: 'Ungrouped',
      ungroupedHint: 'Sessions in no workspace',
      switchFailed: 'Not saved. Try again.',
      features: 'Extras offered to Iridium',
      featuresHint: 'A feature switched off does not show on the Iridium page.',
      omdUltra: 'Ultra (OMD)',
      omdUltraHint: "OMD's collaboration mode, with OMD installed",
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

    /** The folders kept from the Iridium, as the entry has them (a list; text from an earlier build). */
    const kept = (v) => (Array.isArray(v) ? v : String(v ?? '').split(/[,，;；\n]/)).map((x) => String(x).trim()).filter(Boolean)
    /** The same folder, as Windows and dsh may write it differently. */
    const same = (a, b) => a.replaceAll('\\', '/').replace(/\/+$/, '').toLowerCase() === b.replaceAll('\\', '/').replace(/\/+$/, '').toLowerCase()

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

    /** A switch for each of dsh's workspaces: on, the Iridium lists it. */
    function Workspaces(props) {
      const { t } = props
      const items = props.useIridiumWorkspaces((s) => s.items)
      const ready = props.useIridiumWorkspaces((s) => s.phase === 'ready')
      const config = props.useIridiumConfig((s) => s)
      const off = kept(config.value?.unsynced)
      const [busy, setBusy] = useState(null)
      const [failed, setFailed] = useState(null)
      // Kept by its folder, or by its name as an earlier build let it be typed (the Iridium takes both).
      const is = (p, w) => same(p, w.path) || (!!w.title && p.toLowerCase() === w.title.toLowerCase())
      // One write at a time; a failed one says so on its row.
      const write = (key, done) => {
        setBusy(key)
        setFailed(null)
        done()
          .then((ok) => ok || setFailed(key), () => setFailed(key))
          .finally(() => setBusy(null))
      }
      const flip = (w, on) => write(w.path, () => props.setUnsynced(on ? off.filter((p) => !is(p, w)) : [...off.filter((p) => !is(p, w)), w.path]))
      const row = (key, name, sub, on, onChange) =>
        jsxs(
          'div',
          {
            style: st.row,
            children: [
              jsxs('div', {
                style: st.text,
                children: [
                  jsx('div', { style: st.name, title: name, children: name }),
                  jsx('div', { style: st.path, title: sub, children: sub }),
                  failed === key && jsx('div', { style: st.error, role: 'alert', children: t('switchFailed') }),
                ],
              }),
              jsx(ui.Switch, { checked: on, label: name, disabled: !config.writable || busy !== null, onChange }),
            ],
          },
          key,
        )
      // DSH's 未分组 too: the sessions in none of the workspaces, many of them one-off chats, kept
      // from the Iridium by one switch (the user, 2026-11-08).
      const UNGROUPED = ':ungrouped' // no folder's path
      const rows = !ready
        ? [jsx('div', { style: st.note, role: 'status', children: t('loading') }, 'loading')]
        : [
            ...items.map((w) => row(w.path, w.title || w.path, w.path, !off.some((p) => is(p, w)), (next) => flip(w, next))),
            row(UNGROUPED, t('ungrouped'), t('ungroupedHint'), config.value?.ungrouped !== false, (next) => write(UNGROUPED, () => props.setUngrouped(next))),
          ]
      return jsxs('section', {
        children: [jsx('h3', { style: st.heading, children: t('workspaces') }), jsx('p', { style: st.hint, children: t('workspacesHint') }), ...rows],
      })
    }

    // The features this plugin offers (features.js): those it ships an adapter for. The registry
    // lives in the server half, which this page has no channel to; one switched off by another
    // plugin's id is listed too, by its id, so it can be switched on again.
    const FEATURES = [{ id: 'omd.ultra', name: 'omdUltra', hint: 'omdUltraHint' }]

    /** A switch for each feature: on, the Iridium's page shows it. */
    function Features(props) {
      const { t } = props
      const config = props.useIridiumConfig((s) => s)
      const off = kept(config.value?.unfeatured)
      const [busy, setBusy] = useState(null)
      const [failed, setFailed] = useState(null)
      const all = [...FEATURES.map((f) => ({ id: f.id, name: t(f.name), hint: t(f.hint) })), ...off.filter((id) => !FEATURES.some((f) => f.id === id)).map((id) => ({ id, name: id, hint: '' }))]
      const flip = (id, on) => {
        setBusy(id)
        setFailed(null)
        Promise.resolve(props.setUnfeatured(on ? off.filter((x) => x !== id) : [...off.filter((x) => x !== id), id]))
          .then((ok) => ok || setFailed(id), () => setFailed(id))
          .finally(() => setBusy(null))
      }
      return jsxs('section', {
        children: [
          jsx('h3', { style: st.heading, children: t('features') }),
          jsx('p', { style: st.hint, children: t('featuresHint') }),
          ...all.map((f) =>
            jsxs(
              'div',
              {
                style: st.row,
                children: [
                  jsxs('div', {
                    style: st.text,
                    children: [
                      jsx('div', { style: st.name, title: f.name, children: f.name }),
                      f.hint && jsx('div', { style: st.path, title: f.hint, children: f.hint }),
                      failed === f.id && jsx('div', { style: st.error, role: 'alert', children: t('switchFailed') }),
                    ],
                  }),
                  jsx(ui.Switch, { checked: !off.includes(f.id), label: f.name, disabled: !config.writable || busy !== null, onChange: (next) => flip(f.id, next) }),
                ],
              },
              f.id,
            ),
          ),
        ],
      })
    }

    function Page(props) {
      return jsxs('div', {
        style: st.page,
        children: [jsxs('section', { children: [jsx('h3', { style: st.heading, children: props.t('connection') }), jsx(Connection, props)] }), jsx(Workspaces, props), jsx(Features, props)],
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

    const inject = ['slots', 'locale', 'configForms', 'workspaces', 'sessions']

    /**
     * A session another dsh process made here (the Iridium's), filed under a workspace by this
     * plugin (dsh.attach): the workspace lists it at once, but this window's session list is
     * pulled only as its own sessions change, so it would show once the window is reloaded. When a
     * workspace names a session the list does not have, the list is pulled again, and looked at
     * again a moment later (a pull already under way may have begun before the session was
     * there); at most three times for each, so one gone from disk does not pull it over and over.
     */
    function followFiled(ctx) {
      const tries = new Map()
      let timer = null
      const check = (wait = 500) => {
        if (timer !== null) return
        timer = setTimeout(() => {
          timer = null
          const ws = ctx.workspaces.list.getSnapshot()
          const ss = ctx.sessions.list.getSnapshot()
          if (ws.phase !== 'ready' || ss.phase === 'pending') return
          const missing = ws.items.flatMap((w) => w.sessionIds ?? []).filter((id) => !ss.byId[id] && (tries.get(id) ?? 0) < 3)
          if (!missing.length) return
          for (const id of missing) tries.set(id, (tries.get(id) ?? 0) + 1)
          Promise.resolve(ctx.sessions.refresh())
            .catch(() => {})
            .finally(() => check(2000))
        }, wait)
      }
      const offs = [ctx.workspaces.list.subscribe(() => check()), ctx.sessions.list.subscribe(() => check())]
      check()
      return () => {
        clearTimeout(timer)
        for (const off of offs) if (typeof off === 'function') off()
      }
    }

    function apply(ctx) {
      ctx.effect(() => ctx.locale.register(NS, { zh, en }), 'remote-iridium: dictionaries')
      ctx.effect(() => followFiled(ctx), 'remote-iridium: sessions filed here')
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
                inject: () => ({ hooks: { iridiumForm: store, iridiumConfig: scope, iridiumWorkspaces: ctx.workspaces.list }, ...form.actions(), setUnsynced: (list) => scope.set('unsynced', list), setUngrouped: (on) => scope.set('ungrouped', on), setUnfeatured: (list) => scope.set('unfeatured', list) }),
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
