'use strict';

// ---------- small helpers ----------

const $ = (sel, root = document) => root.querySelector(sel);

// el('div', {class: 'x', onclick: fn}, 'text', child) builds DOM safely
// (strings are always text, never HTML).
function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (k === 'class') e.className = v;
    else if (k in e && typeof v !== 'string') e[k] = v;
    else e.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) if (c != null && c !== false) e.append(c.nodeType ? c : String(c));
  return e;
}

// replaceChildren that skips null/false (optional parts of the UI).
function fill(node, ...children) {
  node.replaceChildren(...children.flat().filter((c) => c != null && c !== false));
}

const km = (m) => (m / 1000).toFixed(m < 10000 ? 2 : 1) + ' km';
const meters = (m) => (m == null ? '—' : Math.round(m) + ' m');
const date = (s) => new Date(s).toLocaleDateString();
const dateTime = (s) => new Date(s).toLocaleString();

function showMsg(node, text, kind = '') {
  node.className = 'msg' + (kind ? ' ' + kind : '');
  node.textContent = text;
}

// ---------- API client with automatic token refresh ----------

const session = { token: null, user: null };
let refreshPromise = null;

class ApiError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

// The access token lives only in memory. The refresh token is kept in
// localStorage so the session survives page reloads; it is sent in the body
// of /auth/refresh and /auth/logout (no cookies are used).
const REFRESH_KEY = 'travel-docs.refresh_token';

function storedRefreshToken() {
  try { return localStorage.getItem(REFRESH_KEY); } catch { return null; }
}

function storeRefreshToken(token) {
  try {
    if (token) localStorage.setItem(REFRESH_KEY, token);
    else localStorage.removeItem(REFRESH_KEY);
  } catch { /* storage unavailable: session lasts until reload */ }
}

function setSession(s) {
  session.token = s ? s.access_token : null;
  session.user = s ? s.user : null;
  storeRefreshToken(s ? s.refresh_token : null);
  renderAuth();
}

// Exchanges the refresh token for a new session. Concurrent callers share one
// request, since each refresh rotates the token.
function refreshSession() {
  if (!refreshPromise) {
    const token = storedRefreshToken();
    if (!token) {
      if (session.user) setSession(null);
      return Promise.resolve(false);
    }
    refreshPromise = fetch('/auth/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: token }),
    })
      .then(async (res) => {
        if (res.ok) { setSession(await res.json()); return true; }
        // Another tab may have rotated the token meanwhile; only log out if
        // the stored token is still the one that was rejected.
        if (storedRefreshToken() === token) setSession(null);
        else return refreshSessionAgain();
        return false;
      })
      .catch(() => false)
      .finally(() => { refreshPromise = null; });
  }
  return refreshPromise;
}

function refreshSessionAgain() {
  refreshPromise = null;
  return refreshSession();
}

async function api(path, opts = {}, retried = false) {
  const headers = { ...(opts.headers || {}) };
  let body = opts.body;
  if (opts.json !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.json);
  }
  if (session.token) headers.Authorization = 'Bearer ' + session.token;
  const res = await fetch(path, { method: opts.method || 'GET', headers, body });
  if (res.status === 401 && session.token && !retried) {
    if (await refreshSession()) return api(path, opts, true);
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.error || res.statusText);
  return data;
}

// ---------- dialog ----------

const dialog = $('#dialog');
$('#dialog-close').onclick = () => dialog.close();

function openDialog(title, ...content) {
  $('#dialog-title').textContent = title;
  fill($('#dialog-body'), ...content);
  if (!dialog.open) dialog.showModal();
}

// A form whose submit handler gets the FormData; errors show under the form.
function dialogForm(fields, submitLabel, onSubmit, opts = {}) {
  const msg = el('div', { class: 'msg' });
  const button = el('button', { type: 'submit', class: opts.danger ? 'danger' : 'primary' }, submitLabel);
  const form = el('form', {
    onsubmit: async (e) => {
      e.preventDefault();
      button.disabled = true;
      showMsg(msg, '');
      try {
        await onSubmit(new FormData(form), msg);
      } catch (err) {
        showMsg(msg, err.message, 'error');
      } finally {
        button.disabled = false;
      }
    },
  }, fields, msg, el('div', { class: 'dialog-actions' }, button));
  return form;
}

function field(label, input) {
  return el('label', { class: 'field' }, el('span', {}, label), input);
}

// ---------- state ----------

