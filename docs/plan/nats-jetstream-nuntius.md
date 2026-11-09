# Plan: JetStream und Nuntius für NATS-Gruppen

**Ziel:** Die Rechner einer NATS-Gruppe (siehe [`nats.md`](nats.md)) sollen [Nuntius](https://github.com/JaredCluff/nuntius) nutzen können, einen MCP-Server, der Claude Code mit NATS verbindet. Nuntius kann nicht geändert werden (es gehört nicht zu diesem Projekt). Alles, was nötig ist, geschieht deshalb auf der `viper`-Seite: JetStream am Server, Berechtigungen, Zertifikat, Installation und Startskript.

Beides bleibt optional und standardmäßig aus. Ohne die neuen Einstellungen ändert sich für bestehende Gruppen nichts (Ausnahme: das Zertifikat neuer Gruppen, §4.2).

**Stand:** Der Plan wurde zweimal unabhängig geprüft; die Befunde sind eingearbeitet. Es ist noch nichts umgesetzt.

**Kennzeichnung:** *[V]* heißt, dass es im Code von Nuntius oder von `async-nats` gelesen wurde. *[?]* heißt Vermutung (aus Wissen über nats-server, rustls oder Claude Code); jede solche Stelle ist in Meilenstein 0 zu prüfen, bevor etwas darauf gebaut wird. Stellen, die eine Entscheidung des Betreibers brauchen und hier als Arbeitsannahme stehen, sind mit **(zu bestätigen)** markiert.

## 1 Ausgangslage

Gelesen wurde Nuntius auf dem Commit `709acf4` (25.03.2026, Tag `v0.1.0`; 19 Commits, `Cargo.lock` ist eingecheckt, kein `rust-version`). Die Angaben sind bei Umsetzungsbeginn gegen den dann gewählten Commit zu prüfen.

| Fund | Folge für uns |
|---|---|
| 17 Werkzeuge: 6 mit Core-NATS (`nats_publish`, `nats_request`, `nats_subscribe`, `nats_unsubscribe`, `agent_claim`, `request_permission`), 11 mit JetStream (`js_*` ×5, `kv_*` ×4, `agent_announce`, `agent_discover`) *[V]* | Ohne JetStream laufen die 6 Core-Werkzeuge |
| Beim Start und alle 2 Minuten: Eintrag im KV-Bucket `agents-registry` (TTL 5 Minuten) und Veröffentlichung auf `agents.registry.announce`. Nuntius legt den Bucket an, wenn er fehlt, und ändert die TTL per Stream-Update **nur, wenn sie vom Sollwert abweicht** *[V]* | Legt `viper` den Bucket mit der richtigen TTL selbst an, braucht kein Rechner das Recht, ihn anzulegen oder zu ändern (§4.2) |
| `kv.keys()` (für `kv_keys` und `agent_discover`) legt einen geordneten Push-Consumer ohne Namen an; sein Subject ist exakt `$JS.API.CONSUMER.CREATE.KV_agents-registry` (ohne Suffix) *[V]* | Das Muster `….CREATE.KV_agents-registry.>` passt darauf nicht; die Rechte-Liste wird aus einem Mitschnitt abgeleitet (§4.3, Meilenstein 0) |
| Scheitert die Anmeldung im Register, wird nur gewarnt. Ein Verbindungsfehler beim Start beendet Nuntius mit `exit(1)`, ohne Wiederholung *[V]*. Ohne JetStream-Rechte kann die Anmeldung durch fehlende Antworten einige Sekunden hängen *[?]* | Der Start ohne JetStream ist möglich, aber nicht lautlos; das Startskript wartet auf den Server (§4.4) |
| Auto-Abonnement von `claude.<id>.in.>` *[V]*; `<id>` ist `NUNTIUS_INSTANCE_ID`, sonst zufällig | Wir setzen sie auf den Maschinennamen |
| `nuntius.claim` ist der **Name einer Queue-Gruppe**, kein Subject: `agent_claim` abonniert das vom Aufrufer genannte Subject mit dieser Gruppe *[V]* | Keine eigene Berechtigung; `agent_claim` geht nur auf Subjects, die ohnehin erlaubt sind (`viper.<g>.>`) |
| Nuntius veröffentlicht auf `agents.registry.announce` und liest dort nie *[V]* | Nur Senderecht; ein Leserecht wäre ein reiner Einschleusungsweg |
| `request_permission` sendet an `animus.in.permission_request` *[V]* | Bleibt gesperrt: Es fehlt der Gegenpart (Animus) |
| `nats_request` nutzt das Standard-Inbox-Präfix `_INBOX.`, keine Einstellung *[V]* | Antworten sind nur möglich, wenn `_INBOX.>` gelesen werden darf |
| Authentifizierung: Token vor Passwort vor NKey; alle Einstellungen kommen aus `NUNTIUS_*`-Umgebungsvariablen, auch `NUNTIUS_AUTH_TOKEN`, `NUNTIUS_USER`, `NUNTIUS_PASS` und `NUNTIUS_STARTUP_SUBS` *[V]* | Das Startskript räumt alle `NUNTIUS_*` ab, bevor es seine eigenen setzt (§4.4) |
| `NUNTIUS_NKEY` ist der Seed selbst, kein Dateipfad *[V]* | Das Startskript liest den Seed aus der Datei |
| TLS: `NUNTIUS_TLS_CERT` und `NUNTIUS_TLS_KEY` (Client-Zertifikat für mTLS) werden gelesen, aber nirgends benutzt; es gibt keine CA-Option *[V]*. `async-nats` 0.38 lädt die System-Zertifikate, wenn keine eigenen angegeben sind (`src/tls.rs`), und Nuntius gibt keine an *[V]* | Das Serverzertifikat muss über die Umgebung oder das System vertraut werden (§4.4) |
| Die System-Zertifikate kommen über `rustls-native-certs` (0.7.3/0.8.3 in `Cargo.lock`); sie beachten `SSL_CERT_FILE` *[?]* | `SSL_CERT_FILE` im Startskript genügt vielleicht |
| **Unser Serverzertifikat ist als CA markiert:** `openssl req -x509` setzt `basicConstraints=critical,CA:TRUE` (auf dem Entwicklungsrechner nachgebaut mit dem Aufruf aus `natscert.sh`) *[V]*. rustls/webpki lehnt ein CA-Zertifikat als Serverzertifikat ab (`CaUsedAsEndEntity`) *[?]*; der Go-Client `nats`, den wir bisher benutzen, ist nachsichtig | Wahrscheinlich scheitert TLS mit Nuntius, und `SSL_CERT_FILE` hilft dann nicht. Die Lösung ist ein Blattzertifikat (`CA:FALSE`, §4.2) |
| Weiterleitung an Claude: Jede Nachricht auf einem **abonnierten** Subject wird als `notifications/claude/channel` gesendet (Text, `subject`, `ts`, ggf. `reply_to`), ohne Absenderangabe und ohne Maskierung *[V]*. Nuntius meldet dafür `experimental.claude/channel` an *[V]* | Siehe §3; ob und wie Claude Code das annimmt, ist offen *[?]* (Meilenstein 0, Tor) |

## 2 Entscheidungen

| Thema | Festlegung |
|---|---|
| JetStream | `NATS_JETSTREAM=off` (Standard), `registry` oder `full` **(zu bestätigen)**. `registry`: JetStream läuft und `viper` legt den Bucket `agents-registry` selbst mit fester Konfiguration an; die Rechner dürfen ihn nur lesen, beschreiben und Consumer darauf anlegen, aber nicht ändern; `agent_announce` und `agent_discover` gehen, `js_*` und `kv_*` mit beliebigen Namen nicht. `full`: alle 17 Werkzeuge; jeder Rechner mit Profil `nuntius` darf die JetStream-Schnittstelle voll nutzen (siehe §3, Punkt 1: das hebt die Trennung der Subjects innerhalb der Gruppe praktisch auf) |
| Berechtigungsprofil | `NATS_PROFILE=default` (Standard, unverändert) oder `nuntius`. Das Profil gilt **je Maschine** **(zu bestätigen)** und steht in einer eigenen Zeile `# viper-profile <maschine> nuntius` in `server.conf` (§4.2). Eine Maschine ohne Profil `nuntius` darf nicht an die Kanäle von Nuntius senden |
| Gruppeneinstellung | Nur `NATS_JETSTREAM` gilt für die Gruppe. Es wird beim Einrichten des **Servers** in `server.conf` festgehalten. Eine abweichende Einstellung auf einem Client ändert nichts; `viper up` auf dem Client warnt sofort, `viper check` meldet sie |
| Nuntius selbst | `NATS_NUNTIUS=true` (verlangt `NATS_PROFILE=nuntius`) installiert und konfiguriert Nuntius auf dem Rechner. Standard: aus. Ohne `NATS_NUNTIUS` bleibt das Profil `nuntius` möglich (der Rechner kann dann die Werkzeuge einer von Hand eingerichteten Nuntius-Installation nutzen) |
| Instanz-ID von Nuntius | Der Name der Maschine (`<maschine>`), ein gültiges einzelnes Token nach `model.CheckNATSName`. Die Gruppe steckt nicht darin: Jede Gruppe hat einen eigenen Server, Kanäle verschiedener Gruppen können sich nicht begegnen |
| Installation von Nuntius | Aus dem Quellcode: `cargo install --locked --git https://github.com/JaredCluff/nuntius --rev <Commit> nuntius` (Paketname angeben, der Workspace hat mehrere Pakete). Der Commit wird in `viper` fest eingetragen und nur bewusst angehoben. Ein Test stellt sicher, dass `--locked` und `--rev` immer im Aufruf stehen |
| Zertifikat | Neue Gruppen bekommen ein Blattzertifikat mit `basicConstraints=critical,CA:FALSE` und `extendedKeyUsage=serverAuth` (§4.2). Für Nuntius dann zuerst `SSL_CERT_FILE=<Serverzertifikat>` im Startskript; der Eintrag im System-Truststore bleibt nur der Rückfall, wenn Meilenstein 0 zeigt, dass das nicht reicht (mit Aufräumen, §4.5) |
| Speicher | JetStream legt Daten auf der Platte des Servers ab, mit Pflichtgrenzen (§4.2). Sie überleben einen Neustart des Dienstes und gehen mit der Maschine verloren |
| Hochverfügbarkeit | Keine. Der Server ist einzeln, ein Stream hat nur ein Replikat (Voreinstellung bei einem Server) |

**Offene Entscheidungen** (bis Ende Meilenstein 0 klären):
1. Wie Nuntius auf den Rechner kommt: **A)** auf jedem Rechner mit `cargo install` (Toolchain per Homebrew oder geprüfter `rustup`-Installation; Dauer und Speicher auf `cpx22` messen), oder **B)** einmal bauen und als Datei mit Prüfsumme verteilen. Vorschlag: A, nur wenn der Build auf `cpx22` mit begrenztem Speicher durchläuft; sonst B.
2. Welche Rust-Version genügt (`Cargo.lock` Version 4 verlangt mindestens Cargo 1.78; Abhängigkeiten verlangen womöglich mehr, als Ubuntu liefert).

