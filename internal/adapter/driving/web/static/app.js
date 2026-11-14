'use strict';
// The page of viper ui. Everything that comes from the server is set as text (textContent), never as markup.
(function () {
  const $ = (id) => document.getElementById(id);
  let overview = { machines: [], groups: [] };
  let logText = '';

  async function api(path, body) {
    const opts = body === undefined
      ? { credentials: 'same-origin' }
      : { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Viper-UI': '1' }, body: JSON.stringify(body) };
    const res = await fetch(path, opts);
    let data = {};
    try { data = await res.json(); } catch (e) { data = {}; }
    if (!res.ok) { throw new Error(data.error || ('HTTP ' + res.status)); }
    return data;
  }

  function cell(row, text, cls) {
    const td = document.createElement('td');
    td.textContent = text || '';
    if (cls) { td.className = cls; }
    row.appendChild(td);
    return td;
  }

  function renderMachines() {
    const body = $('machines').tBodies[0];
    body.replaceChildren();
    for (const m of overview.machines) {
      const tr = document.createElement('tr');
      cell(tr, m.name);
      cell(tr, m.group);
      cell(tr, m.provider);
      cell(tr, m.type);
      cell(tr, m.location);
      cell(tr, m.nats);
      cell(tr, m.error ? 'error: ' + m.error : stateText(m.state), m.error ? 'bad' : 'state-' + m.state);
      const act = cell(tr, '');
      const check = document.createElement('button');
      check.type = 'button';
      check.textContent = 'Check';
      check.disabled = !!m.error;
      check.setAttribute('aria-label', 'Check ' + m.name);
      check.addEventListener('click', () => runCheck(m, check));
      act.appendChild(check);
      const log = document.createElement('button');
      log.type = 'button';
      log.textContent = 'Log';
      log.setAttribute('aria-label', 'Log of ' + m.name);
      log.addEventListener('click', () => { show('logs'); $('log-machine').value = m.name; loadLog(); });
      act.appendChild(log);
      body.appendChild(tr);
    }
    $('overview-note').textContent = overview.machines.length === 0
      ? 'No machines found: machines/<name>.env.local or groups/<name>.env in the settings directory name them.'
      : 'State is what this computer knows; Check asks the machine.';
  }

  function stateText(s) {
    return { ready: 'set up', started: 'started', none: 'not started' }[s] || '';
  }

  function renderGroups() {
    const ul = $('groups');
    ul.replaceChildren();
    for (const g of overview.groups) {
      const li = document.createElement('li');
      li.textContent = g.error ? g.name + ': ' + g.error : g.name + ': ' + (g.machines || []).join(', ');
      ul.appendChild(li);
    }
  }

  function renderLogChoices() {
    const sel = $('log-machine');
    const keep = sel.value;
    sel.replaceChildren();
    const all = document.createElement('option');
    all.value = '';
    all.textContent = 'all machines (events)';
    sel.appendChild(all);
    const seen = new Set();
    for (const m of overview.machines) {
      if (seen.has(m.name)) { continue; }
      seen.add(m.name);
      const o = document.createElement('option');
      o.value = m.name;
      o.textContent = m.name;
      sel.appendChild(o);
    }
    sel.value = keep;
  }

  async function load() {
    try {
      overview = await api('/api/overview');
    } catch (e) {
      $('overview-note').textContent = 'Could not load: ' + e.message;
      return;
    }
    $('version').textContent = overview.version + (overview.readonly ? ' (read only)' : '');
    renderMachines();
    renderGroups();
    renderLogChoices();
  }

  async function runCheck(m, button) {
    button.disabled = true;
    $('check-status').textContent = 'Checking ' + m.name + ' ...';
    $('check-output').textContent = '';
    try {
      const r = await api('/api/check', { machine: m.name, group: m.group || '' });
      $('check-status').textContent = m.name + ': ' + (r.ok ? 'OK' : 'problems (exit ' + r.code + ')');
      $('check-status').className = r.ok ? 'ok' : 'bad';
      $('check-output').textContent = r.output + (r.truncated ? '\n[cut]' : '');
    } catch (e) {
      $('check-status').textContent = m.name + ': ' + e.message;
      $('check-status').className = 'bad';
    } finally {
      button.disabled = false;
    }
  }

  async function loadLog() {
    const machine = $('log-machine').value;
    const full = $('log-full').checked && machine !== '';
    $('log-status').textContent = 'Loading ...';
    try {
      const q = new URLSearchParams();
      if (machine) { q.set('machine', machine); }
      if (full) { q.set('full', '1'); }
      const r = await api('/api/log?' + q.toString());
      logText = r.text + (r.truncated ? '\n[cut]' : '');
      $('log-status').textContent = '';
      filterLog();
    } catch (e) {
      logText = '';
      $('log-output').textContent = '';
      $('log-status').textContent = e.message;
    }
  }

  function filterLog() {
    const q = $('log-filter').value.toLowerCase();
    const lines = logText.split('\n');
    $('log-output').textContent = (q ? lines.filter((l) => l.toLowerCase().includes(q)) : lines).join('\n');
  }

  function show(page) {
    for (const p of ['overview', 'logs']) {
      $('page-' + p).hidden = p !== page;
      $('tab-' + p).setAttribute('aria-pressed', String(p === page));
    }
    if (page === 'logs') { loadLog(); }
  }

  $('tab-overview').addEventListener('click', () => show('overview'));
  $('tab-logs').addEventListener('click', () => show('logs'));
  $('log-reload').addEventListener('click', loadLog);
  $('log-machine').addEventListener('change', loadLog);
  $('log-full').addEventListener('change', loadLog);
  $('log-filter').addEventListener('input', filterLog);
  load();
})();
