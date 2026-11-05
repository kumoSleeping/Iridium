// Gate pages (login, join). UI only: the server makes every check.
'use strict';

// The theme saved by the web app wins over the system's; runs before the first paint.
try {
  const t = localStorage.getItem('theme');
  if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t;
} catch { /* storage unavailable */ }

const ZH = /^zh\b/i.test(navigator.language || '');
const S = {
  wait: ['正在检查', 'Checking'],
  signIn: ['用 Passkey 登录', 'Sign in with passkey'],
  setup: ['还没有 Passkey：在本机运行 ir invite', 'No passkey yet: run ir invite on the host'],
  namePh: ['给这台设备起个名字', 'Name this device'],
  create: ['创建 Passkey', 'Create passkey'],
  badLink: ['链接无效或已过期，请重新生成', 'This link is invalid or expired. Ask for a new one.'],
  name_taken: ['已被占用', 'Already taken'],
  name_reserved: ['这个名字保留给系统', 'Reserved name'],
  name_invalid: ['只能用文字、数字、空格和 - _ .', 'Letters, digits, spaces and - _ . only'],
  name_too_long: ['最多 12 个字', '12 characters at most'],
  waiting: ['等待 Passkey…', 'Waiting for passkey…'],
  verifying: ['正在验证…', 'Verifying…'],
  noSupport: ['这个浏览器不支持 Passkey', 'This browser does not support passkeys'],
  offline: ['连不上，请检查网络', "Can't connect. Check the network."],
  tooMany: ['尝试太多次，请 15 分钟后再试', 'Too many attempts. Try again in 15 minutes.'],
  failed: ['出错了，请再试一次', 'Something went wrong. Try again.'],
  loginFailed: ['这个 Passkey 不认识', 'This passkey is not recognized'],
  noPasskey: ['还没有注册 Passkey', 'No passkey registered yet'],
  refused: ['这台设备拒绝了，换个浏览器试试', 'This device refused. Try another browser.'],
  noStore: ['这台设备没有可用的 Passkey 存储：请设锁屏并开启密码管理器', 'No passkey storage on this device: set a screen lock and turn on a password manager'],
  cancelled: ['已取消，或超时', 'Cancelled, or timed out'],
  provider: ['系统 Passkey 服务出错', 'The system passkey service failed'],
  browser: ['浏览器拒绝了', 'The browser refused'],
};
const t = (k) => (S[k] || [k, k])[ZH ? 0 : 1];

const $ = (id) => document.getElementById(id);
const TOKEN_KEY = 'ir-join';

function b64uToBuf(s) {
  const b = atob(s.replace(/-/g, '+').replace(/_/g, '/').padEnd(Math.ceil(s.length / 4) * 4, '='));
  const u = new Uint8Array(b.length);
  for (let i = 0; i < b.length; i++) u[i] = b.charCodeAt(i);
  return u.buffer;
}

function bufToB64u(buf) {
  const u = new Uint8Array(buf);
  let s = '';
  for (let i = 0; i < u.length; i++) s += String.fromCharCode(u[i]);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function nextUrl() {
  const n = new URLSearchParams(location.search).get('next') || '/';
  return n.startsWith('/') && !n.startsWith('//') && !/[\\\x00-\x1f\x7f]/.test(n) && !/^\/_ir(\/|\?|$)/.test(n) ? n : '/';
}

async function api(path, body) {
  const r = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: body === undefined ? {} : { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
    cache: 'no-store',
  });
  let data = {};
  try { data = await r.json(); } catch { /* empty body */ }
  return { status: r.status, data };
}

function encodeCredential(c) {
  const r = c.response;
  const out = {
    id: c.id,
    rawId: bufToB64u(c.rawId),
    type: c.type,
    clientExtensionResults: c.getClientExtensionResults ? c.getClientExtensionResults() : {},
    response: { clientDataJSON: bufToB64u(r.clientDataJSON) },
  };
  if (c.authenticatorAttachment) out.authenticatorAttachment = c.authenticatorAttachment;
  if (r.attestationObject) {
    out.response.attestationObject = bufToB64u(r.attestationObject);
    out.response.transports = r.getTransports ? r.getTransports() : [];
  } else {
    out.response.authenticatorData = bufToB64u(r.authenticatorData);
    out.response.signature = bufToB64u(r.signature);
    if (r.userHandle) out.response.userHandle = bufToB64u(r.userHandle);
  }
  return out;
}

const decodeIds = (list) => (list || []).map((c) => ({ ...c, id: b64uToBuf(c.id) }));

function view(name) {
  for (const v of ['wait', 'login', 'join', 'bad']) $('v-' + v).hidden = v !== name;
  status('');
}

function status(msg, isError = false) {
  $('status').textContent = msg;
  $('status').classList.toggle('error', isError);
}

function requestError(r) {
  return r.status === 429 ? t('tooMany') : t('failed');
}

// A refusal faster than anyone could cancel: the device has no usable passkey provider.
const INSTANT_FAIL_MS = 1500;

function ceremonyError(err, startedAt) {
  const name = (err && err.name) || 'Error';
  if (name === 'InvalidStateError') return t('refused');
  if (name === 'NotAllowedError') return performance.now() - startedAt < INSTANT_FAIL_MS ? t('noStore') : t('cancelled');
  if (name === 'NotReadableError') return t('provider');
  return `${t('browser')} (${name})`;
}

