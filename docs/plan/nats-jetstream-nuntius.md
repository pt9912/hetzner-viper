# Plan: JetStream und Nuntius für NATS-Gruppen

**Ziel:** Die Rechner einer NATS-Gruppe (siehe [`nats.md`](nats.md)) sollen [Nuntius](https://github.com/JaredCluff/nuntius) nutzen können, einen MCP-Server, der Claude Code mit NATS verbindet. Nuntius kann nicht geändert werden (es gehört nicht zu diesem Projekt). Alles, was nötig ist, geschieht deshalb auf der `viper`-Seite: JetStream am Server, Berechtigungen, Zertifikatsvertrauen und die Installation auf den Rechnern.

Beides bleibt optional und standardmäßig aus. Ohne die neuen Einstellungen ändert sich für bestehende Gruppen nichts.

**Kennzeichnung:** *[V]* heißt, dass es im Code von Nuntius oder von `async-nats` gelesen wurde. *[?]* heißt Vermutung (aus Wissen über nats-server oder Claude Code); jede solche Stelle ist in Meilenstein 0 zu prüfen, bevor etwas darauf gebaut wird. Stellen, die eine Entscheidung des Betreibers brauchen und hier als Arbeitsannahme stehen, sind mit **(zu bestätigen)** markiert.

## 1 Ausgangslage

Gelesen wurde Nuntius auf dem Commit `709acf4` (25.03.2026, Tag `v0.1.0`; 19 Commits, `Cargo.lock` ist eingecheckt, kein `rust-version`). Die Angaben sind bei Umsetzungsbeginn gegen den dann gewählten Commit zu prüfen.

| Fund | Folge für uns |
|---|---|
| 17 Werkzeuge: 6 mit Core-NATS (`nats_publish`, `nats_request`, `nats_subscribe`, `nats_unsubscribe`, `agent_claim`, `request_permission`), 11 mit JetStream (`js_*` ×5, `kv_*` ×4, `agent_announce`, `agent_discover`) *[V]* | Ohne JetStream laufen die 6 Core-Werkzeuge |
| Beim Start und alle 2 Minuten: Eintrag im KV-Bucket `agents-registry` (TTL 5 Minuten, Nuntius legt den Bucket an und setzt die TTL per Stream-Update) und Veröffentlichung auf `agents.registry.announce` *[V]* | Die Registrierung ist die Hauptaufgabe von JetStream in diesem Plan |
| Scheitert die Anmeldung im Register, wird nur gewarnt; ein Verbindungsfehler beim Start beendet Nuntius mit `exit(1)`, ohne Wiederholung *[V]*. Ohne JetStream-Rechte kann die Anmeldung durch fehlende Antworten einige Sekunden hängen *[?]* | Der Start ohne JetStream ist möglich, aber nicht „lautlos“; messen (Meilenstein 0) |
| Auto-Abonnement von `claude.<id>.in.>` *[V]* | `<id>` ist die Instanz-ID (`NUNTIUS_INSTANCE_ID`, sonst zufällig) |
| `nuntius.claim` ist der **Name einer Queue-Gruppe**, kein Subject: `agent_claim` abonniert das vom Aufrufer genannte Subject mit dieser Gruppe *[V]* | Dafür ist keine eigene Berechtigung nötig; `agent_claim` geht nur auf Subjects, die ohnehin erlaubt sind (`viper.<g>.>`) |
| `request_permission` sendet an `animus.in.permission_request` *[V]* | Bleibt gesperrt: Es fehlt der Gegenpart (Animus) |
| `nats_request` nutzt das Standard-Inbox-Präfix `_INBOX.`, keine Einstellung *[V]* | Antworten sind nur möglich, wenn `_INBOX.>` gelesen werden darf |
| TLS: `NUNTIUS_TLS_CERT` und `NUNTIUS_TLS_KEY` (Client-Zertifikat für mTLS) werden gelesen, aber nirgends benutzt; es gibt keine CA-Option *[V]* | Das Serverzertifikat muss auf anderem Weg vertraut werden (§4.4) |
| `async-nats` 0.38 lädt ohne eigene Wurzelzertifikate die System-Zertifikate (`rustls-native-certs`) *[V laut Prüfer, noch nicht von uns bestätigt]* | `SSL_CERT_FILE` im Startskript genügt vielleicht; dann ist kein Eingriff ins System nötig *[?]* |
| `NUNTIUS_NKEY` ist der Seed selbst, kein Dateipfad *[V]*; Auth-Reihenfolge Token, Passwort, NKey | Das Startskript liest den Seed aus der Datei |
| Weiterleitung an Claude: Jede Nachricht auf einem **abonnierten** Subject wird als `notifications/claude/channel` gesendet (Text, `subject`, `ts`, ggf. `reply_to`), ohne Absenderangabe *[V]*. Nuntius meldet dafür `experimental.claude/channel` an *[V]* | Siehe §3; ob Claude Code das ohne Startschalter annimmt, ist offen *[?]* (Meilenstein 0, Tor) |

## 2 Entscheidungen

| Thema | Festlegung |
|---|---|
| JetStream | `NATS_JETSTREAM=off` (Standard), `registry` oder `full` **(zu bestätigen)**. `registry`: JetStream läuft, die Rechte sind auf den Bucket `agents-registry` beschränkt, `agent_announce` und `agent_discover` gehen, `js_*` und `kv_*` mit beliebigen Namen nicht. `full`: alle 17 Werkzeuge; jeder Rechner darf die JetStream-Schnittstelle voll nutzen (siehe §3, Punkt 1: das hebt die Trennung der Subjects innerhalb der Gruppe praktisch auf) |
| Berechtigungsprofil | `NATS_PROFILE=default` (Standard, unverändert) oder `nuntius`. Das Profil gilt **je Maschine** **(zu bestätigen)** und steht in deren Eintrag in `server.conf` (§4.2). Eine Maschine ohne Profil `nuntius` darf nicht an die Kanäle von Nuntius senden |
| Gruppeneinstellung | Nur `NATS_JETSTREAM` gilt für die Gruppe. Es wird beim Einrichten des **Servers** in `server.conf` festgehalten. Eine abweichende Einstellung auf einem Client ändert nichts; `viper up` auf dem Client warnt sofort, `viper check` meldet sie |
| Nuntius selbst | `NATS_NUNTIUS=true` (verlangt `NATS_PROFILE=nuntius`) installiert und konfiguriert Nuntius auf dem Rechner. Standard: aus. Ohne `NATS_NUNTIUS` bleibt das Profil `nuntius` möglich (der Rechner kann dann die 6 Core-Werkzeuge einer von Hand eingerichteten Nuntius-Installation nutzen) |
| Instanz-ID von Nuntius | Der Name der Maschine (`<maschine>`), ein gültiges einzelnes Token nach `model.CheckNATSName`. Die Gruppe steckt nicht darin: Jede Gruppe hat einen eigenen Server, Kanäle verschiedener Gruppen können sich nicht begegnen |
| Installation von Nuntius | Aus dem Quellcode: `cargo install --locked --git https://github.com/JaredCluff/nuntius --rev <Commit> nuntius` (Paketname angeben, der Workspace hat mehrere Pakete). Der Commit wird in `viper` fest eingetragen und nur bewusst angehoben |
| Zertifikat | Zuerst `SSL_CERT_FILE=<Serverzertifikat>` im Startskript von Nuntius. Nur wenn Meilenstein 0 zeigt, dass das nicht reicht, kommt der Eintrag in den System-Truststore dazu (mit Aufräumen, siehe §4.5) |
| Speicher | JetStream legt Daten auf der Platte des Servers ab, mit Pflichtgrenzen (§4.2) |
| Hochverfügbarkeit | Keine. Der Server ist einzeln, ein Stream hat nur ein Replikat (Voreinstellung bei einem Server). Fällt der Server aus, sind die Daten weg, wie schon die Gruppe selbst (`nats.md` §15) |

**Offene Entscheidungen** (bis Ende Meilenstein 0 klären):
1. Standardgrenzen für JetStream (Vorschlag: 256 MB Speicher, 1 GB Platte, begrenzt auf höchstens die Hälfte des freien Platzes).
2. Wie Nuntius auf den Rechner kommt: **A)** auf jedem Rechner mit `cargo install` (Toolchain per Homebrew oder `rustup`, Dauer und Speicher auf `cpx22` messen), oder **B)** einmal bauen und als Datei mit Prüfsumme verteilen. Vorschlag: A, nur wenn der Build auf `cpx22` mit wenig Speicher durchläuft; sonst B.
3. Welche Rust-Version genügt (`Cargo.lock` verlangt womöglich eine neuere, als Ubuntu liefert).

