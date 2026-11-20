// Fills a fresh cafard with two projects, channels, a gallery, sinks with
// logs, agents and people, messages, a question and direct messages.
// Usage: node seed.mjs <base> <password> <imgdir>
import { readFileSync } from 'node:fs';
const [base, password, img] = process.argv.slice(2);
const origin = { Origin: base, 'Sec-Fetch-Site': 'same-origin' };
let cookie = '';
async function form(path, fields) {
  const body = new URLSearchParams();
  for (const [k, v] of Object.entries(fields)) for (const x of [].concat(v)) body.append(k, x);
  const r = await fetch(base + path, { method: 'POST', body, redirect: 'manual', headers: { ...origin, Cookie: cookie } });
  if (r.status >= 400) { const t = await r.text(); throw new Error(`${path}: ${r.status} ${(t.match(/banner error[^>]*>([^<]*)/) ?? [, t.slice(0, 200)])[1]}`); }
  return r;
}
async function api(method, path, body, token) {
  const headers = { Accept: 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : { ...origin, Cookie: cookie }) };
  if (body !== undefined && !(body instanceof FormData)) headers['Content-Type'] = 'application/json';
  const r = await fetch(base + path, { method, headers, body: body instanceof FormData || body === undefined ? body : JSON.stringify(body) });
  const text = await r.text();
  if (r.status >= 400) throw new Error(`${method} ${path}: ${r.status} ${text.slice(0, 300)}`);
  return text ? JSON.parse(text) : null;
}
const r = await form('/signin', { password, next: '/' });
cookie = (r.headers.get('set-cookie') ?? '').split(';')[0];
if (!cookie) throw new Error('no admin cookie');

await form('/projects', { name: 'lab', title: 'Lab bench' });
await form('/projects', { name: 'rack', title: 'Rack 2' });
await form('/projects/lab/channels', { name: 'dut1', title: 'DUT 1 (x86 board)', patterns: 'push:lab/dut1' });
await form('/projects/lab/channels', { name: 'dut2', title: 'DUT 2 (arm64)', patterns: 'push:lab/dut2' });
await form('/projects/lab/channels', { name: 'shots', title: 'UI screenshots', kind: 'gallery' });
await form('/projects/rack/channels', { name: 'power', title: 'PDU and power cycling' });
for (const [p, s, t] of [['lab', 'dut1', 'x86 board, serial over USB'], ['lab', 'dut2', 'arm64 board'], ['lab', 'bench-pi', 'the bench Raspberry Pi'], ['rack', 'pdu', 'rack PDU']]) {
  await form(`/projects/${p}/sinks`, { name: s, title: t, sources: '' });
}
const people = {};
async function principal(name, kind, grants) {
  const scopes = kind === 'device' || name === 'rack-bot' ? ['read', 'message', 'ingest'] : ['read', 'message'];
  const t = await api('POST', '/api/tokens', { principal: name, kind, label: 'shots', scopes });
  for (const [project, channel] of grants) {
    await form(`/projects/${project}/grants`, { principal: name, kind, channel: channel ?? '', scopes });
  }
  people[name] = t.token;
  return t.token;
}
await principal('codex-build', 'agent', [['lab'], ['rack']]);
await principal('claude-laptop', 'agent', [['lab']]);
await principal('gemini-ci', 'agent', [['lab', 'dut2']]);
await principal('rack-bot', 'agent', [['rack']]);
await principal('alice', 'human', [['lab'], ['rack']]);
await principal('bob', 'human', [['lab']]);
await principal('bench-pi', 'device', [['lab']]);

