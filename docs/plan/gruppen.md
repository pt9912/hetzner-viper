# Plan: Gruppen-Dateien (`groups/`) – mehrere Rechner gemeinsam starten und stoppen

Stand: 09.11.2026 · Status: Meilensteine 1 bis 4b umgesetzt (echter Lauf: drei Rechner in 7,6 Minuten, alle drei in der Gruppe registriert), der Rest offen

## 1 Ziel

Heute startet und beendet man jeden Rechner einzeln (`viper up <name>`, `viper down <name>`). Eine NATS-Gruppe besteht nur aus Labels und dem privaten Netzwerk beim Anbieter. Neu: Neben `machines/` liegt ein Ordner `groups/`. Eine Datei dort beschreibt eine Gruppe und nennt ihre Rechner. Damit lässt sich die ganze Gruppe mit einem Befehl starten, prüfen und beenden, **parallel**, weil ein Rechner nach dem anderen zu lange dauert.

## 2 Aufbau und Regeln

```
.env.local                 gilt für alle
groups/entwicklung.env     die Gruppe: Rechner und gemeinsame Einstellungen
machines/alpha.env.local   ein Rechner (wie bisher, darf fehlen)
machines/beta.env.local
```

`groups/entwicklung.env`:

```
# Der erste Rechner einer neuen Gruppe wird der NATS-Server; die übrige Reihenfolge ist egal
GROUP_MACHINES=alpha,beta
# Einstellungen für alle Rechner der Gruppe (wie in machines/*.env.local)
NATS_ENABLED=true
NATS_MCP=true
```

* **Name:** Der Dateiname ist der Gruppenname und wird zu `NATS_GROUP`. Setzt die Datei `NATS_GROUP` selbst, muss es dem Dateinamen gleichen (sonst Fehler). Regeln wie für `NATS_GROUP`: 1 bis 31 Kleinbuchstaben, Ziffern, Bindestriche.
* **`GROUP_MACHINES`:** durch Komma getrennt, Leerzeichen werden entfernt, keine Duplikate, nicht leer. Jeder Name gilt nach den Regeln der Rechnernamen (mit NATS zusätzlich `CheckNATSName`). Ein Rechner **braucht keine** eigene Datei in `machines/`; fehlt sie, gelten die Werte aus `.env.local` und der Gruppe (`Load` verträgt das schon).
* **Rangfolge:** `.env.local` → `groups/<g>.env` → `machines/<m>.env.local` (der Rechner gewinnt). Setzt eine Maschinendatei `NATS_GROUP` abweichend, bricht `viper` ab; setzt sie `NATS_ENABLED=false`, meldet `viper` bei Gruppenbefehlen, dass dieser Rechner ohne NATS läuft.
* **Mitgliedschaft:** Ein Rechner steht in höchstens einer Gruppendatei. Steht er in zwei, bricht `viper` bei Befehlen für diese Rechner ab.
* **Einzelne Rechner:** `viper up alpha` funktioniert weiter und erbt die Einstellungen der Gruppe, zu der alpha gehört. Dafür liest `Load` (und `settings.Prefix`, das `viper log` benutzt) künftig alle `groups/*.env`, um die Mitgliedschaft zu finden. **Fehler in der Datei einer fremden Gruppe sind dabei nur eine Warnung**; nur die Datei der eigenen Gruppe zählt. Entfernt man einen Rechner aus der Datei, gilt `NATS_GROUP` für ihn nicht mehr; ein vorhandener Rechner bricht dann mit der bekannten Meldung „belongs to the NATS group …“ ab (kein stilles Wechseln).
* **Geheimnisse:** `groups/*.env` ist für gemeinsame Einstellungen gedacht, nicht für Tokens. Der Preflight (`permissionChecks`) warnt wie bei `machines/`, wenn die Datei für andere lesbar ist; er wird dafür erweitert.
* **Ohne NATS:** `NATS_ENABLED` ist Sache der Gruppe, nicht Voraussetzung. Ohne NATS gibt es keinen „ersten Rechner als Server“; alle Rechner starten dann gleichzeitig ohne Vorlauf. Ist NATS für die Gruppe an, wird der **erste Rechner der Liste mit NATS an** der Server; hat der erste Rechner `NATS_ENABLED=false` und andere nicht, ist das kein Fehler, solange ein Rechner mit NATS folgt. Ist NATS an, aber kein Rechner der Liste hat es an, bricht `viper` vor dem Anlegen ab.