## 3 Risiken, die der Plan benennt

1. **`$JS.API.>` und Zugriff auf Consumer heben die Trennung der Subjects auf *[?]*.** Die Rechte in `server.conf` gelten für das, was ein Rechner selbst sendet und liest. JetStream führt Aufträge im Namen des Servers aus:
    * Ein Stream mit `subjects: ["viper.>"]` fängt den Gruppenverkehr ein, ohne dass der Rechner `viper.<g>.>` lesen dürfte; er liest dann über `DIRECT.GET` oder einen Consumer.
    * Ein Push-Consumer mit frei gewähltem `deliver_subject` lässt den Server auf Subjects veröffentlichen, die der Rechner nicht senden darf (etwa `claude.<anderer>.in.>`). Der Server prüft beim Anlegen nach unserem Wissen nur die API-Rechte, nicht das Senderecht auf `deliver_subject`.

    **Standardannahme: offen.** Mit `full` ist beides möglich. Mit `registry` bleibt der Weg über den Consumer auf dem Bucket (der Rechner kann den Bucket beschreiben und sich die Werte an ein beliebiges Subject liefern lassen); dass der Rechner die Konfiguration des Buckets nicht ändern kann, schließt nur den Weg über `subjects: ["viper.>"]`. Meilenstein 0 versucht beides und weicht von der Standardannahme nur mit einem Gegenbeweis ab. Bleibt es offen, gilt „innerhalb der Gruppe ist JetStream offen“, und das Handbuch sagt es so; „alles andere wird abgelehnt“ gilt dann nur ohne JetStream.