## 3 Risiken, die der Plan benennt

1. **`$JS.API.>` hebt die Trennung der Subjects auf *[?]*.** Die Rechte in `server.conf` gelten für das, was ein Rechner selbst sendet und liest. JetStream führt Aufträge im Namen des Servers aus:
   * Ein Stream mit `subjects: ["viper.>"]` fängt den Gruppenverkehr ein, ohne dass der Rechner `viper.<g>.>` lesen dürfte; er liest dann über einen Consumer.
   * Ein Push-Consumer mit frei gewähltem `deliver_subject` lässt den Server auf Subjects veröffentlichen, die der Rechner nicht senden darf (etwa `claude.<anderer>.in.>`).

   Mit `NATS_JETSTREAM=full` sind das offene Wege. Mit `registry` sind sie enger, aber nicht sicher verschlossen, solange ein Rechner Consumer auf dem Stream des Registers anlegen darf (er kann den Bucket beschreiben und sich die Werte an ein beliebiges Subject liefern lassen). **Meilenstein 0 muss das mit einem Test klären** (Server-Version `v2.15.2`, Benutzer mit NKeys in der Datei, kein Accounts-System). Fällt der Test negativ aus, gilt: `registry` und `full` sind „offen innerhalb der Gruppe“ und werden im Handbuch so beschrieben; die Aussage „alles andere wird abgelehnt“ gilt dann nur ohne JetStream.