const state = {
  tab: 'routes',
  page: 1,
  selected: null,       // selected route
  layer: null,          // selected route on the map
  previewLayer: null,   // old version preview
  collection: null,     // opened collection
  collectionScope: 'mine',
  collectionPage: 1,
  collectionRoutePage: 1,
};

// ---------- map ----------

const map = L.map('map').setView([46, 8], 4);
// OpenStreetMap's tile usage policy requires a Referer header; without it
// tiles come back as "Access blocked" images.
L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
  maxZoom: 19,
  attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
  referrerPolicy: 'strict-origin-when-cross-origin',
}).addTo(map);

function clearPreview() {
  if (state.previewLayer) { state.previewLayer.remove(); state.previewLayer = null; }
}

// ---------- auth UI ----------

function renderAuth() {
  const area = $('#auth-area');
  if (session.user) {
    fill(area,
      el('span', { class: 'who' }, session.user.display_name),
      el('button', { type: 'button', onclick: openAccount }, 'Account'),
      el('button', { type: 'button', onclick: openActivity }, 'Activity'),
      el('button', { type: 'button', onclick: logout }, 'Log out'),
    );
  } else {
    fill(area,
      el('button', { type: 'button', onclick: () => openLogin('login') }, 'Log in'),
      el('button', { type: 'button', class: 'primary', onclick: () => openLogin('register') }, 'Sign up'),
    );
  }
  $('#upload-section').hidden = !session.user;
  renderScopeSelect();
}

// Rebuilds the scope options; switching between logged in and out resets the
// selection to the default ("mine" or "public").
let scopeForUser = undefined;
function renderScopeSelect() {
  const select = $('#scope-select');
  const userID = session.user ? session.user.id : null;
  if (userID === scopeForUser) return;
  scopeForUser = userID;
  const options = session.user
    ? [['mine', 'My routes'], ['shared', 'Shared with me'], ['public', 'Public routes'], ['all', 'Everything I can see']]
    : [['public', 'Public routes']];
  select.replaceChildren(...options.map(([v, l]) => el('option', { value: v }, l)));
  select.value = options[0][0];
}

function openLogin(mode) {
  const register = mode === 'register';
  const fields = [
    field('Email', el('input', { type: 'email', name: 'email', required: true, autocomplete: 'email' })),
    register && field('Display name', el('input', { name: 'display_name', maxlength: 100, placeholder: 'Optional' })),
    field('Password', el('input', {
      type: 'password', name: 'password', required: true, minlength: register ? 8 : null,
      autocomplete: register ? 'new-password' : 'current-password',
    })),
  ];
  const form = dialogForm(fields, register ? 'Create account' : 'Log in', async (fd) => {
    const s = await api(register ? '/auth/register' : '/auth/login', { method: 'POST', json: Object.fromEntries(fd) });
    setSession(s);
    dialog.close();
    afterAuthChange();
  });
  const switchLink = el('button', { type: 'button', class: 'link', onclick: () => openLogin(register ? 'login' : 'register') },
    register ? 'Already have an account? Log in' : 'No account yet? Sign up');
  openDialog(register ? 'Create account' : 'Log in', form, switchLink);
}

async function logout() {
  await fetch('/auth/logout', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refresh_token: storedRefreshToken() }),
  });
  setSession(null);
  afterAuthChange();
}

function afterAuthChange() {
  state.page = 1;
  state.collection = null;
  state.collectionScope = session.user ? 'mine' : 'public';
  deselect();
  reload();
}

