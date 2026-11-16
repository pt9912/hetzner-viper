'use strict';
// The engine for the texts of the page. The texts themselves are in i18n.en.js and i18n.de.js: the page loads only the file of
// its language (and the other one when the language changes). The output of viper itself, and the errors of the server, stay
// English.
(function () {
  const dicts = {};
  const languages = ['en', 'de'];
  let lang = 'en';

  function initial() {
    try { const saved = window.localStorage.getItem('viper-ui-lang'); if (saved === 'en' || saved === 'de') { return saved; } } catch (e) { /* no storage: the browser decides */ }
    return (navigator.language || 'en').toLowerCase().startsWith('de') ? 'de' : 'en';
  }

  // add is called by the file of a language
  function add(l, texts) { dicts[l] = texts; }

  // load gives a promise: true when the file of the language is there, false when it could not be read
  function load(l) {
    if (dicts[l]) { return Promise.resolve(true); }
    return new Promise((resolve) => {
      const s = document.createElement('script');
      s.src = '/i18n.' + l + '.js';
      s.onload = () => resolve(!!dicts[l]);
      s.onerror = () => resolve(false);
      document.head.appendChild(s);
    });
  }

  // t gives the text of a key; {0}, {1} are filled with the arguments (as text, never as markup).
  function t(key, ...args) {
    const text = (dicts[lang] && dicts[lang][key]) ?? (dicts.en && dicts.en[key]) ?? key;
    return text.replace(/\{(\d)\}/g, (m, i) => (args[Number(i)] === undefined ? '' : String(args[Number(i)])));
  }

  // apply sets the texts of the elements that name a key: data-i18n (the text), data-i18n-label (aria-label)
  function apply() {
    document.documentElement.lang = lang;
    document.title = t('title');
    for (const el of document.querySelectorAll('[data-i18n]')) { el.textContent = t(el.dataset.i18n); }
    for (const el of document.querySelectorAll('[data-i18n-label]')) { el.setAttribute('aria-label', t(el.dataset.i18nLabel)); }
    for (const el of document.querySelectorAll('[data-i18n-placeholder]')) { el.setAttribute('placeholder', t(el.dataset.i18nPlaceholder)); }
  }

  // setLang loads the language if needed, then switches and sets the texts; a language that could not be loaded is not taken
  // (and not kept), and of two quick choices the last one counts
  let wanted = '';
  async function setLang(l) {
    if (!languages.includes(l)) { return; }
    wanted = l;
    const ok = await load(l);
    if (!ok || wanted !== l) { return; }
    lang = l;
    try { window.localStorage.setItem('viper-ui-lang', l); } catch (e) { /* not kept */ }
    apply();
  }

  lang = initial();
  // ready: the texts of the first language are there (the page waits for it)
  const ready = load(lang).then((ok) => (ok ? true : (lang = 'en', load('en')))); // without the file of the language: English
  window.viperI18n = { t, apply, setLang, add, ready, lang: () => lang, languages };
})();
