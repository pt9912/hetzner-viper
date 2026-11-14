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
      for (const a of ['preflight', 'up', 'down']) {
        const b = document.createElement('button');
        b.type = 'button';
        b.textContent = a[0].toUpperCase() + a.slice(1);
        b.disabled = !!m.error || overview.readonly;
        b.setAttribute('aria-label', b.textContent + ' ' + m.name);
        b.addEventListener('click', () => startRun({ action: a, machine: m.name, group: m.group || '' }));
        act.appendChild(b);
      }
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
    const body = $('groups').tBodies[0];
    body.replaceChildren();
    for (const g of overview.groups) {
      const tr = document.createElement('tr');
      cell(tr, g.name);
      cell(tr, g.error ? 'error: ' + g.error : (g.machines || []).join(', '), g.error ? 'bad' : '');
      const act = cell(tr, '');
      for (const a of ['preflight', 'up', 'down']) {
        const b = document.createElement('button');
        b.type = 'button';
        b.textContent = a[0].toUpperCase() + a.slice(1) + ' group';
        b.disabled = !!g.error || overview.readonly;
        b.setAttribute('aria-label', b.textContent + ' ' + g.name);
        b.addEventListener('click', () => startRun({ action: a, group: g.name, scope: 'group' }));
        act.appendChild(b);
      }
      body.appendChild(tr);
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
    renderNats();
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

  // ---- NATS: viper nats as runs; the page only builds the request ----
  function natsGroup() { return overview.nats.find((g) => g.name === $('nats-group').value); }

  function renderNats() {
    const groups = overview.nats || [];
    $('nats-none').hidden = groups.length > 0;
    $('nats-main').hidden = groups.length === 0;
    const sel = $('nats-group');
    const keep = sel.value;
    sel.replaceChildren();
    for (const g of groups) { addOption(sel, g.name, g.name); }
    if (groups.some((g) => g.name === keep)) { sel.value = keep; }
    natsGroupChanged();
  }

  function addOption(sel, value, text) {
    const o = document.createElement('option');
    o.value = value;
    o.textContent = text;
    sel.appendChild(o);
  }

  function natsGroupChanged() {
    const g = natsGroup();
    if (!g) { return; }
    const from = $('nats-from');
    const to = $('nats-to');
    const keepFrom = from.value;
    const keepTo = to.value;
    from.replaceChildren();
    addOption(from, '', g.operator ? 'the operator (key found here)' : 'a machine of the group (over ssh)');
    to.replaceChildren();
    for (const m of g.machines) { addOption(from, m, 'machine ' + m); addOption(to, m, m); }
    if (g.machines.includes(keepFrom)) { from.value = keepFrom; }
    if (g.machines.includes(keepTo)) { to.value = keepTo; }
    $('nats-mode').textContent = g.operator
      ? 'This computer has the key of the operator: viper nats speaks as the operator.'
      : 'No key of the operator here (viper nats init ' + g.name + '): viper nats speaks as a machine of the group, over ssh.';
    natsKindChanged();
  }

  function natsKindChanged() {
    const kind = $('nats-kind').value;
    $('nats-to').disabled = kind === 'broadcast';
    $('nats-topic').disabled = kind !== 'broadcast';
  }

  async function natsRun(extra) {
    const g = natsGroup();
    if (!g) { return; }
    const req = Object.assign({ action: 'nats', nats: g.name }, extra);
    fetching = extra.sub === 'fetch';
    if ($('nats-from').value) { req.from = $('nats-from').value; }
    await startRun(req);
  }

  async function natsSend(ev) {
    ev.preventDefault();
    const status = $('nats-send-status');
    const kind = $('nats-kind').value;
    const req = { sub: kind, text: $('nats-text').value };
    if (kind === 'broadcast') { req.topic = $('nats-topic').value.trim(); } else { req.to = $('nats-to').value; }
    if (kind === 'request') { req.timeout = Number($('nats-timeout').value) || 30; }
    if ($('nats-yes').checked) { req.yes = true; }
    const file = $('nats-file').files[0];
    try {
      if (file) {
        status.textContent = 'Handing over the file ...';
        const type = fileKind(file);
        const res = await fetch('/api/upload?kind=' + type + '&name=' + encodeURIComponent(file.name), {
          method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/octet-stream', 'X-Viper-UI': '1' }, body: file,
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) { throw new Error(data.error || 'HTTP ' + res.status); }
        req.upload = data.id;
      }
      status.textContent = '';
      await natsRun(req);
    } catch (e) {
      status.textContent = e.message;
    }
  }

  function fileKind(file) {
    const n = file.name.toLowerCase();
    if (n.endsWith('.pdf') || file.type === 'application/pdf') { return 'pdf'; }
    if (n.endsWith('.zip') || file.type === 'application/zip') { return 'zip'; }
    return 'image';
  }

  // a line of the output of viper nats --json, as text; the lines of anything else stay as they are
  function natsText(line) {
    let e;
    try { e = JSON.parse(line); } catch (x) { return { text: line }; }
    if (!e || typeof e !== 'object' || !e.event) { return { text: line }; }
    const att = e.att_id ? ' [file ' + (e.att_name || '') + ', ' + (e.att_type || '') + ', ' + (e.att_size || 0) + ' bytes]' : '';
    let text;
    switch (e.event) {
      case 'peer': text = 'present: ' + e.from + (e.text ? ' (topics: ' + e.text + ')' : ''); break;
      case 'configured': text = 'set in the settings: ' + e.from + ': ' + (e.text || 'no topic'); break;
      case 'message': text = (e.via === 'pub' ? '#' + e.topic + ' ' : '') + e.from + (e.to ? ' -> ' + e.to : '') + ': ' + (e.text || '') + att; break;
      case 'answer': text = 'answer from ' + e.from + ': ' + (e.text || '') + att; break;
      case 'sent': text = 'sent' + att; break;
      case 'fetched': text = 'fetched: ' + (e.text || ''); break;
      case 'ready': text = 'ready: ' + (e.me || '') + ' in ' + (e.group || ''); break;
      default: text = line;
    }
    return { text, fetch: e.att_id ? { id: e.att_id, sha: e.att_sha256 || '' } : null };
  }

  let natsCarry = '';

  function showNatsOutput(out, text) {
    natsCarry += text;
    const lines = natsCarry.split('\n');
    natsCarry = lines.pop();
    for (const line of lines) {
      if (!line.startsWith('{')) { out.appendChild(document.createTextNode(line + '\n')); continue; }
      const r = natsText(line);
      out.appendChild(document.createTextNode(r.text + '\n'));
      if (r.fetch) {
        const b = document.createElement('button');
        b.type = 'button';
        b.textContent = 'Fetch this file';
        b.addEventListener('click', () => { $('nats-id').value = r.fetch.id; $('nats-sha').value = r.fetch.sha; show('nats'); $('nats-id').focus(); });
        out.appendChild(b);
        out.appendChild(document.createTextNode('\n'));
      }
    }
  }

  // ---- runs: viper itself, started by the server; the page attaches to its output ----
  let source = null;
  let runs = [];
  let fetching = false;
  let current = '';
  let runState = '';

  async function startRun(req) {
    const repo = $('repo').value.trim();
    if (repo && req.action !== 'down' && req.action !== 'nats') { req.repo = repo; }
    try {
      const r = await api('/api/run', req);
      show('runs');
      await loadRuns();
      attach(r.id);
    } catch (e) {
      show('runs');
      $('run-status').textContent = 'Not started: ' + e.message;
      $('run-status').className = 'bad';
    }
  }

  async function loadRuns() {
    let r;
    try { r = await api('/api/runs'); } catch (e) { return; }
    runs = r.runs;
    const ul = $('run-list');
    ul.replaceChildren();
    for (const run of r.runs) {
      const li = document.createElement('li');
      const b = document.createElement('button');
      b.type = 'button';
      b.textContent = run.action + ' ' + (run.machine || run.group) + ' (' + run.state + (run.state === 'done' ? ', exit ' + run.code : '') + ')';
      b.setAttribute('aria-pressed', String(run.id === current));
      b.addEventListener('click', () => attach(run.id));
      li.appendChild(b);
      ul.appendChild(li);
    }
  }

  function attach(id) {
    if (source) { source.close(); }
    current = id;
    $('run-output').textContent = '';
    source = new EventSource('/api/events?id=' + encodeURIComponent(id));
    natsCarry = '';
    $('run-download').hidden = true;
    const isNats = (runs.find((r) => r.id === id) || {}).action === 'nats';
    const action = (runs.find((r) => r.id === id) || {}).action;
    source.addEventListener('out', (ev) => {
      const e = JSON.parse(ev.data);
      const out = $('run-output');
      if (isNats) { showNatsOutput(out, e.text); } else { out.appendChild(document.createTextNode(e.text)); }
      while (out.childNodes.length > 5000) { out.removeChild(out.firstChild); }
      out.scrollTop = out.scrollHeight;
    });
    source.addEventListener('state', (ev) => {
      const st = JSON.parse(ev.data);
      runState = st.state;
      $('run-status').textContent = statusText(st);
      $('run-status').className = st.state === 'done' ? (st.code === 0 ? 'ok' : 'bad') : '';
      $('run-form').hidden = st.state !== 'waiting';
      $('run-cancel').disabled = st.state === 'done' || st.state === 'cancelling';
      if (st.state === 'waiting') { $('run-answer').focus(); }
      if (st.state === 'done') {
        if (action === 'nats' && st.code === 0 && fetching) { $('run-download-link').href = '/api/download?id=' + encodeURIComponent(id); $('run-download').hidden = false; }
        source.close(); source = null; load();
      }
      loadRuns();
    });
  }

  function statusText(st) {
    switch (st.state) {
      case 'running': return 'Running ...';
      case 'waiting': return 'Waiting for your answer';
      case 'cancelling': return 'Ending (this can take a while) ...';
      default: return st.code === 0 ? 'Done: exit 0' : 'Ended: exit ' + st.code + (st.code === 130 ? ' (cancelled)' : '');
    }
  }

  $('run-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const text = $('run-answer').value;
    $('run-answer').value = '';
    try { await api('/api/answer', { id: current, text }); } catch (e) { $('run-status').textContent = e.message; }
  });
  $('run-cancel').addEventListener('click', async () => {
    try { await api('/api/cancel', { id: current }); } catch (e) { $('run-status').textContent = e.message; }
  });

  function show(page) {
    for (const p of ['overview', 'runs', 'nats', 'logs']) {
      $('page-' + p).hidden = p !== page;
      $('tab-' + p).setAttribute('aria-pressed', String(p === page));
    }
    if (page === 'logs') { loadLog(); }
    if (page === 'runs') { loadRuns(); }
  }

  $('tab-overview').addEventListener('click', () => show('overview'));
  $('tab-runs').addEventListener('click', () => show('runs'));
  $('tab-nats').addEventListener('click', () => show('nats'));
  $('tab-logs').addEventListener('click', () => show('logs'));
  $('nats-group').addEventListener('change', natsGroupChanged);
  $('nats-kind').addEventListener('change', natsKindChanged);
  $('nats-peers').addEventListener('click', () => natsRun({ sub: 'peers', timeout: Number($('nats-timeout').value) || 5 }));
  $('nats-topics').addEventListener('click', () => natsRun({ sub: 'topics', timeout: Number($('nats-timeout').value) || 5 }));
  $('nats-listen').addEventListener('click', () => natsRun({ sub: 'listen', topic: $('nats-listen-topic').value.trim(), all: $('nats-listen-all').checked }));
  $('nats-send').addEventListener('submit', natsSend);
  $('nats-fetch').addEventListener('submit', (ev) => { ev.preventDefault(); natsRun({ sub: 'fetch', id: $('nats-id').value.trim(), sha256: $('nats-sha').value.trim().toLowerCase() }); });
  $('log-reload').addEventListener('click', loadLog);
  $('log-machine').addEventListener('change', loadLog);
  $('log-full').addEventListener('change', loadLog);
  $('log-filter').addEventListener('input', filterLog);
  load();
})();