2. **Prompt-Injection zwischen den Rechnern.** Nuntius meldet an Claude Code jede Nachricht auf einem abonnierten Subject als `notifications/claude/channel` (Text der Nachricht, nicht als UTF-8 lesbare Daten als Base64, dazu `subject`, `ts`, `reply_to`) *[V]*. Abonniert sind:
    1. `claude.<id>.in.>` (automatisch beim Start),
    2. die Subjects aus `NUNTIUS_STARTUP_SUBS`,
    3. alles, was Claude selbst zur Laufzeit mit `nats_subscribe` abonniert, auch auf Anweisung aus einer eingegangenen Nachricht.

    Es gibt **keine Absenderangabe**: Wer auf `claude.<id>.in.>` senden darf, kann Text in die Sitzung des Agenten schreiben, ohne dass dieser erkennt, von wem er stammt. Mit `reply_to` kann er den Agenten auch zu einer Antwort an eine gewünschte Adresse bringen. Die Maskierung von `<` und `&` (`format_channel_notification`) wird auf diesem Weg nicht benutzt *[V]*. Ein kompromittierter Rechner wird so zum Weg auf alle anderen Agenten, die Code lesen und ausführen. Gegenmaßnahmen, die in §4 und §8 umgesetzt sind:
    * Das Senden an `claude.*.in.>` bekommen nur Rechner mit dem Profil `nuntius` (je Maschine). Sonst könnte jede Maschine der Gruppe, auch ohne Nuntius, in die Sitzungen anderer schreiben.
    * `NUNTIUS_STARTUP_SUBS` ist leer, und das Startskript räumt es ab (eine geerbte Variable würde sonst fremde Subjects abonnieren).
    * Das Handbuch beschreibt die Gefahr und sagt, dass die Gruppe eine Vertrauensgrenze ist (`nats.md` §6: jede Maschine darf die ganze Gruppe schon mitlesen).
    * Persistente Einschleusung: Ein KV-Wert oder Stream kann bösartige Anweisungen speichern, die ein Agent später mit `kv_get` oder `js_consume` als Daten liest. Mit `registry` ist das auf den Bucket des Registers begrenzt.
3. **Das Register ist nicht fälschungssicher.** `agent_announce` lässt die `agent_id` frei wählen *[V]*, und jeder Rechner darf in `$KV.agents-registry.>` schreiben. Ein Rechner kann sich daher als ein anderer ausgeben und dessen Eintrag überschreiben. Das Register ist eine Hilfe zum Finden, kein Nachweis der Identität.
4. **Der Seed gehört dem Agenten.** Nuntius bekommt ihn als Umgebungsvariable (`/proc/<pid>/environ`); der Agent läuft als derselbe Nutzer und kann `client.nk` ohnehin lesen. Nach einer Injektion kann ein Agent die Identität der Maschine missbrauchen, auch ohne Nuntius. Deshalb sind die Rechte je Maschine so eng wie möglich (Punkt 2). Das Profil ist **kein Schutz** vor einem, der die Konfiguration des Servers ändern kann: Das tut nur `viper` vom Rechner des Betreibers aus (die Clients haben seinen SSH-Schlüssel nicht, `ForwardAgent` ist aus; in Meilenstein 0 zu bestätigen).
5. **Überflutung und Plattenplatz.** Ein Rechner kann andere Agenten mit Nachrichten überfluten (Kosten, gefüllter Kontext) oder JetStream füllen; ist die Grenze erreicht, scheitern Schreibvorgänge für alle, auch das Anmelden im Register. Die Grenzen des Servers (§4.2) gelten deshalb schon, sobald eine Maschine das Profil `nuntius` hat, nicht erst mit JetStream. Pro Nutzer sind sie nicht einstellbar.
6. **Keine Nachverfolgung.** Ohne Accounts gibt es keine Absenderangabe auf Nachrichten und kein brauchbares Protokoll darüber, wer was gesendet hat.
7. **Ungeprüfter Fremdcode.** Nuntius läuft mit den Rechten von `builder` (passwortloses `sudo`) und sieht Claude-Token und Repository. Der Commit ist fest gepinnt und wird vor dem Anheben gelesen. Es gibt weder Binaries noch Prüfsummen vom Autor; die Abhängigkeiten sind über `Cargo.lock` gesichert, die Rust-Toolchain muss mit einer Prüfsumme oder über Homebrew kommen (kein ungeprüftes `curl | sh`).
8. **Breite der Rechte.** `_INBOX.>` zu lesen erlaubt, Antworten anderer Maschinen mitzulesen, und mit JetStream auch deren Nachrichten (Pull-Antworten, Acks). Das gilt nur mit dem Profil `nuntius`.

## 4 Umsetzung

### 4.1 Einstellungen und Modell