2. **Prompt-Injection zwischen den Rechnern.** Nuntius meldet an Claude Code jede Nachricht auf einem abonnierten Subject als `notifications/claude/channel` (Text der Nachricht, nicht als UTF-8 lesbare Daten als Base64, dazu `subject`, `ts`, `reply_to`) *[V]*. Abonniert sind:
    1. `claude.<id>.in.>` (automatisch beim Start),
    2. die Subjects aus `NUNTIUS_STARTUP_SUBS`,
    3. alles, was Claude selbst zur Laufzeit mit `nats_subscribe` abonniert, auch auf Anweisung aus einer eingegangenen Nachricht.

    Es gibt **keine Absenderangabe**: Wer auf `claude.<id>.in.>` senden darf, kann Text in die Sitzung des Agenten schreiben, ohne dass dieser erkennt, von wem er stammt. Mit `reply_to` kann er den Agenten auch zu einer Antwort an eine gewünschte Adresse bringen. Die Maskierung von `<` und `&` (`format_channel_notification`) wird auf diesem Weg nicht benutzt *[V]*. Ein kompromittierter Rechner wird so zum Weg auf alle anderen Agenten, die Code lesen und ausführen. Gegenmaßnahmen:
    * Das Senden an `claude.*.in.>` bekommen nur Rechner mit dem Profil `nuntius` (je Maschine, siehe §2). Sonst könnte jede Maschine der Gruppe, auch ohne Nuntius, in die Sitzungen anderer schreiben.
    * `NUNTIUS_STARTUP_SUBS` bleibt leer.
    * Das Handbuch beschreibt die Gefahr und sagt, dass die Gruppe eine Vertrauensgrenze ist (`nats.md` §6: jede Maschine darf die ganze Gruppe schon mitlesen).
    * Persistente Einschleusung: Ein KV-Wert oder Stream kann bösartige Anweisungen speichern, die ein Agent später mit `kv_get` oder `js_consume` als Daten liest. Mit `registry` ist das auf den Bucket des Registers begrenzt.