/** Disable the buttons while fn runs and spin the one that started it. */
async function busy(btn, fn) {
  const buttons = [...document.querySelectorAll('button')];
  const was = buttons.map((b) => b.disabled);
  for (const b of buttons) b.disabled = true;
  btn.setAttribute('aria-busy', 'true');
  try {
    await fn();
  } catch {
    status(t('offline'), true);
  } finally {
    btn.removeAttribute('aria-busy');
    buttons.forEach((b, i) => { b.disabled = was[i]; });
  }
}

async function login() {
  status(t('waiting'));
  const opt = await api('/_ir/api/login/options', {});
  if (opt.status !== 200) {
    status(opt.data.error === 'no_passkey' ? t('noPasskey') : requestError(opt), true);
    return;
  }
  const o = opt.data.options;
  o.challenge = b64uToBuf(o.challenge);
  if (o.allowCredentials) o.allowCredentials = decodeIds(o.allowCredentials);
  let cred;
  const startedAt = performance.now();
  try {
    cred = await navigator.credentials.get({ publicKey: o });
  } catch (err) {
    status(ceremonyError(err, startedAt), true);
    return;
  }
  status(t('verifying'));
  const v = await api('/_ir/api/login/verify', { flow: opt.data.flow, response: encodeCredential(cred) });
  if (v.status === 200) location.replace(nextUrl());
  else status(v.status === 429 ? t('tooMany') : t('loginFailed'), true);
}

// --- join ---

let token = '';
let checkSeq = 0;
let checkTimer = 0;

function nameNote(code) {
  $('name-note').textContent = code ? t('name_' + code) : '';
  $('name').setAttribute('aria-invalid', code ? 'true' : 'false');
}

async function checkName() {
  const name = $('name').value;
  const seq = ++checkSeq;
  if (!name.trim()) {
    nameNote('');
    $('join').disabled = true;
    return;
  }
  const r = await api('/_ir/api/join/check', { token, name });
  if (seq !== checkSeq) return; // typed again meanwhile
  if (r.status === 403) return badLink();
  if (r.status !== 200) return status(requestError(r), true);
  nameNote(r.data.nameError || '');
  $('join').disabled = !!r.data.nameError;
}

function badLink() {
  try { sessionStorage.removeItem(TOKEN_KEY); } catch { /* ignore */ }
  view('bad');
}

async function join() {
  const name = $('name').value;
  status(t('waiting'));
  const opt = await api('/_ir/api/join/options', { token, name });
  if (opt.status === 403) return badLink();
  if (opt.status === 409) {
    nameNote(opt.data.nameError || '');
    status('');
    return;
  }
  if (opt.status !== 200) return status(requestError(opt), true);
  const o = opt.data.options;
  o.challenge = b64uToBuf(o.challenge);
  o.user = { ...o.user, id: b64uToBuf(o.user.id) };
  if (o.excludeCredentials) o.excludeCredentials = decodeIds(o.excludeCredentials);
  let cred;
  const startedAt = performance.now();
  try {
    cred = await navigator.credentials.create({ publicKey: o });
  } catch (err) {
    status(ceremonyError(err, startedAt), true);
    return;
  }
  status(t('verifying'));
  const v = await api('/_ir/api/join/verify', { flow: opt.data.flow, response: encodeCredential(cred) });
  if (v.status === 200) {
    try { sessionStorage.removeItem(TOKEN_KEY); } catch { /* ignore */ }
    location.replace('/');
  } else if (v.status === 403) badLink();
  else if (v.status === 409 && v.data.nameError) { nameNote(v.data.nameError); status(''); }
  else status(requestError(v), true);
}

async function initJoin() {
  // The token is in the fragment, which never reaches the server. Take it out of the
  // address bar (and history); keep it for this tab in case the page reloads.
  token = location.hash.slice(1);
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token);
    else token = sessionStorage.getItem(TOKEN_KEY) || '';
  } catch { /* storage unavailable */ }
  if (location.hash) history.replaceState(null, '', location.pathname);
  if (!token) return badLink();

  const r = await api('/_ir/api/join/check', { token });
  if (r.status === 403) return badLink();
  if (r.status !== 200) {
    view('join');
    return status(requestError(r), true);
  }
  view('join');
  const input = $('name');
  input.oninput = () => {
    clearTimeout(checkTimer);
    $('join').disabled = true;
    checkTimer = setTimeout(() => checkName().catch(() => status(t('offline'), true)), 250);
  };
  $('join-form').onsubmit = (e) => {
    e.preventDefault();
    if (!$('join').disabled) busy($('join'), join);
  };
  if (r.data.name) {
    input.value = r.data.name;
    $('join').disabled = false;
  }
  input.focus();
}

async function initLogin() {
  const s = (await api('/_ir/api/session')).data;
  if (s.authenticated) return location.replace(nextUrl());
  view('login');
  if (s.setup) {
    $('login').hidden = true;
    $('setup').hidden = false;
  }
  $('login').onclick = () => busy($('login'), login);
}

async function init() {
  document.documentElement.lang = ZH ? 'zh-CN' : 'en';
  for (const el of document.querySelectorAll('[data-t]')) el.textContent = t(el.dataset.t);
  for (const el of document.querySelectorAll('[data-tp]')) {
    el.placeholder = t(el.dataset.tp);
    el.setAttribute('aria-label', t(el.dataset.tp));
  }
  document.querySelector('.spinner').setAttribute('aria-label', t('wait'));

  if (!window.PublicKeyCredential) {
    view('bad');
    document.querySelector('#v-bad .note').textContent = t('noSupport');
    return;
  }
  try {
    if (location.pathname === '/_ir/join') await initJoin();
    else await initLogin();
  } catch {
    view('bad');
    document.querySelector('#v-bad .note').textContent = t('offline');
  }
}

document.addEventListener('DOMContentLoaded', init);