`NATS_JETSTREAM`, `NATS_PROFILE` und `NATS_NUNTIUS` werden in `internal/model` und `settingsfile` gelesen und geprüft: `NATS_JETSTREAM` ist `off`, `registry` oder `full`; `NATS_PROFILE` ist `default` oder `nuntius`; `NATS_NUNTIUS` verlangt Profil `nuntius`; alle drei verlangen `NATS_ENABLED`. Ohne `NATS_ENABLED=true` werden sie nicht gelesen und stören nicht (wie bisher). `Permissions` bekommt statt einzelner Parameter eine Struktur `Policy{Profile, JetStream}`; `Client` bekommt das Feld `Profile`. Aufrufer und Tests werden angepasst.

### 4.2 Server (`internal/natsserver` und Skripte)

* **Zertifikat (gilt für neue Gruppen, unabhängig von JetStream):** `natscert.sh` erzeugt ein Blattzertifikat: `-addext basicConstraints=critical,CA:FALSE`, `-addext keyUsage=critical,digitalSignature`, `-addext extendedKeyUsage=serverAuth`, SAN mit der IP wie bisher. Bestehende Zertifikate bleiben (sie werden nicht erneuert; die Gruppen sind kurzlebig), `viper check` weist bei einem CA-Zertifikat darauf hin, wenn Nuntius genutzt wird. Ein Test erzeugt das Zertifikat mit dem echten Skript und prüft die Erweiterungen und dass `nats-server` und der `nats`-Client es weiter akzeptieren. Das ist die einzige Änderung für Gruppen ohne die neuen Einstellungen.
* **Kopfzeilen von `server.conf`:** `# viper-jetstream off|registry|full` (nur wenn nicht `off`) und je Maschine mit Profil `nuntius` eine Zeile `# viper-profile <maschine> nuntius`. Die Zeilen `# viper-client <maschine> <schlüssel>` bleiben unverändert mit genau zwei Feldern: Eine ältere `viper`-Version, die nur zwei Felder liest, würde sonst Maschinen mit weiteren Feldern bei der nächsten Registrierung stillschweigend aus der Datei werfen; unbekannte Zeilen übersieht sie dagegen. Eine ältere Version würde die neuen Zeilen allerdings beim nächsten Schreiben verlieren; das steht im Handbuch („alle Rechner der Gruppe mit derselben Version arbeiten lassen“).
* **`ParseSettings`** analog `ParseClients` und `ParseEndpoint`: liest `# viper-jetstream` und die Profilzeilen; Unbekanntes wird abgelehnt oder fällt auf den Standard.
* **`Ops.Update`:** Die Funktion `change` bleibt `[]Client → []Client` (das Profil ist Teil von `Client`). `Update` liest die Gruppeneinstellung selbst aus der vorhandenen Datei (`ParseSettings`) und reicht sie an `Render` weiter, sodass eine Registrierung sie nie verändert. Nur der Einrichtungspfad des Servers übergibt eine abweichende Einstellung.
* **`Render`:** Bei `registry` oder `full` ein Block `jetstream { store_dir: "/var/lib/viper-nats/jetstream", max_memory_store: …, max_file_store: … }` mit **festen Standardwerten** (Vorschlag: 256 MB Speicher, 1 GB Platte; ein freier Platz ist Zustand der Maschine und gehört nicht in eine reine Funktion). Dazu `max_payload`, `max_connections` und `max_subscriptions` mit festen Werten (Vorschlag: 256 KB, 128, 256), sobald JetStream an ist **oder** mindestens eine Maschine das Profil `nuntius` hat; ohne beides schreibt `Render` unverändert dieselbe Datei wie heute (eine Referenzdatei mit der heutigen Ausgabe liegt im Repository, §6).
* **Platz prüfen:** Das Skript, das den Server einrichtet, bricht ab, wenn der freie Platz unter dem Doppelten von `max_file_store` liegt.
* **Bucket des Registers (nur `registry`):** Nach dem Start von JetStream legt `viper` den Bucket mit dem mitgelieferten `nats`-Programm an: `nats kv add agents-registry --ttl 5m --history 1 --replicas 1 --max-value-size 4096 --max-bucket-size 8MB` (Werte fest; die TTL muss der von Nuntius erwarteten entsprechen, damit es sie nicht ändert *[V]*). Das geschieht mit einer **Einrichtungs-Identität**: einem Schlüsselpaar, das `viper` im Speicher erzeugt, für die Dauer des Befehls mit den nötigen JetStream-Rechten in die Konfiguration einträgt (Reload), benutzt und sofort wieder entfernt. Die Rechner (auch der Server selbst) haben nie das Recht, den Bucket anzulegen oder zu ändern. Meilenstein 0 prüft den Ablauf (Rechte, Reload, Bucket bleibt nach Neustart).
* **Ein- und Ausschalten braucht einen Neustart *[?]*:** `natsconfig.sh` führt bisher immer `systemctl reload` aus, was nur ein `kill -HUP` ist und auch dann Erfolg meldet, wenn der Server die Datei später ablehnt. Neu für jede Änderung: der Reload (oder bei geänderten JetStream-Zeilen der Neustart; das Skript vergleicht die JetStream-Zeilen der alten und neuen Datei) und danach die **Nachprüfung** über das INFO des Servers (`"jetstream":true/false`, und dass ein Login mit einem vorhandenen Schlüssel noch geht). Scheitert sie, spielt das Skript die gesicherte alte `server.conf` zurück, startet bzw. lädt neu und meldet erst dann den Fehler (mit der Meldung des Dienstes), damit `Restart=on-failure` nicht mit einer kaputten Datei schleift. `viper up` auf dem Server sagt, dass die Verbindungen beim Neustart kurz abreißen. Beim Ausschalten bleiben die Daten auf der Platte.
* **systemd:** `StateDirectory=viper-nats` (legt `/var/lib/viper-nats` für `User=builder` an, schreibbar auch unter `ProtectSystem=strict`) wird **nur** in die Unit geschrieben, wenn JetStream an ist; bei `off` bleibt die Unit byte-identisch zu heute, damit sich bestehende Gruppen nicht ändern. Systemd legt das Verzeichnis selbst an. `natsunit.sh` startet den Dienst bisher nur (`start`); bei geänderter Unit führt es künftig `daemon-reload` und `restart` aus (nur dann).
* **`nats-server -t`** prüft die Datei, legt aber `store_dir` nicht an und prüft keine Schreibrechte; ein Schreibfehler fällt erst beim Start auf. Der Neustart-Pfad fängt das über die Nachprüfung ab.
* `natscheck.sh` erwartet JetStream nur, wenn die Gruppe es eingeschaltet hat (bisher schlägt es fehl, wenn es läuft); der Test in `natsserver_test.go`, der verlangt, dass `jetstream` nie in der Konfiguration vorkommt, wird auf die Fälle `off` (nirgends, auch nicht als Kommentarzeile) und `registry`/`full` angepasst.