3. **Der Seed gehört dem Agenten.** Nuntius bekommt ihn als Umgebungsvariable (`/proc/<pid>/environ`); der Agent läuft als derselbe Nutzer und kann `client.nk` ohnehin lesen. Nach einer Injektion kann ein Agent die Identität der Maschine missbrauchen, auch ohne Nuntius. Deshalb sind die Rechte je Maschine so eng wie möglich (Punkt 2).
4. **Überflutung und Plattenplatz.** Ein Rechner kann andere Agenten mit Nachrichten überfluten (Kosten, gefüllter Kontext) oder JetStream füllen; ist die Grenze erreicht, scheitern Schreibvorgänge für alle, auch das Anmelden im Register. Serverseitig gelten `max_payload`, `max_connections`, `max_subscriptions` und die Pflichtgrenzen für JetStream (§4.2). Pro Nutzer ist das nicht einstellbar.
5. **Keine Nachverfolgung.** Ohne Accounts gibt es keine Absenderangabe auf Nachrichten und kein brauchbares Protokoll darüber, wer was gesendet hat.
6. **Ungeprüfter Fremdcode.** Nuntius läuft mit den Rechten von `builder` (passwortloses `sudo`) und sieht Claude-Token und Repository. Der Commit ist fest gepinnt und wird vor dem Anheben gelesen. Es gibt weder Binaries noch Prüfsummen vom Autor; die Abhängigkeiten sind über `Cargo.lock` gesichert, die Rust-Toolchain muss mit einer Prüfsumme oder über Homebrew kommen (kein ungeprüftes `curl | sh`).
7. **Breite der Rechte.** `_INBOX.>` zu lesen erlaubt, Antworten anderer Maschinen mitzulesen, und mit JetStream auch deren Nachrichten (Pull-Antworten, Acks). Das gilt nur mit dem Profil `nuntius`.

## 4 Umsetzung

### 4.1 Einstellungen und Modell

`NATS_JETSTREAM`, `NATS_PROFILE` und `NATS_NUNTIUS` werden in `internal/model` und `settingsfile` gelesen und geprüft: `NATS_JETSTREAM` ist `off`, `registry` oder `full`; `NATS_PROFILE` ist `default` oder `nuntius`; `NATS_NUNTIUS` verlangt Profil `nuntius`; alle drei verlangen `NATS_ENABLED`. Ohne `NATS_ENABLED=true` werden sie nicht gelesen und stören nicht (wie bisher). Für Rechte schlägt `Permissions` eine Struktur `Policy{Profile, JetStream}` statt einzelner Parameter vor; Aufrufer und Tests werden angepasst.

### 4.2 Server (`internal/natsserver`)