## 3 Befehle

| Befehl | Wirkung |
|---|---|
| `viper up --group g` | startet alle Rechner der Gruppe |
| `viper down --group g` | beendet alle Rechner der Gruppe |
| `viper check --group g` | prüft alle Rechner und die Gruppe |
| `viper ssh --group g --cmd …` | führt einen Befehl auf allen Rechnern aus (nur mit `--cmd`) |

`--group` und ein Rechnername zusammen sind ein Fehler. `--repo` gilt bei `up` für alle Rechner. VS Code wird in Gruppenbefehlen **nicht** geöffnet (sonst N Fenster; `--no-code` ist dort der Standard); `viper vscode <rechner>` bleibt einzeln. `--force` gibt es nur bei `down` und gilt dort je Rechner wie heute. Zusätzlich `--parallel N` (Standard 8).

### Vor dem Start (alle Gruppenbefehle)

Der Befehl liest zuerst den Zustand der Gruppe beim Anbieter und bricht **vor dem Anlegen** ab, wenn er nicht passt:

* Das Netzwerk `viper-nats-<g>` besteht, aber die Gruppe hat keinen Server: Abbruch mit klarer Meldung (erst `down --group` für die Clients). Heute weigert sich `natsRole` in diesem Fall ebenfalls; seine Meldung („run viper up on the machine that was to be the server“) hilft nicht und wird mitkorrigiert.
* Alle Rechner: `PROVIDER=hetzner`, dieselbe Netzwerkzone (unterschiedliche `SERVER_LOCATION` derselben Zone sind erlaubt), gültige Namen.
* Der Preflight jedes Rechners läuft einmal vorab für alle; die Läufe der einzelnen Rechner überspringen ihren Preflight (neue Option, damit nichts doppelt abfragt und ausgibt).

### Start, parallel

* **Haltepunkt:** `start.Run` kennt heute keinen Zeitpunkt „der Server existiert beim Anbieter“. Neu: `UpOptions.OnServerCreated func()`, aufgerufen in `Run` direkt nach `ensureServerInGroup`. Bei einem **neu angelegten** Server stehen Rolle, Zustand und privates Netz dann schon (sie werden mit `CreateServer` gesetzt). Bei einem **wiederverwendeten Server ohne NATS-Labels** setzt erst `natsSetup` Netz und Labels (nach `reachServer`); das Signal kommt dort erst nach `AttachPrivateNetwork` und `UpdateNATSMetadata` (diese Schritte ziehen vor das Signal), sonst fiele der erste Rechner auf `firstStartWindow` zurück. Beides bekommt einen Test. `groupops` startet die übrigen Rechner erst nach diesem Signal; kommt es nicht (der Lauf endet vorher), startet kein Client.
* **Neue Gruppe:** Zuerst läuft der erste Rechner der Liste. Er legt das Netzwerk und seinen Server an (Sekunden; so entscheidet nicht der Zufall, wer Server wird). Nach dem Signal starten alle übrigen **gleichzeitig**, während der Server sich einrichtet. `natsRole` macht sie zu Clients, weil der Server schon mit Rolle `server` und Zustand `provisioning` da ist. `firstStartWindow` bleibt als Rückfall.
* **Bestehende Gruppe:** Alle fehlenden Rechner starten gleichzeitig.
* **Vorarbeit vor jeder Parallelphase** (neue Gruppe, bestehende Gruppe und Gruppen ohne NATS): Das Anlegen von Login-Schlüssel (`<prefix>-login`) und Firewall (`<prefix>-ssh`) geschieht einmal je Prefix und Schlüssel-Fingerabdruck vor dem Start der parallelen Läufe. Der erste Rechner hat sie bei einer neuen Gruppe nur für seinen eigenen Prefix angelegt; Rechner mit anderem `SERVER_PREFIX` brauchen eigene. Zusätzlich wiederholt `EnsureLoginKey` bei einem Eindeutigkeitsfehler das Lesen (wie `EnsurePrivateNetwork`). Das Hinzufügen von Firewall-Regeln (`FirewallAddRule` liest alle Regeln und setzt sie neu) ist ein Überschreiben, kein Fehler: es läuft deshalb nur in der Vorarbeit, nie parallel.
* **Wartezeit der Clients:** Im Gruppenstart warten sie bis zu 15 statt 5 Minuten auf einen Server im Zustand `ready` (Parameter von `natsWaitForServer`); sie warten länger, weil der Server nach SSH-Erreichbarkeit noch NATS, Zertifikat und eventuell einen Neustart braucht. Scheitert der Server-Lauf, bricht `groupops` die Läufe der Clients über den gemeinsamen Kontext ab, damit sie nicht umsonst warten und Kosten verursachen.
* **Polling:** Die wartenden Clients fragen alle 5 s die Gruppe ab (das SDK wiederholt bei `rate_limit_exceeded` selbst; ob das unter 8 Rechnern reicht, ist im echten Lauf zu messen). `groupops` verteilt den Start der Abfragen versetzt (Jitter) und ein Client mit mehr als 4 Rechnern gleichzeitig wartet länger zwischen den Abfragen, um die API-Rate zu schonen (die Rate-Grenze von Hetzner ist im Repo nicht belegt: im echten Lauf messen).