### 4.3 Berechtigungen

`Permissions(Policy)` liefert zusätzlich zu den Listen aus `nats.md` §6, je nach Profil der Maschine und `NATS_JETSTREAM` der Gruppe:

| Bedingung | Senden | Lesen |
|---|---|---|
| immer (bisher) | `viper.<g>.<m>.>`, `viper.<g>.*.req.>` | `viper.<g>.>`, `_INBOX.<m>.>` |
| Profil `nuntius` | `claude.*.in.>`, `agents.registry.announce` | `claude.<m>.in.>`, `_INBOX.>` |
| JetStream `registry` (Profil `nuntius`) | `$JS.API.STREAM.INFO.KV_agents-registry`, `$JS.API.CONSUMER.CREATE.KV_agents-registry` und `….CREATE.KV_agents-registry.>` (benannte/gefilterte Consumer), `$JS.API.CONSUMER.DELETE.KV_agents-registry.>`, `$JS.API.DIRECT.GET.KV_agents-registry.>`, `$KV.agents-registry.>`, `$JS.FC.>` | (schon durch das Profil) |
| JetStream `full` (Profil `nuntius`) | `$JS.API.>`, `$JS.FC.>`, `$JS.ACK.>`, `$KV.>` | (schon durch das Profil) |

Anmerkungen:
* **Die Subjects für `registry` sind eine Ableitung aus dem Code *[V für kv.keys(), sonst ?]*.** Meilenstein 0 schneidet die tatsächlich gesendeten Subjects von **jedem der 17 Werkzeuge** mit (an einem Server ohne Einschränkung, z. B. mit `nats sub '$JS.API.>'` und den Verstößen im Protokoll) und leitet die Tabelle daraus ab; ausdrücklich zu klären sind geordnete Consumer (`kv_keys`, `agent_discover`), `$JS.FC.>`, ob `$JS.ACK.>` für `registry` überflüssig ist (geordnete Consumer brauchen keine Acks) und was Nuntius sonst beim Anlegen des Buckets sendet. In `registry` stehen **kein** `STREAM.CREATE` und **kein** `STREAM.UPDATE`: Den Bucket legt `viper` an (§4.2).
* **JetStream ohne Profil `nuntius`** (Profil `default`) erhält **keine** JetStream-Rechte: Rechte kommen nie aus der Gruppeneinstellung allein, sondern immer aus Gruppeneinstellung *und* Profil der Maschine. Ist JetStream an, aber keine Maschine hat das Profil `nuntius`, meldet `viper check` einen **Hinweis**.
* `allow_responses: true` bleibt wie bisher gesetzt und deckt Antworten auf Nachrichten, die der Rechner bekommen hat (etwa an `reply_to`).
* `animus.in.permission_request` bleibt gesperrt; `agents.registry.announce` ist nur Senden (Nuntius liest dort nie).

### 4.4 Rechner (`internal/usecase/start`)

Nur mit `NATS_NUNTIUS=true`, nach dem Beitritt zur Gruppe; Voraussetzung: `claude` ist auf dem Rechner installiert (das macht `viper up` ohnehin vorher).
1. **Rust:** Toolchain per Homebrew (`VIPER_HOMEBREW=1` vorausgesetzt) oder mit geprüfter `rustup`-Installation (Version und Prüfsumme in `viper`); die nötige Version steht nach Meilenstein 0 fest. Bei kleinen Servern `CARGO_BUILD_JOBS` begrenzen; ein Build braucht mehrere hundert MB Speicher *[?]*.
2. `cargo install --locked --git … --rev <Commit> nuntius` (einmalig, idempotent über die installierte Version), danach `~/.cargo/registry` und das Build-Verzeichnis aufräumen.
3. **Startskript** `~/.viper-nats/nuntius-mcp` (Modus 0700, mit absoluten Pfaden): 
    * `unset` für **alle** `NUNTIUS_*`-Variablen, bevor eigene gesetzt werden (insbesondere `NUNTIUS_AUTH_TOKEN`, `NUNTIUS_USER`, `NUNTIUS_PASS`, `NUNTIUS_STARTUP_SUBS`),
    * liest `connection.env` und setzt `NUNTIUS_NATS_URL` (aus `NATS_URL`, mit `tls://`), `NUNTIUS_NKEY` (Inhalt von `client.nk`; der Seed steht nicht in `argv` und nicht in `ps`), `NUNTIUS_INSTANCE_ID` (Maschinenname) und `SSL_CERT_FILE` (Serverzertifikat),
    * wartet vor dem Start bis zu einer Minute darauf, dass der TCP-Port des Servers antwortet, weil Nuntius bei einem Verbindungsfehler ohne Wiederholung endet (nach einem Neustart des Servers oder beim Start vor dem Netz),
    * startet `nuntius`.