* **Gruppeneinstellung im Kopf von `server.conf`:** `# viper-jetstream off|registry|full` neben den Zeilen `# viper-client <maschine> <schlüssel> [<profil>]`. Das Profil ist ein optionales drittes Feld (`default` oder `nuntius`); `ParseClients` liest bisher genau zwei Felder und wird rückwärtskompatibel erweitert (fehlt das Feld, gilt `default`). Dazu kommt `ParseSettings` analog `ParseClients` und `ParseEndpoint`: Unbekanntes wird abgelehnt oder fällt auf den Standard, Dateien ohne Kopfzeilen bleiben unverändert (Test: Hash gleich nach einer Registrierung ohne neue Einstellungen).
* **`Ops.Update`:** `change` bekommt und liefert künftig eine Struktur `{Clients, Settings}`, damit eine Registrierung die Gruppeneinstellung unverändert durchreicht. Nur der Server selbst ändert `Settings` (eigener Pfad bei der Einrichtung des Servers).
* **`Render`:** Bei `registry` oder `full` ein Block `jetstream { store_dir: "/var/lib/viper-nats/jetstream", max_memory_store: …, max_file_store: … }`; die Grenzen sind Pflicht (ohne sie nimmt nats-server standardmäßig bis zu 75 % des Arbeitsspeichers und der freien Platte *[?]*). Dazu für alle Gruppen mit JetStream `max_payload`, `max_connections`, `max_subscriptions` in sinnvollen Grenzen.
* **Ein- und Ausschalten braucht einen Neustart *[?]*:** `natsconfig.sh` führt bisher immer `systemctl reload` aus, was nur ein `kill -HUP` ist und auch dann Erfolg meldet, wenn der Server die Datei ablehnt. Neu: ein Parameter „Neustart statt Reload“ bei jeder Änderung der JetStream-Zeilen, und danach die Prüfung, dass das INFO des Servers `"jetstream":true` (bzw. `false`) zeigt. `viper up` auf dem Server sagt, dass die Verbindungen kurz abreißen. Beim Ausschalten bleiben die Daten auf der Platte.
* **systemd:** `StateDirectory=viper-nats` (legt `/var/lib/viper-nats` für `User=builder` an, schreibbar auch unter `ProtectSystem=strict`) statt verschachtelter `ReadOnlyPaths`/`ReadWritePaths` *[?]*. Für bestehende Gruppen legt `natsunit.sh` das Verzeichnis an, bevor die neue Unit gilt (sonst endet der Dienst mit `status=226/NAMESPACE`); Reihenfolge: Verzeichnis, Unit, `daemon-reload`, Neustart.
* **`nats-server -t`** prüft die Datei, legt aber `store_dir` nicht an und prüft keine Schreibrechte; ein Schreibfehler fällt erst beim Start auf. Der Neustart-Pfad bricht deshalb mit der Meldung des Dienstes ab, statt Erfolg zu melden.
* `natscheck.sh` erwartet JetStream nur, wenn die Gruppe es eingeschaltet hat (bisher schlägt es fehl, wenn es läuft); der Test in `natsserver_test.go`, der ausdrücklich verlangt, dass `jetstream` nie in der Konfiguration steht, wird für die Fälle `registry` und `full` angepasst.

### 4.3 Berechtigungen

`Permissions` liefert zusätzlich zu den Listen aus `nats.md` §6, je nach Profil der Maschine und `NATS_JETSTREAM` der Gruppe:

| Bedingung | Senden | Lesen |
|---|---|---|
| immer (bisher) | `viper.<g>.<m>.>`, `viper.<g>.*.req.>` | `viper.<g>.>`, `_INBOX.<m>.>` |
| Profil `nuntius` | `claude.*.in.>`, `agents.registry.announce` | `claude.<m>.in.>`, `agents.registry.announce`, `_INBOX.>` |
| JetStream `registry` (Profil `nuntius`) | `$JS.API.STREAM.INFO.KV_agents-registry`, `$JS.API.STREAM.CREATE.KV_agents-registry`, `$JS.API.STREAM.UPDATE.KV_agents-registry`, `$JS.API.CONSUMER.CREATE.KV_agents-registry.>`, `$JS.API.DIRECT.GET.KV_agents-registry.>`, `$KV.agents-registry.>`, `$JS.ACK.>`, `$JS.FC.>` | `_INBOX.>` (schon durch das Profil) |
| JetStream `full` (Profil `nuntius`) | `$JS.API.>`, `$JS.ACK.>`, `$JS.FC.>`, `$KV.>` | `_INBOX.>` (schon durch das Profil) |