### Stopp, parallel in zwei Wellen

* Erst alle Clients gleichzeitig, danach der Server. Das Netzwerk löscht die zweite Welle nach der Server-Löschung, wenn die Gruppe leer ist. Grund: Ein Server mit Clients wird ohne `--force` abgelehnt (`stop/nats.go`), und ein Client widerruft seinen Schlüssel am Server, solange dieser läuft. Scheitert oder bricht ein Client ab, startet die Server-Welle nicht.
* **Fragen:** `stop.Run` fragt heute mitten im Ablauf (Gruppenname in `planNATS`, `DISCARD` nach dem SSH-Befund, `DELETE`, `CLEANUP`). Deshalb wird `stop` in zwei Phasen geteilt: Phase 1 ermittelt (Server suchen, Gruppenplan, SSH-Befund über ungesicherte Dateien) und liefert die nötigen Fragen, statt sie zu stellen; `groupops` stellt sie nacheinander, sammelt die Antworten; Phase 2 führt parallel aus. Phase 1 ist **nicht rein lesend**: wie heute vor `DELETE` schreibt sie lokale Dateien (`ForgetStalePin`, `WriteConfig`, `PinHostKey`), was harmlos ist. Für den Einzelbefehl ändert sich nichts (er ruft beide Phasen nacheinander); alle vorhandenen Tests in `stop_test.go` müssen unverändert grün bleiben.
* **Schnappschuss des Servers:** Phase 1 des Servers sieht heute alle Clients als „andere“ (`others`), lehnt ohne `--force` ab und fragt mit `--force` nach dem Gruppennamen; `after()` entscheidet mit demselben Schnappschuss über das Löschen des Netzwerks. Phase 1 bekommt deshalb die Menge der gleichzeitig mitgelöschten Rechner (`departing`) und zieht sie von `others` ab. Gelesen wird sie für den Server **nach** der ersten Welle neu. `DELETE` wird einmal je Rechner gefragt (Sicherheitsabfrage bleibt je Rechner); der Gruppenname einmal je Gruppe.
* **Fremde im Netzwerk:** Ein Rechner im Netzwerk, der nicht in der Datei steht, wird gemeldet, nicht beendet. Dann wird der Server nicht beendet (heute verweigert `planNATS` ihn wegen der Fremden ohne `--force` und fragt mit `--force` nach dem Gruppennamen; `down --group` überspringt ihn stattdessen mit einer Meldung: neuer Zweig), das Netzwerk bleibt, und der Exit-Status ist ungleich 0. `check --group` meldet sie ebenfalls.

### Ausgabe, Abbruch, Ergebnis

* **Ausgabe:** Ein kleiner Präfix-Schreiber im CLI-Adapter (`[alpha] …`) setzt Teilzeilen zusammen, behandelt `\r` und schreibt je Terminal unter einem Mutex (aufbauend auf dem vorhandenen `lockedWriter` in `app.go`; beide Ströme, `l.Out()` und `l.Err()`, laufen über denselben Mutex); stdout und stderr bleiben getrennt. `viper log` und das Transkript bleiben je Rechner (die Protokolldatei verträgt gleichzeitiges Anhängen).
* **Abbruch (Strg-C):** ein gemeinsamer Kontext für alle Läufe; Exit-Status 130; die Tabelle zeigt „abgebrochen“.
* **Fehler:** Scheitert ein Rechner, laufen die übrigen weiter und am Ende steht eine Tabelle (Rechner, Ergebnis). Exit-Status ungleich 0, wenn einer scheiterte. Scheitert der Server, bevor das Signal kam, startet kein Client; scheitert er danach, werden die Clients abgebrochen (siehe oben).
* **Begrenzung:** Höchstens 8 Rechner gleichzeitig (`--parallel N`), wegen der API-Rate und weil die Installer (Claude, Homebrew) je Adresse begrenzen.

