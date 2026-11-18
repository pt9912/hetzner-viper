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

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) { e.className = cls; }
    if (text !== undefined) { e.textContent = text; }
    return e;
  }

  const CONV = { rechner: 'conv.machine', thema: 'conv.topic', paar: 'conv.pair', alle: 'conv.stream' };
  const PRESENCE = { here: 'presence.here', gone: 'presence.gone', unknown: 'presence.unknown' };
  const colours = ['c1', 'c2', 'c3', 'c4', 'c5'];
  const colourOf = (name) => colours[Array.from(name).reduce((a, c) => a + c.charCodeAt(0), 0) % colours.length];
  const keyOf = (kind, name) => (kind === 'alle' ? 'alle' : kind + '__' + name.split(' ').join('__'));
  const groupOf = (name) => state.groups.find((g) => g.name === name);
  const hhmm = (iso) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  const day = (iso) => new Date(iso).toLocaleDateString([], { weekday: 'short', day: 'numeric', month: 'short' });

  // ---- the list

  function presence(g, name) {
    const s = live[g.name] && live[g.name].snapshot;
    return (s && s.machines && s.machines[name]) || { state: 'unknown' };
  }

  function lamp(st) {
    const led = el('span', 'led' + (st === 'here' ? '' : st === 'gone' ? ' off' : ' warn'));
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
    b.setAttribute('aria-current', String(!!current && current.group === g.name && current.key === key));
    b.addEventListener('click', () => select(g, kind, name));
    li.appendChild(b);
    return li;
  }

  function section(box, title, items) {
    if (!items.length) { return; }
    box.appendChild(el('div', 'sec', title));
    const ul = el('ul');
    for (const i of items) { ul.appendChild(i); }
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
    return { topics: Array.from(topics).sort(), pairs: pairs.sort() };
  }

  function renderList() {
    const root = $('groups');
    root.textContent = '';
    for (const g of state.groups) {
      const box = el('section', 'group');
      const h = el('h2');
      const led = el('span', 'led' + (g.operator ? '' : ' off'));
      led.setAttribute('aria-hidden', 'true');
      h.append(led, document.createTextNode(g.name));
      box.appendChild(h);
      if (g.operator) {
        const e = entries(g);
        section(box, '', [convButton(g, 'alle', '')]);
        section(box, t('sec.topics'), e.topics.map((n) => convButton(g, 'thema', n)));
        section(box, t('sec.machines'), (g.machines || []).map((m) => convButton(g, 'rechner', m.name)));
        section(box, t('sec.pairs'), e.pairs.map((n) => convButton(g, 'paar', n)));
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
    if (rec.type === 'req') { head.appendChild(el('span', 'tag', t('msg.req'))); }
    if (rec.type === 'res') { head.appendChild(el('span', 'tag', t('msg.res'))); }
    if (rec.unknown) { head.appendChild(el('span', 'tag warn', t('msg.unknown'))); }
    head.appendChild(el('time', '', hhmm(rec.t)));
    wrap.appendChild(head);
    if (rec.text) { wrap.appendChild(body(rec.text)); }
    if (rec.att_name) { wrap.appendChild(el('div', 'att', t('msg.file', rec.att_name))); }
    return wrap;
  }

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
      parts.push(t(PRESENCE[p.state] || PRESENCE.unknown, p.seen_ago));
      const m = (g.machines || []).find((x) => x.name === name);
      if (m && m.push_from_operator) { parts.push(t('conv.push')); }
    }
    if (kind === 'paar' || kind === 'alle') { parts.push(t('conv.readonly.pair')); } else if (state.readonly) { parts.push(t('conv.readonly')); }
    $('conv-sub').textContent = parts.join(' · ');
    $('filter').hidden = kind !== 'alle';
  }

  async function select(g, kind, name) {
    current = { group: g.name, key: keyOf(kind, name), kind, name };
    thread = [];
    renderList();
    $('empty').hidden = true;
    $('conv').hidden = false;
    header();
    document.querySelector('.shell').classList.add('reading');
    $('main').focus();
    const mine = current;
    try {
      const r = await api('/api/chat/messages?group=' + encodeURIComponent(g.name) + '&conv=' + encodeURIComponent(mine.key) + '&limit=200');
      if (current !== mine) { return; }
      thread = r.records;
    } catch (e) { banner(t('err.messages', e.message)); }
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

  // ---- what happens in the groups

  function banner(text) {
    $('banner').textContent = text || '';
    $('banner').hidden = !text;
  }

  function showStatus(g) {
    const s = live[g.name] && live[g.name].snapshot && live[g.name].snapshot.status;
    if (!s || s.status === 'connected' || s.status === 'connecting') { banner(s && s.dropped ? t('status.dropped', g.name, s.dropped) : ''); return; }
    if (s.status === 'auth') { banner(t('status.auth', g.name, 'viper nats init ' + g.name)); return; }
    banner(t('status.disturbed', g.name, s.reason || '', s.retry_in || 0));
  }

  async function loadListing(g) {
    const r = await api('/api/chat/conversations?group=' + encodeURIComponent(g.name));
    const convs = {};
    for (const c of r.conversations) { convs[c.key] = c; }
    live[g.name] = Object.assign(live[g.name] || {}, { snapshot: r.snapshot, convs });
    renderList();
    showStatus(g);
  }

  function onMessage(g, ev) {
    const rec = ev.record;
    const L = live[g.name];
    if (!rec || !L) { return; }
    for (const key of ev.keys || []) {
      const c = L.convs[key] || { key, kind: key === 'alle' ? 'alle' : key.split('__')[0], name: key === 'alle' ? '' : key.split('__').slice(1).join(' '), unread: 0 };
      c.last = Object.assign({}, rec, { text: rec.text.length > 140 ? rec.text.slice(0, 140) + ' ...' : rec.text });
      const open = current && current.group === g.name && current.key === key;
      if (!open && rec.dir === 'in') { c.unread = (c.unread || 0) + 1; }
      L.convs[key] = c;
    }
    if (current && current.group === g.name && (ev.keys || []).includes(current.key) && !thread.some((x) => x.n === rec.n)) {
      thread.push(rec);
      append(rec);
      markRead();
    }
    renderList();
  }

  function listen(g) {
    const L = live[g.name] || (live[g.name] = { convs: {} });
    if (L.es) { return; }
    const es = new EventSource('/api/chat/events?group=' + encodeURIComponent(g.name));
    L.es = es;
    es.addEventListener('snapshot', (e) => { L.snapshot = JSON.parse(e.data); showStatus(g); renderList(); header(); });
    es.addEventListener('status', (e) => {
      const ev = JSON.parse(e.data);
      L.snapshot = Object.assign(L.snapshot || { machines: {}, topics: {} }, { status: ev.status });
      showStatus(g);
    });
    es.addEventListener('message', (e) => onMessage(g, JSON.parse(e.data)));
    let seen = false;
    es.onopen = async () => { // after a break: what was missed comes by the numbers
      if (seen) { await catchUp(g); }
      seen = true;
    };
  }

  async function catchUp(g) {
    try {
      await loadListing(g);
      if (current && current.group === g.name && thread.length) {
        const r = await api('/api/chat/messages?group=' + encodeURIComponent(g.name) + '&conv=' + encodeURIComponent(current.key) + '&after=' + thread[thread.length - 1].n + '&limit=500');
        for (const rec of r.records) { thread.push(rec); append(rec); }
      }
    } catch (e) { banner(t('err.load', e.message)); }
  }

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
