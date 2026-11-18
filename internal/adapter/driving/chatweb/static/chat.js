'use strict';
// The page of viper chat. Everything that comes from the server is set as text (textContent), never as markup. The texts are in
// i18n.en.js and i18n.de.js (loaded by i18n.js, one language at a time).
(function () {
  const $ = (id) => document.getElementById(id);
  const { t } = window.viperI18n;
  let state = { groups: [], readonly: false, no_pairs: false };
  const live = {}; // by group: { snapshot, convs: { key: conversation }, es: EventSource }
  let current = null; // { group, key, kind, name }
  let thread = []; // the records of the open conversation
  let filter = 'all';
  let readTimer = 0;
  // the topics of a machine in its conversation: the machine may answer on a topic and not to the operator (off until the person turns it on)
  let withTopics = false;
  try { withTopics = window.localStorage.getItem('viper-chat-with-topics') === '1'; } catch (e) { /* off */ }
  const topicsParam = (conv) => (withTopics && conv.kind === 'rechner' ? '&topics=1' : '');
  // a message that belongs to the open conversation: it is in it, or (with the topics) the open machine wrote it on a topic
  const inOpen = (rec, keys) => !!current && ((keys || []).includes(current.key) || (withTopics && current.kind === 'rechner' && rec.dir === 'in' && rec.from === current.name && (keys || []).some((k) => k.startsWith('thema__'))));
  let loading = false; // the thread is being loaded: what arrives meanwhile waits in pending
  let pending = [];
  const asking = {}; // questions of this page that wait for an answer: id -> true | the reason that none came
  const acked = {}; // by conversation: the machines whose agent reads the text in its session, which the person has said is fine
  let asked = null; // { key, push }: the question that waits for the OK of the person
  let sending = false;
  let replying = null; // { id, to, text }: the answer that is being written

  async function api(path, body) {
    const opts = body === undefined
      ? { credentials: 'same-origin' }
      : { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Viper-UI': '1' }, body: JSON.stringify(body) };
    const res = await fetch(path, opts);
    let data = {};
    try { data = await res.json(); } catch (e) { data = {}; }
    if (!res.ok) { const err = new Error(data.error || ('HTTP ' + res.status)); if (res.status === 409 && data.push) { err.push = data.push; } throw err; }
    return data;
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) { e.className = cls; }
    if (text !== undefined) { e.textContent = text; }
    return e;
  }

  const CONV = { rechner: 'conv.machine', thema: 'conv.topic', paar: 'conv.pair', alle: 'conv.stream' };
  const PRESENCE = { here: 'presence.here', gone: 'presence.gone', unknown: 'presence.unknown' };
  const GROUP_WORD = { connected: 'group.connected', connecting: 'group.connecting', disturbed: 'group.disturbed', down: 'group.down', auth: 'group.auth', nokey: 'group.nokey' };
  const PRESENCE_SHORT = { here: 'presence.short.here', gone: 'presence.short.gone', unknown: 'presence.unknown' };
  const colours = ['c1', 'c2', 'c3', 'c4', 'c5'];
  const colourOf = (name) => colours[Array.from(name).reduce((a, c) => a + c.charCodeAt(0), 0) % colours.length];
  const keyOf = (kind, name) => (kind === 'alle' ? 'alle' : kind + '__' + name.split(' ').join('__'));
  const groupOf = (name) => state.groups.find((g) => g.name === name);
  const ago = (s) => (s < 0 ? '' : s < 60 ? s + ' s' : s < 3600 ? Math.round(s / 60) + ' min' : Math.round(s / 3600) + ' h');
  const hhmm = (iso) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  const day = (iso) => new Date(iso).toLocaleDateString([], { weekday: 'short', day: 'numeric', month: 'short' });

  // ---- the list

  function presence(g, name) {
    const s = live[g.name] && live[g.name].snapshot;
    return (s && s.machines && s.machines[name]) || { state: 'unknown' };
  }

  function lamp(st) {
    if (st !== 'here' && st !== 'gone') { return document.createDocumentFragment(); } // never seen: no lamp
    const led = el('span', 'led' + (st === 'here' ? '' : ' off'));
    led.setAttribute('aria-hidden', 'true');
    return led;
  }

  function convButton(g, kind, name) {
    const key = keyOf(kind, name);
    const c = (live[g.name] && live[g.name].convs[key]) || {};
    const li = el('li');
    const b = el('button', 'conv-btn');
    b.type = 'button';
    b.dataset.key = g.name + '/' + key;
    const label = kind === 'alle' ? t('conv.all') : name;
    const av = el('div', 'av ' + (kind === 'thema' ? 'topic' : kind === 'alle' ? 'all' : colourOf(name)), kind === 'thema' ? '#' : kind === 'alle' ? '*' : name.slice(0, 2));
    av.setAttribute('aria-hidden', 'true'); // the name beside it is the name of the button
    if (kind === 'rechner') { av.appendChild(lamp(presence(g, name).state)); }
    const body = el('div');
    body.appendChild(el('div', 'nm', label));
    if (c.last && c.last.text) { body.appendChild(el('div', 'pv', c.last.text)); }
    b.append(av, body);
    if (c.unread > 0) {
      const badge = el('span', 'badge', String(c.unread));
      badge.setAttribute('aria-label', t('list.unread', c.unread));
      b.appendChild(badge);
    }
    const words = [label];
    if (kind === 'rechner') { words.push(t(PRESENCE_SHORT[presence(g, name).state] || PRESENCE_SHORT.unknown)); }
    if (c.unread > 0) { words.push(t('list.unread', c.unread)); }
    b.setAttribute('aria-label', words.join(', '));
    b.setAttribute('aria-current', String(!!current && current.group === g.name && current.key === key));
    b.addEventListener('click', () => select(g, kind, name));
    li.appendChild(b);
    return li;
  }

  // what the person has folded: groups and the parts of their lists (kept in the browser)
  const folded = (() => {
    try { return JSON.parse(window.localStorage.getItem('viper-chat-fold') || '{}') || {}; } catch (e) { return {}; }
  })();

  function fold(key) {
    folded[key] = !folded[key];
    try { window.localStorage.setItem('viper-chat-fold', JSON.stringify(folded)); } catch (e) { /* not kept */ }
    renderList();
  }

  // a title that folds its part of the list
  function foldButton(cls, key, label, count) {
    const b = el('button', cls);
    b.type = 'button';
    b.dataset.fold = key;
    b.setAttribute('aria-expanded', String(!folded[key]));
    b.appendChild(el('span', 'tri', folded[key] ? '▸' : '▾'));
    b.appendChild(el('span', '', label));
    if (count !== undefined) { b.appendChild(el('span', 'count', String(count))); }
    b.addEventListener('click', () => fold(key));
    return b;
  }

  function section(box, group, title, items, hint) {
    if (!items.length && !hint) { return; }
    const key = group + '/' + title;
    box.appendChild(foldButton('sec', key, title, items.length));
    if (folded[key]) { return; }
    const ul = el('ul');
    for (const i of items) { ul.appendChild(i); }
    if (!items.length) { const li = el('li', 'hint', hint); ul.appendChild(li); }
    box.appendChild(ul);
  }

  // what the list shows of one group: the machines and topics of the settings, and what only the history knows
  function entries(g) {
    const convs = (live[g.name] && live[g.name].convs) || {};
    const topics = new Set(g.topics || []);
    const pairs = [];
    for (const c of Object.values(convs)) {
      if (c.kind === 'thema') { topics.add(c.name); }
      if (c.kind === 'paar' && !state.no_pairs) { pairs.push(c.name); }
    }
    if (!state.no_pairs) { // few machines: every pair has its place, also before the two have written; many: only those that did
      const ms = (g.machines || []).map((m) => m.name).sort();
      if (ms.length <= 6) { for (let i = 0; i < ms.length; i++) { for (let j = i + 1; j < ms.length; j++) { if (!pairs.includes(ms[i] + ' ' + ms[j])) { pairs.push(ms[i] + ' ' + ms[j]); } } } }
    }
    return { topics: Array.from(topics).sort(), pairs: pairs.sort() };
  }

  function unreadTotal() {
    let n = 0;
    for (const L of Object.values(live)) { for (const c of Object.values(L.convs || {})) { if (c.key !== 'alle' && c.kind !== 'paar') { n += c.unread || 0; } } }
    return n;
  }

  // what of a snapshot the list shows: the state of the machines, not the seconds
  function listSignature(s) {
    return s && s.machines ? (s.status ? s.status.status : '') + '|' + Object.keys(s.machines).sort().map((m) => m + ':' + s.machines[m].state).join(',') : '';
  }

  function renderList() {
    const had = document.activeElement && document.activeElement.classList.contains('conv-btn') ? document.activeElement.dataset.key : null;
    const pane = $('list-pane');
    const top = pane.scrollTop;
    drawList();
    pane.scrollTop = top; // a list that is built again stays where it was, and keeps the focus
    if (had) { const b = Array.from(pane.querySelectorAll('.conv-btn')).find((x) => x.dataset.key === had); if (b) { b.focus(); } }
  }

  // the state of a group: no key here, or what the stream says (a group that is not running is not reachable)
  function groupState(g) {
    if (!g.operator) { return 'nokey'; }
    const s = live[g.name] && live[g.name].snapshot && live[g.name].snapshot.status;
    return s ? s.status : 'connecting';
  }

  // the lamp of a group and its word
  function lampIn(hb, g) {
    const gs = groupState(g);
    const led = hb.querySelector('.led');
    led.className = 'led' + (gs === 'connected' ? '' : gs === 'connecting' || gs === 'disturbed' ? ' warn' : ' off');
    hb.querySelector('.sr').textContent = t(GROUP_WORD[gs] || GROUP_WORD.connecting);
  }

  function lampOf(g) {
    const hb = Array.from($('groups').querySelectorAll('button.fold')).find((b) => b.dataset.fold === g.name);
    if (hb) { lampIn(hb, g); }
  }

  function drawList() {
    const total = unreadTotal();
    document.title = (total > 0 ? '(' + total + ') ' : '') + t('title');
    const root = $('groups');
    root.textContent = '';
    for (const g of state.groups) {
      const box = el('section', 'group');
      const h = el('h2');
      const hb = foldButton('fold', g.name, g.name);
      const led = el('span', 'led');
      led.setAttribute('aria-hidden', 'true');
      hb.insertBefore(led, hb.children[1]);
      hb.appendChild(el('span', 'sr'));
      lampIn(hb, g); // the lamp has a word
      h.appendChild(hb);
      box.appendChild(h);
      if (g.operator && !folded[g.name]) {
        const e = entries(g);
        const all = el('ul');
        all.appendChild(convButton(g, 'alle', ''));
        box.appendChild(all);
        section(box, g.name, t('sec.topics'), e.topics.map((n) => convButton(g, 'thema', n)));
        section(box, g.name, t('sec.machines'), (g.machines || []).map((m) => convButton(g, 'rechner', m.name)));
        section(box, g.name, t('sec.pairs'), e.pairs.map((n) => convButton(g, 'paar', n)), state.no_pairs ? '' : t('sec.pairs.none'));
      }
      root.appendChild(box);
    }
  }

  // ---- the thread

  // text: only code blocks (```), everything else is plain text; no links, no markup
  function body(text) {
    const box = el('div', 'tx');
    const parts = String(text).split('```');
    parts.forEach((p, i) => {
      if (i % 2 === 1 && parts.length > i + 1) {
        const code = el('pre', 'code');
        code.appendChild(el('code', '', p.replace(/^[a-z0-9]*\n/, '').replace(/\n$/, '')));
        box.appendChild(code);
      } else if (p.trim()) {
        box.appendChild(el('p', '', (i % 2 === 1 ? '```' : '') + p.trim()));
      }
    });
    return box;
  }

  function side(rec) {
    if (!current) { return 'l'; }
    if (current.kind === 'paar') { return rec.from === current.name.split(' ')[0] ? 'l' : 'r'; }
    return rec.dir === 'out' ? 'r' : 'l';
  }

  function who(rec) {
    if (current.kind === 'rechner' && rec.dir !== 'out') { return ''; }
    if (current.kind === 'alle') { return rec.from + (rec.to ? ' → ' + rec.to : rec.topic ? ' → #' + rec.topic : ''); }
    return rec.from + (rec.to && current.kind !== 'paar' ? ' → ' + rec.to : '');
  }

  function bubble(rec) {
    const wrap = el('article', 'msg ' + side(rec) + (current.kind === 'alle' ? ' wide' : ''));
    wrap.dataset.n = String(rec.n);
    const head = el('div', 'meta');
    const w = who(rec);
    if (w) { head.appendChild(el('span', 'who', w)); }
    if (current.kind === 'rechner' && rec.topic) { head.appendChild(el('span', 'tag', '#' + rec.topic)); } // it was written on a topic
    if (rec.type === 'req') { head.appendChild(el('span', 'tag', t('msg.req'))); }
    if (rec.type === 'req' && rec.dir === 'out' && asking[rec.id] === true) { head.appendChild(el('span', 'tag warn', t('msg.waiting'))); }
    if (rec.type === 'req' && rec.dir === 'out' && typeof asking[rec.id] === 'string') { head.appendChild(el('span', 'tag warn', asking[rec.id] || t('msg.noanswer'))); }
    if (rec.type === 'res') { head.appendChild(el('span', 'tag', t('msg.res'))); }
    if (rec.unknown) { head.appendChild(el('span', 'tag warn', t('msg.unknown'))); }
    head.appendChild(el('time', '', hhmm(rec.t)));
    wrap.appendChild(head);
    if (rec.text) { wrap.appendChild(body(rec.text)); }
    if (rec.att_name) { wrap.appendChild(card(rec)); }
    if (isOpen(rec)) {
      head.appendChild(el('span', 'tag warn', t('msg.open')));
      if (!state.readonly && current.kind === 'rechner') {
        const b = el('button', 'link', t('msg.answer'));
        b.type = 'button';
        b.addEventListener('click', () => startReply(rec));
        wrap.appendChild(b);
      }
    }
    return wrap;
  }

  // a file of a message: a card with a download (never shown in the page itself)
  function card(rec) {
    const box = el('div', 'card');
    box.appendChild(el('div', 'nm', rec.att_name));
    box.appendChild(el('div', 'pv', [rec.att_type, size(rec.att_size), rec.att_sha256 ? rec.att_sha256.slice(0, 12) : ''].filter(Boolean).join(' · ')));
    if (rec.att_id && current) {
      const a = el('a', 'dl', t('file.download'));
      a.href = '/api/chat/file?group=' + encodeURIComponent(current.group) + '&conv=' + encodeURIComponent(current.kind === 'rechner' && rec.topic ? 'thema__' + rec.topic : current.key) + '&n=' + rec.n;
      a.setAttribute('download', rec.att_name);
      box.appendChild(a);
    }
    return box;
  }

  function size(n) { return !n ? '' : n >= 1048576 ? (n / 1048576).toFixed(1) + ' MiB' : Math.max(1, Math.round(n / 1024)) + ' KiB'; }

  // a question of a machine that nobody has answered yet (in what the page has)
  const isOpen = (rec) => rec.type === 'req' && rec.dir === 'in' && !!rec.id && !thread.some((x) => x.type === 'res' && x.re === rec.id);

  const shown = (rec) => filter === 'all' || current.kind !== 'alle' || rec.type === filter;

  function paint() {
    const box = $('msgs');
    box.textContent = '';
    let last = '';
    const rows = thread.filter(shown);
    if (!rows.length) { box.appendChild(el('p', 'muted', t(thread.length ? 'msgs.nomatch' : 'msgs.none'))); return; }
    for (const rec of rows) {
      const d = day(rec.t);
      if (d !== last) { box.appendChild(el('div', 'day', d)); last = d; }
      box.appendChild(bubble(rec));
    }
    box.scrollTop = box.scrollHeight;
  }

  function append(rec) {
    const box = $('msgs');
    if (!shown(rec)) { return; }
    const near = box.scrollHeight - box.scrollTop - box.clientHeight < 80;
    if (!box.querySelector('article')) { box.textContent = ''; }
    const d = day(rec.t);
    const prev = thread.length > 1 ? thread[thread.length - 2] : null;
    if (!prev || day(prev.t) !== d) { box.appendChild(el('div', 'day', d)); }
    box.appendChild(bubble(rec));
    if (near) { box.scrollTop = box.scrollHeight; }
  }

  function topicInfo(g, name) {
    const s = live[g.name] && live[g.name].snapshot;
    const ts = s && s.topics && s.topics[name];
    if (!ts) { return ''; }
    if (ts.known) { return ts.subscribers.length ? t('topic.subs', ts.subscribers.length, ts.subscribers.join(', ')) : t('topic.nobody'); }
    return t('topic.some', ts.subscribers.length);
  }

  function header() {
    if (!current) { return; }
    const g = groupOf(current.group);
    const av = $('conv-av');
    const { kind, name } = current;
    av.textContent = kind === 'thema' ? '#' : kind === 'alle' ? '*' : name.slice(0, 2);
    av.className = 'av ' + (kind === 'thema' ? 'topic' : kind === 'alle' ? 'all' : colourOf(name));
    if (kind === 'rechner') { av.appendChild(lamp(presence(g, name).state)); }
    $('conv-name').textContent = kind === 'alle' ? t('conv.all') : (kind === 'thema' ? '#' : '') + name.split(' ').join(' · ');
    const parts = [t(CONV[kind], g.name)];
    if (kind === 'thema') { const i = topicInfo(g, name); if (i) { parts.push(i); } }
    if (kind === 'rechner') {
      const p = presence(g, name);
      parts.push(t(PRESENCE[p.state] || PRESENCE.unknown, ago(p.seen_ago)));
      const m = (g.machines || []).find((x) => x.name === name);
      if (m && m.push_from_operator) { parts.push(t('conv.push')); }
    }
    if (kind === 'paar' || kind === 'alle') { parts.push(t('conv.readonly.pair')); } else if (state.readonly) { parts.push(t('conv.readonly')); }
    $('conv-sub').textContent = parts.join(' · ');
    $('filter').hidden = kind !== 'alle';
    $('topicsw').hidden = kind !== 'rechner';
    $('withtopics').checked = withTopics;
    $('msgs').setAttribute('aria-live', kind === 'alle' ? 'off' : 'polite'); // a stream of everything is not read out line by line
    $('del').hidden = $('delall').hidden = state.readonly;
  }

  async function select(g, kind, name) {
    current = { group: g.name, key: keyOf(kind, name), kind, name };
    thread = [];
    pending = [];
    loading = true;
    $('msgs').textContent = '';
    asked = null;
    toDelete = null;
    $('confirm').hidden = true;
    showComposer();
    renderList();
    $('empty').hidden = true;
    $('conv').hidden = false;
    header();
    document.querySelector('.shell').classList.add('reading');
    $('main').focus();
    const mine = current;
    try {
      const r = await api('/api/chat/messages?group=' + encodeURIComponent(g.name) + '&conv=' + encodeURIComponent(mine.key) + topicsParam(mine) + '&limit=200');
      if (current !== mine) { return; }
      thread = r.records;
    } catch (e) { banner(t('err.messages', e.message)); }
    loading = false;
    for (const rec of pending) { if (!thread.some((x) => x.n === rec.n)) { thread.push(rec); } }
    pending = [];
    paint();
    markRead();
  }

  // the person has seen everything up to the newest record, if the page is in front
  function markRead() {
    clearTimeout(readTimer);
    readTimer = setTimeout(() => {
      if (!current || document.visibilityState !== 'visible' || !thread.length) { return; }
      const n = thread[thread.length - 1].n;
      const c = live[current.group] && live[current.group].convs[current.key];
      if (c && c.unread > 0) { c.unread = 0; renderList(); }
      api('/api/chat/read', { group: current.group, conv: current.key, n }).catch(() => {});
    }, 400);
  }

  // ---- writing

  const draftKey = () => 'viper-chat-draft/' + current.group + '/' + current.key;
  const canWrite = () => !!current && !state.readonly && (current.kind === 'rechner' || current.kind === 'thema');
  const bytes = (text) => new TextEncoder().encode(text).length;

  function saveDraft() {
    if (!current) { return; }
    try { if ($('text').value) { window.localStorage.setItem(draftKey(), $('text').value); } else { window.localStorage.removeItem(draftKey()); } } catch (e) { /* not kept */ }
  }

  // ---- files

  let picked = null; // the file that goes with the next message

  function fileLimit() {
    const g = current && groupOf(current.group);
    return g && g.file_max > 0 ? Math.min(g.file_max, 64 * 1048576) : 0;
  }

  function showFile() {
    $('chip').hidden = !picked;
    if (picked) { $('chip-text').textContent = picked.name + ' (' + size(picked.size) + ')'; }
    const limit = fileLimit();
    $('attach').disabled = !limit;
    $('attach').title = limit ? t('file.limit', size(limit)) : t('file.nostore');
    count();
  }

  function setFile(f) {
    if (!canWrite() || replying) { return; }
    const limit = fileLimit();
    if (!limit) { $('send-status').textContent = t('file.nostore'); return; }
    if (f.size < 1 || f.size > limit) { $('send-status').textContent = t('file.toobig', size(f.size), size(limit)); return; }
    picked = f;
    $('send-status').textContent = '';
    showFile();
  }

  async function putFile(f, group) {
    const res = await fetch('/api/chat/upload?group=' + encodeURIComponent(group) + '&name=' + encodeURIComponent(f.name), {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/octet-stream', 'X-Viper-UI': '1' }, body: f,
    });
    let data = {};
    try { data = await res.json(); } catch (e) { data = {}; }
    if (!res.ok) { throw new Error(data.error || ('HTTP ' + res.status)); }
    return data.id;
  }

  $('attach').addEventListener('click', () => $('picker').click());
  $('picker').addEventListener('change', () => { if ($('picker').files[0]) { setFile($('picker').files[0]); } $('picker').value = ''; });
  function unpick() { // a file that was handed over and is not sent is given back to the server
    if (picked && picked.id) { api('/api/chat/drop', { group: picked.group, id: picked.id }).catch(() => {}); }
    picked = null;
  }

  $('chip-x').addEventListener('click', () => { unpick(); showFile(); });
  $('text').addEventListener('paste', (e) => {
    const f = e.clipboardData && e.clipboardData.files && e.clipboardData.files[0];
    if (f) { e.preventDefault(); setFile(f); }
  });
  for (const id of ['msgs', 'composer']) {
    $(id).addEventListener('dragover', (e) => { if (canWrite()) { e.preventDefault(); } });
    $(id).addEventListener('drop', (e) => {
      const f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
      if (f && canWrite()) { e.preventDefault(); setFile(f); }
    });
  }

  // where the text goes: the machine or the topic, in which group; a group that is not running takes nothing
  function showTarget() {
    if (!current || !canWrite()) { return; }
    const down = groupState(groupOf(current.group)) === 'down';
    $('target').textContent = down ? t('send.down', current.group) : t(current.kind === 'thema' ? 'send.totopic' : 'send.to', current.name, current.group);
    $('target').classList.toggle('bad', down);
    $('text').disabled = down;
    $('attach').disabled = down || !fileLimit();
    count();
  }

  function showComposer() {
    const box = $('composer');
    box.hidden = !canWrite();
    if (box.hidden) { return; }
    let draft = '';
    try { draft = window.localStorage.getItem(draftKey()) || ''; } catch (e) { draft = ''; }
    $('text').value = draft;
    $('kind').hidden = current.kind !== 'rechner';
    $('kind').value = 'msg';
    $('wait').hidden = true;
    $('ack').hidden = true;
    replying = null;
    unpick();
    $('replying').hidden = true;
    $('send-status').textContent = '';
    showFile();
    showTarget();
  }

  function count() {
    const n = bytes($('text').value);
    $('count').textContent = t('send.count', n, 16384);
    $('count').classList.toggle('over', n > 16384);
    $('send').disabled = sending || (n === 0 && !picked) || n > 16384 || (!!current && groupState(groupOf(current.group)) === 'down');
  }

  function startReply(rec) {
    replying = { id: rec.id, to: rec.from, text: rec.text || '' };
    unpick();
    showFile();
    $('replying-text').textContent = t('send.replying', rec.from, (rec.text || '').slice(0, 80));
    $('replying').hidden = false;
    $('kind').hidden = true;
    $('wait').hidden = true;
    $('text').focus();
  }

  function stopReply() {
    replying = null;
    $('replying').hidden = true;
    $('kind').hidden = !current || current.kind !== 'rechner';
    $('wait').hidden = $('kind').hidden || $('kind').value !== 'ask';
  }

  function request() {
    const body = { group: current.group, text: $('text').value };
    if (replying) { Object.assign(body, { kind: 'reply', to: replying.to, re: replying.id }); }
    else if (current.kind === 'thema') { Object.assign(body, { kind: 'topic', topic: current.name }); }
    else { Object.assign(body, { kind: $('kind').value, to: current.name, timeout: Number($('wait').value) }); }
    body.ack = acked[current.group + '/' + current.key] || [];
    return body;
  }

  async function send(ok) {
    if (!canWrite() || $('send').disabled || sending) { return; }
    const mine = current;
    const ckey = mine.group + '/' + mine.key;
    if (ok && asked && asked.key === ckey) { acked[ckey] = asked.push; }
    if (ok) { $('ack').hidden = true; asked = null; } // the question is answered: the line goes, whatever the sending says
    const body = request();
    const file = picked && !replying ? picked : null; // the file of this message, whatever happens to the chip meanwhile
    sending = true;
    $('send').disabled = true;
    $('send-status').textContent = t('send.sending');
    try {
      if (file) { if (!file.id) { file.id = await putFile(file, mine.group); file.group = mine.group; }
        body.file = file.id; }
      const r = await api('/api/chat/send', body);
      if (body.kind === 'ask' && r.record.id) { asking[r.record.id] = true; }
      if (!r.warning) { onMessage(groupOf(mine.group), { record: r.record, keys: r.keys }); }
      try { if (window.localStorage.getItem('viper-chat-draft/' + ckey) === body.text) { window.localStorage.removeItem('viper-chat-draft/' + ckey); } } catch (e) { /* not kept */ }
      if (current === mine) {
        $('ack').hidden = true;
        asked = null;
        if ($('text').value === body.text) { $('text').value = ''; } // what was typed meanwhile stays
        stopReply();
        if (picked === file) { picked = null; showFile(); }
        $('send-status').textContent = r.warning || (r.via ? t('send.sent.via', mine.group, r.via) : t('send.sent'));
      }
    } catch (e) {
      if (current === mine && e.push) {
        asked = { key: ckey, push: e.push };
        $('ack-text').textContent = t('send.push', e.push.join(', ')) + ' (' + mine.group + ')';
        $('ack').hidden = false;
        $('ack-ok').focus();
        $('send-status').textContent = '';
      } else {
        if (file) { delete file.id; } // the server gave the file up
        if (current === mine) { $('send-status').textContent = t('send.failed', e.message); }
      }
    }
    sending = false;
    count();
    if (current === mine && $('ack').hidden) { $('text').focus(); }
  }

  $('composer').addEventListener('submit', (e) => { e.preventDefault(); send(false); });
  $('ack-ok').addEventListener('click', () => send(true));
  $('replying-x').addEventListener('click', stopReply);
  $('text').addEventListener('input', () => { count(); saveDraft(); });
  $('text').addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); send(false); } });
  $('kind').addEventListener('change', () => { $('wait').hidden = $('kind').value !== 'ask'; });

  // ---- what happens in the groups

  function banner(text) {
    const b = $('banner');
    if ((b.textContent || '') !== (text || '')) {
      b.textContent = text || ''; // the same text again is not announced again
      b.title = text || ''; // the bar shows one line: the whole text is here
      if (text) { console.warn('viper chat: ' + text); } // also in the console of the browser
    }
    if (b.hidden === !!text) { b.hidden = !text; }
  }

  // One line for all groups, from all of them: it does not change with the group that spoke last. A group that is not running is a state
  // (its lamp says so), not a disturbance.
  function showStatus() {
    const bad = [];
    let dropped = '';
    for (const g of state.groups.filter((x) => x.operator)) {
      const s = live[g.name] && live[g.name].snapshot && live[g.name].snapshot.status;
      if (!s || s.status === 'connecting' || s.status === 'down') { continue; }
      if (s.status === 'auth') { bad.push(t('status.auth', g.name, 'viper nats init ' + g.name)); } else if (s.status === 'disturbed') { bad.push(t('status.disturbed', g.name, s.reason || '', s.retry_in || 0)); } else if (s.dropped) { dropped = t('status.dropped', g.name, s.dropped); }
    }
    if (state.groups.some((g) => g.operator && groupState(g) === 'connecting') && !bad.length) { return; } // a try is on its way: the line stays as it is
    banner(bad.length === 1 ? bad[0] : bad.length > 1 ? t('status.several', bad.length) + ' ' + bad.join(' ') : dropped);
  }

  async function loadListing(g) {
    const r = await api('/api/chat/conversations?group=' + encodeURIComponent(g.name));
    const convs = {};
    for (const c of r.conversations) { convs[c.key] = c; }
    live[g.name] = Object.assign(live[g.name] || {}, { snapshot: r.snapshot, convs });
    renderList();
    showStatus();
  }

  function onMessage(g, ev) {
    const rec = ev.record;
    const L = live[g.name];
    if (!rec || !L) { return; }
    notify(g, ev);
    if (rec.type === 'res' && rec.re && rec.re in asking) { asking[rec.re] = false; }
    for (const key of ev.keys || []) {
      const c = L.convs[key] || { key, kind: key === 'alle' ? 'alle' : key.split('__')[0], name: key === 'alle' ? '' : key.split('__').slice(1).join(' '), unread: 0 };
      c.last = Object.assign({}, rec, { text: (rec.text || '').length > 140 ? rec.text.slice(0, 140) + ' ...' : (rec.text || '') });
      const open = current && current.group === g.name && current.key === key;
      if (!open && rec.dir === 'in') { c.unread = (c.unread || 0) + 1; }
      L.convs[key] = c;
    }
    if (current && current.group === g.name && inOpen(rec, ev.keys) && loading) {
      pending.push(rec);
    } else if (current && current.group === g.name && inOpen(rec, ev.keys) && !thread.some((x) => x.n === rec.n)) {
      thread.push(rec);
      if (rec.type === 'res') { paint(); } else { append(rec); } // an answer closes a question that is above
      markRead();
    }
    renderList();
  }

  function listen(g) {
    const L = live[g.name] || (live[g.name] = { convs: {} });
    if (L.es) { return; }
    const es = new EventSource('/api/chat/events?group=' + encodeURIComponent(g.name));
    L.es = es;
    es.addEventListener('snapshot', (e) => {
      const before = listSignature(L.snapshot);
      L.snapshot = JSON.parse(e.data);
      showStatus();
      if (listSignature(L.snapshot) !== before) { renderList(); } // the list is not built again every five seconds
      header();
    });
    es.addEventListener('status', (e) => {
      const ev = JSON.parse(e.data);
      L.snapshot = Object.assign(L.snapshot || { machines: {}, topics: {} }, { status: ev.status });
      showStatus();
      lampOf(g); // only the lamp of the group, not the whole list
      showTarget();
    });
    es.addEventListener('message', (e) => onMessage(g, JSON.parse(e.data)));
    es.addEventListener('ask_end', (e) => {
      const ev = JSON.parse(e.data);
      if (ev.record && asking[ev.record.re] === true) { asking[ev.record.re] = ev.record.text ? ev.record.text : false; }
      if (current && current.group === g.name) { paint(); }
    });
    es.addEventListener('resync', () => catchUp(g)); // the server could not give it all: ask by the numbers
    let seen = false;
    es.onopen = async () => { // after a break: what was missed comes by the numbers
      if (seen) { await catchUp(g); }
      seen = true;
    };
  }

  async function catchUp(g) {
    try {
      await loadListing(g);
      if (!current || current.group !== g.name || loading) { return; }
      const mine = current;
      for (;;) { // in pieces, until nothing is left
        const from = thread.length ? thread[thread.length - 1].n : 0;
        const r = await api('/api/chat/messages?group=' + encodeURIComponent(g.name) + '&conv=' + encodeURIComponent(mine.key) + topicsParam(mine) + '&after=' + from + '&limit=500');
        if (current !== mine) { return; }
        for (const rec of r.records) { if (!thread.some((x) => x.n === rec.n)) { thread.push(rec); append(rec); } }
        if (r.records.length < 500) { break; }
      }
    } catch (e) { banner(t('err.load', e.message)); }
  }

  // ---- search, deleting, notifications

  let searchTimer = 0;
  let searchSeq = 0;

  async function search() {
    const q = $('q').value.trim();
    const box = $('results');
    const mine = ++searchSeq;
    if (Array.from(q).length < 2) { box.hidden = true; $('groups').hidden = false; return; }
    const out = [];
    for (const g of state.groups.filter((x) => x.operator)) {
      try {
        const r = await api('/api/chat/search?group=' + encodeURIComponent(g.name) + '&q=' + encodeURIComponent(q));
        for (const h of r.hits) { out.push({ g, h }); }
      } catch (e) { banner(t('err.load', e.message)); }
    }
    if (mine !== searchSeq) { return; }
    out.sort((a, b) => b.h.record.t.localeCompare(a.h.record.t));
    box.textContent = '';
    box.appendChild(el('div', 'sec', t('search.count', out.length)));
    const ul = el('ul');
    for (const { g, h } of out) {
      const li = el('li');
      const b = el('button', 'conv-btn hit');
      b.type = 'button';
      const body = el('div');
      body.appendChild(el('div', 'nm', (state.groups.length > 1 ? g.name + ' / ' : '') + (h.kind === 'alle' ? t('conv.all') : (h.kind === 'thema' ? '#' : '') + h.name.split(' ').join(' · ')) + ' · ' + day(h.record.t) + ' ' + hhmm(h.record.t)));
      body.appendChild(el('div', 'pv', (h.record.from ? h.record.from + ': ' : '') + h.record.text));
      b.appendChild(el('span'));
      b.appendChild(body);
      b.addEventListener('click', () => { $('q').value = ''; search(); select(g, h.kind, h.name); });
      li.appendChild(b);
      ul.appendChild(li);
    }
    box.appendChild(ul);
    box.hidden = false;
    $('groups').hidden = true;
  }

  $('q').addEventListener('input', () => { clearTimeout(searchTimer); searchTimer = setTimeout(search, 300); });
  $('search').addEventListener('submit', (e) => { e.preventDefault(); clearTimeout(searchTimer); search(); });

  let toDelete = null; // { conv, text }

  function askDelete(all) {
    if (!current || state.readonly) { return; }
    toDelete = { conv: all ? '' : current.key };
    $('confirm-text').textContent = all ? t('del.confirm.all', current.group) : t('del.confirm.conv', $('conv-name').textContent);
    $('confirm').hidden = false;
    $('confirm-x').focus();
  }

  async function doDelete() {
    if (!toDelete || !current) { return; }
    const group = current.group;
    const conv = toDelete.conv;
    const mine = current;
    $('confirm-ok').disabled = true;
    try {
      await api('/api/chat/delete', { group, conv });
      $('confirm').hidden = true;
      toDelete = null;
      if (current === mine && (conv === '' || conv === current.key)) { thread = []; paint(); }
      $('del').focus();
    } catch (e) { $('confirm-text').textContent = t('del.failed', e.message); $('confirm-ok').disabled = false; return; }
    $('confirm-ok').disabled = false;
    try { await loadListing(groupOf(group)); search(); } catch (e) { banner(t('err.load', e.message)); }
  }

  $('del').addEventListener('click', () => askDelete(false));
  $('delall').addEventListener('click', () => askDelete(true));
  $('confirm-ok').addEventListener('click', doDelete);
  $('confirm-x').addEventListener('click', () => { toDelete = null; $('confirm').hidden = true; $('del').focus(); });

  // The notification says only who wrote, never what; it is off until the person turns it on (the browser asks then).
  const notifyOn = () => $('notify').checked && typeof Notification !== 'undefined' && Notification.permission === 'granted';

  async function setNotify() {
    if ($('notify').checked) {
      if (typeof Notification === 'undefined') { $('notify').checked = false; banner(t('notify.none')); return; }
      if (Notification.permission !== 'granted' && (await Notification.requestPermission()) !== 'granted') { $('notify').checked = false; banner(t('notify.denied')); return; }
    }
    try { window.localStorage.setItem('viper-chat-notify', $('notify').checked ? '1' : '0'); } catch (e) { /* not kept */ }
  }

  function notify(g, ev) {
    const rec = ev.record;
    const key = (ev.keys || [])[0];
    if (!notifyOn() || rec.dir !== 'in' || !key || key === 'alle' || key.startsWith('paar__')) { return; }
    const open = current && current.group === g.name && current.key === key;
    if (open && document.visibilityState === 'visible') { return; }
    try { new Notification(t('notify.text', rec.from || ''), { tag: g.name + '/' + key, silent: true }); } catch (e) { /* the browser refuses */ }
  }

  $('notify').addEventListener('change', setNotify);
  try { $('notify').checked = window.localStorage.getItem('viper-chat-notify') === '1' && typeof Notification !== 'undefined' && Notification.permission === 'granted'; } catch (e) { /* off */ }

  $('withtopics').addEventListener('change', () => {
    withTopics = $('withtopics').checked;
    try { window.localStorage.setItem('viper-chat-with-topics', withTopics ? '1' : '0'); } catch (e) { /* not kept */ }
    if (current) { const g = groupOf(current.group); select(g, current.kind, current.name); } // the thread is read again
  });

  // ---- keyboard and narrow width

  // the list: arrow keys move between the entries, Home and End go to the first and the last
  $('list-pane').addEventListener('keydown', (e) => {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key) || !e.target.classList.contains('conv-btn')) { return; }
    const all = Array.from($('list-pane').querySelectorAll('.conv-btn'));
    const i = all.indexOf(e.target);
    const to = e.key === 'Home' ? 0 : e.key === 'End' ? all.length - 1 : Math.max(0, Math.min(all.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1)));
    e.preventDefault();
    all[to].focus();
  });
  $('q').addEventListener('keydown', (e) => { if (e.key === 'Escape') { $('q').value = ''; search(); } });

  // on a narrow screen the thread has the place of the list: Back gives it back, with the focus on the entry that was open
  $('back').addEventListener('click', () => {
    document.querySelector('.shell').classList.remove('reading');
    const b = current && document.querySelector('.conv-btn[data-key="' + current.group + '/' + current.key + '"]');
    if (b) { b.focus(); }
  });

  // ---- the page

  function showEmpty(h, text, code) {
    const box = $('empty');
    box.textContent = '';
    box.appendChild(el('h2', '', h));
    box.appendChild(el('p', '', text));
    if (code) { const p = el('p'); p.appendChild(el('code', '', code)); box.appendChild(p); }
    box.hidden = false;
    $('conv').hidden = true;
  }

  function render() {
    renderList();
    const gs = state.groups;
    $('version').textContent = state.version || '';
    const me = $('me');
    me.textContent = '';
    const led = el('span', 'led');
    led.setAttribute('aria-hidden', 'true');
    if (!gs.length) { led.className = 'led off'; me.append(led, t('me.none')); } else {
      const on = gs.find((g) => g.operator);
      if (on) { me.append(led, t('me.in', on.operator_name || 'operator')); } else { led.className = 'led off'; me.append(led, t('me.out')); }
    }
    if (!gs.length) { showEmpty(t('empty.nogroup.h'), t('empty.nogroup.t')); return; }
    const out = gs.find((g) => !g.operator);
    if (out && !gs.some((g) => g.operator)) { showEmpty(t('empty.nologin.h'), t('empty.nologin.t', out.name), 'viper nats init ' + out.name); return; }
    if (!current) { showEmpty(t('empty.pick.h'), t('empty.pick.t')); }
  }

  async function load() {
    try {
      state = await api('/api/chat/state');
      banner('');
    } catch (e) { banner(t('err.load', e.message)); return; }
    render();
    for (const g of state.groups.filter((x) => x.operator)) {
      try { await loadListing(g); } catch (e) { banner(t('err.load', e.message)); }
      listen(g);
    }
  }

  $('lang').addEventListener('change', async () => {
    await window.viperI18n.setLang($('lang').value);
    window.viperI18n.apply();
    render();
    header();
    if (current) { paint(); }
  });
  $('filter').addEventListener('change', () => { filter = $('filter').value; paint(); });
  document.addEventListener('visibilitychange', markRead);
  window.viperI18n.ready.then(() => {
    window.viperI18n.apply();
    $('lang').value = window.viperI18n.lang();
    load();
  });
})();