4. **MCP-Eintrag** im Benutzerbereich von Claude Code (`claude mcp add --scope user nuntius -- <absoluter Pfad>/nuntius-mcp`); ist er schon vorhanden, wird er ersetzt (idempotent). Das Kommando darf nichts erfragen.
5. **Channels *[?]*:** Nach unserem Wissen sind Channels eine Vorschau-Funktion von Claude Code. Sie brauchen einen Startschalter (`--channels server:<name>`, für nicht freigegebene Server `--dangerously-load-development-channels` mit einer Bestätigung beim Start), eine Anmeldung über claude.ai (kein API-Schlüssel) und womöglich eine Freigabe der Organisation. Ein unbeaufsichtigter Start auf einem Server wäre damit nicht ohne Weiteres möglich. Meilenstein 0 klärt das (Tor); davon hängt ab, ob `viper` einen Alias oder Hinweis einrichtet, oder ob der Plan ohne Empfang weitergeführt wird (nur Senden).
6. **Verbindungstest:** Das Startskript ohne Claude kurz starten, die Anmeldung und (bei JetStream) den Eintrag im Register prüfen.

### 4.5 `viper down` und `viper check`

* `down`: Die Berechtigungen verschwinden mit dem Schlüssel; die JetStream-Daten gehen mit dem Server. Wird ein Eintrag im System-Truststore angelegt, entfernt `down` ihn wieder; das Startskript, der MCP-Eintrag und das `nuntius`-Binary bleiben (die Maschine wird ohnehin gelöscht), und das steht im Handbuch. Beim Entfernen eines Clients entfällt auch seine Profilzeile.
* `check` zusätzlich:
  1. JetStream in `server.conf` an/aus und gleich der Einstellung des Servers (auf einem Client: die abweichende Einstellung wird gemeldet).
  2. Auf dem Server: Belegung von `store_dir` per `du` gegen die Grenze (kein Monitoring-Port; ein ungefährer Wert genügt).
  3. Mit `NATS_NUNTIUS=true`: `nuntius` installiert, MCP-Eintrag vorhanden, das Startskript lesbar, das Zertifikat nicht als CA markiert. Der Eintrag im Register hat eine TTL von 5 Minuten und besteht nur, solange Claude läuft; sein Fehlen ist ein Hinweis, kein Fehler.
  4. Profil in `server.conf` und Einstellung des Rechners stimmen überein. JetStream an ohne eine Maschine mit Profil `nuntius` ist ein **Hinweis**.

## 5 Meilensteine

| Nr. | Meilenstein | Ergebnis |
|---|---|---|
| 0 | Verifikation | Mit einer von Hand gebauten `server.conf` und dem `nats`-Programm auf echten Servern; Ergebnisse als „bestanden/nicht bestanden“ in den Plan zurück (Prüfliste unten). **Tor:** Nimmt Claude Code `notifications/claude/channel` unter den Bedingungen eines unbeaufsichtigten Servers an? Falls nicht, wird entschieden, ob der Plan ohne Empfang weitergeführt wird, bevor M1 beginnt |
| 1 | Einstellungen, Modell, Zertifikat | `NATS_JETSTREAM`, `NATS_PROFILE`, `NATS_NUNTIUS`, `Policy`, Profil im `Client`; Validierung; Blattzertifikat in `natscert.sh` mit Test; Referenzdatei für `Render` |
| 2 | Server und Rechte | `Render` mit Kopfzeilen, Grenzen, `ParseSettings`, Profilzeilen, Einrichtungs-Identität und Bucket, Neustart-Pfad mit Nachprüfung und Rücksprung, `StateDirectory` nur bei JetStream, `Permissions` nach §4.3 (die Rechte gehören zu diesem Meilenstein, weil ein JetStream-Server ohne sie von keinem Client nutzbar ist); echter Lauf mit zwei Maschinen und dem `nats`-Programm |
| 3 | Nuntius auf dem Rechner | Startskript, Installation, MCP-Eintrag; echter Lauf: zwei Rechner, `agent_discover` sieht beide, `nats_request` bekommt eine Antwort, Empfang auf `claude.<m>.in.>` (je nach Ergebnis des Tors) |
| 4 | `check`, `down`, Doku | Prüfungen aus §4.5, Handbuch (mit den Warnungen aus §3), Maintainer-Doku, Plan aktualisieren |

**Meilenstein 0, Prüfliste** (je bestanden/nicht bestanden; das Ergebnis nennt Server-Version und Datum). Die Punkte 1, 2 und 3 sind unabhängig von `viper` und können sofort laufen; sie entscheiden, ob der Plan weitergeht. Punkt 3 steht vor allen, die Nuntius ausführen:
1. Claude Code nimmt `notifications/claude/channel` an: mit/ohne Schalter, mit welcher Anmeldung (claude.ai oder API-Schlüssel), ob beim Start eine Bestätigung nötig ist; wie erscheint der Inhalt im Kontext.
2. TLS: `async-nats` (über Nuntius) verbindet sich mit dem **jetzigen** Zertifikat (`CA:TRUE`) und mit dem Blattzertifikat (`CA:FALSE`), jeweils über `SSL_CERT_FILE` und über den System-Truststore; das Blattzertifikat mit IP-SAN wird als Vertrauensanker akzeptiert.
3. Build von Nuntius auf Ubuntu 26.04 (amd64, arm64): nötige Rust-Version, Dauer, Speicher und Plattenplatz auf `cpx22`; Entscheidung A/B (§2).
4. Mitschnitt der Subjects jedes der 17 Werkzeuge; Tabelle für `registry` und `full` daraus ableiten und mit den Rechten aus §4.3 jedes Werkzeug einmal ausführen.
5. Hebt `$JS.API.>` (`full`) bzw. der Consumer-Zugriff (`registry`) die Trennung der Subjects auf (§3 Punkt 1): Stream auf `viper.>` und Push-Consumer mit fremdem `deliver_subject` versuchen; ohne Gegenbeweis gilt „offen“.
6. Ein- und Ausschalten von JetStream per Reload oder nur per Neustart; Verhalten von Grenzen und `store_dir` bei Reload; ob ein abgelehnter Reload einen Fehler meldet; Rücksprung nach fehlgeschlagenem Neustart.
7. `StateDirectory` unter `ProtectSystem=strict` mit `User=builder`; Verhalten bei fehlendem Verzeichnis.
8. Bucket durch die Einrichtungs-Identität: Anlegen, Reload der Rechte, Entfernen der Identität; Bucket bleibt nach Neustart; Nuntius ruft bei richtiger TTL `update_stream` nicht auf.
9. Start von Nuntius ohne JetStream-Rechte: Dauer bis `initialize`; Wiederverbindung nach einem Neustart des Servers (Abos, Consumer, Heartbeat).
10. Zwei Nuntius-Instanzen mit gleicher ID auf einer Maschine; gleichzeitiger Start mehrerer Instanzen mit `create_key_value` (Race).
11. Instanz-ID = Maschinenname als gültiges Token; `claude mcp add --scope user` ohne Nachfrage und mit absolutem Pfad; die Clients haben den SSH-Schlüssel des Servers nicht (§3 Punkt 4).