## 4 Risiken des Parallelbetriebs (gemeinsamer Zustand)

Das sind die Stellen, die bei gleichzeitigen Läufen kaputtgehen können, jede mit Maßnahme und Test:

| Stelle | Gefahr | Maßnahme |
|---|---|---|
| `natsconfig.sh` (Fehler im vorhandenen Code, auch ohne Gruppenbefehle; **behoben**, siehe Commit „natsconfig.sh: eigene temporäre Datei“) | das Skript schreibt `server.conf.new` (gleicher Name für alle) **vor** dem `flock`: zwei Clients überschreiben sich schon vor dem Sperren, einer setzt die Konfiguration des anderen ein und meldet Erfolg, ohne dass sein Schlüssel drinsteht | stdin erst nach dem `flock` schreiben (oder `mktemp`); **Test, der das Skript wirklich parallel ausführt** (nicht nur mit gefälschtem `Run`) |
| `server.conf` des NATS-Servers (`natsgroup.Ops.Update`) | 7 Clients ändern gleichzeitig; 6 Versuche ohne Pause können alle verlieren („changed meanwhile“), beim Widerruf bleibt dann ein Rechner stehen | zufällige Pause, mehr Versuche; Pause und Zufall über eine injizierbare Quelle (`Ops.Rand`, Uhr), denn `Sleep == nil` fällt auf `time.Sleep` zurück und `revokeAt` setzt `Sleep` auf einen No-op; Test mit 8 gleichzeitigen Änderungen |
| SSH-Konfiguration des Servers (`Ops.Peer` → `sshconfig.WriteConfig`, auch `announceServer`/`pinHostKey` des Server-Laufs) | `os.WriteFile` kürzt zuerst: ein anderer `ssh -F` liest eine leere Datei | atomar schreiben (temporäre Datei, Rename) und nur bei geändertem Inhalt; `PinHostKey` ebenso |
| Include-Zeile in `~/.ssh/config` | Lesen-Ändern-Schreiben | schon per `flock` (`ssh-config.lock`) serialisiert; Test mit gleichzeitigen Aufrufen |
| Login-Schlüssel, Firewall (`EnsureLoginKey`, `EnsureFirewall`, `FirewallAddRule`) | zwei Läufe legen gleichzeitig an; Regeln überschreiben sich (kein Fehler, also keine Wiederholung) | Vorarbeit einmal je Prefix und Fingerabdruck vor jeder Parallelphase; Wiederholung bei `EnsureLoginKey` und `FirewallCreate` |
| Netzwerk `viper-nats-<g>` | Wettlauf um den Namen | schon gelöst (`EnsurePrivateNetwork`, getestet) |
| Ausgabe | verschränkte Zeilen | Präfix-Schreiber |
| `RepoState`, Protokolle | gleiche Datei | je Rechner getrennt; prüfen |

## 5 Umsetzung