function openAccount() {
  const u = session.user;
  const profile = dialogForm([
    field('Display name', el('input', { name: 'display_name', value: u.display_name, required: true, maxlength: 100 })),
    field('Email', el('input', { type: 'email', name: 'email', value: u.email, required: true })),
    field('Current password (needed to change email)', el('input', { type: 'password', name: 'current_password', autocomplete: 'current-password' })),
  ], 'Save profile', async (fd, msg) => {
    const user = await api('/auth/me', { method: 'PATCH', json: Object.fromEntries(fd) });
    session.user = user;
    renderAuth();
    showMsg(msg, 'Saved', 'ok');
  });

  const password = dialogForm([
    field('Current password', el('input', { type: 'password', name: 'current_password', required: true, autocomplete: 'current-password' })),
    field('New password', el('input', { type: 'password', name: 'new_password', required: true, minlength: 8, autocomplete: 'new-password' })),
  ], 'Change password', async (fd, msg) => {
    setSession(await api('/auth/me/password', { method: 'PUT', json: Object.fromEntries(fd) }));
    showMsg(msg, 'Password changed. Other devices were logged out.', 'ok');
  });

  const sessions = el('div', { class: 'dialog-actions' },
    el('button', {
      type: 'button',
      onclick: async () => {
        if (!confirm('Log out on all devices, including this one?')) return;
        await api('/auth/logout-all', { method: 'POST' });
        setSession(null);
        dialog.close();
        afterAuthChange();
      },
    }, 'Log out everywhere'));

  const del = dialogForm([
    field('Password', el('input', { type: 'password', name: 'password', required: true, autocomplete: 'current-password' })),
  ], 'Delete my account', async (fd) => {
    if (!confirm('Delete your account and all your routes and collections? This cannot be undone.')) return;
    await api('/auth/me', { method: 'DELETE', json: Object.fromEntries(fd) });
    setSession(null);
    dialog.close();
    afterAuthChange();
  }, { danger: true });

  openDialog('Account',
    el('h4', {}, 'Profile'), profile,
    el('h4', {}, 'Password'), password,
    el('h4', {}, 'Sessions'), sessions,
    el('h4', {}, 'Delete account'), del);
}

const actionLabels = {
  'user.register': 'Created account', 'user.login': 'Logged in', 'user.login_failed': 'Failed login attempt',
  'user.logout': 'Logged out', 'user.logout_all': 'Logged out everywhere', 'user.update': 'Updated profile',
  'user.password_change': 'Changed password', 'user.refresh_token_reuse': 'Session ended: reused token detected',
  'route.create': 'Uploaded route', 'route.update': 'Edited route', 'route.geometry_update': 'Replaced route geometry',
  'route.restore': 'Restored route version', 'route.visibility_change': 'Changed route visibility',
  'route.delete': 'Deleted route', 'route.share': 'Shared route', 'route.unshare': 'Removed route share',
  'collection.create': 'Created collection', 'collection.update': 'Edited collection',
  'collection.delete': 'Deleted collection', 'collection.share': 'Shared collection',
  'collection.unshare': 'Removed collection share', 'collection.route_add': 'Added route to collection',
  'collection.route_remove': 'Removed route from collection',
};

function openActivity() {
  const table = el('table', { class: 'table' });
  const more = el('button', { type: 'button' }, 'Load more');
  let before = 0;
  async function load() {
    more.disabled = true;
    const res = await api('/audit-events?limit=30' + (before ? '&before_id=' + before : ''));
    for (const ev of res.items) {
      const details = ev.details || {};
      const extra = details.name || details.email || (details.fields || []).join(', ') ||
        (details.to ? `${details.from} → ${details.to}` : '');
      table.append(el('tr', {},
        el('td', {}, actionLabels[ev.action] || ev.action,
          extra ? el('div', { class: 'meta' }, extra) : null,
          ev.actor_id && ev.actor_id !== session.user.id ? el('div', { class: 'meta' }, 'by ' + (ev.actor_name || 'deleted user')) : null),
        el('td', { class: 'meta' }, dateTime(ev.occurred_at))));
    }
    before = res.next_before_id || 0;
    more.hidden = !before;
    more.disabled = false;
  }
  more.onclick = load;
  openDialog('Activity', table, el('div', { class: 'dialog-actions' }, more));
  load().catch((e) => table.replaceChildren(el('tr', {}, el('td', {}, 'Error: ' + e.message))));
}

// ---------- tabs ----------

document.querySelectorAll('.tabs button').forEach((b) => {
  b.onclick = () => {
    state.tab = b.dataset.tab;
    document.querySelectorAll('.tabs button').forEach((x) => x.classList.toggle('active', x === b));
    $('#routes-panel').hidden = state.tab !== 'routes';
    $('#collections-panel').hidden = state.tab !== 'collections';
    reload();
  };
});

function reload() {
  if (state.tab === 'routes') loadRoutes();
  else loadCollections();
}

// ---------- route list ----------

