'use strict';
// The page of viper ui. Everything that comes from the server is set as text (textContent), never as markup. The texts of
// the page are in i18n.js.
(function () {
  const $ = (id) => document.getElementById(id);
  const { t } = window.viperI18n;
  const PAGES = ['overview', 'runs', 'nats', 'settings', 'logs'];
  let overview = { machines: [], groups: [], nats: [] };
  let logText = '';
  let page = 'overview';

  function banner(text) {
    $('banner').textContent = text || '';
    $('banner').hidden = !text;
  }

  async function api(path, body) {
    const opts = body === undefined
      ? { credentials: 'same-origin' }
      : { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Viper-UI': '1' }, body: JSON.stringify(body) };
    const res = await fetch(path, opts);
    let data = {};
    try { data = await res.json(); } catch (e) { data = {}; }
    if (res.status === 403) { banner(t('banner.session')); }
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

  function button(text, label, onclick, disabled) {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = text;
    if (label) { b.setAttribute('aria-label', label); }
    b.disabled = !!disabled;
    b.addEventListener('click', onclick);
    return b;
  }

  function addOption(sel, value, text) {
    const o = document.createElement('option');
    o.value = value;
    o.textContent = text;
    sel.appendChild(o);
  }

  const ACTIONS = ['preflight', 'up', 'down'];

  function renderMachines() {
    const body = $('machines').tBodies[0];
    body.replaceChildren();
    for (const m of overview.machines) {
      const tr = document.createElement('tr');
      cell(tr, m.name); cell(tr, m.group); cell(tr, m.provider); cell(tr, m.type); cell(tr, m.location); cell(tr, m.nats);
      cell(tr, m.error ? t('error.prefix', m.error) : stateText(m.state), m.error ? 'bad' : 'state-' + m.state);
      const act = cell(tr, '');
      const check = button(t('btn.check'), t('aria.check', m.name), () => runCheck(m, check), !!m.error);
      act.appendChild(check);
      for (const a of ACTIONS) {
        act.appendChild(button(t('btn.' + a), t('aria.' + a, m.name), () => startRun({ action: a, machine: m.name, group: m.group || '' }), !!m.error || overview.readonly));
      }
      act.appendChild(button(t('btn.log'), t('aria.log', m.name), () => { show('logs'); $('log-machine').value = m.name; loadLog(); }));
      body.appendChild(tr);
    }
    $('overview-note').textContent = overview.machines.length === 0 ? t('note.none') : t('note.state');
  }

  function stateText(s) { return s ? t('state.' + s) : ''; }

  function renderGroups() {
    const body = $('groups').tBodies[0];
    body.replaceChildren();
    for (const g of overview.groups) {
      const tr = document.createElement('tr');
      cell(tr, g.name);
      cell(tr, g.error ? t('error.prefix', g.error) : (g.machines || []).join(', '), g.error ? 'bad' : '');
      const act = cell(tr, '');
      for (const a of ACTIONS) {
        act.appendChild(button(t('btn.group.' + a), t('aria.group.' + a, g.name), () => startRun({ action: a, group: g.name, scope: 'group' }), !!g.error || overview.readonly));
      }
      body.appendChild(tr);
    }
  }

  function renderLogChoices() {
    const sel = $('log-machine');
    const keep = sel.value;
    sel.replaceChildren();
    addOption(sel, '', t('log.all'));
    const seen = new Set();
    for (const m of overview.machines) {
      if (!seen.has(m.name)) { seen.add(m.name); addOption(sel, m.name, m.name); }
    }
    sel.value = keep;
  }

  function renderVersion() {
    $('version').textContent = (overview.version || '') + (overview.readonly ? ' (' + t('readonly') + ')' : '');
  }

  async function load() {
    try {
      overview = await api('/api/overview');
    } catch (e) {
      $('overview-note').textContent = t('note.load', e.message);
      return;
    }
    renderAll();
  }

  function renderAll() {
    renderVersion();
    renderMachines();
    renderGroups();
    renderLogChoices();
    renderNats();
    if (!checked) { $('check-status').textContent = t('check.hint'); }
  }

  let checked = false;

  async function runCheck(m, btn) {
    checked = true;
    btn.disabled = true;
    $('check-status').className = 'muted';
    $('check-status').textContent = t('check.running', m.name);
    $('check-output').textContent = '';
    try {
      const r = await api('/api/check', { machine: m.name, group: m.group || '' });
      $('check-status').textContent = r.ok ? t('check.ok', m.name) : t('check.problems', m.name, r.code);
      $('check-status').className = r.ok ? 'ok' : 'bad';
      $('check-output').textContent = r.output + (r.truncated ? '\n' + t('cut') : '');
    } catch (e) {
      $('check-status').textContent = t('check.error', m.name, e.message);
      $('check-status').className = 'bad';
    } finally {
      btn.disabled = false;
    }
  }

  async function loadLog() {
    const machine = $('log-machine').value;
    const full = $('log-full').checked && machine !== '';
    $('log-status').textContent = t('log.loading');
    try {
      const q = new URLSearchParams();
      if (machine) { q.set('machine', machine); }
      if (full) { q.set('full', '1'); }
      const r = await api('/api/log?' + q.toString());
      logText = r.text + (r.truncated ? '\n' + t('cut') : '');
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

  // ---- settings: names, and the values the server shows ----
  async function loadSettings() {
    const box = $('settings-files');
    let r;
    try { r = await api('/api/settings'); } catch (e) { box.textContent = e.message; return; }
    box.replaceChildren();
    if (!r.files || r.files.length === 0) { box.textContent = t('settings.empty'); return; }
    for (const f of r.files) {
      const h = document.createElement('h3');
      h.className = 'file-title';
      h.textContent = f.name === 'environment' ? t('settings.env') : f.name;
      box.appendChild(h);
      if (f.name === 'environment') {
        const p = document.createElement('p');
        p.className = 'muted';
        p.textContent = t('settings.envnote');
        box.appendChild(p);
      }
      if (f.error) { const p = document.createElement('p'); p.className = 'bad'; p.textContent = t('error.prefix', f.error); box.appendChild(p); continue; }
      if (!f.settings.length) { const p = document.createElement('p'); p.className = 'muted'; p.textContent = t('settings.noentries'); box.appendChild(p); continue; }
      const table = document.createElement('table');
      const cap = document.createElement('caption');
      cap.className = 'sr';
      cap.textContent = f.name === 'environment' ? t('settings.env') : f.name;
      table.appendChild(cap);
      const head = table.createTHead().insertRow();
      for (const k of ['th.key', 'th.value']) { const th = document.createElement('th'); th.scope = 'col'; th.textContent = t(k); head.appendChild(th); }
      const body = table.createTBody();
      for (const s of f.settings) {
        const tr = body.insertRow();
        cell(tr, s.key);
        cell(tr, s.shown ? s.value : t('settings.set'), s.shown ? '' : 'muted');
      }
      box.appendChild(table);
    }
  }

  // ---- NATS: viper nats as runs; the page only builds the request ----
  function natsGroup() { return (overview.nats || []).find((g) => g.name === $('nats-group').value); }

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

  function natsGroupChanged() {
    const g = natsGroup();
    if (!g) { return; }
    const from = $('nats-from');
    const to = $('nats-to');
    const keepFrom = from.value;
    const keepTo = to.value;
    from.replaceChildren();
    to.replaceChildren();
    addOption(from, '', g.operator ? t('nats.from.operator') : t('nats.from.machine'));
    for (const m of g.machines) { addOption(from, m, t('nats.machine', m)); addOption(to, m, m); }
    if (g.machines.includes(keepFrom)) { from.value = keepFrom; }
    if (g.machines.includes(keepTo)) { to.value = keepTo; }
    $('nats-mode').textContent = g.operator ? t('nats.mode.operator') : t('nats.mode.machine', g.name);
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
        status.textContent = t('nats.handing');
        const res = await fetch('/api/upload?kind=' + fileKind(file) + '&name=' + encodeURIComponent(file.name), {
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
    const att = e.att_id ? t('nl.file', e.att_name || '', e.att_type || '', e.att_size || 0) : '';
    let text;
    switch (e.event) {
      case 'peer': text = t('nl.peer', e.from) + (e.text ? t('nl.peer.topics', e.text) : ''); break;
      case 'configured': text = t('nl.configured', e.from, e.text || t('nl.notopic')); break;
      case 'message': text = (e.via === 'pub' ? '#' + e.topic + ' ' : '') + e.from + (e.to ? ' -> ' + e.to : '') + ': ' + (e.text || '') + att; break;
      case 'answer': text = t('nl.answer', e.from, e.text || '') + att; break;
      case 'sent': text = t('nl.sent') + att; break;
      case 'fetched': text = t('nl.fetched', e.text || ''); break;
      case 'ready': text = t('nl.ready', e.me || '', e.group || ''); break;
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
        out.appendChild(button(t('nats.fetchthis'), '', () => { $('nats-id').value = r.fetch.id; $('nats-sha').value = r.fetch.sha; show('nats'); $('nats-id').focus(); }));
        out.appendChild(document.createTextNode('\n'));
      }
    }
  }

  // ---- runs: viper itself, started by the server; the page attaches to its output ----
  let source = null;
  let runs = [];
  let fetching = false;
  let current = '';

  async function startRun(req) {
    const repo = $('repo').value.trim();
    if (repo && req.action !== 'down' && req.action !== 'nats') { req.repo = repo; }
    $('run-status').textContent = t('status.running');
    $('run-status').className = '';
    try {
      const r = await api('/api/run', req);
      show('runs');
      await loadRuns();
      attach(r.id);
    } catch (e) {
      show('runs');
      $('run-status').textContent = t('run.notstarted', e.message);
      $('run-status').className = 'bad';
    }
  }

  function runLabel(run) {
    const st = run.state === 'done' ? t('run.state.done', run.code) : t('run.state.' + run.state);
    return t('run.item', run.action, run.machine || run.group || run.nats || '', st);
  }

  async function loadRuns() {
    let r;
    try { r = await api('/api/runs'); } catch (e) { return; }
    runs = r.runs;
    const ul = $('run-list');
    ul.replaceChildren();
    if (runs.length === 0) { const li = document.createElement('li'); li.className = 'muted'; li.textContent = t('runs.none'); ul.appendChild(li); }
    for (const run of runs) {
      const li = document.createElement('li');
      const b = button(runLabel(run), '', () => attach(run.id));
      b.setAttribute('aria-pressed', String(run.id === current));
      li.appendChild(b);
      ul.appendChild(li);
    }
  }

  function attach(id) {
    if (source) { source.close(); }
    current = id;
    $('run-output').textContent = '';
    $('run-status').textContent = t('status.running'); // not the end of the run that was shown before
    $('run-status').className = '';
    $('run-form').hidden = true;
    source = new EventSource('/api/events?id=' + encodeURIComponent(id));
    natsCarry = '';
    $('run-download').hidden = true;
    const action = (runs.find((r) => r.id === id) || {}).action;
    source.addEventListener('out', (ev) => {
      const e = JSON.parse(ev.data);
      const out = $('run-output');
      if (action === 'nats') { showNatsOutput(out, e.text); } else { out.appendChild(document.createTextNode(e.text)); }
      while (out.childNodes.length > 5000) { out.removeChild(out.firstChild); }
      out.scrollTop = out.scrollHeight;
    });
    source.addEventListener('state', (ev) => {
      const st = JSON.parse(ev.data);
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
      case 'running': return t('status.running');
      case 'waiting': return t('status.waiting');
      case 'cancelling': return t('status.cancelling');
      default: return st.code === 0 ? t('status.done0') : t('status.ended', st.code) + (st.code === 130 ? t('status.cancelled') : '');
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

  // ---- pages and tabs: a tablist with arrow keys, as the pattern says ----
  function show(name) {
    page = name;
    for (const p of PAGES) {
      const on = p === name;
      $('page-' + p).hidden = !on;
      $('tab-' + p).setAttribute('aria-selected', String(on));
      $('tab-' + p).tabIndex = on ? 0 : -1;
    }
    if (name === 'logs') { loadLog(); }
    if (name === 'runs') { loadRuns(); }
    if (name === 'settings') { loadSettings(); }
  }

  $('tabs').addEventListener('keydown', (ev) => {
    const i = PAGES.indexOf(page);
    const next = { ArrowRight: (i + 1) % PAGES.length, ArrowLeft: (i + PAGES.length - 1) % PAGES.length, Home: 0, End: PAGES.length - 1 }[ev.key];
    if (next === undefined) { return; }
    ev.preventDefault();
    show(PAGES[next]);
    $('tab-' + PAGES[next]).focus();
  });
  for (const p of PAGES) { $('tab-' + p).addEventListener('click', () => show(p)); }

  function translate() {
    window.viperI18n.apply();
    $('lang').value = window.viperI18n.lang();
    renderAll();
    if (page === 'settings') { loadSettings(); }
    if (page === 'runs') { loadRuns(); }
  }

  $('lang').addEventListener('change', () => { window.viperI18n.setLang($('lang').value); translate(); });
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
  window.viperI18n.apply();
  $('lang').value = window.viperI18n.lang();
  load();
})();
