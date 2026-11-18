'use strict';
// The texts of the chat in English. A key that the other language lacks is a mistake that a test finds.
(function () {
  window.viperI18n.add('en', {
    'title': 'viper chat',
    'skip': 'Skip to the conversation',
    'lang.label': 'Language',
    'list.label': 'Conversations',
    'msgs.label': 'Messages',
    'msgs.none': 'No messages yet.',
    'me.in': 'Operator {0} logged in',
    'me.out': 'not logged in',
    'me.none': 'no NATS group',
    'sec.topics': 'Topics',
    'sec.machines': 'Machines',
    'sec.pairs': 'Between machines',
    'empty.pick.h': 'Choose a conversation',
    'empty.pick.t': 'The machines and topics of your NATS groups are on the left.',
    'empty.nogroup.h': 'No NATS group here',
    'empty.nogroup.t': 'viper chat talks to the machines of a NATS group (NATS_ENABLED=true in the settings of the machines). This directory has none.',
    'empty.nologin.h': 'You are not logged in in this group',
    'empty.nologin.t': 'Conversations with the machines of {0} need a key of your own, the operator. It belongs to you only and stays on this computer. Log in at the terminal:',
    'conv.machine': 'Machine of the group {0}',
    'conv.topic': 'Topic of the group {0}',
    'conv.push': 'The agent there reads your text in its session.',
    'conv.readonly': 'This page only reads (--readonly).',
    'err.load': 'The groups could not be read: {0}',
  });
})();