async function send(who, fields, files = []) {
  const f = new FormData();
  for (const [k, v] of Object.entries(fields)) f.append(k, String(v));
  for (const file of files) f.append('files[]', new Blob([readFileSync(`${img}/${file}`)], { type: 'image/png' }), file);
  return api('POST', '/api/messages', f, people[who]);
}
async function log(who, sink, lines) {
  const r = await fetch(`${base}/api/log?stream=${sink}`, { method: 'POST', headers: { Authorization: 'Bearer ' + people[who], 'Content-Type': 'text/plain' }, body: lines.join('\n') + '\n' });
  if (r.status >= 400) throw new Error(`log ${sink}: ${r.status} ${await r.text()}`);
}
await log('bench-pi', 'dut1', ['U-Boot 2024.04 (Nov 16 2026 - 09:12:44 +0000)', 'DRAM:  8 GiB', 'Starting kernel ...', '[    0.000000] Linux version 6.18.0-rc3 (codex@build) #812', '[    1.204411] dwc3 fe800000.usb: failed to initialize core: -110', '[    2.000102] systemd[1]: Started Journal Service.', 'login: ']);
await log('bench-pi', 'dut2', ['[    0.000000] Booting Linux on physical CPU 0x0', '[    0.812000] mmc0: new HS200 MMC card at address 0001', 'dut2 login: ']);
await log('bench-pi', 'bench-pi', ['systemd[1]: Started cafard push.', 'cafard-push[402]: watching /dev/ttyUSB0 and /dev/ttyUSB1']);
await log('rack-bot', 'pdu', ['outlet 3: on', 'outlet 4: off (requested by codex-build)']);

await send('codex-build', { channel: 'lab/dut1', text: 'Flashed image v42 on dut1, `dwc3` fails to initialise again, see https://git.example.com/k/4a1f' });
await send('alice', { channel: 'lab/dut1', text: 'The cable is the short blue one now, try again?' }, ['photo.png']);
await send('claude-laptop', { channel: 'lab/dut1', text: 'Retried with the new cable: the USB controller comes up, enumerating at 5 Gb/s.' });
// An open question: ask_human waits for the answer, so leave it waiting.
async function ask(who, args) {
  const h = { Authorization: 'Bearer ' + people[who], 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' };
  const init = await fetch(base + '/mcp', { method: 'POST', headers: h, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'seed', version: '1' } } }) });
  const sid = init.headers.get('mcp-session-id');
  await init.text();
  const hs = { ...h, ...(sid ? { 'Mcp-Session-Id': sid } : {}) };
  await (await fetch(base + '/mcp', { method: 'POST', headers: hs, body: JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }) })).text();
  const ac = new AbortController();
  setTimeout(() => ac.abort(), 1500);
  await fetch(base + '/mcp', { method: 'POST', headers: hs, signal: ac.signal, body: JSON.stringify({ jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name: 'ask_human', arguments: args } }) })
    .then((r) => r.text()).catch(() => {});
}
await ask('claude-laptop', { question: 'Power-cycle dut1? The USB hub is stuck.', choices: ['done', "can't"], channel: 'lab/dut1' });
await send('bob', { channel: 'lab/general', text: 'Morning! Bench is free from 10:00.' });
await send('codex-build', { channel: 'lab/general', text: 'Build 812 is green on all boards.' });
await send('gemini-ci', { channel: 'lab/dut2', text: 'dut2: eMMC test passed, 3 runs, no errors.' });
await send('rack-bot', { channel: 'rack/power', text: 'Outlet 4 turned off for codex-build.' });
await send('codex-build', { channel: 'rack/general', text: 'Rack 2 is mine until 16:00 for the soak test.' });
let i = 0;
for (const [title, section] of [['login page', 'login flow'], ['login page after fix', 'login flow'], ['settings, dark theme', 'settings'], ['firmware update dialog', 'settings'], ['boot splash', ''], ['dashboard with 3 boards', '']]) {
  i++;
  await send('claude-laptop', { channel: 'lab/shots', text: title, ...(section ? { section } : {}) }, [`shot${i}.png`]);
}
await send('codex-build', { to: 'admin', text: 'Build 812 is green. Want me to tag it?' });
await send('claude-laptop', { to: 'admin', text: 'I need the bench at 14:00 for the USB tests.' });
await send('alice', { to: 'admin', text: 'Can you give bob access to Rack 2?' });
console.log('seeded');
