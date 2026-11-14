# Plan: Eine Oberfläche im Browser (`viper ui`)

**Ziel:** `viper ui` startet auf dem eigenen Rechner einen kleinen Webserver im selben Programm, öffnet den Browser und zeigt dort, was `viper` weiß und kann: die Rechner und Gruppen dieses Rechners, ihren Zustand (`viper check`), die Protokolle, die NATS-Gruppen (Anwesenheit, Themen, Nachrichten mitlesen und senden, Dateien) und, in einer späteren Stufe, `up`, `down` und `preflight` mit Ausgabe und Rückfragen. Es gibt keine neue Bibliothek, kein cgo und keinen Build-Schritt für die Seiten (eingebettet mit `embed`, reines HTML, CSS und JavaScript), die Release-Binaries bleiben reines Go für fünf Ziele.

**Stand:** Entwurf (14.11.2026), einmal unabhängig gegen den Code geprüft (§10); Meilenstein 0 und 1 sind gemacht (§6), der Rest nicht. *[V]* heißt: im Code gelesen; *[?]* ist eine Vermutung, die vor dem Bauen zu prüfen ist.

## 1 Ausgangslage

* `viper` ist ein Programm mit Befehlen (`internal/adapter/driving/cli`), die über Ports (`internal/port/driving`) die Anwendungsfälle aufrufen; die Außenwelt (Programme, Terminal, Umgebung, Ein- und Ausgabe) steckt in `cli.Host` und wird in Tests ersetzt *[V]*. Ein zweiter treibender Adapter (`internal/adapter/driving/web`) passt in diese Architektur.
* Die Befehle schreiben auf `Host.Stdout` und `Host.Stderr` und lesen Rückfragen („Type DELETE to continue“, „Type DISCARD …“) von `Host.Stdin` *[V]*. `forbidigo` verbietet `os.Stdout` und `os.Stderr` außerhalb von `cmd/viper` *[V]*: ein Webadapter bekommt seine Writer.
* Nur `viper nats` kennt `--json` *[V]*; die Ausgabe von `viper check` und `viper log` ist Text. **Ob `check` gut war, sagt der Exit-Code (0 gut, 1 Probleme oder Fehler, 130 abgebrochen), nicht der Text** *[V]*: Die letzte Zeile „Check OK.“ oder „Check: N problem(s).“ allein unterscheidet einen Fehler nicht von Problemen.
* **Eine Liste der Rechner und Gruppen gibt es nicht** (`Settings.Load` lädt einen Rechner, `groupMachines` die Rechner einer Gruppe) *[V]*: Sie muss geschrieben werden (`machines/*.env.local`, `groups/*.env` durchsuchen, dazu die Marken des Zustandsverzeichnisses).
* **Alle Rückfragen** gehen über `newAsker` (`down`) und `groupAsker` (Gruppen): „Type DELETE“ (`stop.go`), die Frage nach der Registry („Type DELETE to delete it“), „CLEANUP“ (Reste eines nicht mehr vorhandenen Rechners), „DISCARD“ (ungesicherte Arbeit) und der Gruppenname beim Löschen des Servers einer NATS-Gruppe (`stop/nats.go`) *[V]*. Sie schreiben auf **stderr**, **ohne Zeilenende**, und lesen eine Zeile von `Host.Stdin`; ein Ende der Eingabe ohne Zeilenende gilt als keine Antwort. Keine Rückfrage liest `/dev/tty`: ohne Terminal hängt nichts, außer wenn die Eingabe geschlossen ist *[V]*.
* **Ohne Terminal unbrauchbar** sind `viper ssh` (ohne `--cmd`, und `--tmux` verlangt ein Terminal) und der Aufruf von VS Code: `viper up` öffnet VS Code (`code --remote`), außer mit `--no-code` (bei `--group` ohnehin nicht) *[V]*.
* **Abbrechen:** Das erste SIGINT, SIGTERM oder SIGHUP bricht den Kontext ab; Programme wie `ssh` laufen mit `exec.CommandContext` und werden beendet, `viper` beendet sich geordnet (Protokoll, Exit 130); ein zweites SIGINT beendet sofort. `up` prüft den Abbruch zwischen den Schritten, nicht mitten in einem Aufruf des Anbieters: ein halb angelegter Server kann bleiben (`signals.go`, `usecase/start/provision.go`) *[V]*. Strg-C am Terminal ist also **sicher im Sinn von Strg-C, nicht „rückgängig“**.
* **Protokolle sind nicht gefiltert:** `runlog` schreibt, was die Befehle drucken (`fmt.Fprintf`); „`viper` druckt keine Zugangsdaten“ ist eine Behauptung des Codes, kein Filter. `events.log` und `logs/<server>.log` enthalten Ausgaben von `ssh` und Anbietern und können Steuerzeichen, ANSI-Folgen und Markup enthalten; `natsmcp.Clean` gilt nur für NATS-Inhalte *[V]*.
* Was dieser Rechner über Rechner und Gruppen weiß, liegt in Dateien: `machines/<m>.env.local`, `groups/<g>.env`, im Zustandsverzeichnis `~/.local/state/viper/servers/<präfix>/` (`id-…`, `ip-…`, `ready-…`, `group-…`), `events.log` und `logs/<server>.log` *[V]*. Die Einstellungen enthalten **Zugangsdaten** (`HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `VIPER_GH_TOKEN`, `VIPER_CLAUDE_TOKEN`, `VIPER_CODE_SERVER_PASSWORD`, AWS- und Azure-Angaben, …); ebenso die Schlüssel unter `~/.ssh` und der Schlüssel des Betreibers `~/.local/state/viper/groups/<gruppe>/operator.nk` *[V]*.
* `viper nats` als Betreiber arbeitet im Prozess (`natsmcp.RunCLI` über einen SSH-Tunnel) *[V]*; als Maschine führt es `viper-nats-mcp` per SSH aus.

## 2 Grundregeln

1. **Nur lokal.** Der Server lauscht auf `127.0.0.1` (nie `0.0.0.0`, nie einen anderen Namen), mit einem freien Port oder `--port`. Es gibt keinen Zugriff von anderen Rechnern und keine Anmeldung mit Benutzern; wer sich von außen erreichen will, tunnelt selbst.
2. **Die Oberfläche kann nichts, was `viper` nicht kann, und sie umgeht keine Rückfrage.** Sie ruft dieselben Befehle auf wie der Benutzer (§4) und zeigt deren Ausgabe. Was am Terminal bestätigt werden muss (Löschen, ungesicherte Arbeit), bestätigt man in der Oberfläche, mit demselben Wort.
3. **Keine Zugangsdaten auf dem Bildschirm.** Die Seiten zeigen Namen, Zustand und Ausgaben, nie den Inhalt der Einstellungsdateien (nur eine Liste der Schlüsselnamen mit „gesetzt/nicht gesetzt“) und nie die Schlüssel (`operator.nk`, SSH-Schlüssel). **Auch die Ausgaben werden gefiltert** (§3): Weil `viper` Zugangsdaten nur nicht druckt, aber nicht filtert, ersetzt die Oberfläche die Werte der bekannten geheimen Einstellungen, wo sie in einer Ausgabe vorkommen, und reinigt Steuerzeichen.
4. **Alles von außen ist Text.** Nachrichten anderer Agenten, Zeilen aus Protokollen, Namen von Dateien und Rechnern werden nur als Text eingesetzt (`textContent`), nie als HTML.
5. **Was ein Prozess der Oberfläche anlegt, gehört `viper`.** Die Oberfläche löscht und ändert nichts, was der Befehl nicht ändert.

## 3 Sicherheitsmodell (der wichtigste Teil)

Der Browser ist ein Programm, das auch fremde Seiten lädt. Ein Port auf `127.0.0.1` ist für diese erreichbar. Die Gegner sind: (a) eine fremde Webseite im Browser des Benutzers (Anfragen an `http://127.0.0.1:<port>`, auch mit DNS-Rebinding), (b) ein anderer Benutzer desselben Rechners, (c) bösartiger **Inhalt** (NATS-Nachrichten, Protokolle, Dateinamen), (d) ein versehentlich zu großer oder zu vieler Zugriff.

| Maßnahme | gegen |
|---|---|
| Der Server lauscht nur auf `127.0.0.1` (IPv4; kein IPv6, kein anderer Name); jede Anfrage braucht den Host-Kopf **genau** `127.0.0.1:<port>`, sonst 403. **Nicht `localhost`:** Der Name kann über eine Hosts-Datei auf eine andere Adresse zeigen, und eine Seite mit Rebinding kann ihn benutzen | DNS-Rebinding, Zugriff über einen anderen Namen |
| Ein zufälliger Schlüssel (128 Bit) je Start; die erste Anfrage tauscht ihn gegen ein Sitzungs-Cookie (`HttpOnly`, `SameSite=Strict`, Pfad `/`; kein `__Host-`-Name, denn der verlangt `Secure`, und es gibt kein TLS) und leitet mit `Cache-Control: no-store` und `Referrer-Policy: no-referrer` auf eine Adresse ohne Schlüssel um. Der Tausch gilt **nur einmal (atomar unter einer Sperre)** und **höchstens 60 Sekunden** nach dem Start. Das Cookie gilt bis zum Ende des Servers, bei jedem Start ein neues. Vergleich in konstanter Zeit. **Der Schlüssel in der Adresse ist für andere Benutzer in `ps` und `/proc/<pid>/cmdline` des Programms, das den Browser öffnet, kurz sichtbar:** Entscheidung in Meilenstein 0 (§9): eine Startdatei (`0600` in einem privaten Verzeichnis) statt der Adresse, oder die Adresse nur im Terminal ausgeben (`--no-open`) | andere Benutzer des Rechners, Verlauf und Referer des Browsers |
| Zustandsändernde Anfragen sind `POST` mit `Content-Type: application/json`, dem Kopf `X-Viper-UI: 1`, `Sec-Fetch-Site: same-origin` (wo der Browser ihn sendet) und einem `Origin`, der genau `http://127.0.0.1:<port>` ist: ein fehlender oder `null`-Origin wird abgelehnt. Kein CORS. **Jede GET-Anfrage ist ohne Nebenwirkung** (auch die der Ereignisströme: ein Lauf wird mit `POST` gestartet, ein `GET` hängt sich nur an die ID des Laufs an); `EventSource` kann keine Köpfe setzen, trägt aber das Cookie | CSRF und einfache Formularanfragen fremder Seiten |
| `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`; keine fremden Ressourcen, **keine Inline-Skripte und keine Inline-Stile** (kein `style="…"`, keine `setAttribute('style')`; `element.style.x = …` bleibt erlaubt), kein `eval`, kein `innerHTML` | Skripteinschleusung, Einbetten in fremde Seiten |
| Befehle laufen als Programm mit **Argumentliste** (nie über eine Shell); Rechner- und Gruppennamen kommen aus der Liste der vorhandenen (nicht aus freiem Text), werden mit denselben Prüfungen wie am Terminal gelesen, und ein `--` steht vor Wörtern, die keine Optionen sind (ein Name aus einem Dateinamen darf nicht mit `-` beginnen) | Befehlseinschleusung |
| Nur die Pfade der eigenen Seiten und der Programmschnittstelle (`/api/…`); keine Datei aus dem Einstellungs- oder Zustandsverzeichnis, kein Verzeichnislisten; Dateien zum Herunterladen nur über eine ID, mit `Content-Type: application/octet-stream` (erzwungen: sonst zeigte der Browser ein PDF an), `Content-Disposition: attachment; filename*=` mit einem Namen von `natsmcp.CleanName` und `nosniff` | Pfadumgehung, Lesen der Zugangsdaten |
| Hochgeladene Dateien (für `--pdf`/`--image`/`--zip`): `http.MaxBytesReader` mit `NATS_OBJECTS_FILE_MAX` **der gewählten Gruppe** (eine Einstellung der Gruppe), `multipart` als Strom (nicht `ParseMultipartForm` in den Speicher), in eine Datei mit Zufallsnamen in einem Verzeichnis von `os.MkdirTemp` (0700), gelöscht, wenn der Lauf endet oder scheitert; der Befehl prüft sie wie am Terminal | große Uploads, Namen vom Browser |
| Höchstens N gleichzeitige Verbindungen; **ein Strom je Browser-Tab** (der Browser erlaubt sechs je Ursprung), ein Lauf je Rechner und Gruppe in einer Tabelle auf dem Server (`down --group` sperrt jedes Mitglied); Wartezeiten, Größen und die Zeilen im Browser (Ringpuffer, z. B. 1000) und die Rate der Ereignisse begrenzt | Erschöpfung |
| **Alle** Ausgaben, auch die Protokolle (`events.log`, `logs/*.log`) und die Ausgabe der Läufe, werden auf dem Server mit `natsmcp.Clean` gereinigt und danach geschwärzt: Die Werte der geheimen Einstellungen (die Liste ist die Tabelle der geheimen Namen in `editor/vscode.go`, keine neue) kommen nicht durch; in der Oberfläche werden sie mit `textContent` gesetzt, **nie** als `href`, `src` oder `download` aus Inhalten | Skripteinschleusung über Inhalte, Zugangsdaten in Ausgaben |
| `viper ui --readonly` bietet keine Aktion an (nur Ansehen); die Aktionen sind sonst erst in Stufe 2 da; `viper ui` als `root` warnt | versehentliches Löschen |

**Kein TLS:** Der Verkehr bleibt auf dem Rechner. **Kein Speichern von Schlüsseln im Browser:** das Cookie ist nur ein Sitzungsschlüssel dieses Starts.

## 4 Aufbau

* **Neuer Adapter** `internal/adapter/driving/web`: `net/http`, `embed` für die Seiten (`web/static/`), JSON für die Programmschnittstelle, Server-Sent Events (`text/event-stream`) für Ausgabe und Nachrichten. Er bekommt von `cli` einen kleinen Zugang (`cli.UI`): die Liste der Rechner und Gruppen (aus den Einstellungsdateien, ohne Anbieter-Anfrage), das Zustandsverzeichnis und eine Funktion, die einen Befehl als Prozess startet. **Der Befehl `viper ui` ist ein Eintrag in der Tabelle der Befehle** (`help.go` beschreibt ihn).
* **Lesen ohne Anbieter:** `GET /api/machines` und `/api/groups` liefern, was die Dateien sagen (Name, Anbieter, Gruppe, NATS-Gruppe, Größe, Zustand der Marken `id-`, `ready-`, `group-`), ohne dass `viper` einen Anbieter fragt. Das ist schnell und kostet nichts; „läuft das wirklich?“ beantwortet `viper check` auf Wunsch.
* **Lesen im Prozess, Aktionen als Prozess (Mischform).** Was nur liest, ruft die Anwendungsfälle im Prozess auf: die Liste (neu zu schreiben, §1), `Services.Check` (liefert die Zahl der Probleme, kein Text), `ShowLog`. Das vermeidet das Zerlegen von Text, die Kosten eines Prozesses und das harte Beenden. **Was ändert** (`up`, `down`, `preflight` und die NATS-Läufe), startet `viper <befehl> …` als Unterprozess des eigenen Programms (`os.Executable()`, mit `--dir`, **immer `--no-code`**): Sperren, Protokolle, Rückfragen und Prüfungen sind genau die am Terminal, und es gibt keinen zweiten Weg, etwas anzulegen oder zu löschen. Das Ergebnis von Läufen ist der **Exit-Code** (0 gut, 1 Probleme oder Fehler, 130 abgebrochen), nicht der Text.
* **Rückfragen:** Die Oberfläche liest **stdout und stderr in Stücken**, nicht zeilenweise: Eine Rückfrage hat kein Zeilenende und erscheint sonst erst mit der nächsten Zeile. Ein Lauf hat die Zustände „läuft“ und „wartet auf Eingabe“ (die Ausgabe endet ohne Zeilenende, eine kurze Zeit lang kommt nichts), dann zeigt die Oberfläche die bisherige Ausgabe samt Frage und ein Eingabefeld; die Antwort geht mit `\n` auf die **offen gehaltene** Standardeingabe des Prozesses (eine geschlossene Eingabe gibt jeder Rückfrage die Antwort „“). Ein Knopf „Löschen bestätigen“ gibt es nicht: Der Benutzer tippt das Wort selbst. Bei einem Lauf mit `--group` kommt die Ausgabe in Blöcken je Rechner, mit dem Vorsatz `[rechner]`, und eine Rückfrage wartet, bis sie an der Reihe ist (`ws.turn`); die Oberfläche zeigt einen Strom und erkennt den Vorsatz. Zwei Tabs, die dieselbe Rückfrage beantworten: Die erste Antwort gilt, der Server reiht sie ein.
* **NATS:** Anwesenheit und Themen laufen über `viper nats peers|topics --json`; „Mitlesen“ ist `viper nats listen --json` als langer Lauf mit Ereignissen. Senden, Rundruf und Anfrage rufen `viper nats send|broadcast|request` mit dem Text auf der Standardeingabe auf; Dateien als Upload in ein privates temporäres Verzeichnis und `--pdf|--image|--zip PFAD`; `fetch` legt die Datei in ein privates Verzeichnis und bietet sie zum Herunterladen an. Alles ohne neue Fähigkeit der NATS-Seite.
* **Abbrechen** ist das Strg-C des Terminals und **kein Rückgängigmachen** (§1: ein halb angelegter Server kann bleiben); die Oberfläche sagt das neben dem Knopf. Unter Linux und macOS ein SIGINT **an den Prozess** (nicht an die Gruppe: `ssh` und andere Kinder beendet `viper` selbst geordnet), nach 20 Sekunden SIGKILL. Unter Windows `CREATE_NEW_PROCESS_GROUP` und `CTRL_BREAK`; ob das einen geordneten Abbruch bewirkt, ist in Meilenstein 0 zu prüfen, sonst das Beenden des Prozesses ohne Aufräumen mit der Warnung. Ein Abbruch mit dem Schließen des Tabs gibt es nicht (§9).

## 5 Die Seiten (Stufe 1 bis 4)

1. **Übersicht:** Rechner und Gruppen als Tabelle; je Rechner „Zustand prüfen“ (`viper check`, die Ausgabe auf einer Seite, das Ergebnis als Anzeige „ok/Probleme“), „DNS“, „Protokoll“.
2. **Protokolle:** Ereignisse aller Rechner, eines Rechners, die vollständige Ausgabe (`viper log`), mit Suchen im Text.
3. **Aktionen (Stufe 2):** `preflight`, `up`, `down` (je Rechner und `--group`), mit der Ausgabe live, der Rückfrage, Abbrechen; eine Shell gibt es **nicht** (`viper ssh` zeigt den Befehl zum Kopieren).
4. **NATS (Stufe 3):** Gruppe wählen; Anwesenheit (`peers`), Themen, Mitlesen (SSE), Senden, Rundruf, Anfrage, Datei senden und holen; die Zeile mit dem Betreiber-Schlüssel („als Betreiber“ oder „als Maschine X“) steht immer da.
5. **Einstellungen (nur lesend, Stufe 4):** Die Schlüsselnamen aus `.env.local`, der Gruppe und der Rechnerdatei mit „gesetzt“ und dem Ort; Werte nur bei Schlüsseln einer **Positivliste** nicht geheimer Namen (`SERVER_TYPE`, `SERVER_LOCATION`, `PROVIDER`, `NATS_*` außer Schlüsseln, …), alles andere „gesetzt“: Eine Negativliste verfehlt die Namen eines Anbieters (AWS, Azure). Werte, die nur in der Umgebung stehen, zeigt die Liste als „aus der Umgebung“ ohne Wert.

Sprache der Oberfläche: Englisch wie die Hilfe von `viper` und die Meldungen; ein Umschalten auf Deutsch ist Sache einer späteren Stufe (Texte in einer Datei, damit es geht).

## 6 Meilensteine

0. **Prüfen (nichts bauen), zusätzlich zu den Punkten des Reviews:** (e) eine Rückfrage ohne Zeilenende kommt rechtzeitig an (Stücke statt Zeilen); (f) ein SIGINT an einen `viper up` gegen einen gefälschten Anbieter lässt den Lauf geordnet enden; (g) ein `//go:embed` verletzt `gochecknoglobals` nicht (Umhüllung durch eine Funktion); (h) die Startdatei statt der Adresse mit dem Schlüssel funktioniert in Firefox und Chrome (ein `POST` von einer `file://`-Seite hat den `Origin` `null`: Der Tausch nimmt ihn als einzige Ausnahme an); (i) das Öffnen des Browsers: `xdg-open`, `open`, `rundll32 url.dll,FileProtocolHandler` (nie `cmd /c start`, das `&` in der Adresse deutet). Dazu: (a) Reicht ein Unterprozess für die Rückfragen (wie fragen `down` und `down --group` heute, liest jede Rückfrage von `Host.Stdin`, hängt etwas an einem Terminal-Test?). (b) Wie viel von `viper check`, `viper log` und den Rechnerlisten lässt sich ohne Anbieter ablesen. (c) Ein Versuch mit `net/http` unter dem Sicherheitsmodell: Host-Kopf, Cookie, `Origin`, CSP, Umleitung nach dem Tausch; mit einem Test durch `httptest` und einem echten Browser von Hand (Firefox und Chrome): fremde Seite, `fetch` an den Port, DNS-Rebinding mit einem Namen auf `127.0.0.1`. (d) Windows: Browser öffnen, Prozess beenden.
1. **Gerüst und Nur-Lesen (gemacht, nach v0.24.0):** `viper ui` (`--port`, `--no-open`, `--readonly`), Server, Sicherheit aus §3 mit Tests, die Seite „Übersicht“ und „Protokolle“, `viper check` auf Wunsch. Hilfe, Handbuch, README.
2. **Aktionen:** Läufe als Prozess mit Ausgabe, Rückfragen, Abbrechen, ein Lauf je Rechner; `preflight`, `up`, `down`.
3. **NATS:** Anwesenheit, Themen, Mitlesen, Senden, Dateien; mit dem Betreiber-Schlüssel und als Maschine.
4. **Einstellungen (lesend), Deutsch, Feinschliff:** Zugänglichkeit (Tastatur, Kontrast, Screenreader-Namen), Größe der Seiten, Fehlerseiten.
5. **Handbuch und Läufe:** Kapitel „Oberfläche im Browser“, `developer.md`, Läufe mit Firefox und Chrome auf Linux, macOS und Windows (so weit erreichbar), gegen echte Docker-Maschinen (Compose), k3d und, mit Erlaubnis, Hetzner.

### Ergebnis von Meilenstein 0 (14.11.2026, Linux)

Gemacht mit einem Wegwerf-Paket (`internal/adapter/driving/webprobe`, wieder entfernt) und dem echten `viper`:

| Punkt | Ergebnis |
|---|---|
| (c) Sicherheitsmodell mit `net/http` + `httptest` | **Ja.** Geprüft und grün: falscher Host (auch `localhost`), Tausch genau einmal, ohne Cookie 403, `Origin` fehlend/`null`/fremd/`localhost` 403, `Sec-Fetch-Site: cross-site` und `text/plain` 403, `..` im Pfad, CSP mit `object-src 'none'`, Umleitung 303 auf `/` mit `Referrer-Policy: no-referrer`. Nicht gemacht: echter Browser, 60-Sekunden-Ablauf. |
| (e) Rückfrage ohne Zeilenende | **Ja.** Mit Leser in Stücken (stdout und stderr in eine Röhre) kommt „Type DELETE to continue: “ sofort an; die Antwort mit `\n` auf die offene Eingabe wird gelesen. Ein Zeilenleser hätte sie nicht geliefert. |
| (f) SIGINT | **Ja, mit Vorbehalt.** `exec.Cmd.Cancel` mit `Process.Signal(SIGINT)` an den Unterprozess, `WaitDelay` 20 s: Ein Testprogramm mit `trap` endet mit 130. Das echte `viper up` (gegen ein gefälschtes `docker`) beendet das Kind (`docker`), beendet den laufenden Schritt, schreibt „interrupted“ ins Protokoll und endet **erst nach dem Ende der laufenden Vorprüfung (hier rund 15 s)**: Die Oberfläche braucht also „wird abgebrochen …“ als Zustand und darf nicht nach einer Sekunde SIGKILL schicken. Unter Windows nicht geprüft; `Process.Signal(SIGINT)` geht dort nicht, der Code braucht eine Datei je Betriebssystem (`//go:build`). |
| (g) `//go:embed` und `gochecknoglobals` | **Kein Verstoß:** `//go:embed static` an einer Variable und eine Funktion `Static()` darüber bestehen `golangci-lint` mit der Konfiguration des Projekts (der einzige Fund war ein ungenutzter Parameter). |
| (a) Reicht ein Unterprozess für Rückfragen | **Ja** (e); keine Rückfrage liest `/dev/tty`. |
| (b) Lesen im Prozess | `port/driving.Check` liefert die Zahl der Probleme (`Check(ctx, c, out, errOut) (int, error)`) und schreibt den Text auf `out`; für `ShowLog` und die Liste gibt es **noch keinen Port**. |
| (d) Windows, (h) Startdatei in Firefox/Chrome, (i) Browser öffnen | **Nicht geprüft** (kein Browser, kein Windows/macOS in dieser Umgebung); bleibt Aufgabe vor Meilenstein 1 und Meilenstein 5. `GOOS=windows go vet` des Probe-Pakets ging durch. |

**Neu aus dieser Prüfung:**

* Die Regel `driving-adapters-call-ports` in der `.golangci.yml` verbietet einem Adapter, `usecase` zu importieren. Das Paket `driving/web` ruft die Anwendungsfälle nur über `internal/port/driving`; für die Liste und `ShowLog` ist dort je eine Schnittstelle zu ergänzen, und `app` verdrahtet sie. Kein neuer Eintrag in `depguard` nötig, die Regel gilt für `internal/adapter/driving/**` schon.
* Abbrechen braucht den Zustand „wird abgebrochen“ (rund 15 s sind normal), SIGKILL erst nach 20 s.

## 7 Teststrategie

* `httptest` für jede Sicherheitsregel aus §3 (falscher Host, auch `localhost`; kein Cookie; falscher oder fehlender Ursprung; `Sec-Fetch-Site`; falsche Methode; falscher `Content-Type`; zu große Hochladung; unbekannter Pfad; Pfadumgehung `..`; Verzeichnisliste; zweiter Tausch des Schlüssels; Tausch nach 60 Sekunden), mit der Sicherung, dass **keine** Antwort einen Wert aus den Einstellungsdateien enthält (ein Test legt Zugangsdaten in die Dateien und sucht in allen Antworten, auch in Protokollen und Läufen).
* Ein gefälschter Unterprozess (eigene Funktion statt `os.Executable()`) für Ausgabe in Stücken, Rückfrage ohne Zeilenende, Antwort, Abbrechen (SIGINT, dann SIGKILL), Exit-Codes, Parallelität und die Sperre je Rechner.
* Gerüche der Seiten ohne Browser: kein `eval`, kein `innerHTML`, kein `style=`, keine fremde Adresse (ein Test sucht in den eingebetteten Dateien).
* Die Liste der Rechner und Gruppen gegen ein Verzeichnis mit Beispieldateien; Ausgaben mit ANSI-Folgen und geheimen Werten gegen die Reinigung.
* Windows-CI: Pfade, Beenden des Prozesses; Tests von Signalen und Prozessgruppen mit `//go:build !windows`, die Tests mit `httptest` laufen überall. Tests im Paket `_test` (Regel `testpackage`): Die Handler sind nur über die ausgeführte Schnittstelle erreichbar.
* Von Hand: ein echter Browser, eine fremde Seite, die versucht, den Port anzusprechen.

## 8 Nicht Teil dieses Plans

* Zugriff von anderen Rechnern, Benutzer und Rechte, TLS, ein Dienst im Hintergrund.
* Ein Terminal im Browser (`viper ssh`); die Einstellungsdateien bearbeiten; neue Rechner oder Gruppen anlegen (Dateien schreiben).
* Eine Oberfläche für `viper find` (später, lesend und ungefährlich).

## 9 Entscheidungen und offene Fragen

Entschieden (Prüfung, §10):

* **Lesen im Prozess, Aktionen als Unterprozess** (§4); das Ergebnis eines Laufs ist der Exit-Code.
* **Protokoll der Oberfläche:** Pfad und Methode, nie die Adresse mit dem Schlüssel und kein Query-String; nicht in `events.log`.
* **Port belegt:** ein Fehler mit dem Hinweis auf `--port`; ohne `--port` wählt das Betriebssystem einen freien.
* **Beenden:** Strg-C am Terminal: `Server.Shutdown`, danach laufende Läufe abbrechen (wie §4) und auf sie warten, höchstens 20 Sekunden.
* **Mehrere Tabs:** ein Strom je Tab, Läufe gehören dem Server, nicht dem Tab; ein neuer Tab sieht die laufenden Läufe. Das Schließen eines Tabs bricht nichts ab.
* **Sitzung:** kein Ablauf; das Cookie gilt bis zum Ende des Servers.
* **Als `root`:** eine Warnung beim Start.
* **Sprache:** Englisch zuerst, alle Texte in einer Datei (später übersetzbar).
* **Andere Wege, je ein Satz:** eine Oberfläche im Terminal (TUI) löst das Problem nicht, das Browser-Leute haben (Mausbedienung, Tabellen); Wails und Fyne brauchen cgo oder einen Browser-Motor, was das Ziel „ein Programm, kein cgo“ verletzt.

Offen:

* **`--force` und Antworten ohne Tippen:** Eine Rückfrage kann `down --force` vermeiden. Das Wort selbst zu tippen (§4) ist die Regel; ob `--force` als Auswahlfeld angeboten wird, ist zu entscheiden (Vorschlag: nein in Stufe 2).
* **Linter:** Ein neues Paket `internal/adapter/driving/web` muss in die Grenzregeln der `.golangci.yml` (depguard o. ä.) aufgenommen werden; vor Meilenstein 1 prüfen. Funktionen klein halten (`funlen`), die eingebetteten Dateien in einer Funktion kapseln (`gochecknoglobals`).
* **Barrierefreiheit:** `aria-live` für Ausgaben kollidiert mit schnellen Strömen; die Rate wird begrenzt (§3), die Tastaturbedienung der Rückfrage ist in Stufe 2 zu prüfen.

* **Der Schlüssel in der Adresse** (Meilenstein 0, §6): Startdatei oder `--no-open`.
* **`--json` für `check` und `log`:** Später; die Oberfläche braucht es nicht, weil sie im Prozess liest.
* **Zwei Fenster, zwei Läufe:** `viper ui` und ein Terminal können gleichzeitig denselben Rechner anfassen. Die Sperren gelten wie bisher; die Oberfläche zeigt „läuft schon“.
* **Windows:** Abbrechen (§4) ist zu prüfen.
* **Wartung:** Neue Optionen eines Befehls müssen in den Formularen auftauchen; ein Test wie bei der Hilfe kann sie vergleichen.

## 10 Prüfung

Ein unabhängiger Prüfer hat den ersten Entwurf gegen den Code gelesen. Die Funde und was daraus wurde:

| Fund | Folge im Plan |
|---|---|
| Rückfragen gehen auf stderr ohne Zeilenende und lesen eine Zeile Eingabe | Lesen in Stücken, Zustand „wartet auf Eingabe“, offene Eingabe (§1, §4) |
| Abbrechen: SIGINT nur an den Prozess; Windows braucht eine eigene Prozessgruppe | §4, Prüfung in Meilenstein 0 |
| Protokolle sind nicht gefiltert | Reinigung und Schwärzung aller Ausgaben (§2, §3) |
| Es gibt keine Liste der Rechner | neu zu schreiben (§1) |
| Ob `check` gut war, sagt der Exit-Code | §1, §4 |
| `localhost` im Host-Kopf | gestrichen (§3) |
| Schlüssel in `ps` und `/proc/<pid>/cmdline` | Startdatei oder `--no-open` (§3, §9) |
| `up` öffnet VS Code, `ssh` braucht ein Terminal | immer `--no-code`, `ssh` nicht angeboten (§1, §4) |
| CSRF, SSE und `EventSource`, CSP, Downloads, Uploads | §3 |
| Lint und `//go:embed` | Meilenstein 0 (g) |
| Fehlende Punkte: Protokoll, Port, Beenden, Tabs, Sitzung, `root`, Sprache | §9 |
| Aufwand: Lister, Browser-Öffnen auf drei Systemen, Signaltests nur außerhalb Windows, Linter-Grenzen, `--force` | §6, §7, §9 |
