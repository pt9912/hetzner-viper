'use strict';
// The page of viper ui. Everything that comes from the server is set as text (textContent), never as markup. The texts of
// the page are in i18n.js.
(function () {
  const $ = (id) => document.getElementById(id);
  const { t } = window.viperI18n;
  const PAGES = ['overview', 'runs', 'nats', 'find', 'settings', 'change', 'secrets', 'create', 'logs'];
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
    if (res.status === 403 && !data.error) { banner(t('banner.session')); } // the answer of the guard has no JSON; a refusal of an action has
    if (!res.ok) { const err = new Error(data.error || ('HTTP ' + res.status)); err.status = res.status; throw err; }
    return data;
  }

  function cell(row, text, cls) {
    const td = document.createElement('td');
    td.textContent = text || '';
    if (cls) { td.className = cls; }
    row.appendChild(td);
    return td;
  }

  // kind: primary (the likely next step), quiet (there, but not asking for attention), danger (what deletes or stops)
  function button(text, label, onclick, disabled, kind) {
    const b = document.createElement('button');
    b.type = 'button';
    if (kind) { b.className = kind; }
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

  // The overview is the groups first, each a rack with its machines as units; the machines without a group come last. A machine
  // that is in several groups stands under each of them.
  // ---- filter and sort of the units (the groups stay by name, a group stays together) ----
  let sortKey = '';
  let sortDir = 1; // 1 ascending, -1 descending
  const STATE_RANK = { ready: 0, started: 1, none: 2 };

  function sortValue(m, key) {
    if (key === 'state') { return STATE_RANK[m.state || 'none']; }
    return (m[key] || '').toLowerCase();
  }

  function sortedUnits(units) {
    if (!sortKey) { return units; }
    return units.slice().sort((a, b) => {
      const x = sortValue(a, sortKey);
      const y = sortValue(b, sortKey);
      const c = typeof x === 'number' ? x - y : String(x).localeCompare(String(y), undefined, { numeric: true });
      return c * sortDir || a.name.localeCompare(b.name);
    });
  }

  function filterTerms() { return $('f-text').value.toLowerCase().split(/\s+/).filter(Boolean); }

  function hay(m) { return [m.name, m.group, m.provider, m.type, m.location, m.nats, stateText(m.state)].join(' ').toLowerCase(); }

  // the units of a group that the filter lets through: a group whose name fits shows all its machines (of the state asked for)
  function visibleUnits(g, members) {
    const terms = filterTerms();
    const st = $('f-state').value;
    const groupHit = !!g && terms.length > 0 && terms.every((x) => g.name.toLowerCase().includes(x));
    return members.filter((m) => (!st || (m.state || 'none') === st) && (terms.length === 0 || groupHit || terms.every((x) => hay(m).includes(x))));
  }

  function filtering() { return filterTerms().length > 0 || $('f-state').value !== ''; }

  function renderSortState() {
    for (const th of document.querySelectorAll('#machines thead th')) {
      const b = th.querySelector('button.sort');
      if (!b) { continue; }
      const on = b.dataset.sort === sortKey;
      th.setAttribute('aria-sort', on ? (sortDir === 1 ? 'ascending' : 'descending') : 'none');
    }
  }

  // the table is built anew when the overview changes: the button that has the focus has it again afterwards (found by its name)
  function focusKey() {
    const el = document.activeElement;
    if (!el || !$('machines').contains(el)) { return null; }
    if (el.dataset.sort) { return { sort: el.dataset.sort }; }
    const tr = el.closest('tr');
    return { label: el.getAttribute('aria-label') || el.textContent, row: tr ? tr.dataset.row || '' : '' };
  }

  function restoreFocus(key) {
    if (!key) { return; }
    const els = Array.from($('machines').querySelectorAll('button'));
    const same = (b) => { const tr = b.closest('tr'); return (!tr ? '' : tr.dataset.row || '') === key.row; }; // the row of a machine, also when it stands in several groups
    const hit = els.find((b) => (key.sort ? b.dataset.sort === key.sort : (b.getAttribute('aria-label') || b.textContent) === key.label && same(b)));
    if (hit && !hit.disabled) { hit.focus(); return; }
    // a button that is off now (a run holds the machine): the name of the machine in that row takes the focus
    const row = key.sort ? null : Array.from($('machines').querySelectorAll('tr')).find((tr) => (tr.dataset.row || '') === key.row && key.row !== '');
    const name = row && row.querySelector('button.namebtn');
    if (name) { name.focus(); }
  }

  function renderMachines() {
    const key = focusKey();
    renderMachinesBody();
    restoreFocus(key);
  }

  function renderMachinesBody() {
    const table = $('machines');
    for (const old of Array.from(table.tBodies)) { old.remove(); }
    const loose = [];
    const byGroup = new Map();
    for (const m of overview.machines) {
      if (m.group) { if (!byGroup.has(m.group)) { byGroup.set(m.group, []); } byGroup.get(m.group).push(m); } else { loose.push(m); }
    }
    let shown = 0;
    const add = (g, members) => {
      const units = sortedUnits(visibleUnits(g, members));
      shown += units.length;
      const groupNameFits = !!g && filterTerms().length > 0 && filterTerms().every((x) => g.name.toLowerCase().includes(x));
      // with a filter a group without a visible machine is left out (an empty group shows when its name is asked for)
      if (!filtering() || units.length > 0 || (g && g.error && filterTerms().length === 0) || (groupNameFits && $('f-state').value === '' && members.length === 0)) {
        table.appendChild(rack(g, units, filtering()));
      }
    };
    for (const g of overview.groups) { add(g, byGroup.get(g.name) || []); byGroup.delete(g.name); }
    for (const rest of byGroup.values()) { loose.push(...rest); } // of a group that the overview does not list
    if (loose.length > 0) { add(null, loose); }
    const total = overview.machines.length;
    $('f-count').textContent = filtering() ? (shown === 0 ? t('filter.none') : t('filter.count', shown, total)) : '';
    $('f-clear').hidden = !filtering();
    renderSortState();
    $('overview-note').textContent = overview.machines.length === 0 && overview.groups.length === 0 ? t('note.none') : t('note.state');
  }

  // one group (or the machines without one) as a body of the table: the head, then the units
  function rack(g, members, filtered) {
    const body = document.createElement('tbody');
    body.className = 'rack';
    if (g || overview.groups.length > 0) { body.appendChild(groupHead(g, members, filtered)); }
    if (!g || !g.error) { for (const m of members) { body.appendChild(unit(m)); } }
    return body;
  }

  // (the state of a group is that of all its machines, also when the filter shows some of them)
  function groupHead(g, members, filtered) {
    const tr = document.createElement('tr');
    tr.className = g ? 'grouphead real' : 'grouphead';
    const th = document.createElement('th');
    th.scope = 'rowgroup';
    th.colSpan = 5;
    let name;
    if (g) {
      name = button(g.name, t('aria.settings.group', g.name), () => goSettings('group', g.name, ''), false, 'namebtn gname');
    } else {
      name = document.createElement('span');
      name.className = 'gname';
      name.textContent = t('group.none');
    }
    th.appendChild(name);
    tr.appendChild(th);
    if (!g) { const td = document.createElement('td'); td.colSpan = 2; tr.appendChild(td); return tr; }
    if (g.error) { const td = cell(tr, t('error.prefix', g.error), 'bad'); td.colSpan = 2; return tr; }
    // the state of the group: what its machines say, as a lamp and a word, with how many of them are set up
    const count = (s) => members.filter((m) => (m.state || 'none') === s).length;
    if (filtered && g) { members = overview.machines.filter((m) => m.group === g.name); }
    const ready = count('ready');
    const kind = members.length === 0 || count('none') === members.length ? 'none' : ready === members.length ? 'ready' : 'partial';
    // a group that is only partly there says how many machines are in which state
    const detail = ['ready', 'started', 'none'].filter((s) => count(s) > 0).map((s) => count(s) + ' ' + t('state.' + s)).join(', ');
    const td = document.createElement('td');
    const box = document.createElement('span');
    // machines that a run is busy with: the lamp of the group beats, and the word says what is done and with how many
    const busy = members.filter((m) => m.busy);
    box.className = 'state state-' + (kind === 'partial' ? 'started' : kind) + (busy.length ? ' is-busy' : '');
    const led = document.createElement('span');
    led.className = 'led';
    const word = document.createElement('span');
    const plain = members.length === 0 ? t('group.empty') : t(kind === 'partial' ? 'state.partial' : 'state.' + kind) + (kind === 'partial' ? ' (' + detail + ')' : '');
    const doing = new Set(busy.map((m) => m.busy));
    word.textContent = busy.length ? (doing.size === 1 ? t('busy.' + busy[0].busy) : t('busy.mixed')) + ' ' + busy.length + '/' + members.length + ' (' + plain + ')' : plain;
    box.append(led, word);
    td.appendChild(box);
    tr.appendChild(td);
    const act = cell(tr, '', 'actions');
    const kinds = { preflight: 'quiet', up: '', down: 'danger' };
    // the check of the whole group: viper check --group, as a run (its output has a table with a line for each machine)
    act.appendChild(button(t('btn.group.check'), t('aria.group.check', g.name), () => startRun({ action: 'check', group: g.name, scope: 'group' }), members.length === 0 || busy.length > 0, 'quiet'));
    for (const a of ACTIONS) {
      act.appendChild(button(t('btn.group.' + a), t('aria.group.' + a, g.name), () => startRun({ action: a, group: g.name, scope: 'group' }), members.length === 0 || overview.readonly || busy.length > 0, kinds[a]));
    }
    if (overview.edit) { act.appendChild(button(t('btn.dup'), t('aria.group.dup', g.name), () => duplicate('group', g.name), !!g.error, 'quiet')); }
    return tr;
  }

  // the name of a machine is a button to its settings
  function nameCell(tr, m) {
    const td = document.createElement('td');
    td.appendChild(button(m.name, t('aria.settings', m.name), () => goSettings('machine', m.name, m.group || ''), false, 'namebtn'));
    tr.appendChild(td);
  }

  // a machine: one unit of the rack
  function unit(m) {
    const tr = document.createElement('tr');
    tr.dataset.row = m.name + '/' + (m.group || '');
    tr.className = 'unit ' + (m.error ? 'is-error' : 'is-' + (m.state || 'none')); // the stripe at the edge has the colour of the state
    nameCell(tr, m);
    cell(tr, m.provider); cell(tr, m.type); cell(tr, m.location); cell(tr, m.nats);
    stateCell(tr, m);
    const act = cell(tr, '', 'actions');
    // the likely next step is filled: a machine that is not there is made, one that is there is looked at
    const there = m.state === 'ready' || m.state === 'started';
    const check = button(t('btn.check'), t('aria.check', m.name), () => runCheck(m, check), !!m.error || checking.has(m.name + '/' + (m.group || '')), there ? 'primary' : '');
    act.appendChild(check);
    const kinds = { preflight: 'quiet', up: there ? '' : 'primary', down: 'danger' };
    for (const a of ACTIONS) {
      act.appendChild(button(t('btn.' + a), t('aria.' + a, m.name), () => startRun({ action: a, machine: m.name, group: m.group || '' }), !!m.error || overview.readonly || !!m.busy, kinds[a]));
    }
    act.appendChild(button(t('btn.log'), t('aria.log', m.name), () => { $('log-machine').value = m.name; show('logs'); }, false, 'quiet'));
    if (overview.edit) { act.appendChild(button(t('btn.dup'), t('aria.dup', m.name), () => duplicate('machine', m.name), !!m.error, 'quiet')); }
    return tr;
  }

  function stateText(s) { return s ? t('state.' + s) : ''; }

  // the state is a lamp and a word (never a colour alone)
  function stateCell(tr, m) {
    const td = document.createElement('td');
    const box = document.createElement('span');
    if (m.error) {
      box.className = 'bad';
      box.textContent = t('error.prefix', m.error);
    } else {
      box.className = 'state state-' + (m.state || 'none') + (m.busy ? ' is-busy' : '');
      const led = document.createElement('span');
      led.className = 'led';
      const word = document.createElement('span');
      // a run that goes on with the machine: the lamp beats, and the word says what is done (the state follows with the marks)
      word.textContent = m.busy ? t('busy.' + m.busy) + ' (' + stateText(m.state) + ')' : stateText(m.state);
      box.append(led, word);
    }
    td.appendChild(box);
    tr.appendChild(td);
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
    $('version').textContent = (overview.version || '') + (overview.readonly ? ' (' + t('readonly') + ')' : '') + (overview.edit ? ' (' + t('editing') + ')' : '');
  }

  async function load() {
    try {
      overview = await api('/api/overview');
    } catch (e) {
      $('overview-note').textContent = t('note.load', e.message);
      return false;
    }
    renderAll();
    return true;
  }

  function renderAll() {
    renderVersion();
    showActiveOptions();
    $('opt-force').disabled = $('opt-registry').disabled = !!overview.readonly; // down is no action of a page that is read only
    $('tab-create').hidden = !overview.edit;
    $('tab-secrets').hidden = !overview.edit;
    $('tab-change').hidden = !overview.edit;
    if (!overview.edit && (page === 'create' || page === 'secrets' || page === 'change')) { show('overview'); }
    renderMachines();
    renderLogChoices();
    renderNats();
    if (!checked) { $('check-status').textContent = t('check.hint'); }
  }

  let checked = false;

  const checking = new Set(); // the machines whose check goes on (the table is built anew meanwhile: the set, not the button, knows)
  async function runCheck(m, btn) {
    checked = true;
    const ck = m.name + '/' + (m.group || '');
    checking.add(ck);
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
      checking.delete(ck);
      btn.disabled = false;
      renderMachines(); // the button of this machine is on again
    }
  }

  let logSeq = 0;

  async function loadLog() {
    const seq = ++logSeq; // an answer that comes late must not replace a newer one
    const machine = $('log-machine').value;
    const full = $('log-full').checked && machine !== '';
    $('log-status').textContent = t('log.loading');
    try {
      const q = new URLSearchParams();
      if (machine) { q.set('machine', machine); }
      if (full) { q.set('full', '1'); }
      const r = await api('/api/log?' + q.toString());
      if (seq !== logSeq) { return; }
      logText = r.text + (r.truncated ? '\n' + t('cut') : '');
      $('log-status').textContent = '';
      filterLog();
    } catch (e) {
      if (seq !== logSeq) { return; }
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
  // The list has the files (a block each that opens); the settings of a file are asked for when it opens, 200 at a time, and a
  // search asks the server for the settings of all files by their name: neither the server nor the page has all of it at once.
  const settingsPage = 200;

  function fileTitle(name) { return name === 'environment' ? t('settings.env') : name; }

  function settingsTable(name, list) {
    const table = document.createElement('table');
    const cap = document.createElement('caption');
    cap.className = 'sr';
    cap.textContent = fileTitle(name);
    table.appendChild(cap);
    const head = table.createTHead().insertRow();
    for (const k of ['th.key', 'th.value']) { const th = document.createElement('th'); th.scope = 'col'; th.textContent = t(k); head.appendChild(th); }
    table.createTBody();
    addSettingRows(table, list);
    return table;
  }

  function addSettingRows(table, list) {
    const body = table.tBodies[0];
    for (const st of list) {
      const tr = body.insertRow();
      cell(tr, st.key);
      cell(tr, st.shown ? st.value : t('settings.set'), st.shown ? '' : 'muted');
    }
  }

  // loadFile fills the block of a file with a page of its settings (from offset on), and a button for the rest
  async function loadFile(det, f, offset) {
    const body = det.querySelector('.file-body');
    const pressed = body.querySelector('button.more');
    if (pressed) { pressed.disabled = true; } // a second click would ask for the same page
    let r;
    try { r = await api('/api/settings?file=' + encodeURIComponent(f.name) + '&offset=' + offset + '&limit=' + settingsPage); } catch (e) { if (pressed) { pressed.disabled = false; } body.append(document.createTextNode(e.message)); return; }
    const list = (r.file && r.file.settings) || [];
    if (offset === 0) { body.replaceChildren(); }
    if (r.file && r.file.error) { const p = document.createElement('p'); p.className = 'bad'; p.textContent = t('error.prefix', r.file.error); body.appendChild(p); return; }
    if (offset === 0 && list.length === 0) { const p = document.createElement('p'); p.className = 'muted'; p.textContent = t('settings.noentries'); body.appendChild(p); return; }
    let table = body.querySelector('table');
    if (!table) { table = settingsTable(f.name, []); body.appendChild(table); }
    addSettingRows(table, list);
    const old = body.querySelector('button.more');
    if (old) { old.remove(); }
    const shown = r.offset + list.length;
    if (shown < r.file.count) {
      const left = r.file.count - shown;
      const more = button(t('settings.more', Math.min(settingsPage, left), left), '', () => loadFile(det, f, shown), false, 'quiet more');
      body.appendChild(more);
    }
  }

  // the block of a file: closed, with its name and how many settings it has; the settings come when it opens
  function fileBlock(f, open) {
    const det = document.createElement('details');
    det.className = 'file';
    det.dataset.file = f.name;
    const sum = document.createElement('summary');
    sum.className = 'file-title';
    sum.textContent = fileTitle(f.name) + (f.error ? '' : ' (' + t('settings.count', f.count) + ')');
    det.appendChild(sum);
    if (f.name === 'environment') {
      const p = document.createElement('p');
      p.className = 'muted file-note';
      p.textContent = t('settings.envnote');
      det.appendChild(p);
    }
    const body = document.createElement('div');
    body.className = 'file-body';
    det.appendChild(body);
    if (f.error) { const p = document.createElement('p'); p.className = 'bad'; p.textContent = t('error.prefix', f.error); body.appendChild(p); det.open = open; return det; }
    let loaded = false;
    det.loadOnce = () => { if (!loaded) { loaded = true; loadFile(det, f, 0); } };
    det.addEventListener('toggle', () => { if (det.open) { det.loadOnce(); } });
    det.open = open;
    return det;
  }

  let settingsSeq = 0;
  async function loadSettings() {
    const mine = ++settingsSeq;
    const box = $('settings-files');
    const wasOpen = new Set(Array.from(box.querySelectorAll('details.file[open]')).map((d) => d.dataset.file));
    let r;
    try { r = await api('/api/settings'); } catch (e) { box.textContent = e.message; return; }
    if (mine !== settingsSeq) { return; }
    box.replaceChildren();
    if (!r.files || r.files.length === 0) { box.textContent = t('settings.empty'); return; }
    const first = wasOpen.size === 0 && !box.dataset.shown; // the first time: .env.local is open
    box.dataset.shown = '1';
    for (const f of r.files) {
      const det = fileBlock(f, wasOpen.has(f.name) || (first && f.name === '.env.local') || f.name === pendingFile);
      box.appendChild(det);
      if (det.open && det.loadOnce) { det.loadOnce(); } // the blocks that were open stay open, and have their settings again
    }
    if (r.more > 0) { const p = document.createElement('p'); p.className = 'muted'; p.textContent = t('settings.morefiles', r.more); box.appendChild(p); }
    showPendingFile();
  }

  // the search: the settings of all files whose name has the word
  let searchTimer = 0;
  async function searchSettings() {
    const q = $('settings-q').value.trim();
    const hits = $('settings-hits');
    $('settings-files').hidden = q !== '';
    hits.hidden = q === '';
    if (q === '') { hits.replaceChildren(); return; }
    let r;
    try { r = await api('/api/settings?q=' + encodeURIComponent(q)); } catch (e) { hits.textContent = e.message; return; }
    if ($('settings-q').value.trim() !== q) { return; } // an older answer
    hits.replaceChildren();
    const p = document.createElement('p');
    p.setAttribute('role', 'status');
    p.textContent = t('settings.hits', r.hits.length) + (r.more ? ' ' + t('settings.hits.more') : '');
    hits.appendChild(p);
    if (r.hits.length === 0) { return; }
    const table = document.createElement('table');
    const cap = document.createElement('caption');
    cap.className = 'sr';
    cap.textContent = t('settings.q.label');
    table.appendChild(cap);
    const head = table.createTHead().insertRow();
    for (const k of ['th.file', 'th.key', 'th.value']) { const th = document.createElement('th'); th.scope = 'col'; th.textContent = t(k); head.appendChild(th); }
    const body = table.createTBody();
    for (const h of r.hits) {
      const tr = body.insertRow();
      cell(tr, h.file, 'mono');
      cell(tr, h.key);
      cell(tr, h.shown ? h.value : t('settings.set'), h.shown ? '' : 'muted');
    }
    hits.appendChild(table);
  }

  // ---- from the overview to the settings of a machine or group ----
  let pendingFile = ''; // the file the person asked for from the overview

  // settingsFileOf is the file whose settings a machine or group has: its own, else (a machine only a group names) the file of the
  // group, else .env.local
  async function settingsFileOf(kind, name, group) {
    let names = [];
    try { names = ((await api('/api/settings')).files || []).map((f) => f.name); } catch (e) { names = []; }
    if (kind === 'group') { return { path: 'groups/' + name + '.env', note: '' }; }
    const own = 'machines/' + name + '.env.local';
    if (names.includes(own)) { return { path: own, note: '' }; }
    if (group && names.includes('groups/' + group + '.env')) { return { path: 'groups/' + group + '.env', note: t('settings.viagroup', name, 'groups/' + group + '.env') }; }
    return { path: '.env.local', note: t('settings.viabase', name) };
  }

  async function goSettings(kind, name, group) {
    const r = await settingsFileOf(kind, name, group);
    pendingFile = r.path;
    show(overview.edit ? 'change' : 'settings');
    $(overview.edit ? 'ch-status' : 'settings-note').textContent = r.note;
  }

  // the file block of the settings page that was asked for gets the focus
  function showPendingFile() {
    if (!pendingFile || page !== 'settings') { return; }
    const d = Array.from(document.querySelectorAll('#settings-files details.file')).find((x) => x.dataset.file === pendingFile);
    pendingFile = '';
    if (d) { d.open = true; const sum = d.querySelector('summary'); sum.scrollIntoView({ block: 'start' }); sum.focus(); }
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

  // no action while the page is read only; and no file for a group that has no object store
  function natsEnabled() {
    const g = natsGroup();
    const off = !!overview.readonly || !g;
    for (const id of ['nats-peers', 'nats-topics', 'nats-listen', 'nats-timeout', 'nats-reqtimeout', 'nats-listen-topic', 'nats-listen-all', 'nats-kind', 'nats-to', 'nats-topic', 'nats-text', 'nats-yes', 'nats-id', 'nats-sha', 'nats-from']) { $(id).disabled = off; }
    for (const f of ['nats-send', 'nats-fetch']) { for (const el of $(f).querySelectorAll('button')) { el.disabled = off; } }
    // the operator: made (init) when this computer has no key, removed (revoke) when it has; the removal asks once more
    $('nats-init').hidden = !g || g.operator;
    $('nats-revoke').hidden = !g || !g.operator;
    $('nats-init').disabled = off;
    $('nats-revoke').disabled = off;
    if (!g || !g.operator) { disarmRevoke(); }
    const noFile = off || !g.file_max;
    $('nats-file').disabled = noFile;
    $('nats-file-clear').disabled = noFile;
    $('nats-nofile').hidden = !g || off || g.file_max > 0;
    if (!off) { natsKindChanged(); }
    listenState(!!listenId);
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
    natsEnabled();
  }

  function natsKindChanged() {
    const kind = $('nats-kind').value;
    if (overview.readonly) { return; }
    $('nats-reqtimeout').disabled = kind !== 'request';
    $('nats-to').disabled = kind === 'broadcast';
    $('nats-topic').disabled = kind !== 'broadcast';
  }

  async function natsRun(extra) {
    const g = natsGroup();
    if (!g) { return false; }
    const req = Object.assign({ action: 'nats', nats: g.name }, extra);
    if ($('nats-from').value) { req.from = $('nats-from').value; }
    return startRun(req);
  }

  let revokeArmed = false;
  function disarmRevoke() { revokeArmed = false; $('nats-revoke').textContent = t('nats.revoke'); }

  async function natsInit() { await natsRun({ sub: 'init' }); }

  // removing the operator deletes its key: the first click asks, the second does it
  async function natsRevoke() {
    if (!revokeArmed) { revokeArmed = true; $('nats-revoke').textContent = t('nats.revoke.sure'); return; }
    disarmRevoke();
    await natsRun({ sub: 'revoke' });
  }

  // ---- a file or a text from the mouse or the clipboard: it goes where the file dialog and the keyboard put it ----
  const MAX_TEXT = 16 << 10;

  function sizeText(n) { return n >= 1 << 20 ? Math.round(n / (1 << 20)) + ' MiB' : Math.max(1, Math.ceil(n / 1024)) + ' KiB'; }

  // what a file is for: a file that goes with the message (the kinds the command takes), a text for the message, or nothing
  function fileUse(f) {
    const n = (f.name || '').toLowerCase();
    if (n.endsWith('.pdf') || f.type === 'application/pdf' || n.endsWith('.zip') || f.type === 'application/zip' || f.type === 'application/x-zip-compressed' || f.type.startsWith('image/')) { return 'file'; }
    if (f.type.startsWith('text/') || /\.(txt|md|log|json|csv)$/.test(n)) { return 'text'; }
    return 'none';
  }

  function nameOf(f) {
    if (f.name) { return f; }
    const ext = { 'image/png': 'png', 'image/jpeg': 'jpg', 'image/gif': 'gif', 'image/webp': 'webp' }[f.type] || 'bin';
    return new File([f], 'pasted.' + ext, { type: f.type });
  }

  function addText(text) {
    const box = $('nats-text');
    let t = box.value ? box.value + '\n' + text : text;
    let cut = false;
    if (new TextEncoder().encode(t).length > MAX_TEXT) { t = t.slice(0, MAX_TEXT); cut = true; }
    box.value = t;
    return cut;
  }

  // acceptFiles takes what was dropped or pasted: the first file for the message, a text file as text; it says what it did
  async function acceptFiles(files) {
    const status = $('nats-send-status');
    const g = natsGroup();
    const notes = [];
    let attached = false;
    let more = false;
    for (const f0 of files) {
      const f = nameOf(f0);
      const use = fileUse(f);
      if (use === 'text' && f.size <= MAX_TEXT * 4) {
        const cut = addText(await f.text());
        notes.push(t('nats.texttaken', f.name) + (cut ? ' (' + t('nats.texttoolong') + ')' : ''));
      } else if (use === 'file' && !g.file_max) {
        notes.push(t('nats.nofile'));
      } else if (use === 'file' && f.size > g.file_max) {
        notes.push(t('nats.toolarge', f.name, sizeText(g.file_max)));
      } else if (use === 'file' && !attached) {
        const dt = new DataTransfer();
        dt.items.add(f);
        $('nats-file').files = dt.files;
        attached = true;
        notes.push(t('nats.taken', f.name));
      } else if (use === 'file') {
        more = true;
      } else {
        notes.push(t('nats.unsupported', f.name || f.type));
      }
    }
    if (more) { notes.push(t('nats.more')); }
    status.textContent = notes.join(' · ');
  }

  function natsFormUsable() {
    return !$('page-nats').hidden && !overview.readonly && !!natsGroup() && !$('nats-text').disabled;
  }

  function hasFiles(ev) { return ev.dataTransfer && Array.from(ev.dataTransfer.types || []).includes('Files'); }

  function dropSetup() {
    const zone = $('nats-send');
    // a file that is dropped beside the form must not make the browser leave the page
    for (const type of ['dragover', 'drop']) { document.addEventListener(type, (ev) => { if (hasFiles(ev)) { ev.preventDefault(); } }); }
    for (const type of ['dragenter', 'dragover']) {
      zone.addEventListener(type, (ev) => { if (natsFormUsable() && (hasFiles(ev) || (ev.dataTransfer && ev.dataTransfer.types.includes('text/plain')))) { ev.preventDefault(); zone.classList.add('dragging'); } });
    }
    zone.addEventListener('dragleave', (ev) => { if (!zone.contains(ev.relatedTarget)) { zone.classList.remove('dragging'); } });
    zone.addEventListener('drop', async (ev) => {
      zone.classList.remove('dragging');
      if (!natsFormUsable()) { return; }
      const files = ev.dataTransfer ? ev.dataTransfer.files : [];
      if (files.length > 0) { ev.preventDefault(); await acceptFiles(files); return; }
      const text = ev.dataTransfer ? ev.dataTransfer.getData('text/plain') : '';
      if (text && ev.target !== $('nats-text')) { ev.preventDefault(); if (addText(text)) { $('nats-send-status').textContent = t('nats.texttoolong'); } }
    });
    document.addEventListener('paste', async (ev) => {
      if (!natsFormUsable() || !ev.clipboardData) { return; }
      if (ev.clipboardData.files.length > 0) { ev.preventDefault(); await acceptFiles(ev.clipboardData.files); return; }
      const inField = ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement && document.activeElement.tagName);
      const text = ev.clipboardData.getData('text/plain');
      if (!inField && text) { ev.preventDefault(); if (addText(text)) { $('nats-send-status').textContent = t('nats.texttoolong'); } }
    });
    $('nats-file-clear').addEventListener('click', () => { $('nats-file').value = ''; $('nats-send-status').textContent = ''; });
  }

  async function natsSend(ev) {
    ev.preventDefault();
    const status = $('nats-send-status');
    const kind = $('nats-kind').value;
    const req = { sub: kind, text: $('nats-text').value };
    if (kind === 'broadcast') { req.topic = $('nats-topic').value.trim(); } else { req.to = $('nats-to').value; }
    if (kind === 'request') { req.timeout = Number($('nats-reqtimeout').value) || 30; }
    if ($('nats-yes').checked) { req.yes = true; }
    const file = $('nats-file').files[0];
    try {
      if (file) {
        status.textContent = t('nats.handing');
        const res = await fetch('/api/upload?kind=' + fileKind(file) + '&group=' + encodeURIComponent(natsGroup().name) + '&name=' + encodeURIComponent(file.name), {
          method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/octet-stream', 'X-Viper-UI': '1' }, body: file,
        });
        const data = await res.json().catch(() => ({}));
        if (res.status === 403 && !data.error) { banner(t('banner.session')); }
        if (!res.ok) { throw new Error(data.error || 'HTTP ' + res.status); }
        req.upload = data.id;
      }
      status.textContent = '';
      if (await natsRun(req)) { // what was sent is not sent again by the next click
        $('nats-text').value = '';
        $('nats-file').value = '';
      }
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

  const runView = { carry: '' }; // the unfinished line of what the terminal of the runs shows

  function showNatsOutput(out, text, last, view = runView) {
    view.carry += text;
    const lines = view.carry.split('\n');
    view.carry = last ? '' : lines.pop(); // at the end of the run the last line is shown too
    for (const line of lines) {
      if (line === '') { continue; }
      if (!line.startsWith('{')) { out.appendChild(document.createTextNode(line + '\n')); continue; }
      const r = natsText(line);
      out.appendChild(document.createTextNode(r.text + '\n'));
      if (r.fetch) {
        out.appendChild(button(t('nats.fetchthis'), '', () => { $('nats-id').value = r.fetch.id; $('nats-sha').value = r.fetch.sha; show('nats'); $('nats-id').focus(); }));
        out.appendChild(document.createTextNode('\n'));
      }
    }
  }

  // ---- listen: its own area on the page of NATS; the run is one of the server like any other (it is in the list of the runs as well) ----
  const listenView = { carry: '' };
  let listenSource = null;
  let listenId = '';
  let listenGroup = '';

  function listenState(on) {
    $('nats-listen').disabled = on || !!overview.readonly || !natsGroup();
    $('nats-listen-stop').disabled = !on;
  }

  function listenEnd(text) {
    if (listenSource) { listenSource.close(); listenSource = null; }
    listenId = '';
    listenState(false);
    $('nats-listen-status').textContent = text;
  }

  // follow a listen run: what it prints goes into the area, a line at a time
  function listenAttach(id, group) {
    if (listenSource) { listenSource.close(); }
    listenId = id;
    listenGroup = group;
    listenView.carry = '';
    listenState(true);
    $('nats-listen-status').textContent = t('listen.on', group);
    const out = $('nats-listen-out');
    listenSource = new EventSource('/api/events?id=' + encodeURIComponent(id));
    listenSource.onerror = () => {
      if (listenSource && listenSource.readyState === EventSource.CLOSED) { // the server refused the stream (the session is gone, or too many streams are open)
        api('/api/runs').catch(() => {});
        listenEnd(t('listen.nostream'));
      }
    };
    listenSource.addEventListener('out', (ev) => {
      const e = JSON.parse(ev.data);
      const atEnd = out.scrollTop + out.clientHeight >= out.scrollHeight - 24; // a person who reads back is not pulled down
      showNatsOutput(out, e.text, false, listenView);
      while (out.childNodes.length > 2000) { out.removeChild(out.firstChild); }
      if (atEnd) { out.scrollTop = out.scrollHeight; }
    });
    listenSource.addEventListener('state', (ev) => {
      const st = JSON.parse(ev.data);
      if (st.state !== 'done') { return; }
      showNatsOutput(out, '', true, listenView);
      listenEnd(st.code === 0 || st.code === 130 ? t('listen.ended') : t('listen.failed', st.code));
      loadRuns();
    });
  }

  async function listenStart() {
    const g = natsGroup();
    if (!g || listenId) { return; }
    const req = { action: 'nats', nats: g.name, sub: 'listen', topic: $('nats-listen-topic').value.trim(), all: $('nats-listen-all').checked };
    if ($('nats-from').value) { req.from = $('nats-from').value; }
    $('nats-listen-status').textContent = '';
    try {
      const r = await api('/api/run', req);
      listenAttach(r.id, g.name);
      loadRuns();
    } catch (e) {
      $('nats-listen-status').textContent = t('run.notstarted', e.message);
    }
  }

  async function listenStop() {
    if (!listenId) { return; }
    $('nats-listen-stop').disabled = true;
    $('nats-listen-status').textContent = t('listen.ending');
    try { await api('/api/cancel', { id: listenId }); } catch (e) { $('nats-listen-status').textContent = e.message; $('nats-listen-stop').disabled = false; }
  }

  // a page that was opened or loaded again finds a listen run that is still going and follows it
  function listenFind() {
    if (listenId) { return; }
    const g = natsGroup();
    const run = runs.find((r) => r.action === 'nats' && r.sub === 'listen' && r.state !== 'done' && (!g || r.nats === g.name));
    if (run) { listenAttach(run.id, run.nats); }
  }

  // ---- find: the search for a server type; its own area on its page, the run is one of the server like any other ----
  let findSource = null;
  let findId = '';

  function findState(on) {
    $('find-go').disabled = on;
    $('find-stop').disabled = !on;
  }

  let findEnded = '';
  function findEnd(text) {
    if (findSource) { findSource.close(); findSource = null; }
    findEnded = findId; // a late list of runs still says "running": it does not attach again
    findId = '';
    findState(false);
    $('find-status').textContent = text;
    $('find-out').focus(); // the answer is here: the person can read it
  }

  function findAttach(id) {
    if (findSource) { findSource.close(); }
    findId = id;
    findState(true);
    $('find-status').textContent = t('find.running');
    const out = $('find-out');
    out.textContent = '';
    findSource = new EventSource('/api/events?id=' + encodeURIComponent(id));
    findSource.onerror = () => {
      if (findSource && findSource.readyState === EventSource.CLOSED) { api('/api/runs').catch(() => {}); findEnd(t('find.nostream')); }
    };
    findSource.addEventListener('out', (ev) => {
      const e = JSON.parse(ev.data);
      out.appendChild(document.createTextNode(e.text)); // text, as it comes: the table stays a table
      while (out.childNodes.length > 2000) { out.removeChild(out.firstChild); }
    });
    findSource.addEventListener('state', (ev) => {
      const st = JSON.parse(ev.data);
      if (st.state !== 'done') { return; }
      // exit 1 is "nothing fits" or "a provider was skipped": not a failure of the page
      findEnd(st.code === 0 ? t('find.done') : st.code === 130 ? t('find.cancelled') : st.code === 1 ? t('find.none') : t('find.refused', 'exit ' + st.code));
      loadRuns();
    });
  }

  // the question of the form, or the reason there is none
  function findRequest() {
    const num = (id) => { const v = $(id).value.trim(); return v === '' ? 0 : Number(v); };
    const req = { action: 'find', cpu: num('f-cpu'), ram: num('f-ram'), disk: num('f-disk') };
    if (![req.cpu, req.ram, req.disk].every((x) => Number.isFinite(x) && x >= 0) || req.cpu !== Math.floor(req.cpu) || req.disk !== Math.floor(req.disk)) { return { error: t('find.badnumber') }; }
    if (!req.cpu && !req.ram && !req.disk) { return { error: t('find.need') }; }
    if ($('f-arch').value) { req.arch = $('f-arch').value; }
    if ($('f-provider').value) { req.provider = $('f-provider').value; }
    const loc = $('f-location').value.trim().toLowerCase(); // the names are in small letters
    if (loc) {
      if (!req.provider) { return { error: t('find.needprovider') }; }
      req.location = loc;
    }
    const vat = $('f-vat').value.trim();
    if (vat !== '') {
      const v = Number(vat);
      if (!Number.isFinite(v) || v < 0 || v > 100) { return { error: t('find.badnumber') }; }
      req.vat = v;
    }
    return { req };
  }

  let findSending = false; // the request of a search is on its way: a second click does not send another
  async function findStart(ev) {
    ev.preventDefault();
    if (findId || findSending) { return; }
    const q = findRequest();
    if (q.error) { $('find-status').textContent = q.error; return; }
    $('find-status').textContent = '';
    findSending = true;
    try {
      const r = await api('/api/run', q.req);
      findAttach(r.id);
      loadRuns();
    } catch (e) { $('find-status').textContent = t('find.refused', e.message); } finally { findSending = false; }
  }

  async function findStop() {
    if (!findId) { return; }
    $('find-stop').disabled = true;
    try { await api('/api/cancel', { id: findId }); } catch (e) { $('find-status').textContent = e.message; $('find-stop').disabled = false; }
  }

  // a page that was opened or loaded again follows a search that is still going
  function findFind() {
    if (findId) { return; }
    const run = runs.find((r) => r.action === 'find' && r.state !== 'done');
    if (run && run.id !== findEnded) { findAttach(run.id); }
  }

  // ---- runs: viper itself, started by the server; the page attaches to its output ----
  let source = null;
  let runs = [];
  let current = '';

  // the summary of the block says how many options are set, also when the block is closed
  function showActiveOptions() {
    const n = ($('opt-parallel').value.trim() !== '') + $('opt-force').checked + ($('opt-registry').value !== '');
    $('opt-active').textContent = n ? t('opt.active', n) : '';
  }
  ['opt-parallel', 'opt-force', 'opt-registry'].forEach((id) => { $(id).addEventListener('input', showActiveOptions); $(id).addEventListener('change', showActiveOptions); });

  // the options for runs (the block of the overview): how many machines of a group at a time, and what down does. The box --force is
  // read now and cleared at once: it is for this one request
  // the box --force and the choice for the registry are for this one request, as the block may be closed
  function addRunOptions(req) {
    const par = $('opt-parallel').value.trim();
    if (par !== '' && req.scope === 'group' && ['preflight', 'up', 'down', 'check'].includes(req.action)) {
      const n = Number(par);
      if (!Number.isInteger(n) || n < 1 || n > 64) { return t('opt.parallel.bad'); }
      req.parallel = n;
    }
    if (req.action === 'down') {
      if ($('opt-force').checked) { req.force = true; $('opt-force').checked = false; }
      if ($('opt-registry').value) { req.registry = $('opt-registry').value; $('opt-registry').value = ''; }
    }
    showActiveOptions();
    return '';
  }

  // startRun starts a run and shows it
  async function startRun(req) {
    const bad = addRunOptions(req);
    if (bad) { show('runs'); $('run-status').textContent = bad; $('run-status').className = 'bad'; return false; }
    const repo = $('repo').value.trim();
    if (repo && req.action !== 'down' && req.action !== 'nats') { req.repo = repo; }
    $('run-status').textContent = t('status.running');
    $('run-status').className = '';
    try {
      const r = await api('/api/run', req);
      show('runs');
      await loadRuns();
      attach(r.id);
      load(); // the machines the run holds are busy now
      return true;
    } catch (e) {
      show('runs');
      $('run-status').textContent = t('run.notstarted', e.message);
      $('run-status').className = 'bad';
      return false;
    }
  }

  function runLabel(run) {
    const st = run.state === 'done' ? t('run.state.done', run.code) : t('run.state.' + run.state);
    return t('run.item', run.action, run.machine || run.group || [run.nats, run.sub].filter(Boolean).join(' '), st);
  }

  async function loadRuns() {
    let r;
    try { r = await api('/api/runs'); } catch (e) { return; }
    runs = r.runs;
    listenFind();
    findFind();
    // a run in progress: the tab says so (the one thing on the page that moves)
    $('tab-runs').dataset.busy = String(runs.some((x) => x.state === 'running' || x.state === 'waiting' || x.state === 'cancelling'));
    const ul = $('run-list');
    ul.replaceChildren();
    if (runs.length === 0) { const li = document.createElement('li'); li.className = 'muted'; li.textContent = t('runs.none'); ul.appendChild(li); }
    for (const run of runs) {
      const li = document.createElement('li');
      const b = button(runLabel(run), '', () => attach(run.id));
      b.setAttribute('aria-pressed', String(run.id === current));
      li.appendChild(b);
      if (run.state === 'done') { li.appendChild(button(t('runs.remove'), t('runs.remove.aria', runLabel(run)), () => removeRuns(run.id), false, 'quiet')); }
      ul.appendChild(li);
    }
    $('run-clear').disabled = !runs.some((x) => x.state === 'done');
  }

  // finished runs leave the list (one, or all): what is shown of one of them goes with it
  async function removeRuns(id) {
    try { await api('/api/runs/remove', { id }); } catch (e) { $('run-status').textContent = e.message; await loadRuns(); return; } // the list is as the server has it, also when another page was quicker
    await loadRuns();
    if (current && !runs.some((r) => r.id === current)) {
      if (source) { source.close(); source = null; }
      current = '';
      $('run-output').textContent = '';
      $('run-status').textContent = '';
      $('run-form').hidden = true;
      $('run-download').hidden = true;
      $('run-cancel').disabled = true;
    }
    $('run-list').focus();
  }

  function attach(id) {
    if (source) { source.close(); }
    current = id;
    $('run-output').textContent = '';
    $('run-status').textContent = t('status.running'); // not the end of the run that was shown before
    $('run-status').className = '';
    $('run-form').hidden = true;
    source = new EventSource('/api/events?id=' + encodeURIComponent(id));
    // a stream that the server refuses (the session is gone) closes for good: ask once more, as the page does everywhere, so that the banner says why
    source.onerror = () => { if (source && source.readyState === EventSource.CLOSED) { api('/api/runs').catch(() => {}); } };
    runView.carry = '';
    $('run-download').hidden = true;
    const info = runs.find((r) => r.id === id) || {};
    const action = info.action;
    const isFetch = action === 'nats' && info.sub === 'fetch';
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
        if (action === 'nats') { showNatsOutput($('run-output'), '', true); }
        if (isFetch && st.code === 0) { $('run-download-link').href = '/api/download?id=' + encodeURIComponent(id); $('run-download').hidden = false; }
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
  $('run-clear').addEventListener('click', () => removeRuns(''));
  $('run-cancel').addEventListener('click', async () => {
    try { await api('/api/cancel', { id: current }); } catch (e) { $('run-status').textContent = e.message; }
  });

  // ---- pages and tabs: a tablist with arrow keys, as the pattern says ----
  function show(name) {
    page = name;
    $('settings-note').textContent = ''; // a note of an earlier click on a name is of that click
    if (name !== 'settings' && name !== 'change') { pendingFile = ''; }
    for (const p of PAGES) {
      const on = p === name;
      $('page-' + p).hidden = !on;
      $('tab-' + p).setAttribute('aria-selected', String(on));
      $('tab-' + p).tabIndex = on ? 0 : -1;
    }
    if (name === 'logs') { loadLog(); }
    if (name === 'runs' || name === 'nats' || name === 'find') { loadRuns(); } // the page of NATS follows a listen run that is going
    if (name === 'settings') { loadSettings(); }
    if (name === 'create' || name === 'secrets' || name === 'change') { loadTemplates(); }
    if (name === 'change') { loadBackups(); }
    if (name !== 'secrets') { wipeSecret(); }
  }

  const shownPages = () => PAGES.filter((p) => !['create', 'secrets', 'change'].includes(p) || overview.edit);

  $('tabs').addEventListener('keydown', (ev) => {
    const pages = shownPages();
    const i = pages.indexOf(page);
    // the list of pages stands beside the page: Up and Down, and Left and Right as well
    const n = pages.length;
    const next = { ArrowDown: (i + 1) % n, ArrowUp: (i + n - 1) % n, ArrowRight: (i + 1) % n, ArrowLeft: (i + n - 1) % n, Home: 0, End: n - 1 }[ev.key];
    if (next === undefined) { return; }
    ev.preventDefault();
    show(pages[next]);
    $('tab-' + pages[next]).focus();
  });
  for (const p of PAGES) { $('tab-' + p).addEventListener('click', () => show(p)); }

  // ---- create: new settings files (viper ui --edit); the server checks and writes, the page only asks ----
  let templates = null;
  let previewed = '';

  function createRequest() {
    const kind = $('c-kind').value;
    const req = { kind, name: kind === 'base' ? '' : $('c-name').value.trim(), fields: {}, members: [] };
    const put = (k, v) => { if (v.trim()) { req.fields[k] = v.trim(); } };
    if (kind === 'machine') {
      const p = $('c-provider').value;
      put('PROVIDER', p);
      put('SERVER_TYPE', $('c-type').value);
      put('SERVER_LOCATION', $('c-location').value);
      put('DNS_PROVIDER', $('c-dns').value);
      if (p === 'compose') { put('COMPOSE_IMAGE', $('c-image').value); }
      if (p === 'kubernetes') { put('K8S_IMAGE', $('c-image').value); }
    } else if (kind === 'group') {
      req.members = Array.from($('c-members').querySelectorAll('input:checked')).map((x) => x.value);
      if ($('c-nats').checked) { req.fields.NATS_ENABLED = 'true'; }
    } else {
      put('SERVER_PREFIX', $('c-bprefix').value);
      put('DNS_PROVIDER', $('c-bdns').value);
      put('DNS_DOMAIN', $('c-domain').value);
    }
    return req;
  }

  function createInvalidate() {
    if (previewed && previewed !== JSON.stringify(createRequest())) { $('c-status').textContent = t('create.changed'); }
    $('c-apply').disabled = true;
    previewed = '';
  }

  function createLayout() {
    const kind = $('c-kind').value;
    $('c-machine').hidden = kind !== 'machine';
    $('c-group').hidden = kind !== 'group';
    $('c-base').hidden = kind !== 'base';
    for (const id of ['c-name', 'c-name-label', 'c-name-hint']) { $(id).hidden = kind === 'base'; }
    const p = templates && templates.providers.find((x) => x.name === $('c-provider').value);
    if (p) {
      $('c-type').placeholder = p.type;
      $('c-location').placeholder = p.location;
    }
    const container = $('c-provider').value === 'compose' || $('c-provider').value === 'kubernetes';
    $('c-image').disabled = !container;
  }

  function renderCreate() {
    if (!templates) { return; }
    const sel = $('c-provider');
    const keep = sel.value || 'hetzner';
    sel.textContent = '';
    for (const p of templates.providers) { addOption(sel, p.name, p.name); }
    sel.value = keep;
    const box = $('c-members');
    const checked = new Set(Array.from(box.querySelectorAll('input:checked')).map((x) => x.value));
    for (const old of Array.from(box.querySelectorAll('label, p'))) { old.remove(); }
    if ((templates.machines || []).length === 0) {
      const p = document.createElement('p');
      p.className = 'muted';
      p.textContent = t('create.nomembers');
      box.appendChild(p);
    }
    for (const m of (templates.machines || [])) {
      const l = document.createElement('label');
      const c = document.createElement('input');
      c.type = 'checkbox';
      c.value = m;
      c.checked = checked.has(m);
      l.appendChild(c);
      l.appendChild(document.createTextNode(' ' + m));
      box.appendChild(l);
    }
    const base = Array.from($('c-kind').options).find((o) => o.value === 'base');
    base.disabled = templates.base;
    if (templates.base && $('c-kind').value === 'base') { $('c-kind').value = 'machine'; }
    createLayout();
  }

  async function loadTemplates() {
    try {
      templates = await api('/api/templates');
      renderCreate();
      renderSecrets();
      renderChange();
    } catch (e) {
      $('c-status').textContent = e.message;
    }
  }

  // a copy: the form is filled with what the file has that the form knows (never a credential), and the person names it
  let dupSeq = 0;
  async function duplicate(kind, name) {
    const mine = ++dupSeq; // two quick clicks: the last one fills the form
    show('create');
    try {
      const c = await api('/api/duplicate?kind=' + encodeURIComponent(kind) + '&name=' + encodeURIComponent(name));
      await loadTemplates();
      if (mine !== dupSeq) { return; }
      const f = c.fields;
      $('c-kind').value = kind;
      if (kind === 'machine') {
        const known = (p) => Array.from($('c-provider').options).some((o) => o.value === p);
        $('c-provider').value = f.PROVIDER && known(f.PROVIDER) ? f.PROVIDER : ($('c-provider').options[0] || {}).value; // the copy starts from this one's, never from the last form's
        $('c-type').value = f.SERVER_TYPE || '';
        $('c-location').value = f.SERVER_LOCATION || '';
        $('c-dns').value = f.DNS_PROVIDER || '';
        $('c-image').value = ($('c-provider').value === 'kubernetes' ? f.K8S_IMAGE : f.COMPOSE_IMAGE) || '';
      } else {
        for (const x of $('c-members').querySelectorAll('input')) { x.checked = false; } // a machine in two groups breaks
        $('c-nats').checked = f.NATS_ENABLED === 'true';
      }
      createLayout();
      createInvalidate();
      $('c-result').hidden = true;
      $('c-name').value = c.name;
      let msg = c.skipped.length ? t('create.copied', name, c.skipped.join(', ')) : t('create.copied0', name);
      if (kind === 'machine' && !f.PROVIDER) { msg += ' ' + t('create.noprovider'); }
      $('c-status').textContent = msg;
      $('c-name').focus();
      $('c-name').select();
    } catch (e) { $('c-status').textContent = t('create.dupfail', e.message); }
  }

  function showResult(res) {
    const ul = $('c-problems');
    ul.textContent = '';
    const add = (text) => { const li = document.createElement('li'); li.textContent = text; ul.appendChild(li); };
    for (const p of res.problems) { add(t('create.problem', p)); }
    for (const p of res.broken) { add(t('create.broken', p)); }
    for (const p of res.notes) { add(t('create.note', p)); }
    $('c-text').textContent = res.files.map((f) => '# ' + f.path + '\n' + f.text).join('\n');
    $('c-result').hidden = false;
  }

  $('c-preview').addEventListener('click', async () => {
    const req = createRequest();
    $('c-status').textContent = '';
    try {
      const res = await api('/api/edit/preview', req);
      showResult(res);
      const ok = res.problems.length === 0 && res.broken.length === 0;
      $('c-status').textContent = ok ? t('create.ok') : t('create.refused');
      previewed = ok ? JSON.stringify(req) : '';
      $('c-apply').disabled = !ok;
    } catch (e) { $('c-status').textContent = e.message; }
  });

  $('create-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const req = createRequest();
    if (!previewed || previewed !== JSON.stringify(req)) { createInvalidate(); return; }
    $('c-apply').disabled = true;
    try {
      const res = await api('/api/edit/apply', req);
      showResult(res);
      if (res.done) {
        $('c-status').textContent = t('create.done', res.files.map((f) => f.path).join(', '));
        previewed = '';
        $('c-name').value = '';
        await load();
        await loadTemplates();
        await syncStamp();
        $('c-status').setAttribute('tabindex', '-1');
        $('c-status').focus();
      } else {
        $('c-status').textContent = t('create.refused');
      }
    } catch (e) { $('c-status').textContent = e.message; }
  });
  $('create-form').addEventListener('input', createInvalidate);
  $('c-kind').addEventListener('change', createLayout);
  $('c-provider').addEventListener('change', createLayout);

  // ---- credentials (viper ui --edit): the value goes in once and is wiped from the field; the page never shows one ----
  let secretPreview = ''; // the request of the preview whose answer allows "Set"
  let secretEtag = '';
  let removeArmed = false;

  function wipeSecret() {
    $('s-value').value = '';
    secretPreview = '';
    secretEtag = '';
    $('s-apply').disabled = true;
    disarmRemove();
  }

  function selectedFile() {
    return templates && templates.files.find((f) => f.path === $('s-file').value);
  }

  function secretRequest(remove) {
    const f = selectedFile() || { target: '', name: '' };
    const name = $('s-name').value;
    const req = { kind: 'credential', target: f.target, name: f.name, fields: {}, members: [], remove: [], etag: '' };
    if (remove) { req.remove = [name]; } else { req.fields[name] = $('s-value').value; }
    return req;
  }

  function renderSecrets() {
    if (!templates) { return; }
    const body = $('s-table').tBodies[0];
    body.textContent = '';
    for (const n of templates.credentials) {
      const tr = document.createElement('tr');
      cell(tr, n, 'mono');
      const where = templates.files.filter((f) => f.set.includes(n)).map((f) => f.path);
      cell(tr, where.length ? where.join(', ') : t('secrets.notset'), where.length ? '' : 'muted');
      body.appendChild(tr);
    }
    const nameSel = $('s-name');
    const keepName = nameSel.value;
    nameSel.textContent = '';
    for (const n of templates.credentials) { addOption(nameSel, n, n); }
    if (keepName) { nameSel.value = keepName; }
    const fileSel = $('s-file');
    const keepFile = fileSel.value;
    fileSel.textContent = '';
    for (const f of templates.files) { addOption(fileSel, f.path, f.path); }
    if (keepFile) { fileSel.value = keepFile; }
    if (!keepFile) { pickFile(); }
    $('s-remove').disabled = templates.files.length === 0;
    $('s-preview').disabled = templates.files.length === 0;
    if (templates.files.length === 0) { $('s-status').textContent = t('secrets.nofile'); }
  }

  // the file that sets the credential already, else the base file
  function pickFile() {
    const here = templates.files.find((f) => f.set.includes($('s-name').value));
    const base = templates.files.find((f) => f.target === 'base');
    const pick = here || base;
    if (pick) { $('s-file').value = pick.path; }
  }

  function secretResult(res) {
    const ul = $('s-problems');
    ul.textContent = '';
    const add = (text) => { const li = document.createElement('li'); li.textContent = text; ul.appendChild(li); };
    for (const p of res.problems) { add(t('create.problem', p)); }
    for (const p of res.broken) { add(t('create.broken', p)); }
    for (const p of res.notes) { add(p); }
    $('s-text').textContent = res.files.map((f) => '# ' + f.path + '\n' + f.text).join('\n');
    $('s-result').hidden = false;
  }

  function secretOk(res) { return res.problems.length === 0 && res.broken.length === 0; }

  function disarmRemove() {
    removeArmed = false;
    $('s-remove').textContent = t('secrets.remove');
    $('s-remove').removeAttribute('aria-label');
  }

  $('s-preview').addEventListener('click', async () => {
    if (!$('s-value').value) { $('s-status').textContent = t('secrets.needvalue'); return; }
    const req = secretRequest(false);
    $('s-status').textContent = '';
    try {
      const res = await api('/api/edit/preview', req);
      secretResult(res);
      const ok = secretOk(res);
      $('s-status').textContent = ok ? t('secrets.ok') : t('create.refused');
      secretPreview = ok ? JSON.stringify(req) : '';
      secretEtag = ok ? res.etag : '';
      $('s-apply').disabled = !ok;
    } catch (e) { $('s-status').textContent = e.message; }
  });

  async function secretApply(req, etag) {
    req.etag = etag;
    const res = await api('/api/edit/apply', req);
    secretResult(res);
    if (!res.done) { $('s-status').textContent = t('create.refused'); return; }
    wipeSecret();
    $('s-status').textContent = t('secrets.done', req.remove.length ? req.remove[0] : Object.keys(req.fields)[0], res.backup);
    await loadTemplates();
    await syncStamp();
    $('s-status').setAttribute('tabindex', '-1');
    $('s-status').focus();
  }

  $('secret-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const req = secretRequest(false);
    if (!secretPreview || secretPreview !== JSON.stringify(req)) { $('s-apply').disabled = true; $('s-status').textContent = t('secrets.changed'); return; }
    $('s-apply').disabled = true;
    try { await secretApply(req, secretEtag); } catch (e) { $('s-status').textContent = e.message; }
  });

  // removing asks twice, in the button itself: the name of the credential and of the file stand in the question
  $('s-remove').addEventListener('click', async () => {
    const f = selectedFile();
    if (!f) { return; }
    if (!removeArmed) {
      removeArmed = true;
      const text = t('secrets.remove.arm', $('s-name').value, f.path);
      $('s-remove').textContent = text;
      $('s-remove').setAttribute('aria-label', text);
      return;
    }
    disarmRemove();
    const req = secretRequest(true);
    try {
      const pre = await api('/api/edit/preview', req);
      secretResult(pre);
      if (!secretOk(pre)) { $('s-status').textContent = t('create.refused'); return; }
      await secretApply(req, pre.etag);
    } catch (e) { $('s-status').textContent = e.message; }
  });
  $('s-remove').addEventListener('blur', disarmRemove);
  $('secret-form').addEventListener('input', () => { $('s-apply').disabled = true; secretPreview = ''; });
  $('s-name').addEventListener('change', () => { if (templates) { pickFile(); } });
  // a page that is left or restored from the cache (Back) keeps no value in the field
  window.addEventListener('pagehide', wipeSecret);
  window.addEventListener('pageshow', (ev) => { if (ev.persisted) { wipeSecret(); } });

  // ---- change: one setting of a file that exists (viper ui --edit); the server checks every machine and group first ----
  let changeRows = []; // the settings of the file that is chosen, as the settings page lists them
  let changePreview = '';
  let changeEtag = '';
  let changeWord = '';
  let removeAskedFor = '';

  const catalogOf = (n) => templates && templates.catalog.find((c) => c.name === n);
  const fileOf = (p) => templates && templates.files.find((f) => f.path === p);

  function changeRequest(remove) {
    const f = fileOf($('ch-file').value) || { target: '', name: '' };
    const name = remove || $('ch-name').value;
    const req = { kind: 'setting', target: f.target, name: f.name, fields: {}, members: [], remove: [], etag: '', confirm: '' };
    if (remove) { req.remove = [remove]; } else { req.fields[name] = $('ch-value').value; }
    return req;
  }

  // the control for the value follows the kind of the setting: a choice, a number, or text
  function valueControl(keep) {
    const c = catalogOf($('ch-name').value);
    const box = $('ch-valuebox');
    const typed = keep && $('ch-value') ? $('ch-value').value : '';
    box.textContent = '';
    let ctl;
    if (c && c.type === 'enum') {
      ctl = document.createElement('select');
      for (const o of c.options) { addOption(ctl, o, o); }
    } else if (c && c.type === 'lines') {
      ctl = document.createElement('textarea');
      ctl.rows = 8;
      ctl.cols = 60;
      ctl.spellcheck = false;
      ctl.className = 'code';
    } else {
      ctl = document.createElement('input');
      ctl.type = c && c.type === 'int' ? 'number' : 'text';
      if (c && c.type === 'int') { ctl.min = String(c.min); ctl.max = String(c.max); } else { ctl.size = 30; ctl.spellcheck = false; }
    }
    ctl.id = 'ch-value';
    ctl.setAttribute('aria-describedby', 'ch-help ch-limits');
    box.appendChild(ctl);
    if (typed) { ctl.value = typed; } // a change of the language keeps what was typed
    $('ch-help').textContent = (c && c.help && (c.help[window.viperI18n.lang()] || c.help.en)) || ''; // from the server, in the language of the page
    $('ch-limits').textContent = [c && c.type === 'int' ? t('change.limits.int', c.min, c.max) : '', c && c.type === 'lines' ? t('change.limits.lines') : '', c && c.danger ? t('change.limits.danger') : ''].filter(Boolean).join(' ');
  }

  function renderChange() {
    if (!templates) { return; }
    const sel = $('ch-file');
    const keep = sel.value;
    sel.textContent = '';
    for (const f of templates.files) { addOption(sel, f.path, f.path); }
    if (keep) { sel.value = keep; }
    const asked = pendingFile && page === 'change' && templates.files.some((f) => f.path === pendingFile);
    if (asked) { sel.value = pendingFile; }
    const nameSel = $('ch-name');
    const keepName = nameSel.value;
    nameSel.textContent = '';
    for (const c of templates.catalog) { addOption(nameSel, c.name, c.name); }
    if (keepName) { nameSel.value = keepName; }
    if (!$('ch-value')) { valueControl(false); }
    $('ch-preview').disabled = templates.files.length === 0;
    if (templates.files.length === 0) { $('ch-status').textContent = t('change.nofile'); }
    loadRows();
    if (page === 'change') { pendingFile = ''; } // asked for or not there: it is not kept for a later visit
    if (asked) { sel.focus(); }
  }

  let rowsSeq = 0;
  async function loadRows() {
    const mine = ++rowsSeq; // quick changes of the file: the last answer is the one shown
    changeRows = [];
    try {
      const r = await api('/api/settings?file=' + encodeURIComponent($('ch-file').value) + '&limit=1000');
      if (mine !== rowsSeq) { return; }
      changeRows = (r.file && r.file.settings) || [];
      if (r.file && r.file.count > changeRows.length) { $('ch-status').textContent = t('change.many', changeRows.length, r.file.count); }
    } catch (e) { $('ch-status').textContent = e.message; }
    const body = $('ch-table').tBodies[0];
    body.textContent = '';
    for (const s of changeRows) {
      if (!catalogOf(s.key)) { continue; } // what this page cannot change is not offered
      const tr = document.createElement('tr');
      cell(tr, s.key, 'mono');
      cell(tr, s.shown ? s.value : (s.set ? t('change.shownot') : ''), s.shown ? '' : 'muted');
      const act = cell(tr, '', 'actions');
      act.appendChild(button(t('change.edit'), t('change.edit') + ' ' + s.key, () => editRow(s), false, 'quiet'));
      const rm = button(t('change.remove'), t('change.remove') + ' ' + s.key, () => removeSetting(s.key, rm), false, 'danger');
      rm.addEventListener('blur', () => { removeAskedFor = ''; rm.textContent = t('change.remove'); rm.setAttribute('aria-label', t('change.remove') + ' ' + s.key); });
      act.appendChild(rm);
      body.appendChild(tr);
    }
  }

  // the form for a setting of the file: its value if the page may show it; the commands (text over several lines) are read apart
  let editSeq = 0;
  async function editRow(s) {
    const mine = ++editSeq;
    $('ch-name').value = s.key;
    valueControl(false);
    const f = fileOf($('ch-file').value);
    if (catalogOf(s.key).type === 'lines' && f) {
      try {
        const r = await api('/api/setting?target=' + encodeURIComponent(f.target) + '&name=' + encodeURIComponent(f.name) + '&setting=' + encodeURIComponent(s.key));
        if (mine === editSeq) { $('ch-value').value = r.value; }
      } catch (e) { $('ch-status').textContent = e.message; }
    } else if (s.cut) {
      $('ch-status').textContent = t('change.cut'); // the page has only the beginning: it is not put in the field to be written back
    } else if (s.shown) {
      $('ch-value').value = s.value;
    }
    $('ch-value').focus();
  }

  function changeResult(res) {
    restoring = null;
    changePreview = ''; // what is shown now is the preview of this answer only: an older one cannot be applied
    $('ch-apply').disabled = true;
    $('ch-restore-go').hidden = true; // a preview that is not of a restore has no button for it
    const ul = $('ch-problems');
    ul.textContent = '';
    const add = (text) => { const li = document.createElement('li'); li.textContent = text; ul.appendChild(li); };
    for (const p of res.problems) { add(t('create.problem', p)); }
    for (const p of res.broken) { add(t('create.broken', p)); }
    for (const p of res.confirm) { add(p); }
    for (const p of res.notes) { add(p); }
    $('ch-text').textContent = res.files.map((f) => '# ' + f.path + '\n' + f.text).join('\n');
    $('ch-result').hidden = false;
    changeWord = res.confirmWord || '';
    $('ch-confirmrow').hidden = !changeWord;
    if (changeWord) { $('ch-confirm-label').textContent = t('change.confirm', changeWord); $('ch-confirm').value = ''; }
  }

  function changeOk(res) { return res.problems.length === 0 && res.broken.length === 0; }

  async function changeApply(req, etag, word) {
    req.etag = etag;
    req.confirm = word;
    const res = await api('/api/edit/apply', req);
    changeResult(res);
    if (!res.done) { $('ch-status').textContent = t('create.refused'); return; }
    changePreview = '';
    $('ch-apply').disabled = true;
    $('ch-status').textContent = t('change.done', req.kind === 'restore' ? req.name : (req.remove[0] || Object.keys(req.fields)[0]), res.backup);
    await loadTemplates();
    loadBackups();
    await syncStamp();
    $('ch-status').setAttribute('tabindex', '-1');
    $('ch-status').focus();
  }

  $('ch-preview').addEventListener('click', async () => {
    if (!$('ch-value').value) { $('ch-status').textContent = t('change.nothing'); return; }
    const req = changeRequest('');
    $('ch-status').textContent = '';
    try {
      const res = await api('/api/edit/preview', req);
      changeResult(res);
      const ok = changeOk(res);
      $('ch-status').textContent = ok ? t('secrets.ok') : t('create.refused');
      changePreview = ok ? JSON.stringify(req) : '';
      changeEtag = ok ? res.etag : '';
      $('ch-apply').disabled = !ok;
    } catch (e) { $('ch-status').textContent = e.message; }
  });

  $('change-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const req = changeRequest('');
    if (!changePreview || changePreview !== JSON.stringify(req)) { $('ch-apply').disabled = true; $('ch-status').textContent = t('secrets.changed'); return; }
    if (changeWord && $('ch-confirm').value !== changeWord) { $('ch-confirm').focus(); $('ch-status').textContent = t('change.confirm', changeWord); return; }
    $('ch-apply').disabled = true;
    try { await changeApply(req, changeEtag, $('ch-confirm').value); } catch (e) { $('ch-status').textContent = e.message; }
  });

  // taking a setting out asks twice, in the button itself
  async function removeSetting(name, btn) {
    if (removeAskedFor !== name) {
      removeAskedFor = name;
      btn.textContent = t('change.remove.arm', name);
      btn.setAttribute('aria-label', t('change.remove.arm', name));
      return;
    }
    removeAskedFor = '';
    const req = changeRequest(name);
    try {
      const pre = await api('/api/edit/preview', req);
      changeResult(pre);
      if (!changeOk(pre)) { $('ch-status').textContent = t('create.refused'); return; }
      if (pre.confirmWord) { // asks for the word: the button under the preview takes it out when the word is typed
        restoring = { req, etag: pre.etag };
        goKey = 'change.remove.go';
        $('ch-restore-go').textContent = t(goKey);
        $('ch-restore-go').hidden = false;
        $('ch-status').textContent = t('change.confirm', pre.confirmWord);
        $('ch-confirm').focus();
        return;
      }
      await changeApply(req, pre.etag, '');
    } catch (e) { $('ch-status').textContent = e.message; }
  }

  // typing the word of the confirmation does not make the preview out of date; changing the setting or the value does
  $('change-form').addEventListener('input', (ev) => { if (ev.target.id === 'ch-confirm') { return; } $('ch-apply').disabled = true; changePreview = ''; });
  $('ch-name').addEventListener('change', () => valueControl(false));
  $('ch-file').addEventListener('change', () => { restoring = null; $('ch-restore-go').hidden = true; loadRows(); }); // a preview that waits is of the file that was chosen then

  // restoring: the preview says what the copy would change; the button under it does it (the credentials that are there stay)
  let goKey = 'backups.restore.go'; // the text of the button under the preview (it changes with the language)
  let restoring = null; // { req, etag } of a preview that has no problem and waits for the button under it (a restore, or taking out a dangerous setting)

  async function restorePreview(b) {
    const req = { kind: 'restore', name: b.name, target: '', fields: {}, members: [], remove: [], etag: '', confirm: '' };
    $('ch-status').textContent = '';
    try {
      const res = await api('/api/edit/preview', req);
      changeResult(res);
      const ok = changeOk(res);
      restoring = ok ? { req, etag: res.etag } : null;
      goKey = 'backups.restore.go';
      $('ch-restore-go').textContent = t(goKey);
      $('ch-restore-go').hidden = !ok;
      $('ch-status').textContent = ok ? t('backups.restoring', (b.files || []).join(', ')) : t('create.refused');
      $('ch-result').scrollIntoView({ block: 'nearest' });
    } catch (e) { $('ch-status').textContent = e.message; }
  }

  $('ch-restore-go').addEventListener('click', async () => {
    if (!restoring) { return; }
    if (changeWord && $('ch-confirm').value !== changeWord) { $('ch-confirm').focus(); $('ch-status').textContent = t('change.confirm', changeWord); return; }
    const { req, etag } = restoring;
    restoring = null;
    $('ch-restore-go').hidden = true;
    try { await changeApply(req, etag, $('ch-confirm').value); } catch (e) { $('ch-status').textContent = e.message; }
  });

  async function loadBackups() {
    const ul = $('backup-list');
    try {
      const res = await api('/api/backups');
      ul.textContent = '';
      if (res.backups.length === 0) { const li = document.createElement('li'); li.className = 'muted'; li.textContent = t('backups.none'); ul.appendChild(li); }
      for (const b of res.backups) {
        const li = document.createElement('li');
        const a = document.createElement('a');
        a.href = '/api/backups/' + encodeURIComponent(b.name);
        a.textContent = t('backups.download');
        a.setAttribute('aria-label', t('backups.aria', (b.files || []).join(', ') + ' ' + b.time.replace('T', ' ').slice(0, 19)));
        a.setAttribute('download', b.name);
        const back = button(t('backups.restore'), t('backups.restore.aria', (b.files || []).join(', ') + ' ' + b.time.replace('T', ' ').slice(0, 19)), () => restorePreview(b), false, 'quiet');
        li.append(document.createTextNode(b.time.replace('T', ' ').slice(0, 19) + '  ' + (b.files || []).join(', ') + '  '), a, document.createTextNode(' '), back);
        ul.appendChild(li);
      }
    } catch (e) { ul.textContent = ''; }
  }

  // ---- keeping up with the files: while the page is seen it asks for a short stamp every few seconds (and when it is seen again),
  // and when the stamp differs it loads what it shows anew. A preview that waits for "apply" is out of date only when the settings
  // files changed (not when a run changed the marks), and nothing is rebuilt under the hands of the person: while a button of a
  // table or a field of a form has the focus, or a second click is waited for (the table of the overview is rebuilt: the focus stays on its button), the refresh waits for the next poll. ----
  let stamp = '';
  let filesStamp = '';
  let polling = false;
  const pollEvery = 5000;
  const pollWhileRunning = 2000; // while a run goes on the marks change: the table follows closer

  function staleStatus() {
    if (changePreview || restoring) { changePreview = ''; restoring = null; $('ch-apply').disabled = true; $('ch-restore-go').hidden = true; $('ch-status').textContent = t('stale'); }
    if (previewed) { previewed = ''; $('c-apply').disabled = true; $('c-status').textContent = t('stale'); }
    if (secretPreview) { secretPreview = ''; secretEtag = ''; $('s-apply').disabled = true; $('s-status').textContent = t('stale'); }
  }

  // busy: the person is in the middle of something that a rebuild of the page would break
  function busy() {
    const el = document.activeElement;
    const inside = (id) => !!el && !!$(id) && $(id).contains(el);
    return removeAskedFor !== '' || removeArmed || inside('ch-table') || inside('create-form') || inside('secret-form') || inside('change-form');
  }

  async function refreshPages() {
    const ok = await load();
    if (['create', 'secrets', 'change'].includes(page)) { await loadTemplates(); }
    if (page === 'change') { loadBackups(); }
    if (page === 'settings') { loadSettings(); }
    return ok;
  }

  // syncStamp takes the state after a change of our own, so that the next poll does not take it for somebody else's
  async function syncStamp() {
    try {
      const r = await api('/api/stamp');
      stamp = r.stamp;
      filesStamp = r.files;
    } catch (e) { /* the next poll sets it */ }
  }

  let sessionGone = false;
  async function poll() {
    if (document.hidden || polling || sessionGone) { return; }
    polling = true;
    try {
      const r = await api('/api/stamp');
      if (stamp !== '' && r.files !== filesStamp) { staleStatus(); filesStamp = r.files; } // a preview made from now on is of this state
      if (stamp === '' || r.stamp === stamp) { stamp = r.stamp; filesStamp = r.files; return; }
      if (busy()) { return; } // the stamp stays the old one: the next poll tries again
      if (await refreshPages()) { stamp = r.stamp; filesStamp = r.files; }
    } catch (e) {
      if (e.status === 403) { sessionGone = true; } // the session is gone: the banner says it, and asking again helps nobody
    } finally {
      polling = false;
    }
  }

  document.addEventListener('visibilitychange', poll);
  window.addEventListener('focus', poll);
  function pollLater() {
    const going = (overview.machines || []).some((m) => m.busy) || (runs || []).some((r) => !['nats', 'find'].includes(r.action) && (r.state === 'running' || r.state === 'waiting' || r.state === 'cancelling'));
    setTimeout(async () => { await poll(); pollLater(); }, going ? pollWhileRunning : pollEvery);
  }
  pollLater();

  function translate() {
    window.viperI18n.apply();
    $('lang').value = window.viperI18n.lang();
    renderAll();
    $('ch-restore-go').textContent = t(goKey); // the language of the page set it back to its first text
    if (changeWord) { $('ch-confirm-label').textContent = t('change.confirm', changeWord); }
    renderCreate();
    renderSecrets();
    valueControl(true);
    if (templates) { loadRows(); }
    disarmRemove();
    if (page === 'settings') { loadSettings(); }
    if (page === 'runs') { loadRuns(); }
  }

  // the filter and the sort
  $('f-text').addEventListener('input', renderMachines);
  $('f-state').addEventListener('change', renderMachines);
  $('f-clear').addEventListener('click', () => { $('f-text').value = ''; $('f-state').value = ''; renderMachines(); $('f-text').focus(); });
  for (const b of document.querySelectorAll('button.sort')) {
    b.addEventListener('click', () => {
      // a second click turns it round, a third one takes the sort away
      if (sortKey !== b.dataset.sort) { sortKey = b.dataset.sort; sortDir = 1; } else if (sortDir === 1) { sortDir = -1; } else { sortKey = ''; sortDir = 1; }
      renderMachines();
    });
  }
  $('lang').addEventListener('change', () => { window.viperI18n.setLang($('lang').value); translate(); });
  $('nats-group').addEventListener('change', natsGroupChanged);
  $('nats-kind').addEventListener('change', natsKindChanged);
  $('nats-peers').addEventListener('click', () => natsRun({ sub: 'peers', timeout: Number($('nats-timeout').value) || 5 }));
  $('nats-topics').addEventListener('click', () => natsRun({ sub: 'topics', timeout: Number($('nats-timeout').value) || 5 }));
  $('find-form').addEventListener('submit', findStart);
  $('find-stop').addEventListener('click', findStop);
  $('nats-listen').addEventListener('click', listenStart);
  $('nats-listen-stop').addEventListener('click', listenStop);
  $('nats-listen-clear').addEventListener('click', () => { $('nats-listen-out').textContent = ''; listenView.carry = ''; $('nats-listen-out').focus(); });
  $('nats-init').addEventListener('click', natsInit);
  $('nats-revoke').addEventListener('click', natsRevoke);
  $('nats-revoke').addEventListener('blur', disarmRevoke);
  $('nats-send').addEventListener('submit', natsSend);
  $('nats-fetch').addEventListener('submit', (ev) => { ev.preventDefault(); natsRun({ sub: 'fetch', id: $('nats-id').value.trim(), sha256: $('nats-sha').value.trim().toLowerCase() }); });
  $('settings-q').addEventListener('input', () => { clearTimeout(searchTimer); searchTimer = setTimeout(searchSettings, 250); });
  $('log-reload').addEventListener('click', loadLog);
  $('log-machine').addEventListener('change', loadLog);
  $('log-full').addEventListener('change', loadLog);
  $('log-filter').addEventListener('input', filterLog);
  dropSetup();
  window.viperI18n.apply();
  $('lang').value = window.viperI18n.lang();
  load().then(poll); // the first stamp is the state the page starts from
})();