### Ergebnisse Meilenstein 0 (Stand 09.11.2026)

**Punkt 1, Channels in Claude Code: nach der offiziellen Dokumentation (`code.claude.com/docs/en/channels-reference`) geklärt, nicht live getestet.**
* Channels sind eine Vorschau-Funktion ab Claude Code v2.1.80; Team- und Enterprise-Organisationen müssen sie ausdrücklich freischalten (`channelsEnabled`).
* Ein eigener Channel-Server steht nicht auf der Freigabeliste. Er läuft nur mit `claude --dangerously-load-development-channels server:<name>`. Claude Code zeigt dann beim Start einen **Warndialog, der bestätigt werden muss**. Im nicht interaktiven Betrieb (`-p`, Agent SDK) wird das Flag ignoriert, der Channel registriert sich nicht.
* Ein unbeaufsichtigter Start ohne Bestätigung ist damit **nicht** möglich: Die Sitzung muss interaktiv gestartet und der Dialog bei jedem Start bestätigt werden (bei `viper` also in der tmux-Sitzung `claude`). Nuntius ist über `~/.claude.json` im Benutzerbereich eingetragen; beim ersten Start je Projekt kann zusätzlich der Dialog zur Zustimmung für neue MCP-Server erscheinen.
* Die Nachricht erscheint im Kontext als `<channel source="<servername>" …>Text</channel>`; Meta-Schlüssel dürfen nur Buchstaben, Ziffern und Unterstriche enthalten (Nuntius: `subject`, `ts`, `reply_to`, also in Ordnung). Ereignisse werden in eine Warteschlange gestellt und bei der nächsten Runde gemeinsam zugestellt; Claude Code verwirft sie ohne Fehlermeldung, wenn die Sitzung den Server nicht als Channel geladen hat.
* Die Dokumentation sagt ausdrücklich: **„An ungated channel is a prompt injection vector.“** Die offiziellen Channels prüfen den Absender gegen eine Liste. Nuntius tut das nicht [V]. Es deklariert auch nicht die Fähigkeit für Berechtigungsabfragen (`claude/channel/permission`).
* Offen bleibt die Art der Anmeldung (claude.ai oder API-Schlüssel); die Seite sagt dazu nichts *[?]*.
* **Folge für den Plan:** Das Tor ist „bedingt bestanden“. Empfang ist nur in einer interaktiven, bestätigten Sitzung möglich; die 6 Core-Werkzeuge zum Senden und Anfragen funktionieren unabhängig davon als normale MCP-Werkzeuge. §4.4 Punkt 5 ist entsprechend zu lesen: `viper` kann den Schalter nicht stillschweigend setzen, es kann nur das Startkommando und einen Hinweis bereitstellen.

**Punkt 2, TLS: bestanden und bestätigt (echter Lauf, Nuntius `709acf4` auf einem `cpx22` in `fsn1`, `nats-server v2.15.2`).**
* Mit **unserem jetzigen Zertifikat** (`basicConstraints=critical,CA:TRUE`) scheitert Nuntius: `NATS connection failed: IO error: invalid peer certificate: CaUsedAsEndEntity`. Das gilt mit und ohne `SSL_CERT_FILE`. Der Go-Client `nats` ist damit zufrieden, deshalb war es bisher unbemerkt. **Das Blattzertifikat aus §4.2 ist also nötig, nicht nur wünschenswert.**
* Mit einem Blattzertifikat (`CA:FALSE`, `keyUsage=digitalSignature`, `extendedKeyUsage=serverAuth`, SAN mit der IP, selbstsigniert) verbindet sich Nuntius (`event: connected`), und der Go-Client weiter auch.
* `SSL_CERT_FILE=<Serverzertifikat>` wird beachtet: Das genügt, ein Eintrag im System-Truststore ist nicht nötig. Ohne beides meldet Nuntius `UnknownIssuer`. Der Eintrag im System-Truststore (`update-ca-certificates`) funktioniert ebenfalls und bleibt als Rückfall; nach dem Test wurde er wieder entfernt.

**Punkt 3, Build: bestanden.** Auf einem `cpx22` (2 vCPU, 3,8 GB RAM) mit Ubuntu 26.04 (amd64): `brew install rust` 95 s (rustc/cargo 1.99.1, als Bottle), `cargo install --locked --git … --rev 709acf4 nuntius` 123 s, Binary rund 10 MB. Entscheidung A (Build auf jedem Rechner) ist damit tragbar; die Rust-Version des Homebrew-Pakets genügt. Nicht geprüft: arm64, ein Rechner mit weniger Speicher, die Prüfung der Toolchain-Herkunft (Homebrew-Bottle statt `rustup`).

**Punkt 9, Teilergebnis:** Ohne die Rechte des Profils `nuntius` und ohne JetStream antwortet Nuntius auf `initialize` erst nach **rund 10 Sekunden**, weil die Anmeldung im Register durch fehlende Antworten wartet. Nuntius loggt dabei die erwarteten Berechtigungsfehler: Abonnement `claude.<id>.in.>`, Veröffentlichung `agents.registry.announce` und Abonnement `_INBOX.<zufällig>.*` (der Antwort-Multiplexer von `nats_request`). Das bestätigt, dass `_INBOX.>` für `nats_request` nötig ist.

