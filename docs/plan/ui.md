# Plan: Eine Oberfläche im Browser (`viper ui`)

**Ziel:** `viper ui` startet auf dem eigenen Rechner einen kleinen Webserver im selben Programm, öffnet den Browser und zeigt dort, was `viper` weiß und kann: die Rechner und Gruppen dieses Rechners, ihren Zustand (`viper check`), die Protokolle, die NATS-Gruppen (Anwesenheit, Themen, Nachrichten mitlesen und senden, Dateien) und, in einer späteren Stufe, `up`, `down` und `preflight` mit Ausgabe und Rückfragen. Es gibt keine neue Bibliothek, kein cgo und keinen Build-Schritt für die Seiten (eingebettet mit `embed`, reines HTML, CSS und JavaScript), die Release-Binaries bleiben reines Go für fünf Ziele.

**Stand:** Entwurf (14.11.2026), noch nichts gebaut, noch nicht geprüft. *[V]* heißt: im Code gelesen; *[?]* ist eine Vermutung, die vor dem Bauen zu prüfen ist.

## 1 Ausgangslage

* `viper` ist ein Programm mit Befehlen (`internal/adapter/driving/cli`), die über Ports (`internal/port/driving`) die Anwendungsfälle aufrufen; die Außenwelt (Programme, Terminal, Umgebung, Ein- und Ausgabe) steckt in `cli.Host` und wird in Tests ersetzt *[V]*. Ein zweiter treibender Adapter (`internal/adapter/driving/web`) passt in diese Architektur.
* Die Befehle schreiben auf `Host.Stdout` und `Host.Stderr` und lesen Rückfragen („Type DELETE to continue“, „Type DISCARD …“) von `Host.Stdin` *[V]*. `forbidigo` verbietet `os.Stdout` und `os.Stderr` außerhalb von `cmd/viper` *[V]*: ein Webadapter bekommt seine Writer.
* Nur `viper nats` kennt `--json` *[V]*; die Ausgabe von `viper check` und `viper log` ist Text, `check` endet mit „Check OK.“ oder „Check: N problem(s).“ *[V]*.
* Was dieser Rechner über Rechner und Gruppen weiß, liegt in Dateien: `machines/<m>.env.local`, `groups/<g>.env`, im Zustandsverzeichnis `~/.local/state/viper/servers/<präfix>/` (`id-…`, `ip-…`, `ready-…`, `group-…`), `events.log` und `logs/<server>.log` *[V]*. Die Einstellungen enthalten **Zugangsdaten** (`HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `VIPER_GH_TOKEN`, …) *[V]*.
* `viper nats` als Betreiber arbeitet im Prozess (`natsmcp.RunCLI` über einen SSH-Tunnel) *[V]*; als Maschine führt es `viper-nats-mcp` per SSH aus.

## 2 Grundregeln

1. **Nur lokal.** Der Server lauscht auf `127.0.0.1` (nie `0.0.0.0`, nie einen anderen Namen), mit einem freien Port oder `--port`. Es gibt keinen Zugriff von anderen Rechnern und keine Anmeldung mit Benutzern; wer sich von außen erreichen will, tunnelt selbst.
2. **Die Oberfläche kann nichts, was `viper` nicht kann, und sie umgeht keine Rückfrage.** Sie ruft dieselben Befehle auf wie der Benutzer (§4) und zeigt deren Ausgabe. Was am Terminal bestätigt werden muss (Löschen, ungesicherte Arbeit), bestätigt man in der Oberfläche, mit demselben Wort.
3. **Keine Zugangsdaten auf dem Bildschirm.** Die Seiten zeigen Namen, Zustand und Ausgaben, nie den Inhalt der Einstellungsdateien (nur eine Liste der Schlüsselnamen mit „gesetzt/nicht gesetzt“) und nie die Datei mit dem Schlüssel des Betreibers.
4. **Alles von außen ist Text.** Nachrichten anderer Agenten, Zeilen aus Protokollen, Namen von Dateien und Rechnern werden nur als Text eingesetzt (`textContent`), nie als HTML.
5. **Was ein Prozess der Oberfläche anlegt, gehört `viper`.** Die Oberfläche löscht und ändert nichts, was der Befehl nicht ändert.

## 3 Sicherheitsmodell (der wichtigste Teil)

Der Browser ist ein Programm, das auch fremde Seiten lädt. Ein Port auf `127.0.0.1` ist für diese erreichbar. Die Gegner sind: (a) eine fremde Webseite im Browser des Benutzers (Anfragen an `http://127.0.0.1:<port>`, auch mit DNS-Rebinding), (b) ein anderer Benutzer desselben Rechners, (c) bösartiger **Inhalt** (NATS-Nachrichten, Protokolle, Dateinamen), (d) ein versehentlich zu großer oder zu vieler Zugriff.

| Maßnahme | gegen |
|---|---|
| Der Server lauscht nur auf `127.0.0.1`, jede Anfrage braucht den Host-Kopf `127.0.0.1:<port>` oder `localhost:<port>`, sonst 403 | DNS-Rebinding, Zugriff über einen anderen Namen |
| Ein zufälliger Schlüssel (128 Bit) je Start steht in der Adresse, die `viper ui` öffnet (`/?t=…`); die erste Anfrage tauscht ihn gegen ein Cookie (`HttpOnly`, `SameSite=Strict`, Pfad `/`) und leitet auf eine Adresse ohne Schlüssel um; er gilt nur einmal. Alle weiteren Anfragen brauchen das Cookie (Vergleich in konstanter Zeit) | andere Benutzer des Rechners, Verlauf und Referer des Browsers |
| Zustandsändernde Anfragen sind `POST` mit `Content-Type: application/json`, einem eigenen Kopf (`X-Viper-UI: 1`) und einem passenden `Origin`; kein CORS, kein `Access-Control-Allow-*` | CSRF und einfache Formularanfragen fremder Seiten |
| `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`; keine fremden Ressourcen, keine Inline-Skripte | Skripteinschleusung, Einbetten in fremde Seiten |
| Befehle laufen als Programm mit **Argumentliste** (nie über eine Shell); Namen und Optionen werden mit denselben Prüfungen wie am Terminal gelesen (Rechner- und Gruppennamen aus einer Liste der vorhandenen, nicht aus freiem Text) | Befehlseinschleusung |
| Nur die Pfade der eigenen Seiten und der Programmschnittstelle (`/api/…`); keine Datei aus dem Einstellungs- oder Zustandsverzeichnis, kein Verzeichnislisten; Dateien zum Herunterladen nur über eine ID, mit `Content-Disposition: attachment` und `nosniff` | Pfadumgehung, Lesen der Zugangsdaten |
| Hochgeladene Dateien (für `--pdf`/`--image`/`--zip`): höchstens `NATS_OBJECTS_FILE_MAX`, in eine Datei mit Zufallsnamen in einem privaten Verzeichnis (0700), danach gelöscht; der Befehl prüft sie wie am Terminal | große Uploads, Namen vom Browser |
| Höchstens N gleichzeitige Verbindungen und ein Lauf je Rechner; Wartezeiten und Größen begrenzt | Erschöpfung |
| Nachrichten und Protokollzeilen kommen schon gereinigt (`natsmcp.Clean`) und werden mit `textContent` gesetzt | Skripteinschleusung über Inhalte |
| `viper ui --readonly` bietet keine Aktion an (nur Ansehen); die Aktionen sind sonst erst in Stufe 2 da | versehentliches Löschen |

**Kein TLS:** Der Verkehr bleibt auf dem Rechner. **Kein Speichern von Schlüsseln im Browser:** das Cookie ist nur ein Sitzungsschlüssel dieses Starts.

## 4 Aufbau

* **Neuer Adapter** `internal/adapter/driving/web`: `net/http`, `embed` für die Seiten (`web/static/`), JSON für die Programmschnittstelle, Server-Sent Events (`text/event-stream`) für Ausgabe und Nachrichten. Er bekommt von `cli` einen kleinen Zugang (`cli.UI`): die Liste der Rechner und Gruppen (aus den Einstellungsdateien, ohne Anbieter-Anfrage), das Zustandsverzeichnis und eine Funktion, die einen Befehl als Prozess startet. **Der Befehl `viper ui` ist ein Eintrag in der Tabelle der Befehle** (`help.go` beschreibt ihn).
* **Lesen ohne Anbieter:** `GET /api/machines` und `/api/groups` liefern, was die Dateien sagen (Name, Anbieter, Gruppe, NATS-Gruppe, Größe, Zustand der Marken `id-`, `ready-`, `group-`), ohne dass `viper` einen Anbieter fragt. Das ist schnell und kostet nichts; „läuft das wirklich?“ beantwortet `viper check` auf Wunsch.
* **Aktionen als Prozess:** Die Oberfläche startet `viper <befehl> …` als Unterprozess des **eigenen Programms** (derselbe `os.Executable()`), mit denselben Einstellungen (`--dir`), und gibt Ausgabe und Fehlerausgabe zeilenweise als Ereignisse an den Browser; Eingaben (Antworten auf Rückfragen) gehen auf die Standardeingabe des Prozesses. So gelten Sperren, Protokolle, Rückfragen und Prüfungen wie am Terminal, und es gibt keinen zweiten Weg, etwas anzulegen oder zu löschen. *Entscheidung zu bestätigen:* statt die Anwendungsfälle im Prozess aufzurufen (das ginge für Nur-Lesen-Befehle ebenso, brächte aber die Rückfragen und das Abbrechen als neue Arbeit).
* **Rückfragen:** Der Prozess schreibt die Frage ohne Zeilenende (`Type DELETE to continue: `). Die Oberfläche zeigt die bisherige Ausgabe samt Frage und ein Eingabefeld; „Abbrechen“ sendet den Abbruch (Unterprozess beenden). Ein fester Knopf „Löschen bestätigen“ gibt es nicht: der Benutzer tippt das Wort selbst.
* **NATS:** Anwesenheit und Themen laufen über `viper nats peers|topics --json`; „Mitlesen“ ist `viper nats listen --json` als langer Lauf mit Ereignissen. Senden, Rundruf und Anfrage rufen `viper nats send|broadcast|request` mit dem Text auf der Standardeingabe auf; Dateien als Upload in ein privates temporäres Verzeichnis und `--pdf|--image|--zip PFAD`; `fetch` legt die Datei in ein privates Verzeichnis und bietet sie zum Herunterladen an. Alles ohne neue Fähigkeit der NATS-Seite.
* **Abbrechen:** Ein Lauf endet mit dem Schließen der Verbindung oder dem Knopf: unter Linux und macOS ein Signal an die Prozessgruppe (SIGINT, danach SIGKILL nach einer Frist), unter Windows das Beenden des Prozesses (ohne Aufräumen: das steht in der Oberfläche).

## 5 Die Seiten (Stufe 1 bis 4)

1. **Übersicht:** Rechner und Gruppen als Tabelle; je Rechner „Zustand prüfen“ (`viper check`, die Ausgabe auf einer Seite, das Ergebnis als Anzeige „ok/Probleme“), „DNS“, „Protokoll“.
2. **Protokolle:** Ereignisse aller Rechner, eines Rechners, die vollständige Ausgabe (`viper log`), mit Suchen im Text.
3. **Aktionen (Stufe 2):** `preflight`, `up`, `down` (je Rechner und `--group`), mit der Ausgabe live, der Rückfrage, Abbrechen; eine Shell gibt es **nicht** (`viper ssh` zeigt den Befehl zum Kopieren).
4. **NATS (Stufe 3):** Gruppe wählen; Anwesenheit (`peers`), Themen, Mitlesen (SSE), Senden, Rundruf, Anfrage, Datei senden und holen; die Zeile mit dem Betreiber-Schlüssel („als Betreiber“ oder „als Maschine X“) steht immer da.
5. **Einstellungen (nur lesend, Stufe 4):** Die Schlüsselnamen aus `.env.local`, der Gruppe und der Rechnerdatei mit „gesetzt“ und dem Ort; Werte nur bei Schlüsseln ohne Zugangsdaten (eine feste Liste nicht geheimer Namen, alles andere „gesetzt“).

Sprache der Oberfläche: Englisch wie die Hilfe von `viper` und die Meldungen; ein Umschalten auf Deutsch ist Sache einer späteren Stufe (Texte in einer Datei, damit es geht).

## 6 Meilensteine

0. **Prüfen (nichts bauen):** (a) Reicht ein Unterprozess für die Rückfragen (wie fragen `down` und `down --group` heute, liest jede Rückfrage von `Host.Stdin`, hängt etwas an einem Terminal-Test?). (b) Wie viel von `viper check`, `viper log` und den Rechnerlisten lässt sich ohne Anbieter ablesen. (c) Ein Versuch mit `net/http` unter dem Sicherheitsmodell: Host-Kopf, Cookie, `Origin`, CSP, Umleitung nach dem Tausch; mit einem Test durch `httptest` und einem echten Browser von Hand (Firefox und Chrome): fremde Seite, `fetch` an den Port, DNS-Rebinding mit einem Namen auf `127.0.0.1`. (d) Windows: Browser öffnen, Prozess beenden.
1. **Gerüst und Nur-Lesen:** `viper ui` (`--port`, `--no-open`, `--readonly`), Server, Sicherheit aus §3 mit Tests, die Seite „Übersicht“ und „Protokolle“, `viper check` auf Wunsch. Hilfe, Handbuch, README.
2. **Aktionen:** Läufe als Prozess mit Ausgabe, Rückfragen, Abbrechen, ein Lauf je Rechner; `preflight`, `up`, `down`.
3. **NATS:** Anwesenheit, Themen, Mitlesen, Senden, Dateien; mit dem Betreiber-Schlüssel und als Maschine.
4. **Einstellungen (lesend), Deutsch, Feinschliff:** Zugänglichkeit (Tastatur, Kontrast, Screenreader-Namen), Größe der Seiten, Fehlerseiten.
5. **Handbuch und Läufe:** Kapitel „Oberfläche im Browser“, `developer.md`, Läufe mit Firefox und Chrome auf Linux, macOS und Windows (so weit erreichbar), gegen echte Docker-Maschinen (Compose), k3d und, mit Erlaubnis, Hetzner.

## 7 Teststrategie

* `httptest` für jede Sicherheitsregel aus §3 (falscher Host, kein Cookie, falscher Ursprung, falsche Methode, falscher `Content-Type`, zu große Hochladung, unbekannter Pfad, Pfadumgehung `..`, Verzeichnisliste), mit der Sicherung, dass **keine** Antwort einen Wert aus den Einstellungsdateien enthält (ein Test legt Zugangsdaten in die Dateien und sucht in allen Antworten).
* Ein gefälschter Unterprozess (über `Host.StreamTool` oder eine eigene Funktion) für Ausgabe, Rückfrage, Abbrechen und Parallelität.
* Eine Prüfung der Seiten ohne Browser: kein `eval`, kein `innerHTML`, keine fremde Adresse (ein Test sucht in den eingebetteten Dateien).
* Von Hand: ein echter Browser, eine fremde Seite, die versucht, den Port anzusprechen.

## 8 Nicht Teil dieses Plans

* Zugriff von anderen Rechnern, Benutzer und Rechte, TLS, ein Dienst im Hintergrund.
* Ein Terminal im Browser (`viper ssh`); die Einstellungsdateien bearbeiten; neue Rechner oder Gruppen anlegen (Dateien schreiben).
* Eine Oberfläche für `viper find` (später, lesend und ungefährlich).

## 9 Offene Fragen und Risiken

* **Unterprozess oder Anwendungsfälle im Prozess:** Der Plan nimmt den Unterprozess (§4). Nachteile: Ausgabe ist Text (Anzeigen wie „ok/Probleme“ lesen die letzte Zeile), Rückfragen brauchen die Eingabe-Zeile.
* **Ausgabe von `viper check` und `viper log` als Text:** Bleibt Text. Soll dafür später ein `--json` entstehen (`check` und `log`), damit die Seiten nicht Text zerlegen?
* **Zwei Fenster, zwei Läufe:** `viper ui` und ein Terminal können gleichzeitig `viper up` für denselben Rechner starten. Die Sperren gelten wie bisher; die Oberfläche zeigt „läuft schon“, wenn ihr eigener Lauf dasselbe tut.
* **Der Schlüssel in der Adresse:** Er steht kurz im Prozess `xdg-open`/`open`/`start` und im Verlauf des Browsers, solange die Umleitung nicht passiert ist; er gilt nur einmal.
* **Zugangsdaten in Ausgaben:** Die Ausgaben von `viper` sind bereinigt (Tokens werden nicht gedruckt); ein Test sucht danach.
* **Windows:** Das Beenden von Unterprozessen ohne Aufräumen ist ein Rückschritt gegenüber Strg-C; die Oberfläche warnt davor.
* **Wartung:** Zwei Oberflächen (Terminal und Browser) für dieselben Befehle: Neue Optionen eines Befehls müssen in den Formularen auftauchen; ein Test wie bei der Hilfe (jede Option des Befehls ist beschrieben) kann sie mit den Formularen vergleichen.