function routeQuery(page, collectionID) {
  const q = new URLSearchParams({ page, page_size: 20 });
  if (collectionID) {
    q.set('collection_id', collectionID);
    return q;
  }
  const f = new FormData($('#filter-form'));
  q.set('scope', f.get('scope') || 'public');
  const [sort, order] = f.get('sort').split(':');
  q.set('sort', sort);
  q.set('order', order);
  if (f.get('name')) q.set('name', f.get('name'));
  if (f.get('min_km')) q.set('min_distance_m', f.get('min_km') * 1000);
  if (f.get('max_km')) q.set('max_distance_m', f.get('max_km') * 1000);
  if (f.get('in_view')) {
    const b = map.getBounds();
    const clamp = (v, lim) => Math.max(-lim, Math.min(lim, v));
    q.set('bbox', [clamp(b.getWest(), 180), clamp(b.getSouth(), 90), clamp(b.getEast(), 180), clamp(b.getNorth(), 90)].join(','));
  }
  return q;
}

function routeItem(r) {
  const li = el('li', { 'data-id': r.id, onclick: () => select(r) },
    el('div', { class: 'name' }, r.name,
      r.visibility === 'public' ? el('span', { class: 'badge public' }, 'public') : null,
      r.my_role !== 'owner' ? el('span', { class: 'badge role' }, r.my_role) : null),
    el('div', { class: 'meta' },
      `${km(r.distance_m)} · ↑ ${meters(r.ascent_m)} · ${date(r.started_at || r.created_at)}`,
      r.my_role !== 'owner' ? ` · by ${r.owner_name}` : ''));
  if (r.id === state.selected?.id) li.classList.add('selected');
  return li;
}

function pager(node, res, onPage) {
  if (res.total_pages <= 1) { node.replaceChildren(); return; }
  node.replaceChildren(
    el('button', { type: 'button', disabled: res.page <= 1, onclick: () => onPage(res.page - 1) }, '‹ Prev'),
    el('span', {}, `Page ${res.page} of ${res.total_pages} · ${res.total} total`),
    el('button', { type: 'button', disabled: res.page >= res.total_pages, onclick: () => onPage(res.page + 1) }, 'Next ›'));
}

async function loadRoutes() {
  const list = $('#route-list');
  try {
    const res = await api('/routes?' + routeQuery(state.page));
    if (res.page > 1 && res.items.length === 0) { state.page = res.total_pages || 1; return loadRoutes(); }
    list.replaceChildren(...res.items.map(routeItem));
    if (!res.items.length) {
      list.append(el('div', { class: 'empty' }, session.user ? 'No routes here yet.' : 'No public routes yet. Log in to upload your own.'));
    }
    pager($('#route-pager'), res, (p) => { state.page = p; loadRoutes(); });
  } catch (e) {
    list.replaceChildren(el('div', { class: 'empty' }, 'Error: ' + e.message));
  }
}

$('#filter-form').onsubmit = (e) => { e.preventDefault(); state.page = 1; loadRoutes(); };
$('#scope-select').onchange = () => { state.page = 1; loadRoutes(); };
map.on('moveend', () => {
  if (state.tab === 'routes' && $('#filter-form').in_view.checked) { state.page = 1; loadRoutes(); }
});

$('#upload-form').onsubmit = async (e) => {
  e.preventDefault();
  const form = e.target;
  const msg = $('[data-msg]', form);
  const btn = form.querySelector('button');
  btn.disabled = true;
  showMsg(msg, 'Uploading…');
  try {
    const route = await api('/routes', { method: 'POST', body: new FormData(form) });
    showMsg(msg, `Uploaded “${route.name}” (${km(route.distance_m)})`, 'ok');
    form.reset();
    state.page = 1;
    await loadRoutes();
    await select(route);
  } catch (err) {
    showMsg(msg, err.message, 'error');
  } finally {
    btn.disabled = false;
  }
};

// ---------- route details ----------

function deselect() {
  state.selected = null;
  if (state.layer) { state.layer.remove(); state.layer = null; }
  clearPreview();
  $('#details').hidden = true;
  document.querySelectorAll('.list li.selected').forEach((li) => li.classList.remove('selected'));
}

async function select(route, { fit = true } = {}) {
  clearPreview();
  state.selected = route;
  document.querySelectorAll('.list li[data-id]').forEach((li) => li.classList.toggle('selected', li.dataset.id === route.id));
  let feature, elevation;
  try {
    [route, feature, elevation] = await Promise.all([
      api(`/routes/${route.id}`),
      api(`/routes/${route.id}/geometry`),
      api(`/routes/${route.id}/elevation`),
    ]);
  } catch (e) {
    if (e.status === 404) { deselect(); reload(); return; }
    throw e;
  }
  state.selected = route;
  if (state.layer) state.layer.remove();
  state.layer = L.geoJSON(feature, { style: { color: '#2563eb', weight: 4 } }).addTo(map);
  if (fit) map.fitBounds(state.layer.getBounds(), { padding: [24, 24] });
  renderDetails(route);
  drawChart(elevation.points);
  map.invalidateSize();
}

