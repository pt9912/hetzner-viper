'use strict';
// The texts of the page, in one file: English (the language of viper's own messages) and German. A key that one language has
// and the other lacks is a mistake that a test finds. The output of viper itself, and the errors of the server, stay English.
(function () {
  const en = {
    'title': 'viper',
    'skip': 'Skip to the content',
    'nav.label': 'Pages',
    'lang.label': 'Language',
    'tab.overview': 'Overview', 'tab.runs': 'Runs', 'tab.nats': 'NATS', 'tab.settings': 'Settings', 'tab.logs': 'Logs',
    'banner.session': 'The session has ended (viper ui was restarted, or the key was used elsewhere). Start viper ui again and open its address.',
    'readonly': 'read only',
    'h.machines': 'Machines',
    'th.machine': 'Machine', 'th.group': 'Group', 'th.provider': 'Provider', 'th.type': 'Type', 'th.location': 'Location', 'th.nats': 'NATS',
    'th.state': 'State', 'th.actions': 'Actions', 'th.machines': 'Machines',
    'note.none': 'No machines found: machines/<name>.env.local or groups/<name>.env in the settings directory name them.',
    'note.state': 'State is what this computer knows; Check asks the machine.',
    'note.load': 'Could not load: {0}',
    'state.ready': 'set up', 'state.started': 'started', 'state.none': 'not started',
    'error.prefix': 'error: {0}',
    'repo.label': 'Repository URL for up and preflight (optional)',
    'h.groups': 'Groups',
    'btn.check': 'Check', 'btn.preflight': 'Preflight', 'btn.up': 'Up', 'btn.down': 'Down', 'btn.log': 'Log',
    'btn.group.preflight': 'Preflight group', 'btn.group.up': 'Up group', 'btn.group.down': 'Down group',
    'aria.check': 'Check {0}', 'aria.preflight': 'Preflight {0}', 'aria.up': 'Up {0}', 'aria.down': 'Down {0}', 'aria.log': 'Log of {0}',
    'aria.group.preflight': 'Preflight group {0}', 'aria.group.up': 'Up group {0}', 'aria.group.down': 'Down group {0}',
    'h.check': 'Check',
    'check.hint': 'Pick a machine and press Check: it runs viper check and shows what it says.',
    'check.running': 'Checking {0} ...', 'check.ok': '{0}: OK', 'check.problems': '{0}: problems (exit {1})', 'check.error': '{0}: {1}',
    'cut': '[cut]',
    'h.runs': 'Runs',
    'runs.intro': 'A run is viper itself, as at the terminal. A question (Type DELETE ...) is answered below, with the word you type.',
    'runs.none': 'No runs yet.',
    'run.answer': 'Answer', 'run.send': 'Send', 'run.cancel': 'Cancel (like Ctrl-C)',
    'run.cancel.hint': 'Cancel does not undo: a server that was half made can remain.',
    'run.download': 'Download the file',
    'run.notstarted': 'Not started: {0}',
    'status.running': 'Running ...', 'status.waiting': 'Waiting for your answer', 'status.cancelling': 'Ending (this can take a while) ...',
    'status.done0': 'Done: exit 0', 'status.ended': 'Ended: exit {0}', 'status.cancelled': ' (cancelled)',
    'run.item': '{0} {1} ({2})', 'run.state.running': 'running', 'run.state.waiting': 'waiting', 'run.state.cancelling': 'cancelling', 'run.state.done': 'done, exit {0}',
    'h.nats': 'NATS group',
    'nats.none': 'No group with NATS machines found (groups/<name>.env with NATS_ENABLED machines).',
    'nats.group': 'Group', 'nats.from': 'Speak as',
    'nats.from.operator': 'the operator (key found here)', 'nats.from.machine': 'a machine of the group (over ssh)', 'nats.machine': 'machine {0}',
    'nats.mode.operator': 'This computer has the key of the operator: viper nats speaks as the operator.',
    'nats.mode.machine': 'No key of the operator here (viper nats init {0}): viper nats speaks as a machine of the group, over ssh.',
    'h.who': 'Who is there', 'nats.wait': 'Wait (seconds)', 'nats.peers': 'Peers', 'nats.topics': 'Topics',
    'h.listen': 'Listen', 'listen.topics': 'Topics (optional, with commas)', 'listen.all': 'everything of the group', 'nats.listen': 'Listen',
    'h.send': 'Send', 'nats.kind': 'What', 'kind.send': 'Message to a machine', 'kind.request': 'Request to a machine (wait for the answer)', 'kind.broadcast': 'Round to a topic',
    'nats.to': 'To', 'nats.topic': 'Topic', 'nats.reqtimeout': 'Wait for the answer (seconds)', 'nats.text': 'Text (up to 16 KiB)', 'nats.file': 'File (PDF, image or ZIP)',
    'nats.nofile': 'This group has no object store (NATS_OBJECTS=true): no files.', 'nats.yes': 'send anyway if the agent there gets it pushed (--yes)', 'nats.send': 'Send', 'nats.handing': 'Handing over the file ...',
    'h.fetch': 'Get a file', 'nats.id': 'ID', 'nats.sha': 'SHA-256', 'nats.fetch': 'Fetch', 'nats.fetchthis': 'Fetch this file',
    'nats.untrusted': 'A fetched file is from another agent: untrusted. Do not run it.',
    'nl.peer': 'present: {0}', 'nl.peer.topics': ' (topics: {0})', 'nl.configured': 'set in the settings: {0}: {1}', 'nl.notopic': 'no topic',
    'nl.answer': 'answer from {0}: {1}', 'nl.sent': 'sent', 'nl.fetched': 'fetched: {0}', 'nl.ready': 'ready: {0} in {1}', 'nl.file': ' [file {0}, {1}, {2} bytes]',
    'h.settings': 'Settings',
    'settings.intro': 'The names of the settings in each file of the settings directory. A value is shown only for settings that are no credentials and hold nothing private; the rest is "set". Nothing here can be changed.',
    'settings.set': 'set', 'settings.empty': 'no files found', 'settings.env': 'environment', 'settings.envnote': 'Set in the environment and in no file (the value is never shown).',
    'settings.noentries': 'no settings', 'th.key': 'Setting', 'th.value': 'Value',
    'h.logs': 'Logs', 'log.machine': 'Machine', 'log.all': 'all machines (events)', 'log.full': 'complete output', 'log.search': 'Search', 'log.reload': 'Reload', 'log.loading': 'Loading ...',
  };
  const de = {
    'title': 'viper',
    'skip': 'Zum Inhalt springen',
    'nav.label': 'Seiten',
    'lang.label': 'Sprache',
    'tab.overview': 'Übersicht', 'tab.runs': 'Läufe', 'tab.nats': 'NATS', 'tab.settings': 'Einstellungen', 'tab.logs': 'Protokolle',
    'banner.session': 'Die Sitzung ist zu Ende (viper ui wurde neu gestartet, oder der Schlüssel wurde woanders benutzt). Starten Sie viper ui neu und öffnen Sie seine Adresse.',
    'readonly': 'nur lesen',
    'h.machines': 'Rechner',
    'th.machine': 'Rechner', 'th.group': 'Gruppe', 'th.provider': 'Anbieter', 'th.type': 'Typ', 'th.location': 'Ort', 'th.nats': 'NATS',
    'th.state': 'Zustand', 'th.actions': 'Aktionen', 'th.machines': 'Rechner',
    'note.none': 'Keine Rechner gefunden: machines/<name>.env.local oder groups/<name>.env im Einstellungsverzeichnis nennen sie.',
    'note.state': 'Der Zustand ist, was dieser Computer weiß; „Prüfen“ fragt den Rechner.',
    'note.load': 'Konnte nicht laden: {0}',
    'state.ready': 'eingerichtet', 'state.started': 'gestartet', 'state.none': 'nicht gestartet',
    'error.prefix': 'Fehler: {0}',
    'repo.label': 'Repository-URL für Up und Preflight (optional)',
    'h.groups': 'Gruppen',
    'btn.check': 'Prüfen', 'btn.preflight': 'Vorprüfung', 'btn.up': 'Up', 'btn.down': 'Down', 'btn.log': 'Protokoll',
    'btn.group.preflight': 'Gruppe vorprüfen', 'btn.group.up': 'Gruppe: Up', 'btn.group.down': 'Gruppe: Down',
    'aria.check': '{0} prüfen', 'aria.preflight': '{0} vorprüfen', 'aria.up': 'Up für {0}', 'aria.down': 'Down für {0}', 'aria.log': 'Protokoll von {0}',
    'aria.group.preflight': 'Gruppe {0} vorprüfen', 'aria.group.up': 'Up für Gruppe {0}', 'aria.group.down': 'Down für Gruppe {0}',
    'h.check': 'Prüfung',
    'check.hint': 'Wählen Sie einen Rechner und drücken Sie „Prüfen“: Das führt viper check aus und zeigt dessen Ausgabe.',
    'check.running': '{0} wird geprüft ...', 'check.ok': '{0}: in Ordnung', 'check.problems': '{0}: Probleme (Exit {1})', 'check.error': '{0}: {1}',
    'cut': '[gekürzt]',
    'h.runs': 'Läufe',
    'runs.intro': 'Ein Lauf ist viper selbst, wie am Terminal. Eine Rückfrage (Type DELETE ...) beantworten Sie unten mit dem Wort, das Sie tippen.',
    'runs.none': 'Noch keine Läufe.',
    'run.answer': 'Antwort', 'run.send': 'Senden', 'run.cancel': 'Abbrechen (wie Strg-C)',
    'run.cancel.hint': 'Abbrechen macht nichts rückgängig: Ein halb angelegter Server kann bleiben.',
    'run.download': 'Datei herunterladen',
    'run.notstarted': 'Nicht gestartet: {0}',
    'status.running': 'Läuft ...', 'status.waiting': 'Wartet auf Ihre Antwort', 'status.cancelling': 'Wird beendet (das kann dauern) ...',
    'status.done0': 'Fertig: Exit 0', 'status.ended': 'Beendet: Exit {0}', 'status.cancelled': ' (abgebrochen)',
    'run.item': '{0} {1} ({2})', 'run.state.running': 'läuft', 'run.state.waiting': 'wartet', 'run.state.cancelling': 'wird beendet', 'run.state.done': 'fertig, Exit {0}',
    'h.nats': 'NATS-Gruppe',
    'nats.none': 'Keine Gruppe mit NATS-Rechnern gefunden (groups/<name>.env mit Rechnern, die NATS_ENABLED haben).',
    'nats.group': 'Gruppe', 'nats.from': 'Sprechen als',
    'nats.from.operator': 'der Betreiber (Schlüssel liegt hier)', 'nats.from.machine': 'ein Rechner der Gruppe (über ssh)', 'nats.machine': 'Rechner {0}',
    'nats.mode.operator': 'Dieser Computer hat den Schlüssel des Betreibers: viper nats spricht als Betreiber.',
    'nats.mode.machine': 'Kein Schlüssel des Betreibers hier (viper nats init {0}): viper nats spricht als Rechner der Gruppe, über ssh.',
    'h.who': 'Wer ist da', 'nats.wait': 'Warten (Sekunden)', 'nats.peers': 'Peers', 'nats.topics': 'Themen',
    'h.listen': 'Mitlesen', 'listen.topics': 'Themen (optional, mit Kommas)', 'listen.all': 'alles in der Gruppe', 'nats.listen': 'Mitlesen',
    'h.send': 'Senden', 'nats.kind': 'Was', 'kind.send': 'Nachricht an einen Rechner', 'kind.request': 'Anfrage an einen Rechner (auf die Antwort warten)', 'kind.broadcast': 'Rundruf an ein Thema',
    'nats.to': 'An', 'nats.topic': 'Thema', 'nats.reqtimeout': 'Auf die Antwort warten (Sekunden)', 'nats.text': 'Text (bis 16 KiB)', 'nats.file': 'Datei (PDF, Bild oder ZIP)',
    'nats.nofile': 'Diese Gruppe hat keinen Objektspeicher (NATS_OBJECTS=true): keine Dateien.', 'nats.yes': 'trotzdem senden, auch wenn der Agent dort es in seine Sitzung geschoben bekommt (--yes)', 'nats.send': 'Senden', 'nats.handing': 'Die Datei wird übergeben ...',
    'h.fetch': 'Datei holen', 'nats.id': 'ID', 'nats.sha': 'SHA-256', 'nats.fetch': 'Holen', 'nats.fetchthis': 'Diese Datei holen',
    'nats.untrusted': 'Eine geholte Datei stammt von einem anderen Agenten: nicht vertrauenswürdig. Nicht ausführen.',
    'nl.peer': 'da: {0}', 'nl.peer.topics': ' (Themen: {0})', 'nl.configured': 'in den Einstellungen: {0}: {1}', 'nl.notopic': 'kein Thema',
    'nl.answer': 'Antwort von {0}: {1}', 'nl.sent': 'gesendet', 'nl.fetched': 'geholt: {0}', 'nl.ready': 'bereit: {0} in {1}', 'nl.file': ' [Datei {0}, {1}, {2} Bytes]',
    'h.settings': 'Einstellungen',
    'settings.intro': 'Die Namen der Einstellungen in jeder Datei des Einstellungsverzeichnisses. Ein Wert wird nur bei Einstellungen gezeigt, die keine Zugangsdaten sind und nichts Privates enthalten; der Rest ist „gesetzt“. Hier lässt sich nichts ändern.',
    'settings.set': 'gesetzt', 'settings.empty': 'keine Dateien gefunden', 'settings.env': 'Umgebung', 'settings.envnote': 'In der Umgebung gesetzt und in keiner Datei (der Wert wird nie gezeigt).',
    'settings.noentries': 'keine Einstellungen', 'th.key': 'Einstellung', 'th.value': 'Wert',
    'h.logs': 'Protokolle', 'log.machine': 'Rechner', 'log.all': 'alle Rechner (Ereignisse)', 'log.full': 'vollständige Ausgabe', 'log.search': 'Suchen', 'log.reload': 'Neu laden', 'log.loading': 'Lädt ...',
  };
  const dicts = { en, de };
  let lang = 'en';

  function initial() {
    try { const saved = window.localStorage.getItem('viper-ui-lang'); if (saved === 'en' || saved === 'de') { return saved; } } catch (e) { /* no storage: the browser decides */ }
    return (navigator.language || 'en').toLowerCase().startsWith('de') ? 'de' : 'en';
  }

  // t gives the text of a key; {0}, {1} are filled with the arguments (as text, never as markup).
  function t(key, ...args) {
    const text = dicts[lang][key] ?? en[key] ?? key;
    return text.replace(/\{(\d)\}/g, (m, i) => (args[Number(i)] === undefined ? '' : String(args[Number(i)])));
  }

  // apply sets the texts of the elements that name a key: data-i18n (the text), data-i18n-label (aria-label)
  function apply() {
    document.documentElement.lang = lang;
    document.title = t('title');
    for (const el of document.querySelectorAll('[data-i18n]')) { el.textContent = t(el.dataset.i18n); }
    for (const el of document.querySelectorAll('[data-i18n-label]')) { el.setAttribute('aria-label', t(el.dataset.i18nLabel)); }
  }

  function setLang(l) {
    if (!dicts[l]) { return; }
    lang = l;
    try { window.localStorage.setItem('viper-ui-lang', l); } catch (e) { /* not kept */ }
    apply();
  }

  lang = initial();
  window.viperI18n = { t, apply, setLang, lang: () => lang, languages: Object.keys(dicts) };
})();