Anmerkungen:
* Die Listen für JetStream sind eine Vermutung aus dem Code von Nuntius und aus Wissen über nats-server *[?]*. Meilenstein 0 führt **jedes der 17 Werkzeuge einmal** mit diesen Rechten aus und ergänzt oder streicht, was nicht stimmt. Ausdrücklich zu klären sind die Subjects für geordnete Consumer (`kv_keys`, Watch), `$JS.FC.>` und was Nuntius beim Anlegen des Buckets sendet.
* **JetStream ohne Profil `nuntius`** (Profil `default`) erhält **keine** JetStream-Rechte: Rechte kommen nie aus der Gruppeneinstellung allein, sondern immer aus Gruppeneinstellung *und* Profil der Maschine. Die Einstellung `NATS_JETSTREAM` ohne eine Maschine mit Profil `nuntius` startet JetStream, aber niemand darf es nutzen; `viper check` weist darauf hin.
* `allow_responses: true` bleibt wie bisher gesetzt und deckt Antworten auf Nachrichten, die der Rechner bekommen hat (etwa an `reply_to`).
* `animus.in.permission_request` bleibt gesperrt.

### 4.4 Rechner (`internal/usecase/start`)

Nur mit `NATS_NUNTIUS=true`, nach dem Beitritt zur Gruppe; Voraussetzung: `claude` ist auf dem Rechner installiert (das macht `viper up` ohnehin vorher).
1. **Rust:** Toolchain per Homebrew (`VIPER_HOMEBREW=1` vorausgesetzt) oder mit geprüfter `rustup`-Installation (Version und Prüfsumme in `viper`); die nötige Version steht nach Meilenstein 0 fest. Bei kleinen Servern `CARGO_BUILD_JOBS` begrenzen; ein Build braucht mehrere hundert MB Speicher *[?]*.
2. `cargo install --locked --git … --rev <Commit> nuntius` (einmalig, idempotent über die installierte Version), danach `~/.cargo/registry` und das Build-Verzeichnis aufräumen.
3. **Startskript** `~/.viper-nats/nuntius-mcp` (Modus 0700): liest `connection.env`, setzt `NUNTIUS_NATS_URL` (aus `NATS_URL`, mit `tls://`), `NUNTIUS_NKEY` (Inhalt von `client.nk`), `NUNTIUS_INSTANCE_ID` (Maschinenname) und `SSL_CERT_FILE` (Serverzertifikat), und startet `nuntius`.
4. **MCP-Eintrag** im Benutzerbereich von Claude Code (`claude mcp add --scope user nuntius -- ~/.viper-nats/nuntius-mcp`); ist er schon vorhanden, wird er ersetzt (idempotent).
5. **Startschalter für Channels *[?]*:** Nach Wissen des Prüfers verlangt Claude Code für `notifications/claude/channel` einen Schalter oder eine Freigabe. Das Ergebnis aus Meilenstein 0 entscheidet, ob `viper` einen Alias oder Hinweis einrichtet, oder ob der Plan ohne Empfang auskommt (nur Senden).
6. **Verbindungstest:** Das Startskript ohne Claude kurz starten, die Anmeldung und (bei JetStream) den Eintrag im Register prüfen.

### 4.5 `viper down` und `viper check`