function renderDetails(r) {
  $('#details').hidden = false;
  $('#d-name').textContent = r.name;
  fill($('#d-meta'),
    `by ${r.my_role === 'owner' ? 'you' : r.owner_name} · version ${r.version} · updated ${date(r.updated_at)}`,
    el('span', { class: 'badge' + (r.visibility === 'public' ? ' public' : '') }, r.visibility),
    r.my_role !== 'owner' ? el('span', { class: 'badge role' }, 'you are ' + r.my_role) : null);
  $('#d-description').textContent = r.description;
  const stats = [
    ['Distance', km(r.distance_m)], ['Ascent', meters(r.ascent_m)], ['Descent', meters(r.descent_m)],
    ['Min elevation', meters(r.min_elevation_m)], ['Max elevation', meters(r.max_elevation_m)], ['Points', r.point_count],
  ];
  $('#d-stats').replaceChildren(...stats.map(([l, v]) =>
    el('div', { class: 'stat' }, el('div', { class: 'v' }, v), el('div', { class: 'l' }, l))));

  const canEdit = r.my_role === 'owner' || r.my_role === 'editor';
  const isOwner = r.my_role === 'owner';
  const inCollection = state.tab === 'collections' && state.collection;
  const canRemoveFromCollection = inCollection &&
    (['owner', 'editor'].includes(state.collection.my_role) || isOwner);
  const btn = (label, onclick, cls) => el('button', { type: 'button', class: cls, onclick }, label);
  fill($('#d-actions'),
    canEdit ? btn('Edit', () => openEdit(r)) : null,
    canEdit ? btn('New version', () => openNewVersion(r)) : null,
    btn('History', () => openHistory(r)),
    isOwner ? btn('Share', () => openShares('route', r)) : null,
    session.user ? btn('Add to collection', () => openAddToCollection(r)) : null,
    canRemoveFromCollection ? btn('Remove from collection', () => removeFromCollection(r)) : null,
    isOwner ? btn('Delete', () => deleteRoute(r), 'danger') : null,
  );
}

// Edits send If-Match so a concurrent change by someone else isn't overwritten.
async function editRoute(r, path, opts) {
  try {
    return await api(path, { ...opts, headers: { ...(opts.headers || {}), 'If-Match': `"${r.version}"` } });
  } catch (e) {
    if (e.status === 412) {
      await select(r, { fit: false });
      throw new Error('Someone else changed this route meanwhile. It has been reloaded — please try again.');
    }
    throw e;
  }
}

async function afterRouteChange(route) {
  await select(route, { fit: false });
  reloadLists();
}

function reloadLists() {
  if (state.tab === 'routes') loadRoutes();
  else if (state.collection) loadCollectionRoutes();
}

function openEdit(r) {
  const fields = [
    field('Name', el('input', { name: 'name', value: r.name, required: true, maxlength: 200 })),
    field('Description', el('textarea', { name: 'description', maxlength: 5000 }, r.description)),
  ];
  if (r.my_role === 'owner') {
    const vis = el('select', { name: 'visibility' },
      el('option', { value: 'private' }, 'Private — only you and people you share with'),
      el('option', { value: 'public' }, 'Public — anyone with the link'));
    vis.value = r.visibility;
    fields.push(field('Visibility', vis));
  }
  openDialog('Edit route', dialogForm(fields, 'Save', async (fd) => {
    const updated = await editRoute(r, `/routes/${r.id}`, { method: 'PATCH', json: Object.fromEntries(fd) });
    dialog.close();
    await afterRouteChange(updated);
  }));
}

function openNewVersion(r) {
  openDialog('Upload new version',
    el('p', { class: 'meta' }, 'Replaces the route geometry with a new GPX file. Name and description stay the same; the current version stays in the history.'),
    dialogForm([
      field('GPX file', el('input', { type: 'file', name: 'file', accept: '.gpx,application/gpx+xml', required: true })),
    ], 'Upload', async (fd) => {
      const updated = await editRoute(r, `/routes/${r.id}/geometry`, { method: 'PUT', body: fd });
      dialog.close();
      await afterRouteChange(updated);
    }));
}

