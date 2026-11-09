# Plan: JetStream und Nuntius für NATS-Gruppen

**Ziel:** Die Rechner einer NATS-Gruppe (siehe [`nats.md`](nats.md)) sollen [Nuntius](https://github.com/JaredCluff/nuntius) nutzen können, einen MCP-Server, der Claude Code mit NATS verbindet. Nuntius kann nicht geändert werden (es gehört nicht zu diesem Projekt). Alles, was nötig ist, geschieht deshalb auf der `viper`-Seite: JetStream am Server, Berechtigungen, Zertifikatsvertrauen und die Installation auf den Rechnern.

Beides bleibt optional und standardmäßig aus. Ohne die neuen Einstellungen ändert sich für bestehende Gruppen nichts.

## 1 Ausgangslage (aus dem Code von Nuntius gelesen)

Gelesen wurde der Stand der Hauptlinie vom 09.11.2026 (19 Commits, kein Release). Die Angaben sind bei Umsetzungsbeginn gegen den dann gewählten Commit zu prüfen (Meilenstein 0).

| Fund | Folge für uns |
|---|---|
| 17 Werkzeuge: 6 mit Core-NATS (`nats_publish`, `nats_request`, `nats_subscribe`, `nats_unsubscribe`, `agent_claim`, `request_permission`), 11 mit JetStream (`js_*` ×5, `kv_*` ×4, `agent_announce`, `agent_discover`) | Ohne JetStream starten Nuntius und die 6 Core-Werkzeuge; die Anmeldung in der Agentenregistrierung scheitert nur mit einer Warnung im Log |
| Beim Start und alle 2 Minuten: Eintrag im KV-Bucket `agents-registry` (TTL 5 Minuten), Veröffentlichung auf `agents.registry.announce` | Die Registrierung ist die Hauptaufgabe von JetStream in diesem Plan |
| Auto-Abonnement von `claude.<id>.in.>`, Registrierung `animus.in.permission_request`, Queue-Gruppe `nuntius.claim` | Fest eingebaute Subjects; unsere Standard-Berechtigungen lassen sie nicht zu |
| `nats_request` nutzt das Standard-Inbox-Präfix `_INBOX.`, keine Einstellung | Antworten sind nur möglich, wenn `_INBOX.>` erlaubt ist |
| TLS: `NUNTIUS_TLS_CERT` und `NUNTIUS_TLS_KEY` werden gelesen, beim Verbinden aber nicht verwendet; keine CA-Datei | Unser selbstsigniertes Zertifikat wird nur akzeptiert, wenn es im System-Truststore steht (zu verifizieren: ob die Bibliothek `async-nats` die System-Zertifikate liest) |
| `NUNTIUS_NKEY` ist der Seed selbst, kein Dateipfad | Ein Startskript liest den Seed aus der Datei |
| Auth-Reihenfolge: Token, Passwort, NKey (kein Zusammenspiel mit TLS-Client-Zertifikaten) | Unser NKey-Login passt |

## 2 Entscheidungen

| Thema | Festlegung |
|---|---|
| JetStream | `NATS_JETSTREAM=true` schaltet JetStream am Server der Gruppe ein. Standard: aus |
| Berechtigungsprofil | `NATS_PROFILE=nuntius` erweitert die Standard-Berechtigungen um das, was Nuntius braucht. Standard: `default` (unverändert) |
| Ort der Einstellung | Gruppenweit, bestimmt vom **Server**: Beide Werte werden bei der Einrichtung des Servers in `server.conf` festgehalten. Abweichende Einstellungen auf anderen Rechnern gelten nicht, `viper check` meldet die Abweichung |
| Nuntius selbst | `NATS_NUNTIUS=true` (nur mit `NATS_PROFILE=nuntius`) installiert und konfiguriert Nuntius auf dem Rechner. Standard: aus |
| Installation von Nuntius | Aus dem Quellcode mit `cargo install --locked --git … --rev <fester Commit>` (es gibt keine Binaries und keine Prüfsummen). Der Commit wird in `viper` festgelegt und bei Bedarf bewusst angehoben |
| Zertifikat | Das Serverzertifikat wird zusätzlich in den System-Truststore der Rechner eingetragen (nur mit `NATS_NUNTIUS=true`) |
| Speicher | JetStream legt Daten auf der Platte des Servers ab, in einer Obergrenze (siehe §4) |
| Hochverfügbarkeit | Keine. Der Server ist einzeln, Streams haben ein Replikat. Fällt der Server aus, sind die Daten weg (wie schon die Gruppe selbst, siehe `nats.md` §15) |

**Offene Entscheidungen** (vor Meilenstein 1 klären):
1. Namen der Einstellungen (`NATS_PROFILE`/`NATS_NUNTIUS`) und ob ein Profil `nuntius` auch ohne JetStream erlaubt sein soll (ja: dann laufen nur die 6 Core-Werkzeuge).
2. Standard-Grenzen für JetStream (Vorschlag: 256 MB Speicher, 1 GB Platte).
3. Rust-Toolchain per Homebrew (`rust`, sofern `VIPER_HOMEBREW=1`) oder per `rustup`; Dauer des Builds auf `cpx22` messen.

## 3 Risiken, die der Plan benennt

* **Prompt-Injection zwischen den Rechnern.** Nuntius meldet an Claude Code jede Nachricht, die auf einem Subject ankommt, das es abonniert hat, als MCP-Benachrichtigung `notifications/claude/channel` (Inhalt der Nachricht plus `subject`, `ts` und ggf. `reply_to`). Der Inhalt ist der Text der Nachricht (nicht als UTF-8 lesbare Daten als Base64). Weitergeleitet wird also:
  1. alles auf `claude.<id>.in.>` (wird beim Start automatisch abonniert),
  2. alles auf den Subjects aus `NUNTIUS_STARTUP_SUBS`,
  3. alles auf Subjects, die Claude selbst zur Laufzeit mit `nats_subscribe` abonniert (das entscheidet der Agent, auch auf Anweisung aus einer eingegangenen Nachricht).

  Nicht weitergeleitet werden Nachrichten auf anderen Subjects. JetStream-Inhalte kommen nur als Ergebnis eines Werkzeugs (`js_consume`, `kv_get`) und nicht ungefragt. Die Nachricht trägt **keine Absenderangabe**: Wer auf `claude.<id>.in.>` senden darf, kann Text in die Sitzung des Agenten auf diesem Rechner schreiben, und der Agent kann nicht erkennen, von wem er stammt. Jeder Rechner der Gruppe darf das mit dem Profil `nuntius` (`claude.*.in.>`). Ein kompromittierter Rechner wird so zum Weg auf alle anderen Agenten, die Code lesen und ausführen. Die kleine Hilfsfunktion von Nuntius, die `<` und `&` maskiert (`format_channel_notification`), wird auf diesem Weg nicht benutzt. Wie Claude Code den Inhalt in den Kontext legt, ist nicht von uns geprüft (Meilenstein 0).

  Gegenmaßnahmen: Das Profil `nuntius` ist ausdrücklich zu wählen und im Handbuch mit dieser Warnung beschrieben. `NUNTIUS_STARTUP_SUBS` bleibt leer. Die Gruppe ist eine Vertrauensgrenze (`nats.md` §6: jeder Rechner darf bereits die ganze Gruppe mitlesen). Prüfen, ob die Sende-Berechtigung enger gefasst werden kann (nur an bestimmte Rechner statt `claude.*.in.>`), solange Nuntius keine Absender kennzeichnet.
* **Ungeprüfter Fremdcode.** Nuntius läuft mit den Rechten des Nutzers `builder`, also root-äquivalent (passwortloses `sudo`), und sieht Claude-Token und Repository. Der Commit wird deshalb fest gepinnt und vor dem Anheben gelesen. Kein Binary und keine Prüfsumme vom Autor: Das Vertrauen liegt im gepinnten Quellcode.
* **Breitere Berechtigungen.** `_INBOX.>` zu lesen erlaubt es, Antworten anderer Maschinen mitzulesen. JetStream-Rechte (`$JS.API.>`) erlauben, beliebige Streams der Gruppe anzulegen und zu löschen. Beides gilt nur innerhalb der Gruppe, nur mit den Einstellungen aus §2, und andere Gruppen bleiben getrennt (eigener Server).
* **Schreibzugriff.** Die systemd-Unit sperrt bisher alles außer lesendem Zugriff auf das Konfigurationsverzeichnis (`ProtectSystem=strict`). Für JetStream braucht der Dienst ein beschreibbares Verzeichnis, aber nur dieses.

## 4 Umsetzung

### 4.1 Einstellungen und Modell

`NATS_JETSTREAM`, `NATS_PROFILE` und `NATS_NUNTIUS` werden in `internal/model` und `settingsfile` gelesen und geprüft: `NATS_PROFILE` ist `default` oder `nuntius`, `NATS_NUNTIUS` verlangt `NATS_ENABLED` und Profil `nuntius`, `NATS_JETSTREAM` verlangt `NATS_ENABLED`. Die Werte stehen ohne NATS nicht im Weg (wie bisher).

### 4.2 Server (`internal/natsserver`)

* `Config` bekommt `JetStream bool`, `Profile` und die Grenzen. `Render` schreibt bei JetStream einen Block `jetstream { store_dir: "…/jetstream", max_memory_store: …, max_file_store: … }`.
* Die Gruppeneinstellungen stehen wie die Clientliste maschinenlesbar am Anfang der Datei (`# viper-jetstream on`, `# viper-profile nuntius`). `Ops.Update` übernimmt sie aus der vorhandenen Datei, wenn eine Maschine sich registriert; sie ändern sich nur, wenn der Server selbst eingerichtet wird. So überschreibt kein Client die Einstellung der Gruppe.
* **Ein- und Ausschalten von JetStream** verlangt einen Neustart des Dienstes (Neuladen reicht dafür nicht; zu verifizieren). `viper up` auf dem Server startet ihn in diesem Fall neu und sagt, dass die Verbindungen kurz abreißen. Beim Ausschalten bleiben die Daten auf der Platte.
* `UnitFile` ergänzt `ReadWritePaths=<Dir>/jetstream`; der Dienst darf nur dort schreiben.
* `natscheck.sh` erwartet JetStream nur, wenn die Gruppe es eingeschaltet hat (bisher schlägt es fehl, wenn es an ist).

### 4.3 Berechtigungen

`Permissions(group, machine, profile, jetstream)` liefert wie bisher die Listen. Zusätzlich:

| Profil / Einstellung | Senden | Lesen |
|---|---|---|
| `default` (bisher) | `viper.<g>.<m>.>`, `viper.<g>.*.req.>` | `viper.<g>.>`, `_INBOX.<m>.>` |
| `nuntius` (zusätzlich) | `claude.*.in.>`, `agents.registry.announce`, `nuntius.claim` | `claude.<id>.in.>`, `nuntius.claim`, `agents.registry.announce`, `_INBOX.>` |
| `NATS_JETSTREAM=true` (zusätzlich) | `$JS.API.>`, `$KV.>` | `$JS.ACK.>`, `$KV.>` |

`<id>` ist die Instanz-ID von Nuntius (`NUNTIUS_INSTANCE_ID`): Wir setzen sie auf `<gruppe>-<maschine>` und erlauben dem Rechner nur den Kanal mit seiner eigenen ID. `animus.in.permission_request` (`request_permission`) bleibt gesperrt: Dafür fehlt der Gegenpart (Animus).

### 4.4 Rechner (`internal/usecase/start`)

Nur mit `NATS_NUNTIUS=true`, nach dem Beitritt zur Gruppe:
1. Serverzertifikat nach `/usr/local/share/ca-certificates/viper-nats-<gruppe>.crt` und `update-ca-certificates`.
2. Rust-Toolchain und `cargo install --locked --git https://github.com/JaredCluff/nuntius --rev <commit>` (einmalig, idempotent über die installierte Version).
3. Startskript `~/.viper-nats/nuntius-mcp`: liest `connection.env`, setzt `NUNTIUS_NATS_URL` (mit `tls://`), `NUNTIUS_NKEY` (Inhalt von `client.nk`) und `NUNTIUS_INSTANCE_ID`, und startet `nuntius`.
4. Eintrag als MCP-Server im Benutzerbereich von Claude Code (`claude mcp add --scope user nuntius -- ~/.viper-nats/nuntius-mcp`).
5. Verbindungstest: Das Startskript ohne Claude kurz starten und die Anmeldung prüfen.

### 4.5 `viper down` und `viper check`

* `down`: keine Änderung am Ablauf; die Berechtigungen verschwinden mit dem Schlüssel. Die JetStream-Daten gehen mit dem Server.
* `check`: zusätzlich (a) JetStream in `server.conf` an/aus und passend zu den anderen Rechnern, (b) Speicherverbrauch von JetStream gegen die Grenze (auf dem Server), (c) mit `NATS_NUNTIUS=true`: `nuntius` installiert, MCP-Eintrag vorhanden, Zertifikat im Truststore, Eintrag des Rechners im KV-Bucket `agents-registry` (nur mit JetStream), (d) Abweichungen des Profils zwischen `server.conf` und der Einstellung des Rechners.

## 5 Meilensteine

| Nr. | Meilenstein | Ergebnis |
|---|---|---|
| 0 | Verifikation | Gegen echte Server und den gewählten Nuntius-Commit geprüft: liest `async-nats` den System-Truststore; funktioniert JetStream mit NKey-Benutzern und diesen Berechtigungen (KV-Bucket anlegen, TTL, Eintrag lesen); braucht das Ein-/Ausschalten einen Neustart; baut Nuntius auf Ubuntu 26.04 (amd64, arm64) und wie lange dauert das auf `cpx22`; welche Berechtigungen braucht `agent_claim` wirklich |
| 1 | Einstellungen und Modell | `NATS_JETSTREAM`, `NATS_PROFILE`, `NATS_NUNTIUS`; Validierung; Tests |
| 2 | Server | JetStream-Block, Grenzen, beschreibbarer Pfad, Gruppeneinstellungen in `server.conf`, Neustart-Verhalten; echter Lauf mit zwei Maschinen |
| 3 | Profile | `Permissions` mit Profil und JetStream; Tests für jeden Fall (erlaubt, abgelehnt); echter Lauf mit `nats`-Werkzeug |
| 4 | Truststore und Nuntius | Zertifikat im System, Installation, Startskript, MCP-Eintrag; echter Lauf: zwei Rechner, `agent_discover` sieht beide, `nats_request` bekommt eine Antwort |
| 5 | `check`, `down`, Doku | Prüfungen aus §4.5, Handbuch (mit der Warnung aus §3), Maintainer-Doku, Plan aktualisieren |

## 6 Teststrategie

* **Unit:** Validierung der Einstellungen; `Render` mit und ohne JetStream, mit beiden Profilen (inklusive Rücklesen der Gruppeneinstellungen); `Permissions` pro Fall; Verhalten von `Ops.Update`, wenn eine Maschine mit anderer Einstellung sich registriert (Einstellung der Gruppe bleibt); Start-Ablauf mit Fakes.
* **Echt (Hetzner, Testprojekt):** wie bei `nats.md`: zwei bis drei `cpx22`, danach alles löschen. Prüfen, dass Nuntius sich anmeldet (Eintrag in `agents-registry`, TTL läuft ab, wenn der Heartbeat ausbleibt), dass `nats_publish` im Bereich der Instanz geht, dass die Ablehnungen greifen (andere Gruppe, `$SYS`, `animus.in.permission_request`), und dass JetStream bei abgeschaltetem Profil wirklich nicht erreichbar ist.
* **Regression:** Alle bestehenden Tests mit den Standardwerten; eine bestehende Gruppe ohne die neuen Einstellungen bleibt unverändert.

## 7 Bewusst nicht enthalten

* Eine Änderung an Nuntius oder ein eigener Fork.
* Animus (`request_permission`) und die Rechtevergabe darüber.
* JetStream mit mehreren Replikaten oder mit Cluster; Sicherung der JetStream-Daten.
* Andere MCP-Server oder andere Agenten als Claude Code.
* Konfigurierbare Subject-Rechte über die Profile hinaus (bleibt in `nats.md` §15).

## 8 Definition of Done

1. Ohne die neuen Einstellungen verhalten sich Gruppen wie in `nats.md`.
2. Mit `NATS_JETSTREAM=true` läuft JetStream am Server mit Grenzen, nur im beschreibbaren Pfad, und `viper check` meldet es; ein Client kann einen KV-Bucket anlegen und lesen.
3. Mit `NATS_PROFILE=nuntius` darf ein Rechner genau die Subjects aus §4.3, alles andere wird abgelehnt (belegt durch Tests und einen echten Lauf).
4. Mit `NATS_NUNTIUS=true` ist Nuntius auf allen Rechnern installiert, als MCP-Server eingetragen und angemeldet; die Rechner sehen einander über `agent_discover`.
5. Die Gruppeneinstellungen kommen vom Server; ein Rechner mit abweichender Einstellung ändert sie nicht.
6. `viper check` meldet Abweichungen und fehlende Teile; `viper down` hinterlässt keine gültige Identität.
7. Das Handbuch beschreibt die Einstellungen und warnt vor Prompt-Injection und vor der Breite der Rechte; Plan und Maintainer-Doku sind aktualisiert.