* `down`: Die Berechtigungen verschwinden mit dem Schlüssel; die JetStream-Daten gehen mit dem Server. Wird ein Eintrag im System-Truststore angelegt, entfernt `down` ihn wieder; das Startskript, der MCP-Eintrag und das `nuntius`-Binary bleiben (die Maschine wird ohnehin gelöscht), und das steht im Handbuch.
* `check` zusätzlich:
  1. JetStream in `server.conf` an/aus und gleich der Einstellung des Servers (auf einem Client: die abweichende Einstellung wird gemeldet).
  2. Auf dem Server: Belegung von `store_dir` per `du` gegen die Grenze (kein Monitoring-Port; ein ungefährer Wert genügt).
  3. Mit `NATS_NUNTIUS=true`: `nuntius` installiert, MCP-Eintrag vorhanden, das Startskript lesbar. Der Eintrag im Register hat eine TTL von 5 Minuten und besteht nur, solange Claude läuft; sein Fehlen ist ein Hinweis, kein Fehler.
  4. Profil in `server.conf` und Einstellung des Rechners stimmen überein; JetStream läuft, obwohl keine Maschine das Profil hat, als Hinweis.

## 5 Meilensteine

| Nr. | Meilenstein | Ergebnis |
|---|---|---|
| 0 | Verifikation | Mit einer von Hand gebauten `server.conf` und dem `nats`-Programm auf echten Servern; Ergebnisse als „bestanden/nicht bestanden“ in den Plan zurück (Tabelle unten). **Tor:** Nimmt Claude Code `notifications/claude/channel` ohne Schalter an? Falls nicht, wird entschieden, ob der Plan ohne Empfang weitergeführt wird, bevor M1 beginnt |
| 1 | Einstellungen und Modell | `NATS_JETSTREAM`, `NATS_PROFILE`, `NATS_NUNTIUS`, `Policy`; Validierung; Tests |
| 2 | Server und Rechte | `Render`, Pflichtgrenzen, `StateDirectory`, Neustart-Pfad mit Prüfung, `ParseSettings`, Profil je Maschine, `Permissions` nach §4.3 (die Rechte gehören zu diesem Meilenstein, weil ein JetStream-Server ohne sie von keinem Client nutzbar ist); echter Lauf mit zwei Maschinen und dem `nats`-Programm |
| 3 | Truststore und Nuntius | Startskript, Installation, MCP-Eintrag; echter Lauf: zwei Rechner, `agent_discover` sieht beide, `nats_request` bekommt eine Antwort, Empfang auf `claude.<m>.in.>` |
| 4 | `check`, `down`, Doku | Prüfungen aus §4.5, Handbuch (mit den Warnungen aus §3), Maintainer-Doku, Plan aktualisieren |

**Meilenstein 0, Prüfliste** (je bestanden/nicht bestanden; Ergebnis nennt Server-Version und Datum):
1. Claude Code nimmt `notifications/claude/channel` an (mit/ohne Schalter; wie erscheint der Inhalt im Kontext).
2. `async-nats` vertraut dem selbstsignierten Serverzertifikat über `SSL_CERT_FILE` (und über den System-Truststore); das Blattzertifikat mit IP-SAN wird als Vertrauensanker akzeptiert.
3. Jedes der 17 Werkzeuge läuft mit den Rechten aus §4.3 (`registry` und `full`); welche Subjects fehlen oder sind zu viel.
4. Hebt `$JS.API.>` (`full`) bzw. der Consumer-Zugriff (`registry`) die Trennung der Subjects auf (Punkt 1 in §3): Stream auf `viper.>`; Push-Consumer mit fremdem `deliver_subject`.
5. Ein- und Ausschalten von JetStream per Reload oder nur per Neustart; Verhalten von Grenzen und `store_dir` bei Reload; ob ein abgelehnter Reload Fehler meldet.
6. `StateDirectory` unter `ProtectSystem=strict` mit `User=builder`; Verhalten bei fehlendem Verzeichnis.
7. Start von Nuntius ohne JetStream-Rechte: Dauer bis `initialize`.
8. Build von Nuntius auf Ubuntu 26.04 (amd64, arm64): nötige Rust-Version, Dauer, Speicher und Plattenplatz auf `cpx22`; Entscheidung A/B (§2).
9. Instanz-ID = Maschinenname als gültiges Token; Verhalten bei zwei Nuntius-Instanzen mit gleicher ID auf einer Maschine.

