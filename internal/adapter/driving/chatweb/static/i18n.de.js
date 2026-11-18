'use strict';
// The texts of the chat in German. A key that the other language lacks is a mistake that a test finds.
(function () {
  window.viperI18n.add('de', {
    'title': 'viper chat',
    'skip': 'Zum Gespräch springen',
    'lang.label': 'Sprache',
    'list.label': 'Gespräche',
    'msgs.label': 'Nachrichten',
    'msgs.none': 'Noch keine Nachrichten.',
    'me.in': 'Operator {0} angemeldet',
    'me.out': 'nicht angemeldet',
    'me.none': 'keine NATS-Gruppe',
    'sec.topics': 'Themen',
    'sec.machines': 'Rechner',
    'sec.pairs': 'Zwischen Rechnern',
    'empty.pick.h': 'Wählen Sie ein Gespräch',
    'empty.pick.t': 'Links stehen die Rechner und Themen Ihrer NATS-Gruppen.',
    'empty.nogroup.h': 'Hier gibt es keine NATS-Gruppe',
    'empty.nogroup.t': 'viper chat spricht mit den Rechnern einer NATS-Gruppe (NATS_ENABLED=true in den Einstellungen der Rechner). In diesem Verzeichnis gibt es keine.',
    'empty.nologin.h': 'Sie sind in dieser Gruppe nicht angemeldet',
    'empty.nologin.t': 'Gespräche mit den Rechnern von {0} brauchen einen eigenen Schlüssel, den „Operator“. Er gehört nur Ihnen und bleibt auf diesem Rechner. Melden Sie sich am Terminal an:',
    'conv.machine': 'Rechner der Gruppe {0}',
    'conv.topic': 'Thema der Gruppe {0}',
    'conv.push': 'Der Agent dort liest Ihren Text in seiner Sitzung.',
    'conv.readonly': 'Diese Seite liest nur (--readonly).',
    'err.load': 'Die Gruppen konnten nicht gelesen werden: {0}',
  });
})();
