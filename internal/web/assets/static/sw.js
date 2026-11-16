// cafard's service worker (PLAN 9.3, CONTRACTS 2.4). It does two things:
// - keeps a copy of the shell (CSS, bundle, icons) for when the network is
//   gone, fetching it fresh whenever it can; pages and the API are never
//   cached, they hold personal data and must be current;
// - sends the share target's answer to /?share=<id> instead of
//   /drop?share=<id>, so the quick drop sheet opens over home.
'use strict';

const CACHE = 'cafard-shell-v1';
const SHELL = ['/static/app.css', '/app/main.js', '/static/icons.svg', '/icons.svg', '/static/favicon.svg', '/manifest.webmanifest'];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).catch(() => {}).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

async function shell(request) {
  try {
    const response = await fetch(request);
    if (response.ok) {
      const copy = response.clone();
      caches.open(CACHE).then((c) => c.put(request, copy)).catch(() => {});
    }
    return response;
  } catch (error) {
    const cached = await caches.match(request);
    if (cached) return cached;
    throw error;
  }
}

async function share(request) {
  // The server stages the files and redirects to /drop?share=<id>; this
  // fetch follows it, and the final URL carries the id. The form is sent
  // again because a navigation's own request follows no redirect here.
  const form = await request.formData();
  const response = await fetch('/share', { method: 'POST', body: form, credentials: 'same-origin' });
  const id = new URL(response.url).searchParams.get('share');
  if (!response.redirected || id === null) return response;
  return Response.redirect(new URL('/?share=' + encodeURIComponent(id), self.location.origin).href, 303);
}

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;
  if (event.request.method === 'POST' && url.pathname === '/share') {
    event.respondWith(share(event.request));
    return;
  }
  if (event.request.method === 'GET' && SHELL.includes(url.pathname)) {
    event.respondWith(shell(event.request));
  }
});