## 6 Teststrategie

* **Unit:** Validierung der Einstellungen; `Render` mit `off`, `registry`, `full`, beiden Profilen und Rücklesen (`ParseClients` mit und ohne drittes Feld, `ParseSettings`); eine alte `server.conf` ohne Kopfzeilen bleibt bei einer Registrierung unverändert (Hash); `Permissions` pro Kombination (erlaubt, abgelehnt); eine Maschine mit abweichender Einstellung ändert die der Gruppe nicht; Start-Ablauf mit Fakes; Skript-Änderungen (Neustart-Parameter) mit dem Fake-Server.
* **Echt (Hetzner, Testprojekt, wie bei `nats.md`):** zwei bis drei `cpx22`, danach alles löschen. Prüfen: Nuntius meldet sich an (Eintrag im Register, TTL läuft ab, wenn der Heartbeat ausbleibt); `nats_publish` im Bereich der Instanz; Empfang auf `claude.<m>.in.>` (je nach Ergebnis des Tors); die Ablehnungen greifen (andere Gruppe, `$SYS`, `animus.in.permission_request`, `claude.*` von einer Maschine ohne Profil `nuntius`); Ein-/Ausschalten/Einschalten von JetStream mit laufenden Verbindungen (Daten bleiben); Grenze erreicht; zwei Instanzen mit gleicher ID.
* **Regression:** Alle bestehenden Tests mit den Standardwerten; eine bestehende Gruppe ohne die neuen Einstellungen bleibt unverändert (Konfiguration und Verhalten).

## 7 Bewusst nicht enthalten

* Eine Änderung an Nuntius oder ein eigener Fork.
* Animus (`request_permission`).
* JetStream mit mehreren Replikaten oder Cluster; Sicherung der JetStream-Daten.
* Ein eigener Account für JetStream (würde die Trennung der Subjects wiederherstellen, ist aber ein großer Umbau des Rechtemodells).
* Andere MCP-Server oder andere Agenten als Claude Code.
* Konfigurierbare Subject-Rechte über Profil und JetStream-Stufe hinaus (bleibt in `nats.md` §15).

## 8 Definition of Done

1. Ohne die neuen Einstellungen verhalten sich Gruppen exakt wie in `nats.md`; bestehende `server.conf`-Dateien werden nicht verändert.
2. Mit `NATS_JETSTREAM=registry` oder `full` läuft JetStream am Server mit Pflichtgrenzen im beschreibbaren Pfad; `viper check` meldet es; ein Rechner mit Profil `nuntius` kann den Bucket des Registers anlegen und lesen.
3. Das Profil gilt je Maschine: Ein Rechner mit Profil `nuntius` darf genau die Subjects aus §4.3, ein Rechner ohne darf das Senden an `claude.*` und JetStream nicht (belegt durch Tests und einen echten Lauf, der **jedes der 17 Werkzeuge** einmal ausführt). Wo §3 Punkt 1 negativ ausfällt, ist das im Handbuch ausdrücklich benannt.
4. Mit `NATS_NUNTIUS=true` ist Nuntius installiert, als MCP-Server eingetragen und angemeldet; die Rechner sehen einander über `agent_discover` (mit JetStream), und die Rechner können Nachrichten empfangen und senden (soweit das Tor in Meilenstein 0 es zulässt).
5. Die Gruppeneinstellung kommt vom Server; ein Client mit abweichender Einstellung ändert sie nicht und wird gewarnt.
6. `viper check` meldet Abweichungen und fehlende Teile; `viper down` hinterlässt keine gültige Identität und keinen systemweiten Truststore-Eintrag.
7. Das Handbuch beschreibt die Einstellungen und warnt vor Prompt-Injection, vor der Breite der Rechte und vor der fehlenden Nachverfolgung; Plan und Maintainer-Doku sind aktualisiert.