const changeLabels = { create: 'Uploaded', metadata: 'Edited details', geometry: 'New geometry', restore: 'Restored' };

async function openHistory(r) {
  const res = await api(`/routes/${r.id}/versions`);
  const canEdit = r.my_role === 'owner' || r.my_role === 'editor';
  const table = el('table', { class: 'table' }, res.items.map((v) => el('tr', {},
    el('td', {},
      el('strong', {}, `v${v.version} `), changeLabels[v.change] || v.change,
      v.restored_from ? ` from v${v.restored_from}` : '',
      el('div', { class: 'meta' }, `${v.name} · ${km(v.distance_m)} · ↑ ${meters(v.ascent_m)} · ${v.point_count} points`),
      el('div', { class: 'meta' }, `${dateTime(v.created_at)} · ${v.created_by_name || 'unknown'}`)),
    el('td', {},
      el('button', { type: 'button', class: 'link', onclick: () => previewVersion(r, v.version) }, 'Show'),
      canEdit && v.version !== r.version
        ? el('button', { type: 'button', class: 'link', onclick: () => restoreVersion(r, v.version) }, 'Restore')
        : null))));
  openDialog(`History of “${r.name}”`, table,
    el('p', { class: 'meta' }, '“Show” draws that version on the map as an orange dashed line.'));
}

async function previewVersion(r, version) {
  const feature = await api(`/routes/${r.id}/versions/${version}/geometry`);
  clearPreview();
  state.previewLayer = L.geoJSON(feature, { style: { color: '#ea580c', weight: 3, dashArray: '6 6' } }).addTo(map);
  map.fitBounds(state.previewLayer.getBounds().extend(state.layer.getBounds()), { padding: [24, 24] });
  dialog.close();
}

async function restoreVersion(r, version) {
  if (!confirm(`Restore version ${version}? It will be saved as a new version.`)) return;
  try {
    const updated = await editRoute(r, `/routes/${r.id}/versions/${version}/restore`, { method: 'POST' });
    dialog.close();
    await afterRouteChange(updated);
  } catch (e) {
    alert(e.message);
  }
}

async function deleteRoute(r) {
  if (!confirm(`Delete “${r.name}” and its history?`)) return;
  try {
    await api(`/routes/${r.id}`, { method: 'DELETE' });
    deselect();
    reloadLists();
  } catch (e) {
    alert('Delete failed: ' + e.message);
  }
}

// ---------- sharing (routes and collections) ----------

async function openShares(kind, item) {
  const base = `/${kind === 'route' ? 'routes' : 'collections'}/${item.id}/shares`;
  const table = el('table', { class: 'table' });
  async function refresh() {
    const res = await api(base);
    table.replaceChildren(...res.items.map((s) => el('tr', {},
      el('td', {}, s.display_name, el('div', { class: 'meta' }, s.email)),
      el('td', {}, s.role, ' ',
        el('button', {
          type: 'button', class: 'link',
          onclick: async () => { await api(`${base}/${s.user_id}`, { method: 'DELETE' }); refresh(); },
        }, 'Remove')))));
    if (!res.items.length) table.append(el('tr', {}, el('td', { class: 'meta' }, 'Not shared with anyone yet.')));
  }
  const form = dialogForm([
    el('div', { class: 'row' },
      el('input', { type: 'email', name: 'email', placeholder: 'Their account email', required: true }),
      el('select', { name: 'role' },
        el('option', { value: 'viewer' }, 'Can view'),
        el('option', { value: 'editor' }, 'Can edit'))),
  ], 'Share', async (fd, msg) => {
    await api(base, { method: 'PUT', json: Object.fromEntries(fd) });
    form.reset();
    showMsg(msg, 'Shared', 'ok');
    refresh();
  });
  const hint = kind === 'route'
    ? 'Editors can change the name, description and geometry, and restore versions. Only you can share or delete.'
    : 'Viewers see the collection and the routes their owners added to it. Editors can also add and remove routes.';
  openDialog(`Share “${item.name}”`, el('h4', {}, 'People with access'), table,
    el('h4', {}, 'Add someone'), form, el('p', { class: 'meta' }, hint));
  refresh().catch((e) => table.replaceChildren(el('tr', {}, el('td', {}, 'Error: ' + e.message))));
}

// ---------- collections ----------

