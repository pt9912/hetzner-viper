'use strict';
// The page of viper chat. Everything that comes from the server is set as text (textContent), never as markup. The texts are in
// i18n.en.js and i18n.de.js (loaded by i18n.js, one language at a time).
(function () {
  const $ = (id) => document.getElementById(id);
  const { t } = window.viperI18n;
  let state = { groups: [], readonly: false };
  let current = null; // { group, kind: 'machine' | 'topic', name }

  async function api(path) {
    const res = await fetch(path, { credentials: 'same-origin' });
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

  const colours = ['c1', 'c2', 'c3', 'c4', 'c5'];
  const colourOf = (name) => colours[Array.from(name).reduce((a, c) => a + c.charCodeAt(0), 0) % colours.length];

  function convButton(group, kind, name) {
    const li = el('li');
    const b = el('button', 'conv-btn');
    b.type = 'button';
    b.dataset.key = group.name + '/' + kind + '/' + name;
    const av = el('div', 'av ' + (kind === 'topic' ? 'topic' : colourOf(name)), kind === 'topic' ? '#' : name.slice(0, 2));
    av.setAttribute('aria-hidden', 'true'); // the name beside it is the name of the button
    const body = el('div');
    body.appendChild(el('div', 'nm', name));
    b.appendChild(av);
    b.appendChild(body);
    b.setAttribute('aria-current', String(!!current && current.group === group.name && current.kind === kind && current.name === name));
    b.addEventListener('click', () => select(group, kind, name));
    li.appendChild(b);
    return li;
  }

  function renderList() {
    const root = $('groups');
    root.textContent = '';
    for (const g of state.groups) {
      const box = el('section', 'group');
      const h = el('h2');
      const led = el('span', 'led' + (g.operator ? '' : ' off'));
      led.setAttribute('aria-hidden', 'true');
      h.appendChild(led);
      h.appendChild(document.createTextNode(g.name));
      box.appendChild(h);
      if (g.operator) {
        if ((g.topics || []).length) {
          box.appendChild(el('div', 'sec', t('sec.topics')));
          const ul = el('ul');
          for (const n of g.topics) { ul.appendChild(convButton(g, 'topic', n)); }
          box.appendChild(ul);
        }
        box.appendChild(el('div', 'sec', t('sec.machines')));
        const ul = el('ul');
        for (const m of g.machines || []) { ul.appendChild(convButton(g, 'machine', m.name)); }
        box.appendChild(ul);
      }
      root.appendChild(box);
    }
  }

  function showEmpty(h, text, code) {
    const box = $('empty');
    box.textContent = '';
    box.appendChild(el('h2', '', h));
    box.appendChild(el('p', '', text));
    if (code) { const p = el('p'); p.appendChild(el('code', '', code)); box.appendChild(p); }
    box.hidden = false;
    $('conv').hidden = true;
  }

  function select(group, kind, name) {
    current = { group: group.name, kind, name };
    renderList();
    $('empty').hidden = true;
    $('conv').hidden = false;
    $('conv-av').textContent = kind === 'topic' ? '#' : name.slice(0, 2);
    $('conv-av').className = 'av ' + (kind === 'topic' ? 'topic' : colourOf(name));
    $('conv-name').textContent = (kind === 'topic' ? '#' : '') + name;
    const m = (group.machines || []).find((x) => x.name === name);
    const parts = [t(kind === 'topic' ? 'conv.topic' : 'conv.machine', group.name)];
    if (kind === 'machine' && m && m.push_from_operator) { parts.push(t('conv.push')); }
    if (state.readonly) { parts.push(t('conv.readonly')); }
    $('conv-sub').textContent = parts.join(' · ');
    document.querySelector('.shell').classList.add('reading');
    $('main').focus();
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
      $('banner').hidden = true;
    } catch (e) {
      $('banner').textContent = t('err.load', e.message);
      $('banner').hidden = false;
      return;
    }
    render();
  }

  $('lang').addEventListener('change', async () => {
    await window.viperI18n.setLang($('lang').value);
    window.viperI18n.apply();
    render();
  });
  window.viperI18n.ready.then(() => {
    window.viperI18n.apply();
    $('lang').value = window.viperI18n.lang();
    load();
  });
})();
