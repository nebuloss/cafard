// cafard admin UI.
//
// Plain JavaScript on /admin/api, no build step: what is in git is what is
// served (internal/httpapi/admin/ui.go). The page is one view at a time,
// chosen by the URL's hash; every change is sent to the API and the view is
// read again from it, so what is on screen is what the server holds.
//
// Nothing here is a style attribute or an inline script: the page's
// Content-Security-Policy refuses both. Widths that depend on data are set
// through element.style after rendering (sizeBars).
'use strict';

(() => {
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const ICONS = {
  people: '<circle cx="9" cy="8" r="3.2"/><path d="M3 19c.6-3.3 3-5 6-5s5.4 1.7 6 5"/><rect x="15" y="6" width="6" height="6" rx="1.5"/><path d="M15 17h6"/>',
  folder: '<path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z"/>',
  list: '<path d="M8 6h12M8 12h12M8 18h12"/><circle cx="4" cy="6" r="1"/><circle cx="4" cy="12" r="1"/><circle cx="4" cy="18" r="1"/>',
  disk: '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6"/><path d="M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/>',
  plus: '<path d="M12 5v14M5 12h14"/>', close: '<path d="M6 6l12 12M18 6L6 18"/>', chev: '<path d="M9 5l7 7-7 7"/>',
  search: '<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>', warn: '<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17v.5"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>', info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8v.5"/>',
  lock: '<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 018 0v3"/>',
};
const ic = (n) => `<svg class="ic" viewBox="0 0 24 24" aria-hidden="true">${ICONS[n]}</svg>`;
const LOGO = '<svg viewBox="0 0 32 32" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><ellipse cx="16" cy="19" rx="7" ry="9.5" fill="currentColor" fill-opacity=".18"/><path d="M16 10v18"/><path d="M13 9.5C11 6 8 4 5 4M19 9.5C21 6 24 4 27 4"/><path d="M9.5 16 5 14M9.5 21 4.5 22M22.5 16 27 14M22.5 21l5 1"/></svg>';

// ---------- time and sizes ----------
const ago = (iso) => {
  if (!iso) return 'never';
  const m = (Date.now() - new Date(iso)) / 60000;
  if (m < 1) return 'just now';
  if (m < 60) return Math.floor(m) + ' min ago';
  if (m < 1440) return Math.floor(m / 60) + ' h ago';
  return Math.floor(m / 1440) + ' days ago';
};
const until = (iso) => {
  const m = (new Date(iso) - Date.now()) / 60000;
  if (m <= 0) return 'expired';
  if (m < 60) return 'expires in ' + Math.ceil(m) + ' min';
  if (m < 1440) return 'expires in ' + Math.round(m / 60) + ' h';
  return 'expires in ' + Math.round(m / 1440) + ' days';
};
const when = (iso) => {
  const d = new Date(iso), now = new Date();
  const hm = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  if (d.toDateString() === now.toDateString()) return 'Today ' + hm;
  const y = new Date(now); y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return 'Yesterday ' + hm;
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) + ' ' + hm;
};
const size = (b) => b >= 1 << 30 ? (b / (1 << 30)).toFixed(1) + ' GB' : b >= 1 << 20 ? Math.round(b / (1 << 20)) + ' MB' : b >= 1024 ? Math.round(b / 1024) + ' KB' : b + ' B';

// ---------- API ----------
class Refused extends Error { constructor(status, code, message) { super(message); this.status = status; this.code = code; } }

async function api(method, path, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const resp = await fetch('/admin/api' + path, opt);
  if (resp.status === 204) return {};
  let data = {};
  try { data = await resp.json(); } catch (_) { /* an empty body */ }
  if (!resp.ok) {
    if (resp.status === 401 && path !== '/signin') { S.me = null; renderSignin('You were signed out. Sign in again.'); }
    throw new Refused(resp.status, data.error || 'error', data.message || 'cafard could not do that.');
  }
  return data;
}

// run does an action and shows a refusal as a toast: every refusal names
// what to do instead, so its message is the whole of the error handling.
async function run(fn, done) {
  try { const r = await fn(); if (done) toast(done); return r; }
  catch (e) { if (e.status !== 401) toast(e.message); return null; }
}

// ---------- state ----------
const S = { me: null, members: [], projects: [], audit: [], links: [], config: null, filter: 'all', q: '', auditFilter: 'all', open: null, confirm: null };

async function loadAll() {
  const [m, p] = await Promise.all([api('GET', '/members'), api('GET', '/projects')]);
  S.members = m.members; S.projects = p.projects;
  const v = view();
  if (v === 'audit') S.audit = (await api('GET', '/audit')).entries;
  if (v === 'storage') S.links = (await api('GET', '/links')).links;
  if (v === 'config' && !S.config) S.config = await api('GET', '/config');
}
const member = (name) => S.members.find((m) => m.name === name);
const project = (name) => S.projects.find((p) => p.name === name);

// ---------- shell ----------
const VIEWS = { members: 'Members', projects: 'Projects', audit: 'Audit log', storage: 'Storage', config: 'Configuration' };
const VICON = { members: 'people', projects: 'folder', audit: 'list', storage: 'disk', config: 'gear' };
const view = () => { const h = location.hash.slice(1); return VIEWS[h] ? h : 'members'; };

function renderShell() {
  $('#root').innerHTML = `<div class="shell">
    <header class="top"><div class="brand">${LOGO} cafard <small>admin</small></div>
      <span class="where" title="This UI is served only on admin-listen">${ic('lock')}Internal network only</span>
      <span class="grow"></span>
      <span class="me"><span>${esc(S.me.display_name || S.me.name)}</span><button data-act="signout">Sign out</button></span></header>
    <div class="body"><nav class="nav" id="nav" aria-label="Admin sections"></nav><main class="main" id="main"></main></div></div>`;
}

function renderNav() {
  const v = view();
  const n = { members: S.members.length, projects: S.projects.length };
  $('#nav').innerHTML = Object.keys(VIEWS).map((k) => `<a href="#${k}" ${k === v ? 'aria-current="page"' : ''}>${ic(VICON[k])}${VIEWS[k]}${n[k] ? `<span class="n">${n[k]}</span>` : ''}</a>`).join('');
}

function render() {
  if (!S.me) return;
  if (!$('#nav')) renderShell();
  renderNav();
  const v = view();
  const fn = { members: vMembers, projects: vProjects, audit: vAudit, storage: vStorage, config: vConfig }[v];
  $('#main').innerHTML = `<div class="page">${fn()}</div>`;
  sizeBars();
}

async function refresh() {
  await run(loadAll);
  render();
  if (S.open) reopen();
}

// sizeBars sets meter widths from data-w, which the page's policy does not
// let markup do with a style attribute.
function sizeBars(root = document) {
  $$('[data-w]', root).forEach((el) => { el.style.width = Math.min(100, Number(el.dataset.w)).toFixed(1) + '%'; });
}

// ---------- pieces ----------
const kindLabel = { person: 'Person', agent: 'Agent', device: 'Device' };
function avatar(m, cls = '') {
  const t = m.kind === 'person' ? (m.display_name || m.name)[0].toUpperCase() : m.name.slice(0, 2).toUpperCase();
  return `<span class="av ${m.kind === 'person' ? '' : m.kind} ${cls}" title="${esc(m.name)}">${esc(t)}</span>`;
}
const projAv = (name, cls = '') => `<span class="av proj ${cls}">${esc(name.slice(0, 2).toUpperCase())}</span>`;
const sw = (on, act, v, label) => `<button class="switch" role="switch" aria-checked="${on}" aria-label="${esc(label)}" data-act="${act}" data-v="${esc(v)}"></button>`;

function meter(p) {
  const pct = p.quota_bytes ? p.bytes / p.quota_bytes * 100 : 0;
  const warn = pct >= 80;
  return `<div class="meter ${warn ? 'warn' : ''}"><div class="mt"><span>${size(p.bytes)}</span><span>of ${size(p.quota_bytes)} · ${p.retention_days}-day retention</span></div>
    <div class="bar" role="img" aria-label="${Math.round(pct)}% of quota used"><i data-w="${pct}"></i></div>
    ${warn ? `<div class="note">${ic('warn')}${Math.round(pct)}% of quota: uploads stop at 100%</div>` : ''}</div>`;
}

function subOf(m) {
  if (m.kind === 'person') return '@' + m.name + (m.joined ? (m.signins ? ` · ${m.signins} sign-in${m.signins === 1 ? '' : 's'}` : '') : '');
  if (m.kind === 'agent') return m.description || 'No description: say what this machine can do.';
  return 'Sends only' + (m.description ? ' · ' + m.description : '');
}

function linkPill(m) {
  if (m.joined || !m.link) return '';
  if (m.link.state === 'waiting') return '<span class="pill warn">link waiting</span>';
  if (m.link.state === 'expired') return '<span class="pill err">link expired</span>';
  if (m.link.state === 'cancelled') return '<span class="pill muted">link cancelled</span>';
  return '';
}

// ---------- views ----------
function vMembers() {
  const q = S.q.toLowerCase();
  const cnt = (k) => S.members.filter((m) => k === 'all' || m.kind === k).length;
  const rows = S.members.filter((m) => (S.filter === 'all' || m.kind === S.filter) &&
    (!q || [m.name, m.display_name, m.description, ...m.projects].join(' ').toLowerCase().includes(q)));
  const stale = S.members.filter((m) => m.last_used && Date.now() - new Date(m.last_used) > 30 * 864e5);
  const lost = S.members.filter((m) => !m.joined && m.link && m.link.state === 'expired');
  const review = [...stale.map((m) => `${esc(m.name)} unused for ${Math.floor((Date.now() - new Date(m.last_used)) / 864e5)} days`),
    ...lost.map((m) => `the link for ${esc(m.name)} expired`)];
  const seg = [['all', 'All'], ['person', 'People'], ['agent', 'Agents'], ['device', 'Devices']]
    .map(([k, l]) => `<button aria-pressed="${S.filter === k}" data-act="filter" data-v="${k}">${l} ${cnt(k)}</button>`).join('');
  const list = rows.map((m) => {
    const tags = (m.kind !== 'person' ? `<span class="kind ${m.kind}">${m.kind}</span>` : '') + (m.admin ? '<span class="kind admin">admin</span>' : '') +
      linkPill(m) + (!m.enabled ? '<span class="pill muted">paused</span>' : '');
    const last = m.joined || m.kind === 'device' ? `<b>${ago(m.last_used)}</b>${esc(m.last_from || '')}` : '<b>Not joined</b>';
    const chips = m.projects.map((p) => `<span class="chip">${esc(p)}</span>`).join('') || '<span class="sub">No project</span>';
    return `<div class="irow ${m.enabled ? '' : 'off'}" role="button" tabindex="0" data-act="open" data-v="${esc(m.name)}">${avatar(m)}
      <span class="who"><span class="nm">${esc(m.display_name || m.name)}${tags}</span><span class="sub">${esc(subOf(m))}</span></span>
      <span class="chips">${chips}</span><span class="last">${last}</span>
      ${m.name === S.me.name ? '<span></span>' : sw(m.enabled, 'pause', m.name, (m.enabled ? 'Pause ' : 'Resume ') + m.name)}<span class="chev">${ic('chev')}</span></div>`;
  }).join('');
  return `<div class="page-head"><div class="t"><h1>People, agents and devices</h1><p>Everyone who can reach cafard. People sign in, agents connect with a token their install link gave them, devices only send. What each can see comes from its projects.</p></div><button class="btn filled" data-act="new">${ic('plus')}New</button></div>
    ${review.length ? `<div class="notice">${ic('warn')}<p><b>${review.length} to review.</b> ${review.join(' · ')}.</p></div>` : ''}
    <div class="toolbar"><label class="search">${ic('search')}<input id="q" type="search" placeholder="Search by name, description or project" value="${esc(S.q)}" autocomplete="off"></label><div class="seg-wrap"><div class="seg" role="group" aria-label="Show">${seg}</div></div></div>
    <div class="list">${list || '<div class="empty">Nobody matches.</div>'}</div>`;
}

function vProjects() {
  const rows = [...S.projects].sort((a, b) => (a.archived - b.archived) || a.name.localeCompare(b.name)).map((p) => {
    const stack = p.members.slice(0, 5).map((m) => avatar(m)).join('') + (p.members.length > 5 ? `<span class="more">+${p.members.length - 5}</span>` : '');
    return `<button class="prow ${p.archived ? 'archived' : ''}" data-act="proj" data-v="${esc(p.name)}">${projAv(p.name)}
      <span class="who"><span class="nm">${esc(p.name)}${p.archived ? '<span class="pill muted">archived · read-only</span>' : ''}</span>
      <span class="sub">${p.conversations} conversations · ${p.files} files · ${p.members.length} members</span></span>
      <span class="stack">${stack}</span>${meter(p)}<span class="chev">${ic('chev')}</span></button>`;
  }).join('');
  return `<div class="page-head"><div class="t"><h1>Projects</h1><p>A project decides who sees what: its people and agents read and write all its conversations, devices only send to it. People already in a project can add members from the chat too.</p></div><button class="btn filled" data-act="new-project">${ic('plus')}New project</button></div>
    <div class="list">${rows || '<div class="empty">No project yet. Make one, then put people and agents in it.</div>'}</div>`;
}

function auditKind(e) {
  if (/signed (in|out)|sign-in/.test(e.action)) return 'signins';
  if (/project/.test(e.action)) return 'projects';
  return 'access';
}

function vAudit() {
  const f = S.auditFilter;
  const seg = [['all', 'All'], ['access', 'Access'], ['projects', 'Projects'], ['signins', 'Sign-ins']]
    .map(([k, l]) => `<button aria-pressed="${f === k}" data-act="afilter" data-v="${k}">${l}</button>`).join('');
  const rows = S.audit.filter((e) => f === 'all' || auditKind(e) === f)
    .map((e) => `<tr><td class="time">${when(e.at)}</td><td><b>${esc(e.actor)}</b> ${esc(e.action)} <b>${esc(e.target)}</b></td><td><span class="mono">${esc(e.origin)}</span></td></tr>`).join('');
  return `<div class="page-head"><div class="t"><h1>Audit log</h1><p>Every change to who can reach what, from this UI, from the chat and from cafard admin on the box: who did it, when, and from where.</p></div></div>
    <div class="toolbar"><div class="seg-wrap"><div class="seg" role="group" aria-label="Show">${seg}</div></div></div>
    <div class="tbl-wrap"><table><thead><tr><th>When</th><th>What</th><th>From</th></tr></thead><tbody>${rows || '<tr><td colspan="3" class="empty">Nothing yet.</td></tr>'}</tbody></table></div>`;
}

function vStorage() {
  const total = S.projects.reduce((s, p) => s + p.bytes, 0);
  const files = S.projects.reduce((s, p) => s + p.files, 0);
  const waiting = S.links.filter((l) => l.state === 'waiting');
  const links = waiting.map((l) => `<div class="line"><div class="m"><b>${esc(l.member)}</b><span>${l.kind === 'agent' ? 'install link' : l.kind === 'password' ? 'password link' : 'join link'} · made by ${esc(l.created_by)} ${ago(l.created_at)} · ${until(l.expires_at)}</span></div><button class="btn sm danger-text" data-act="cancel-link" data-v="${l.id}">Cancel</button></div>`).join('')
    || '<p>No link is waiting to be used.</p>';
  return `<div class="page-head"><div class="t"><h1>Storage</h1><p>Files are kept per project for its retention; messages stay when files expire.</p></div></div>
    <div class="grid2">
      <div class="card"><h2>Files</h2><div class="big">${size(total)} <small>in ${files} files</small></div><p>Counted per project: the same file sent to two projects counts in each, though it is stored once.</p></div>
      <div class="card"><h2>Links waiting</h2>${links}</div>
    </div>
    <h2 class="h2-gap">By project</h2>
    <div class="list">${S.projects.map((p) => `<button class="prow storage" data-act="proj" data-v="${esc(p.name)}">${projAv(p.name)}<span class="who"><span class="nm">${esc(p.name)}${p.archived ? '<span class="pill muted">archived</span>' : ''}</span><span class="sub">${p.files} files</span></span>${meter(p)}<span class="chev">${ic('chev')}</span></button>`).join('') || '<div class="empty">No project yet.</div>'}</div>`;
}

function vConfig() {
  const c = S.config;
  if (!c) return '<p class="loading">Loading…</p>';
  return `<div class="page-head"><div class="t"><h1>Configuration</h1><p>Where cafard listens and what it allows. These come from <code>/etc/cafard/config.yaml</code> and change only there, then a restart.</p></div></div>
    <div class="grid2">
      <div class="card"><h2>Listeners</h2>
        <div class="listen"><code>listen ${esc(c.listen)}</code><span>Published: the chat app, /api/v1, join links. People and agents reach it at <code>${esc(c.public_url)}</code>.</span></div>
        <div class="listen"><code>admin-listen ${esc(c.admin_listen)}</code><span>This UI. The public listener answers 404 to every /admin path.</span></div></div>
      <div class="card"><h2>Limits</h2><dl class="kv">
        <dt>Largest upload</dt><dd>${size(c.max_upload)}</dd>
        <dt>Links wait, by default</dt><dd>${c.link_hours} hours</dd>
        <dt>Links wait, at most</dt><dd>${c.link_max_hours / 24} days</dd>
        <dt>An admin sign-in lasts</dt><dd>${c.admin_signin_hours} hours from its last use</dd>
        <dt>A chat sign-in lasts</dt><dd>${c.chat_signin_days} days from its last use</dd>
        <dt>State directory</dt><dd><code>${esc(c.state_dir)}</code></dd>
        <dt>Build</dt><dd><code>${esc(c.version)}</code></dd></dl></div>
    </div>`;
}

// ---------- drawer & modal ----------
const drawer = (html) => { $('#overlay').innerHTML = `<div class="scrim right" data-scrim><aside class="drawer" role="dialog" aria-modal="true">${html}</aside></div>`; sizeBars($('#overlay')); };
const modal = (html) => { $('#overlay').innerHTML = `<div class="scrim center" data-scrim><div class="modal" role="dialog" aria-modal="true">${html}</div></div>`; };
const modal2 = (html) => { $('#overlay2').innerHTML = `<div class="scrim center" data-scrim2><div class="modal" role="dialog" aria-modal="true">${html}</div></div>`; };
function close() { $('#overlay').innerHTML = ''; S.open = null; S.confirm = null; stopPoll(); }
function close2() { $('#overlay2').innerHTML = ''; PK = null; }
function reopen() { if (!S.open) return; S.open.type === 'member' ? openMember(S.open.name) : openProject(S.open.name); }

async function openMember(name) {
  const m = member(name);
  if (!m) return close();
  S.open = { type: 'member', name };
  const kindTxt = m.kind === 'person' ? (m.admin ? 'Person · admin' : 'Person') : m.kind === 'agent' ? 'Agent · connects with its token' : 'Device · sends only';
  const me = m.name === S.me.name;
  let h = `<div class="d-head">${avatar(m)}<div class="t"><h2>${esc(m.display_name || m.name)}${!m.enabled ? '<span class="pill muted">paused</span>' : ''}</h2><p>${kindTxt} · @${esc(m.name)}</p></div><button class="icon-btn" data-act="close" aria-label="Close">${ic('close')}</button></div><div class="d-body">`;
  if (m.kind !== 'device' && !m.joined) {
    const st = m.link ? m.link.state : 'none';
    const msg = st === 'waiting' ? `The ${m.kind === 'agent' ? 'install' : 'join'} link is waiting to be used · ${until(m.link.expires_at)}.` : st === 'expired' ? 'The link expired before it was used.' : st === 'cancelled' ? 'The link was cancelled.' : 'No link yet.';
    h += `<div class="sec"><div class="status">${ic('info')}${msg}</div><div class="row"><button class="btn filled sm" data-act="relink" data-v="${esc(m.name)}">Make a new ${m.kind === 'agent' ? 'install' : 'join'} link</button></div></div>`;
  }
  if (m.kind !== 'person') {
    h += `<div class="sec"><h3>${m.kind === 'agent' ? 'What this machine can do' : 'Note'}</h3><div class="field"><textarea class="input" id="desc" aria-label="Description">${esc(m.description || '')}</textarea>${m.kind === 'agent' ? '<span class="help">Agents read this, so they know whom to ask: "serial console of the be98 test unit; can flash it".</span>' : ''}</div><div class="row"><button class="btn tonal sm" data-act="save-desc" data-v="${esc(m.name)}">Save</button></div></div>`;
  } else {
    h += `<div class="sec"><h3>Account</h3><div class="line"><div class="m"><b>Admin</b><span>Can sign in here and change everything on this page.</span></div>${me ? '<span class="help">you</span>' : sw(!!m.admin, 'admin', m.name, 'Admin')}</div></div>`;
  }
  h += `<div class="sec"><h3>Projects</h3><div class="chips">${m.projects.map((p) => `<span class="chip">${esc(p)}<button data-act="rm-proj" data-v="${esc(m.name)}" data-p="${esc(p)}" aria-label="Remove from ${esc(p)}">${ic('close')}</button></span>`).join('') || '<span class="help">In no project: it can reach nothing.</span>'}</div><div class="row"><button class="btn tonal sm" data-act="manage-projects" data-v="${esc(m.name)}">Add or remove projects</button></div></div>`;
  if (m.kind === 'person' && m.joined) {
    h += `<div class="sec"><h3>Sign-ins</h3><div id="signins"><p class="help">Loading…</p></div><div class="row"><button class="btn tonal sm" data-act="relink" data-v="${esc(m.name)}">New password link</button></div><span class="help">The link sets a new password and ends every sign-in. You never see the password.</span></div>`;
  }
  if (m.kind !== 'person') {
    h += `<div class="sec"><h3>Token</h3><dl class="kv"><dt>Last used</dt><dd>${m.joined ? ago(m.last_used) : 'never'}</dd><dt>From</dt><dd class="mono">${esc(m.last_from || '-')}</dd></dl>
      <div class="row">${m.kind === 'agent' ? `<button class="btn tonal sm" data-act="relink" data-v="${esc(m.name)}">New install link</button>` : `<button class="btn tonal sm" data-act="regen" data-v="${esc(m.name)}">Regenerate token</button>`}</div>
      <span class="help">${m.kind === 'agent' ? "Running the new link on the machine replaces the token; the old one stops working then. An agent's token is never shown." : 'The current token stops working at once.'}</span></div>`;
  }
  if (!me) h += `<div class="sec"><div class="line"><div class="m"><b>Paused</b><span>Stops working until switched back. Nothing is lost.</span></div>${sw(!m.enabled, 'pause', m.name, 'Paused')}</div></div>`;
  if (!me) {
    h += `<div class="sec"><h3>Remove</h3>${S.confirm === m.name ? `<div class="confirm"><p><b>Remove ${esc(m.name)} from cafard?</b></p><ul>
      <li>${m.kind === 'person' ? 'Every sign-in ends now.' : 'Its token stops working on the next request.'}</li><li>It leaves its ${m.projects.length} project${m.projects.length === 1 ? '' : 's'}.</li><li>Its messages stay, shown as “${esc(m.name)} (removed)”, and the name stays taken.</li></ul>
      <div class="row"><button class="btn danger sm" data-act="delete" data-v="${esc(m.name)}">Remove ${esc(m.name)}</button><button class="btn tonal sm" data-act="no-delete">Cancel</button></div></div>`
      : `<div class="row"><button class="btn danger-text sm" data-act="ask-delete" data-v="${esc(m.name)}">Remove ${esc(m.name)}…</button></div>`}</div>`;
  }
  drawer(h + '</div>');
  if (m.kind === 'person' && m.joined) {
    const r = await run(() => api('GET', `/members/${encodeURIComponent(m.name)}/signins`));
    const box = $('#signins');
    if (r && box) {
      box.innerHTML = r.signins.map((s) => `<div class="line"><div class="m"><b>${esc(s.what)}${s.current ? ' · this one' : ''}</b><span>${s.surface === 'admin' ? 'admin UI' : 'chat app'} · ${esc(s.from)} · ${ago(s.last_used)}</span></div>${s.current ? '' : `<button class="btn sm outline" data-act="signout-one" data-v="${esc(m.name)}" data-i="${s.id}">Sign out</button>`}</div>`).join('')
        || '<p class="help">Not signed in anywhere.</p>';
    }
  }
}

function openProject(name) {
  const p = project(name);
  if (!p) return close();
  S.open = { type: 'project', name };
  const cnt = (k) => p.members.filter((m) => m.kind === k).length;
  const opt = (v, cur, l) => `<option value="${v}" ${String(v) === String(cur) ? 'selected' : ''}>${l}</option>`;
  let h = `<div class="d-head">${projAv(p.name)}<div class="t"><h2>${esc(p.name)}${p.archived ? '<span class="pill muted">archived · read-only</span>' : ''}</h2><p>${p.conversations} conversations · ${p.files} files</p></div><button class="icon-btn" data-act="close" aria-label="Close">${ic('close')}</button></div><div class="d-body">`;
  h += `<div class="sec"><h3>Members · ${cnt('person')} people, ${cnt('agent')} agents, ${cnt('device')} devices</h3>${p.members.slice(0, 6).map((m) => `<div class="line">${avatar(m, 'sm')}<div class="m"><b>${esc(m.display_name || m.name)}</b><span>${m.kind === 'person' ? 'Person' : m.kind === 'agent' ? 'Agent · reads and writes' : 'Device · sends only'}</span></div></div>`).join('') || '<span class="help">Nobody yet.</span>'}${p.members.length > 6 ? `<span class="more-line">and ${p.members.length - 6} more</span>` : ''}
    ${p.archived ? '<span class="help">Archived: members cannot change until it is unarchived.</span>' : `<div class="row"><button class="btn tonal sm" data-act="manage-members" data-v="${esc(p.name)}">Add or remove members</button></div>`}</div>`;
  const quotas = [1 << 30, 2 << 30, 5 << 30, 10 << 30, 20 << 30, 50 << 30];
  if (!quotas.includes(p.quota_bytes)) quotas.push(p.quota_bytes);
  h += `<div class="sec"><h3>Files</h3>${meter(p)}
    <div class="field"><label for="pRet">Retention</label><select class="input" id="pRet" data-v="${esc(p.name)}">${[7, 14, 30, 60, 90, 365].concat([7, 14, 30, 60, 90, 365].includes(p.retention_days) ? [] : [p.retention_days]).map((d) => opt(d, p.retention_days, d + ' days')).join('')}</select><span class="help">Files older than this expire; messages stay.</span></div>
    <div class="field"><label for="pQuota">Quota</label><select class="input" id="pQuota" data-v="${esc(p.name)}">${quotas.sort((a, b) => a - b).map((b) => opt(b, p.quota_bytes, size(b))).join('')}</select></div></div>`;
  h += `<div class="sec"><h3>${p.archived ? 'Archived' : 'Archive'}</h3><p class="help">${p.archived ? 'Everything is readable and nothing can be posted.' : 'Keeps everything readable and stops all posting. You can undo it.'}</p><div class="row"><button class="btn tonal sm" data-act="archive" data-v="${esc(p.name)}">${p.archived ? 'Unarchive' : 'Archive project'}</button></div></div>`;
  h += `<div class="sec"><h3>Delete</h3>${S.confirm === p.name ? `<div class="confirm"><p><b>This cannot be undone.</b> Deleting ${esc(p.name)} removes, for every member:</p><ul><li>${p.conversations} conversations and their messages</li><li>${p.files} files, ${size(p.bytes)}</li><li>${p.members.length} memberships</li></ul>
    <div class="field"><label for="pName">Type <b>${esc(p.name)}</b> to confirm</label><input class="input" id="pName" autocomplete="off"></div>
    <div class="row"><button class="btn danger sm" id="pDel" data-act="p-delete" data-v="${esc(p.name)}" disabled>Delete ${esc(p.name)}</button><button class="btn tonal sm" data-act="no-delete">Cancel</button></div></div>`
    : `<div class="row"><button class="btn danger-text sm" data-act="ask-delete" data-v="${esc(p.name)}">Delete project…</button></div>`}</div>`;
  drawer(h + '</div>');
  const nm = $('#pName');
  if (nm) nm.addEventListener('input', () => { $('#pDel').disabled = nm.value.trim() !== p.name; });
}

// ---------- picker: search, kind filter, checklist ----------
let PK = null;
function memberPicker(sel, initial, inline) {
  PK = { what: 'members', kinds: true, kind: 'all', q: '', sel, initial, inline, placeholder: 'Search by name or what it does',
    items: S.members.map((m) => ({ id: m.name, label: m.display_name || m.name, kind: m.kind, av: avatar(m, 'sm'),
      text: [m.name, m.display_name, m.description].join(' ').toLowerCase(),
      sub: m.kind === 'person' ? '@' + m.name + (m.projects.length ? ' · ' + m.projects.join(', ') : '') : m.kind === 'agent' ? (m.description || 'agent') : 'Sends only' + (m.description ? ' · ' + m.description : '') })) };
}
function projectPicker(sel, initial, inline) {
  PK = { what: 'projects', kinds: false, kind: 'all', q: '', sel, initial, inline, placeholder: 'Search projects',
    items: S.projects.map((p) => ({ id: p.name, label: p.name, av: projAv(p.name, 'sm'), text: p.name, disabled: p.archived && !initial.has(p.name),
      sub: p.archived ? 'Archived, read-only: nobody can be added' : `${p.conversations} conversations · ${p.members.length} members` })) };
}
function pickerHtml() {
  const seg = PK.kinds ? `<div class="seg-wrap"><div class="seg" role="group" aria-label="Show">${[['all', 'All'], ['person', 'People'], ['agent', 'Agents'], ['device', 'Devices']].map(([k, l]) => `<button type="button" aria-pressed="${PK.kind === k}" data-act="pk-kind" data-v="${k}">${l}</button>`).join('')}</div></div>` : '';
  return `<div class="picker"><div class="pk-tools"><label class="search">${ic('search')}<input id="pkQ" type="search" placeholder="${PK.placeholder}" autocomplete="off" aria-label="${PK.placeholder}"></label>${seg}</div><div class="pk-list" id="pkList" role="listbox" aria-multiselectable="true"></div><div class="pk-sum" id="pkSum" aria-live="polite"></div></div>`;
}
const pkChanges = () => ({ add: [...PK.sel].filter((k) => !PK.initial.has(k)), rm: [...PK.initial].filter((k) => !PK.sel.has(k)) });
function pkRender() {
  const q = PK.q.trim().toLowerCase();
  // Selected first, then by name, fixed when the picker opens: re-sorting on
  // every tick would move the row out from under the pointer.
  if (!PK.order) {
    const o = [...PK.items].sort((a, b) => (PK.sel.has(b.id) - PK.sel.has(a.id)) || a.label.localeCompare(b.label));
    PK.order = new Map(o.map((it, i) => [it.id, i]));
  }
  const rows = PK.items.filter((it) => (PK.kind === 'all' || it.kind === PK.kind) && (!q || it.text.includes(q))).sort((a, b) => PK.order.get(a.id) - PK.order.get(b.id));
  $('#pkList').innerHTML = rows.map((it) => {
    const on = PK.sel.has(it.id), was = PK.initial.has(it.id);
    const tag = PK.inline ? '' : on && !was ? '<span class="pill ok">will be added</span>' : !on && was ? '<span class="pill err">will be removed</span>' : was ? '<span class="pill muted">member</span>' : '';
    return `<button type="button" class="pk-row" role="option" aria-selected="${on}" data-act="pk-toggle" data-v="${esc(it.id)}" ${it.disabled ? 'disabled' : ''}><span class="box">${on ? ic('check') : ''}</span>${it.av}<span class="m"><b>${esc(it.label)}</b>${it.kind && it.kind !== 'person' ? `<span class="kind ${it.kind}">${it.kind}</span>` : ''}<span class="s">${esc(it.sub)}</span></span>${tag}</button>`;
  }).join('') || `<div class="empty">Nothing matches “${esc(PK.q)}”.</div>`;
  const { add, rm } = pkChanges();
  const shown = rows.length < PK.items.length ? `Showing ${rows.length} of ${PK.items.length}. ` : '';
  $('#pkSum').innerHTML = shown + (PK.inline ? `<b>${PK.sel.size} selected.</b>` : add.length || rm.length ? `<b>${add.length} to add, ${rm.length} to remove.</b>` : 'No change yet. Tick to add, untick to remove.');
  const save = $('#pkSave');
  if (save) { save.disabled = !add.length && !rm.length; save.textContent = save.disabled ? 'Save' : `Save · ${add.length ? '+' + add.length : ''}${add.length && rm.length ? ' ' : ''}${rm.length ? '−' + rm.length : ''}`; }
}
function manageMembers(name) {
  const p = project(name);
  const cur = new Set(p.members.map((m) => m.name));
  memberPicker(new Set(cur), cur, false); PK.target = name;
  modal2(`<div class="m-head"><div class="t"><h2>Members of ${esc(name)}</h2><p>Tick to add, untick to remove. People and agents see every conversation in ${esc(name)}; devices only send to it.</p></div><button class="icon-btn" data-act="close2" aria-label="Close">${ic('close')}</button></div><div class="m-body">${pickerHtml()}</div><div class="m-foot"><button class="btn tonal" data-act="close2">Cancel</button><button class="btn filled" id="pkSave" data-act="pk-save" disabled>Save</button></div>`);
  pkRender(); $('#pkQ').focus();
}
function manageProjects(name) {
  const m = member(name);
  const cur = new Set(m.projects);
  projectPicker(new Set(cur), cur, false); PK.target = name;
  modal2(`<div class="m-head"><div class="t"><h2>Projects of ${esc(m.display_name || m.name)}</h2><p>${m.kind === 'device' ? 'It can send to the ticked projects, and read nothing.' : 'It sees every conversation in the ticked projects.'}</p></div><button class="icon-btn" data-act="close2" aria-label="Close">${ic('close')}</button></div><div class="m-body">${pickerHtml()}</div><div class="m-foot"><button class="btn tonal" data-act="close2">Cancel</button><button class="btn filled" id="pkSave" data-act="pk-save" disabled>Save</button></div>`);
  pkRender(); $('#pkQ').focus();
}
async function pkSave() {
  const { add, rm } = pkChanges();
  const all = [...PK.sel];
  const path = PK.what === 'members' ? `/projects/${encodeURIComponent(PK.target)}` : `/members/${encodeURIComponent(PK.target)}`;
  const body = PK.what === 'members' ? { members: all } : { projects: all };
  close2();
  await run(() => api('PATCH', path, body), `Saved: ${add.length} added, ${rm.length} removed.`);
  await refresh();
}

// ---------- creating ----------
let NEW = null, POLL = null;
const stopPoll = () => { if (POLL) { clearInterval(POLL); POLL = null; } };
const DESC = { person: 'Signs in to the chat app, and here if made an admin.', agent: 'A machine whose MCP clients (Claude Code, Codex…) read and write.', device: 'A script or board that only sends, with curl.' };
const steps = (n) => `<div class="steps">${[1, 2, 3].map((i) => `<i class="${i <= n ? 'on' : ''}"></i>`).join('')}</div>`;

function newMember() {
  NEW = { kind: null };
  modal(`<div class="m-head"><div class="t"><h2>New</h2><p>Who is it?</p>${steps(1)}</div><button class="icon-btn" data-act="close" aria-label="Close">${ic('close')}</button></div>
    <div class="m-body"><div class="kinds">${['person', 'agent', 'device'].map((k) => `<button class="kind-card" data-act="pick-kind" data-v="${k}">${avatar({ kind: k, name: k === 'person' ? 'P' : k })}<b>${kindLabel[k]}</b><span>${DESC[k]}</span></button>`).join('')}</div>
    <p class="help">One agent per machine, not per project: the machine is what holds the token, and what others ask for.</p></div>`);
}

function newMemberForm() {
  const k = NEW.kind;
  projectPicker(new Set(), new Set(), true);
  modal(`<div class="m-head"><div class="t"><h2>New ${k}</h2><p>${DESC[k]}</p>${steps(2)}</div><button class="icon-btn" data-act="close" aria-label="Close">${ic('close')}</button></div>
    <form class="m-body" id="newForm" novalidate>
      ${k === 'person'
        ? `<div class="field"><label for="nDisplay">Name</label><input class="input" id="nDisplay" placeholder="Camille Durand" autocomplete="off"></div>
           <div class="field"><label for="nName">Username</label><input class="input mono" id="nName" placeholder="camille" autocomplete="off"><span class="help">What they type to sign in, and what @ finds them by. They choose their own password.</span></div>`
        : `<div class="field"><label for="nName">Name</label><input class="input mono" id="nName" placeholder="${k === 'agent' ? 'lab-bench' : 'be98-dut'}" autocomplete="off"><span class="help">${k === 'agent' ? 'The machine’s name: people will write @name to call it.' : 'Shown as the sender of what it posts.'}</span></div>
           <div class="field"><label for="nDesc">${k === 'agent' ? 'What this machine can do' : 'Note'}</label><textarea class="input" id="nDesc" placeholder="${k === 'agent' ? 'Serial console of the be98 test unit; can flash and power-cycle it.' : 'The be98 test unit, through curl.'}"></textarea></div>`}
      <span class="ferr" id="nErr" hidden></span>
      <div class="field"><span class="lbl">Projects</span><span class="help">${k === 'device' ? 'It can send to these, and read nothing.' : 'It sees every conversation in these. You can change this later.'}</span>${pickerHtml()}</div>
      ${k !== 'device' ? `<div class="field"><label for="nExp">The link waits</label><select class="input" id="nExp"><option value="1">1 hour</option><option value="24" selected>24 hours</option><option value="168">7 days</option></select></div>` : ''}
    </form>
    <div class="m-foot"><button class="btn tonal" data-act="new">Back</button><button class="btn filled" data-act="new-create">Create</button></div>`);
  pkRender();
  $('#nName').focus();
}

async function createMember() {
  const k = NEW.kind;
  const name = $('#nName').value.trim().toLowerCase();
  const body = { kind: k, name, projects: [...PK.sel] };
  if ($('#nDisplay')) body.display_name = $('#nDisplay').value.trim();
  if ($('#nDesc')) body.description = $('#nDesc').value.trim();
  if ($('#nExp')) body.link_hours = Number($('#nExp').value);
  const err = $('#nErr');
  try {
    const r = await api('POST', '/members', body);
    NEW.created = r.member;
    await run(loadAll); render();
    giveScreen(r.member, r.link, r.token);
  } catch (e) {
    if (e.status === 401) return;
    err.hidden = false; err.textContent = e.message; $('#nName').classList.add('err');
  }
}

// giveScreen is the last step of every creation: what to hand over.
function giveScreen(m, link, token) {
  const who = esc(m.display_name || m.name);
  let body, title, sub, foot;
  if (token) {
    title = `Give this to ${who}`; sub = 'Put the token in the script or the board’s configuration.';
    const url = (S.config && S.config.public_url) || location.origin.replace(/:\d+$/, '');
    body = `<div class="once">${ic('warn')}<span>This is the only time the token is shown. cafard keeps only a hash of it.</span></div>
      <div class="field token-box"><span class="lbl">Token</span><div class="copybox"><code>${esc(token)}</code><button data-act="copy" data-v="${esc(token)}">Copy</button></div></div>
      <div class="field"><span class="lbl">Send a file</span><pre class="snip">curl -T boot.log -H "Authorization: Bearer ${esc(token)}" \\\n  ${esc(url)}/api/v1/send/${esc((PK && [...PK.sel][0]) || '<project>')}/&lt;conversation&gt;/</pre></div>`;
    foot = '<button class="btn filled" data-act="done">I saved the token</button>';
  } else {
    title = m.kind === 'agent' ? `Connect ${who}` : `Send this to ${who}`;
    sub = 'A one-time link. Never paste it in a conversation: everyone there, agents included, would see it.';
    const right = m.kind === 'agent'
      ? `<div class="field"><span class="lbl">Run this on ${esc(m.name)}, as the user its agents run as</span><div class="copybox"><code>${esc(link.command)}</code><button data-act="copy" data-v="${esc(link.command)}">Copy</button></div></div>
         <ul class="does"><li>exchanges the one-time code for ${esc(m.name)}’s token</li><li>keeps it in ~/.config/cafard/token, readable by that user only</li><li>never prints the token</li></ul>`
      : `<div class="field"><span class="lbl">${link.kind === 'password' ? 'Password link' : 'Join link'}</span><div class="copybox"><code>${esc(link.url)}</code><button data-act="copy" data-v="${esc(link.url)}">Copy</button></div></div>
         <p class="help">${who} scans the code with their phone, chooses a password, and can sign in.${link.kind === 'password' ? ' Every sign-in they have now ends when they use it.' : ''}</p>`;
    body = `<div class="give"><div class="qr" id="qrBox">${link.qr ? `<img src="${link.qr}" alt="QR code of the link">` : ''}</div><div class="give-text">${right}</div></div>
      <div class="status" id="joinStatus"><span class="dot"></span>Waiting to be used · ${until(link.expires_at)}</div>`;
    foot = '<button class="btn filled" data-act="done">Done</button>';
    watchLink(m.name, link.id);
  }
  modal(`<div class="m-head"><div class="t"><h2>${title}</h2><p>${sub}</p>${steps(3)}</div><button class="icon-btn" data-act="close" aria-label="Close">${ic('close')}</button></div>
    <div class="m-body">${body}</div><div class="m-foot">${foot}</div>`);
}

// watchLink shows the link turn from waiting to used, from the server.
function watchLink(name, id) {
  stopPoll();
  POLL = setInterval(async () => {
    let r;
    try { r = await api('GET', '/members'); } catch (_) { return; }
    const m = r.members.find((x) => x.name === name);
    const st = $('#joinStatus');
    if (!st) return stopPoll();
    if (m && m.link && m.link.id === id && m.link.state !== 'waiting') {
      stopPoll();
      S.members = r.members; render();
      if (m.link.state === 'used') {
        st.className = 'status used';
        st.innerHTML = `<span class="dot"></span>Used by ${esc(m.name)} ${ago(m.link.used_at)} from ${esc(m.link.used_from)}. The link no longer works.`;
        const q = $('#qrBox'); if (q) q.classList.add('used');
      } else {
        st.innerHTML = `${ic('warn')}The link is ${esc(m.link.state)}.`;
      }
    }
  }, 3000);
}

async function relink(name) {
  const r = await run(() => api('POST', `/members/${encodeURIComponent(name)}/link`, { hours: 24 }));
  if (!r) return;
  await run(loadAll); render();
  giveScreen(member(name) || { name, kind: r.link.kind === 'agent' ? 'agent' : 'person' }, r.link);
}

function newProject() {
  memberPicker(new Set([S.me.name]), new Set(), true);
  modal(`<div class="m-head"><div class="t"><h2>New project</h2><p>A project decides who sees what. Its people and agents see every conversation in it; devices only send.</p></div><button class="icon-btn" data-act="close" aria-label="Close">${ic('close')}</button></div>
    <form class="m-body" id="npForm" novalidate>
      <div class="field"><label for="npName">Name</label><input class="input mono" id="npName" placeholder="speedtest" autocomplete="off"><span class="help">Agents select it by this name, and it starts every conversation’s address.</span><span class="ferr" id="npErr" hidden></span></div>
      <div class="field"><span class="lbl">Members</span>${pickerHtml()}</div>
      <div class="row row-top"><div class="field grow"><label for="npRet">Retention</label><select class="input" id="npRet">${[7, 14, 30, 60, 90].map((d) => `<option value="${d}" ${d === 30 ? 'selected' : ''}>${d} days</option>`).join('')}</select></div>
        <div class="field grow"><label for="npQuota">Quota</label><select class="input" id="npQuota">${[1, 2, 5, 10, 20].map((g) => `<option value="${g * (1 << 30)}" ${g === 5 ? 'selected' : ''}>${g} GB</option>`).join('')}</select></div></div>
      <div class="field"><span class="lbl">To put an agent to work on it</span><span class="help">Nothing to configure. Tell the agent “check cafard, project <span id="npHint">speedtest</span>”, or add <code>cafard project: <span id="npHint2">speedtest</span></code> to the repo’s CLAUDE.md.</span></div>
    </form>
    <div class="m-foot"><button class="btn tonal" data-act="close">Cancel</button><button class="btn filled" data-act="np-create">Create project</button></div>`);
  pkRender();
  const n = $('#npName'); n.focus();
  n.addEventListener('input', () => { $('#npHint').textContent = $('#npHint2').textContent = n.value.trim() || 'speedtest'; });
}

async function createProject() {
  const name = $('#npName').value.trim().toLowerCase();
  try {
    await api('POST', '/projects', { name, members: [...PK.sel], retention_days: Number($('#npRet').value), quota_bytes: Number($('#npQuota').value) });
  } catch (e) {
    if (e.status === 401) return;
    const er = $('#npErr'); er.hidden = false; er.textContent = e.message; $('#npName').classList.add('err');
    return;
  }
  close();
  location.hash = 'projects';
  await refresh();
  toast(`${name} is ready.`);
}

// ---------- sign-in ----------
function renderSignin(message) {
  close(); close2();
  $('#root').innerHTML = `<div class="signin"><form id="signin" novalidate>
    <div class="brand">${LOGO} cafard <small>admin</small></div>
    <h1>Sign in</h1>
    <p>Admins only. Your password is the one you chose with your join link; if you have none, ask another admin, or run <code>cafard admin link NAME</code> on the box.</p>
    ${message ? `<p class="ferr" role="alert">${esc(message)}</p>` : ''}
    <div class="field"><label for="u">Username</label><input class="input" id="u" autocomplete="username" autocapitalize="none" required></div>
    <div class="field"><label for="p">Password</label><input class="input" id="p" type="password" autocomplete="current-password" required></div>
    <span class="ferr" id="sErr" hidden></span>
    <button class="btn filled" type="submit">Sign in</button></form></div>`;
  $('#u').focus();
}

async function signin(e) {
  e.preventDefault();
  const err = $('#sErr');
  try {
    const r = await api('POST', '/signin', { username: $('#u').value, password: $('#p').value });
    S.me = r.member;
    $('#root').innerHTML = '';
    await refresh();
  } catch (x) {
    err.hidden = false; err.textContent = x.message;
  }
}

// ---------- toast ----------
let toastT;
function toast(m) {
  let el = $('.toast');
  if (!el) { el = document.createElement('div'); el.className = 'toast'; el.setAttribute('role', 'status'); document.body.appendChild(el); }
  el.textContent = m; el.hidden = false;
  clearTimeout(toastT); toastT = setTimeout(() => { el.hidden = true; }, 3200);
}

// ---------- events ----------
document.addEventListener('click', async (e) => {
  if (e.target.matches('[data-scrim2]')) return close2();
  if (e.target.matches('[data-scrim]')) return close();
  const t = e.target.closest('[data-act]');
  if (!t) return;
  const v = t.dataset.v;
  const enc = encodeURIComponent;
  switch (t.dataset.act) {
    case 'filter': S.filter = v; render(); break;
    case 'afilter': S.auditFilter = v; render(); break;
    case 'open': openMember(v); break;
    case 'proj': openProject(v); break;
    case 'close': close(); break;
    case 'close2': close2(); break;
    case 'signout': await run(() => api('POST', '/signout')); S.me = null; renderSignin('You are signed out.'); break;
    case 'pause': {
      const m = member(v);
      await run(() => api('PATCH', `/members/${enc(v)}`, { enabled: !m.enabled }), m.enabled ? `${v} is paused.` : `${v} works again.`);
      await refresh(); break;
    }
    case 'admin': { const m = member(v); await run(() => api('PATCH', `/members/${enc(v)}`, { admin: !m.admin })); await refresh(); break; }
    case 'save-desc': await run(() => api('PATCH', `/members/${enc(v)}`, { description: $('#desc').value }), 'Saved.'); await refresh(); break;
    case 'rm-proj': { const m = member(v); await run(() => api('PATCH', `/members/${enc(v)}`, { projects: m.projects.filter((p) => p !== t.dataset.p) }), `Removed from ${t.dataset.p}.`); await refresh(); break; }
    case 'manage-projects': manageProjects(v); break;
    case 'manage-members': manageMembers(v); break;
    case 'pk-toggle': PK.sel.has(v) ? PK.sel.delete(v) : PK.sel.add(v); pkRender(); break;
    case 'pk-kind': PK.kind = v; $$('[data-act="pk-kind"]').forEach((b) => b.setAttribute('aria-pressed', b.dataset.v === v)); pkRender(); break;
    case 'pk-save': await pkSave(); break;
    case 'relink': await relink(v); break;
    case 'regen': {
      const r = await run(() => api('POST', `/members/${enc(v)}/token`));
      if (r) { await refresh(); giveScreen(member(v), null, r.token); }
      break;
    }
    case 'signout-one': await run(() => api('DELETE', `/members/${enc(v)}/signins/${t.dataset.i}`), 'Signed out.'); reopen(); break;
    case 'ask-delete': S.confirm = v; reopen(); break;
    case 'no-delete': S.confirm = null; reopen(); break;
    case 'delete': await run(() => api('DELETE', `/members/${enc(v)}`), `${v} is removed. Their messages stay.`); close(); await refresh(); break;
    case 'new': newMember(); break;
    case 'pick-kind': NEW.kind = v; newMemberForm(); break;
    case 'new-create': await createMember(); break;
    case 'done': close(); break;
    case 'copy':
      try { await navigator.clipboard.writeText(v); toast('Copied.'); } catch (_) { toast('Select the text to copy it.'); }
      break;
    case 'new-project': newProject(); break;
    case 'np-create': await createProject(); break;
    case 'archive': { const p = project(v); await run(() => api('PATCH', `/projects/${enc(v)}`, { archived: !p.archived }), p.archived ? `${v} is open again.` : `${v} is archived and read-only.`); await refresh(); break; }
    case 'p-delete': await run(() => api('DELETE', `/projects/${enc(v)}`, { confirm: $('#pName').value.trim() }), `${v} is deleted.`); close(); await refresh(); break;
    case 'cancel-link': await run(() => api('DELETE', `/links/${v}`), 'The link no longer works.'); await refresh(); break;
  }
});

document.addEventListener('change', async (e) => {
  const t = e.target;
  if (t.id === 'pRet') { await run(() => api('PATCH', `/projects/${encodeURIComponent(t.dataset.v)}`, { retention_days: Number(t.value) }), 'Retention changed.'); await refresh(); }
  if (t.id === 'pQuota') { await run(() => api('PATCH', `/projects/${encodeURIComponent(t.dataset.v)}`, { quota_bytes: Number(t.value) }), 'Quota changed.'); await refresh(); }
});

document.addEventListener('input', (e) => {
  if (e.target.id === 'pkQ') { PK.q = e.target.value; pkRender(); return; }
  if (e.target.id === 'q') {
    S.q = e.target.value; const pos = e.target.selectionStart;
    render(); const q = $('#q'); q.focus(); q.setSelectionRange(pos, pos);
  }
});

document.addEventListener('submit', (e) => {
  if (e.target.id === 'signin') return signin(e);
  e.preventDefault();
  if (e.target.id === 'newForm') createMember();
  if (e.target.id === 'npForm') createProject();
});

document.addEventListener('keydown', (e) => {
  if (e.target.id === 'pkQ' && e.key === 'Enter') { e.preventDefault(); const r = $('#pkList .pk-row:not(:disabled)'); if (r) r.click(); return; }
  if (e.key === 'Escape') { if ($('#overlay2').innerHTML) return close2(); if ($('#overlay').innerHTML) return close(); }
  if ((e.key === 'Enter' || e.key === ' ') && e.target.matches('div[role="button"][data-act]')) { e.preventDefault(); e.target.click(); }
});

window.addEventListener('hashchange', async () => { close(); await refresh(); const m = $('#main'); if (m) m.scrollTop = 0; });

// ---------- start ----------
(async () => {
  try {
    const r = await fetch('/admin/api/me', { credentials: 'same-origin' });
    if (r.status !== 200) return renderSignin();
    S.me = (await r.json()).member;
    S.config = null;
    await refresh();
    if (!S.config) S.config = await api('GET', '/config').catch(() => null);
  } catch (_) {
    $('#root').innerHTML = '<p class="loading">cafard does not answer. Is it running?</p>';
  }
})();
})();