async function openAddToCollection(r) {
  const res = await api('/collections?scope=editable&page_size=100');
  const already = new Set((await api(`/collections?scope=all&page_size=100&route_id=${r.id}`)).items.map((c) => c.id));
  const select = el('select', { name: 'collection', required: true },
    res.items.filter((c) => !already.has(c.id)).map((c) => el('option', { value: c.id }, c.name)));
  const form = dialogForm([
    field('Collection', select),
    field('…or create a new one', el('input', { name: 'new_name', placeholder: 'New collection name' })),
  ], 'Add', async (fd, msg) => {
    let id = fd.get('collection');
    if (fd.get('new_name')) {
      id = (await api('/collections', { method: 'POST', json: { name: fd.get('new_name') } })).id;
    }
    if (!id) throw new Error('Choose a collection or enter a name for a new one.');
    await api(`/collections/${id}/routes/${r.id}`, { method: 'PUT' });
    showMsg(msg, 'Added', 'ok');
    setTimeout(() => dialog.close(), 600);
  });
  if (!select.options.length) select.required = false;
  const hint = r.my_role === 'owner'
    ? 'Everyone who can see the collection will be able to see this route.'
    : 'You can add other people’s routes only while they are public.';
  openDialog(`Add “${r.name}” to a collection`, form, el('p', { class: 'meta' }, hint));
}

async function removeFromCollection(r) {
  const c = state.collection;
  if (!confirm(`Remove “${r.name}” from “${c.name}”? The route itself is not deleted.`)) return;
  try {
    await api(`/collections/${c.id}/routes/${r.id}`, { method: 'DELETE' });
    deselect();
    loadCollectionRoutes();
  } catch (e) {
    alert(e.message);
  }
}

function loadCollections() {
  if (state.collection) return loadCollectionRoutes();
  const head = $('#collection-head');
  const scope = el('select', {
    onchange: (e) => { state.collectionScope = e.target.value; state.collectionPage = 1; loadCollections(); },
  }, (session.user
    ? [['mine', 'My collections'], ['shared', 'Shared with me'], ['public', 'Public collections']]
    : [['public', 'Public collections']]).map(([v, l]) => el('option', { value: v }, l)));
  if (!session.user) state.collectionScope = 'public';
  scope.value = state.collectionScope;
  fill(head,el('div', { class: 'row' }, scope,
    session.user ? el('button', { type: 'button', class: 'primary', onclick: openNewCollection }, 'New collection') : null));

  const list = $('#collection-list');
  api(`/collections?scope=${state.collectionScope}&page=${state.collectionPage}`).then((res) => {
    list.replaceChildren(...res.items.map((c) => el('li', { onclick: () => openCollection(c) },
      el('div', { class: 'name' }, c.name,
        c.visibility === 'public' ? el('span', { class: 'badge public' }, 'public') : null,
        c.my_role !== 'owner' ? el('span', { class: 'badge role' }, c.my_role) : null),
      el('div', { class: 'meta' }, `${c.route_count} route${c.route_count === 1 ? '' : 's'}`,
        c.my_role !== 'owner' ? ` · by ${c.owner_name}` : '', ` · updated ${date(c.updated_at)}`))));
    if (!res.items.length) list.append(el('div', { class: 'empty' }, 'No collections here yet.'));
    pager($('#collection-pager'), res, (p) => { state.collectionPage = p; loadCollections(); });
  }).catch((e) => list.replaceChildren(el('div', { class: 'empty' }, 'Error: ' + e.message)));
}

function collectionForm(c, onSaved) {
  const fields = [
    field('Name', el('input', { name: 'name', value: c?.name || '', required: true, maxlength: 200 })),
    field('Description', el('textarea', { name: 'description', maxlength: 5000 }, c?.description || '')),
  ];
  if (!c || c.my_role === 'owner') {
    const vis = el('select', { name: 'visibility' },
      el('option', { value: 'private' }, 'Private'), el('option', { value: 'public' }, 'Public'));
    vis.value = c?.visibility || 'private';
    fields.push(field('Visibility', vis));
  }
  return dialogForm(fields, c ? 'Save' : 'Create', async (fd) => {
    const saved = c
      ? await api(`/collections/${c.id}`, { method: 'PATCH', json: Object.fromEntries(fd) })
      : await api('/collections', { method: 'POST', json: Object.fromEntries(fd) });
    dialog.close();
    onSaved(saved);
  });
}

function openNewCollection() {
  openDialog('New collection', collectionForm(null, (c) => openCollection(c)));
}

function openCollection(c) {
  state.collection = c;
  state.collectionRoutePage = 1;
  deselect();
  loadCollectionRoutes();
}