* `internal/model` und `internal/port/driven/settings`: `Machine.Group`, `Machine.GroupFile` und `Machine.Warnings` (die Signatur `Load` liefert sonst keinen Weg für „Warnung“); `settings.Prefix` kennt die Gruppendatei.
* `internal/adapter/driven/settingsfile`: Laden von `groups/<g>.env`, Rangfolge, Prüfungen, `Prefix`; Mitgliedschaft aus allen Dateien. Ein fehlendes `groups/` ist kein Fehler (alle vorhandenen Setups bleiben unverändert; Test). Eine Datei einer fremden Gruppe, die sich nicht lesen lässt und im Rohtext `GROUP_MACHINES=` mit dem Namen dieses Rechners enthält, ist ein **Fehler**, sonst verlöre der Rechner stillschweigend seine Gruppe; jede andere fremde Datei nur eine Warnung (einmal je Datei ausgegeben, nicht je Rechner).
* `internal/usecase/preflight`: `permissionChecks` prüft `groups/<g>.env` (über `Machine.GroupFile`, bisher nur `.env.local` und `machines/<name>`).
* `internal/usecase/start`: `OnServerCreated`, „Preflight schon gelaufen“, Wartezeit als Parameter, bessere Meldung in `natsRole`.
* `internal/usecase/stop`: Aufteilung in Phase 1 (lesend, liefert Fragen) und Phase 2.
* `internal/usecase/natsgroup` (auch `scripts/natsconfig.sh`), `internal/adapter/driven/sshconfig`, `internal/adapter/driven/hetzner`: Maßnahmen aus §4.
* Neuer Use Case `internal/usecase/groupops`: arbeitet gegen Ports, nicht gegen die Pakete `start`, `stop`, `check`. `Services.Up(c, UpOptions)` und `Services.Down(c, DownOptions)` sind Fabriken je Rechner; `groupops` bekommt diese Fabriken und einen **Port für den Zugang zum Anbieter** (Vorprüfung: Netzwerk, Zone, Server; Vorarbeit: Schlüssel, Firewall), den die bisherigen Ports nicht bieten. `UpOptions` trägt `OnServerCreated`, „Preflight überspringen“ und die Wartezeit; `driving.Down` bekommt Methoden für Phase 1 und Phase 2. `ssh --group --cmd` braucht einen Lauf mit erfasster Ausgabe (`ExecTool` ersetzt den Prozess und taugt nicht für mehrere Rechner). Er gibt die Ergebnistabelle zurück (druckt nicht selbst); gedruckt wird im CLI-Adapter.
* `internal/adapter/driving/cli`: `--group`, `--parallel` (in `isValueFlag` und `commands()`), neuer Modus ohne Rechnername, Präfix-Schreiber, Exit-Status 130.
* `viper setup` legt `groups/` an. Dokumentation: Handbuch (neues Kapitel, Einstellungen, Änderungsliste), `architektur.md`, `.env.local.example`, `groups/example.env.example`.

## 6 Meilensteine

| Nr. | Ergebnis |
|---|---|
| 1 | Datei laden: Modell (`Group`, `GroupFile`, `Warnings`), `Load`, `Prefix`, Preflight-Rechte, Mitgliedschaft, doppelte Zugehörigkeit, fremde Fehler als Warnung (mit der Ausnahme aus §5), fehlendes `groups/` ohne Fehler; Tests (noch kein neuer Befehl) |
| 2 | Gemeinsamer Zustand (§4): `natsconfig.sh` schreibt erst nach dem `flock` (mit echtem Skripttest, der parallel läuft; behebt einen Fehler, der heute schon besteht), `Update` mit Pause, Zufallsquelle und mehr Versuchen, atomare SSH-Konfiguration, Wiederholung bei `EnsureLoginKey` und `FirewallCreate`; Tests mit gleichzeitigen Aufrufen |
| 3 | `check --group` und `ssh --group` (lesend; Lauf mit erfasster Ausgabe), Präfix-Schreiber, `groupops` mit Ergebnistabelle, Fabriken, Anbieter-Port und Abbruch |
| 4a | Änderungen an `start` je Rechner: `OnServerCreated` (mit dem Sonderfall wiederverwendeter Server), „Preflight überspringen“, Wartezeit als Parameter, bessere Meldung in `natsRole`; getestet mit einem einzelnen `Up` |
| 4b | `up --group`: Vorprüfung des Gruppenzustands, Vorarbeit, Server zuerst, Clients parallel, Jitter, Abbruch; echter Lauf mit drei Rechnern (auch bestehende Gruppe, Server fällt aus) |
| 5a | `stop` in zwei Phasen (mit `departing` und neuem Lesen des Servers), ohne Gruppenbefehl; alle Tests in `stop_test.go` unverändert grün |
| 5b | `down --group`: Clients parallel, danach der Server, Fremde melden; echter Lauf mit Aufräumen |
| 6 | Handbuch, Abnahme |

## 7 Entscheidungen, die feststehen

* Parallel von Anfang an (nicht erst später).
* Rechner ohne Datei in `machines/` sind erlaubt.
* Gruppen ohne NATS sind erlaubt (alle gleichzeitig).
* Rechner im Netzwerk, aber nicht in der Datei, werden gemeldet und nie von `down --group` beendet.
* Jeder Rechner darf seinen eigenen Servertyp, Standort (innerhalb einer Netzwerkzone) und sein Image haben (wie im echten Test mit `cpx22` und `cax11`); `up --group` prüft das nicht auf Gleichheit.

## 8 Offene Fragen

* Soll es einen Befehl geben, der die Gruppe aus dem Zustand beim Anbieter in eine Datei schreibt (`viper group export g`)?