**Noch offen:** Punkte 4 bis 8, 10 und 11 sowie der Rest von Punkt 9 (Wiederverbindung nach Neustart des Servers).

## 6 Teststrategie

* **Unit:** Validierung der Einstellungen; `Render` mit `off`, `registry`, `full`, beiden Profilen und Rücklesen (`ParseClients` bleibt bei zwei Feldern, `ParseSettings`, Profilzeilen); **Referenzdatei:** `Render` bei `off` und Profil `default` ist byte-identisch zur eingecheckten Ausgabe von heute (kein `# viper-jetstream`, keine Grenzen); eine Registrierung auf einer alten Datei ohne Kopfzeilen lässt Einstellung und Dateiaufbau unverändert; `Permissions` pro Kombination (erlaubt, abgelehnt, ohne `$JS.API.STREAM.CREATE/UPDATE` bei `registry`); eine Maschine mit abweichender Einstellung ändert die der Gruppe nicht; `Render` mit JetStream ohne Grenzen wird abgelehnt; die Unit bei `off` ist identisch zu heute; das Zertifikat wird mit dem echten Skript erzeugt und geprüft (`CA:FALSE`, `serverAuth`, SAN); die Installation von Nuntius benutzt `--locked` und `--rev`; das Startskript wird mit einem Fake geprüft (`NUNTIUS_*` abgeräumt, kein Seed in `argv`, Modus 0700, Warten auf den Port); Start-Ablauf mit Fakes.
* **Skripte mit dem Fake-Server:** `natsconfig.sh` mit Neustart-Parameter, Nachprüfung und Rücksprung bei einem Fehler.
* **Echt (Hetzner, Testprojekt, wie bei `nats.md`):** zwei bis drei `cpx22`, danach alles löschen. Prüfen: Nuntius meldet sich an (Eintrag im Register, TTL läuft ab, wenn der Heartbeat ausbleibt); `nats_publish` im Bereich der Instanz; Empfang auf `claude.<m>.in.>` (je nach Ergebnis des Tors); die Ablehnungen greifen (andere Gruppe, `$SYS`, `animus.in.permission_request`, `claude.*` von einer Maschine ohne Profil `nuntius`); Aus-/Ein-/Ausschalten von JetStream mit laufenden Verbindungen (Daten bleiben); Aufstieg einer bestehenden, laufenden Gruppe (Unit-Änderung, Neustart, Daten); Grenze erreicht; zwei Instanzen mit gleicher ID.
* **Regression:** Alle bestehenden Tests mit den Standardwerten; eine bestehende Gruppe ohne die neuen Einstellungen bleibt unverändert (Konfiguration, Unit und Verhalten; Ausnahme ist das Zertifikat neuer Gruppen).

## 7 Bewusst nicht enthalten

* Eine Änderung an Nuntius oder ein eigener Fork.
* Animus (`request_permission`).
* JetStream mit mehreren Replikaten oder Cluster; Sicherung der JetStream-Daten.
* Ein eigener Account für JetStream (würde die Trennung der Subjects wiederherstellen, ist aber ein großer Umbau des Rechtemodells).
* Andere MCP-Server oder andere Agenten als Claude Code.
* Konfigurierbare Subject-Rechte über Profil und JetStream-Stufe hinaus (bleibt in `nats.md` §15).
* Das Erneuern bestehender Zertifikate (bleibt bei „Gruppe neu anlegen“, `nats.md` §5).

## 8 Definition of Done

1. Ohne die neuen Einstellungen verhalten sich Gruppen wie in `nats.md`: `server.conf` und Unit bestehender Gruppen werden nicht verändert; neue Gruppen unterscheiden sich nur im Blattzertifikat.
2. Mit `NATS_JETSTREAM=registry` oder `full` läuft JetStream am Server mit Pflichtgrenzen im beschreibbaren Pfad; `viper check` meldet es. Bei `registry` hat `viper` den Bucket angelegt, und ein Rechner mit Profil `nuntius` kann ihn lesen und beschreiben, aber nicht ändern.
3. Das Profil gilt je Maschine: Ein Rechner mit Profil `nuntius` darf genau die Subjects aus §4.3, ein Rechner ohne darf das Senden an `claude.*` und JetStream nicht (belegt durch Tests und einen echten Lauf, der **jedes der 17 Werkzeuge** einmal ausführt). Wo §3 Punkt 1 offen bleibt, ist das im Handbuch ausdrücklich benannt.
4. Mit `NATS_NUNTIUS=true` ist Nuntius mit dem gepinnten Commit installiert (`--locked`, `--rev`), als MCP-Server eingetragen und angemeldet; das Startskript räumt `NUNTIUS_*` ab und wartet auf den Server; die Rechner sehen einander über `agent_discover` (mit JetStream) und können Nachrichten senden (und empfangen, soweit das Tor in Meilenstein 0 es zulässt).
5. Die Gruppeneinstellung kommt vom Server; ein Client mit abweichender Einstellung ändert sie nicht und wird gewarnt.
6. Die Grenzen des Servers (Nutzlast, Verbindungen, Abonnements) gelten, sobald JetStream an ist oder eine Maschine das Profil `nuntius` hat.
7. `viper check` meldet Abweichungen und fehlende Teile (auch ein als CA markiertes Zertifikat bei Nuntius); `viper down` hinterlässt keine gültige Identität und keinen systemweiten Truststore-Eintrag.
8. Das Handbuch beschreibt die Einstellungen und warnt vor Prompt-Injection, vor der Breite der Rechte (`_INBOX.>`, JetStream), vor dem nicht fälschungssicheren Register und vor der fehlenden Nachverfolgung; Plan und Maintainer-Doku sind aktualisiert.