async function loadCollectionRoutes() {
  const head = $('#collection-head');
  const list = $('#collection-list');
  let c;
  try {
    c = state.collection = await api(`/collections/${state.collection.id}`);
  } catch (e) {
    state.collection = null;
    return loadCollections();
  }
  const canEdit = c.my_role === 'owner' || c.my_role === 'editor';
  fill(head,
    el('div', { class: 'collection-title' },
      el('button', { type: 'button', class: 'link', onclick: () => { state.collection = null; deselect(); loadCollections(); } }, '‹ All'),
      el('h3', {}, c.name),
      el('span', { class: 'badge' + (c.visibility === 'public' ? ' public' : '') }, c.visibility)),
    el('div', { class: 'meta' }, c.my_role === 'owner' ? 'Your collection' : `By ${c.owner_name} · you are ${c.my_role}`),
    c.description ? el('p', { class: 'description' }, c.description) : null,
    el('div', { class: 'actions' },
      canEdit ? el('button', { type: 'button', onclick: () => openDialog('Edit collection', collectionForm(c, () => loadCollectionRoutes())) }, 'Edit') : null,
      c.my_role === 'owner' ? el('button', { type: 'button', onclick: () => openShares('collection', c) }, 'Share') : null,
      c.my_role === 'owner' ? el('button', {
        type: 'button', class: 'danger',
        onclick: async () => {
          if (!confirm(`Delete collection “${c.name}”? Its routes are kept.`)) return;
          await api(`/collections/${c.id}`, { method: 'DELETE' });
          state.collection = null;
          deselect();
          loadCollections();
        },
      }, 'Delete') : null));

  const res = await api('/routes?' + routeQuery(state.collectionRoutePage, c.id));
  list.replaceChildren(...res.items.map(routeItem));
  if (!res.items.length) {
    list.append(el('div', { class: 'empty' }, 'No routes in this collection yet. Select a route and use “Add to collection”.'));
  }
  pager($('#collection-pager'), res, (p) => { state.collectionRoutePage = p; loadCollectionRoutes(); });
}

// ---------- elevation chart ----------

function drawChart(points) {
  const svg = $('#chart');
  const W = svg.clientWidth || 600, H = 140, P = { l: 44, r: 8, t: 8, b: 20 };
  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
  const NS = 'http://www.w3.org/2000/svg';
  const s = (tag, attrs, text) => {
    const n = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
    if (text != null) n.textContent = text;
    return n;
  };
  if (!points.length) {
    svg.replaceChildren(s('text', { x: W / 2, y: H / 2, 'text-anchor': 'middle' }, 'No elevation data in this route'));
    return;
  }
  const maxD = points[points.length - 1].distance_m || 1;
  let minE = Math.min(...points.map((p) => p.elevation_m));
  let maxE = Math.max(...points.map((p) => p.elevation_m));
  if (maxE - minE < 10) { minE -= 5; maxE += 5; }
  const x = (d) => P.l + (d / maxD) * (W - P.l - P.r);
  const y = (e) => P.t + (1 - (e - minE) / (maxE - minE)) * (H - P.t - P.b);
  const line = points.map((p, i) => `${i ? 'L' : 'M'}${x(p.distance_m).toFixed(1)},${y(p.elevation_m).toFixed(1)}`).join('');
  svg.replaceChildren(
    s('line', { class: 'grid', x1: P.l, x2: W - P.r, y1: y(maxE), y2: y(maxE) }),
    s('line', { class: 'grid', x1: P.l, x2: W - P.r, y1: y(minE), y2: y(minE) }),
    s('text', { x: P.l - 4, y: y(maxE) + 4, 'text-anchor': 'end' }, Math.round(maxE) + ' m'),
    s('text', { x: P.l - 4, y: y(minE) + 4, 'text-anchor': 'end' }, Math.round(minE) + ' m'),
    s('text', { x: P.l, y: H - 4 }, '0'),
    s('text', { x: W - P.r, y: H - 4, 'text-anchor': 'end' }, km(maxD)),
    s('path', { class: 'area', d: `${line}L${x(maxD)},${H - P.b}L${x(0)},${H - P.b}Z` }),
    s('path', { class: 'line', d: line }));
}

// ---------- start ----------

renderAuth();
refreshSession().finally(() => {
  state.collectionScope = session.user ? 'mine' : 'public';
  reload();
});
