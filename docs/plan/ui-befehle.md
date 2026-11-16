# Plan: fehlende Befehle in der Oberfläche (`viper ui`)

**Stand:** Entwurf (16.11.2026), einmal unabhängig gegen den Code und das gebaute Programm geprüft (§9), dessen Funde eingearbeitet sind. Gehört zu [`ui.md`](ui.md) (Funktion, Sicherheit, Aufbau) und [`ui-gestaltung.md`](ui-gestaltung.md) (Gestalt). Stufe 1 und 2 sind entschieden (Benutzer, 16.11.2026); Stufe 3 bis 5 sind Vorschläge. *[V]* heißt: im Code gelesen; *[?]* ist eine Vermutung, die vor dem Bauen zu prüfen ist.

**Ziel:** Befehle von `viper`, die in der Oberfläche fehlen, dort anbieten, **ohne** einen zweiten Weg zu schaffen: Was die Oberfläche tut, tut sie als Unterprozess `viper <befehl>` (`ui.md` §4), mit denselben Prüfungen, Rückfragen und Protokollen wie am Terminal.

## 1 Ausgangslage

*[V]* Die Oberfläche deckt heute ab: `preflight`, `up`, `down` (Rechner und Gruppe), `check` (Rechner in der Tabelle, Gruppe als Lauf), `log`, `nats` (alle Unterbefehle), `version` (Kopfzeile). Es fehlen:

| Befehl | Was er tut | Stufe |
|---|---|---|
| `find` | günstigste Server-Typen nach CPU, RAM, Platte, Anbieter | **1** |
| Optionen `--parallel`, `--force`, `--delete-registry`, `--keep-registry` | an `preflight`, `up`, `down`, `check` | **2** |
| `setup` | einmalig `.env.local`, Login-Schlüssel, Host-CA | 3 (Vorschlag) |
| `image build` | Image der Rechner für `compose` und `kubernetes` bauen | 4 (Vorschlag) |
| `ssh --cmd` | einen Befehl auf einem Rechner oder einer Gruppe ausführen | 5 (Vorschlag, heikel) |
| `vscode`, `dns` | VS Code öffnen, DNS-Namen nennen | später, geringer Nutzen |
| `ssh` (Shell, `--tmux`), `k8s-proxy`, `docker-proxy`, `mcp-binary` | brauchen ein Terminal oder sind Hilfsbefehle | **nie** |

## 2 Grundregeln

1. **Nur Felder, die der Server prüft.** Jede Eingabe ist eine Zahl, eine Auswahl aus einer festen Liste oder ein Name nach einem engen Muster; nie freier Text in der Befehlszeile. Optionen stehen in der Form `--name=wert` (ein Wert kann nie als Option gelten, wie bei `--repo=`).
2. **Eine Option gilt nur, wo der Befehl sie kennt.** `--parallel` nur für Gruppenläufe (1 bis 64, wie `viper`), `--force` und die Wahl der Registry nur für `down`. Der Server lehnt alles andere mit 400 und dem Grund ab; er schiebt eine Option nicht still auf einen anderen Befehl.
3. **Was die Oberfläche startet, muss das echte Programm annehmen.** Der Fehler von v0.26.0 (`up --group … --no-code` wurde vom Server der Oberfläche angehängt und vom Programm abgelehnt) entstand, weil die Tests nur einen Fake-Starter benutzten. Darum gehört zu **jeder** neuen Befehlszeile ein Test, der das **echte** `viper` mit ihr startet (mit Fake-Docker und leerem Einstellungsverzeichnis) und prüft, dass die Option **angenommen** wird (die Ausgabe nennt einen Grund, der nicht die Option ist).
4. **Lesende Befehle gehen auch mit `--readonly`:** `check` einer Gruppe (gemacht) und `find`. Was schreibt oder einen Server anlegt oder löscht, bleibt gesperrt.
5. **Läufe ohne Rechner haben ein eigenes Budget.** `find` (und `nats`) halten keinen Rechner und zählen nicht zu den vier Aktionsläufen; sie gehen auch weiter, wenn Einstellungen geändert werden (`beginEdit` sperrt nur Aktionen). Die Hilfsfunktion `isSide(action)` ersetzt die Prüfung `Action == "nats"`.
6. **Zugänglichkeit und Sprache wie überall:** jede neue Seite zwei Sprachen (Paritätstest), axe in Hell und Dunkel, Tastatur, `role=status` für Meldungen, `aria-live="off"` für lange Ausgaben.

## 3 Stufe 1: die Seite „Finden“ (`viper find`)

**Befehl** *[V]*: `viper find [--cpu N] [--ram GB] [--disk GB] [--arch x86|arm] [--provider P] [--location L] [--vat PERCENT]`; mindestens eines von `--cpu`, `--ram`, `--disk`; `--location` braucht `--provider`; braucht keinen Rechner, aber **eine `.env.local`** im Einstellungsverzeichnis (ohne sie: „No .env.local: run viper setup“, Exit 1). *[V]* Es ist **nicht rein lesend**: Es legt Preislisten als Dateien im Cache-Verzeichnis des Benutzers ab (`os.UserCacheDir()/viper`: `aws-ec2-*.json`, `azure-*.json`, `gcp-*.json`, `hetzner-web.json`, `ecb-rates.json`), und Azure und GCP fragen mit den vorhandenen Zugangsdaten der Cloud ab (`AZURE_SUBSCRIPTION_ID`, `GCP_PROJECT` aus der `.env.local`); die Ausgabe hat kein Token gezeigt. Das erste Mal lädt es große Preislisten (Meldungen „reading the price list … a large file“ auf stderr). Exit-Status: 0 Treffer, **1 kein Treffer oder ein Fehler (etwa die fehlende `.env.local`)**; die Seite zeigt beides neutral und verweist auf die Ausgabe, 2 Fehler der Befehlszeile, 130 abgebrochen. Die Ausgabe ist eine Tabelle in Festbreite mit `€` und `*`, einer Zeile `PROVIDER=… SERVER_TYPE=…` und Hinweisen; **englisch auch in der deutschen Oberfläche**; stdout und stderr kommen in einem Strom.

**Seite:** ein neuer Reiter „Finden“ (immer sichtbar, auch mit `--readonly`):

* **Formular:** CPUs (ganze Zahl 1 bis 4096), RAM in GB (Zahl bis 65536, auch 0,5), Platte in GB (ganze Zahl bis 100000), Architektur (alle, x86, arm), Anbieter (alle, hetzner, aws, azure, gcp), Standort (Text nach `[a-z0-9-]`, höchstens 41 Zeichen; nur mit Anbieter), Mehrwertsteuer in Prozent (0 bis 100; leer: der Satz des Projekts, sonst keine). Die Seite meldet fehlende Pflichtangaben selbst; der Server prüft alles noch einmal.
* **Ablauf:** „Suchen“ startet `viper find …` als Lauf (`POST /api/run`, Aktion `find`). Die Ausgabe erscheint in einem **eigenen Bereich dieser Seite** (`pre`, `role=region`, Festbreite, die Tabelle von `viper find` bleibt lesbar), nicht im Terminal der Läufe; der Lauf steht zusätzlich in der Liste der Läufe. „Abbrechen“ schickt Strg-C. Die Seite bleibt auf „Finden“ und merkt sich die Eingaben (bis zum Neuladen).
* **Wiederaufnahme:** Läuft beim Öffnen der Seite ein `find`-Lauf, hängt sich der Bereich daran (wie das Mitlesen in NATS).
* **Wartezeit:** Die Preise kommen aus dem Netz (Sekunden, beim ersten Mal bei AWS und GCP länger). Die Seite sagt „Suche läuft …“ (`role=status`) und zeigt die Ausgabe, sobald sie kommt. **Exit 1 ist „kein Treffer oder ein Anbieter ohne Preisliste“**, nicht „Fehler“: die Seite sagt es neutral („Die Suche hat nichts gefunden oder einen Anbieter übersprungen: siehe die Ausgabe“). Nach dem Ende geht der Fokus auf den Bereich der Ausgabe.
* **Eingabe:** Der Standort steht in Kleinbuchstaben (`[a-z0-9-]`, nicht mit `-` beginnend, höchstens 41 Zeichen; die echten Namen `fsn1`, `eu-central-1b`, `germanywestcentral`, `europe-west3-b` passen); ein Großbuchstabe ist am Terminal kein Fehler, sondern „kein Typ passt“: die Seite schreibt klein oder weist darauf hin. Der Anbieter ist hetzner, aws, azure oder gcp (`compose` und `kubernetes` haben keine Preisliste). Die Mehrwertsteuer leer: **der Satz, den ein Anbieter meldet (Hetzner), sonst 0**.
* **Ausgabe:** Sie geht wie jede Ausgabe durch Reinigung und Schwärzung; die Seite setzt sie als Text (`textContent`).

**Server** *[V, entworfen]*: `runRequest` bekommt `cpu`, `ram`, `disk`, `arch`, `provider`, `location`, `vat`; `findArgv` baut die Befehlszeile (feste Wörter und Zahlen, §2.1); `repo` ist für `find` nicht erlaubt; `isAction` kennt `find`; `isSide` zählt es zu den Läufen ohne Rechner (§2.5: `beginEdit` und die Sperre `editing` betreffen nur Aktionen; kein `forgetOverview` am Ende). **Eigenes Budget: höchstens eine Suche zugleich** (`maxFinds`), getrennt vom Budget der NATS-Läufe (vier): jede Suche liest vier Preislisten und teilt die Cache-Dateien. `startRun` lässt `find` bei `--readonly` zu (die Knöpfe der Seite sind dort nicht aus); die Abfrage alle zwei Sekunden gilt nur, solange ein **Aktionslauf** geht (nicht für `find` oder `nats`). **Schreibt `find` bei `--readonly`?** Ja: Preislisten im Cache-Verzeichnis (siehe oben); das Handbuch sagt es, `--readonly` meint „nichts an Einstellungen und Rechnern“.

**Nicht Teil der Stufe:** „Typ und Standort in ein Formular übernehmen“ (Anlegen). Es setzt voraus, dass die Ausgabe maschinenlesbar ist (`viper find --json` gibt es nicht): ein späterer Schritt, zuerst am Befehl.

## 4 Stufe 2: Optionen für Läufe

* **Ort in der Oberfläche:** unter dem Feld „Repository-URL“ der Übersicht ein aufklappbarer Block **„Optionen für Läufe“** (`<details>`, zu): 
  * **Gleichzeitig (Gruppen):** Zahl 1 bis 64, leer: der Standard von `viper` (8). Gilt für Gruppenläufe von Vorprüfung, Up, Down und Prüfen (am Terminal nimmt auch ein einzelner Rechner die Option und ignoriert sie; die Oberfläche schickt sie nur für Gruppen).
  * **Down erzwingen (`--force`):** Kontrollkästchen. *[V, korrigiert nach der Prüfung]* `--force` überspringt die Prüfung auf ungesicherte Arbeit **nur, wenn die SSH-Verbindung zum Server scheitert**; ist der Server erreichbar, läuft die Prüfung mit oder ohne `--force` (`stop.go`). Außerdem löscht `--force` einen NATS-Server, der noch Peers hat, und löscht, auch wenn das Widerrufen des Schlüssels scheitert. Die Beschriftung sagt darum: „Auch löschen, wenn der Rechner nicht erreichbar ist oder noch Mitglieder hat (`--force`)“, mit dem Hinweis, dass es bei einem erreichbaren Rechner kaum etwas ändert. **Es gilt für die eine Anfrage**: Die Seite liest das Kästchen im Augenblick des Klicks auf „Down“ und schaltet es sofort wieder aus (derselbe Zug des Browsers, damit zwei Klicks oder zwei Reiter es nicht doppelt nutzen).
  * **Registry bei Down (k3d):** Auswahl „fragen“ (Standard), „löschen“ (`--delete-registry`), „behalten“ (`--keep-registry`).
* **Sichtbarkeit:** Der Befehl, den die Oberfläche startet, steht in der ersten Zeile des Laufs (`$ viper down web --force`): Was die Option bewirkt hat, ist im Protokoll zu sehen.
* **Bestätigung:** `viper down` fragt weiter selbst, in dieser Reihenfolge: `DISCARD` (nur, wenn die Prüfung auf ungesicherte Arbeit lief), „Type DELETE to continue“, danach (nur beim letzten Rechner eines k3d-Clusters) die Frage nach der Registry, die `--delete-registry` oder `--keep-registry` ersetzt (beide zusammen sind ein Fehler: Exit 2). Bei einer Gruppe fragt jeder Rechner einzeln (Zeilen mit `[name]`), die Registry einmal. `--force` ändert die Fragen nicht. Eine zusätzliche Bestätigung in der Oberfläche gibt es nicht: der Kasten warnt, die Zeile im Lauf zeigt es, und die Rückfrage von `viper` bleibt.
* **Server** *[V, entworfen]*: `machineOptions` baut `--parallel=N`, `--force`, `--delete-registry` oder `--keep-registry` und lehnt sie an anderer Stelle ab (§2.2).
* **Nicht Teil der Stufe:** `--cmd`, `--tmux` (Stufe 5 und nie).

## 5 Stufen 3 bis 5 (Vorschläge, einzeln zu entscheiden)

* **Stufe 3: `setup`** (**zurückgestellt**, Benutzer, 16.11.2026: schreibt außerhalb des Einstellungsverzeichnisses; erst ein eigener Plan mit Bestätigungswort und Prüfung). Legt `.env.local`, den Login-Schlüssel und die Host-CA an; ohne beides scheitert die Vorprüfung. Als Lauf mit Rückfragen (die Oberfläche kann Rückfragen); ein Knopf auf der Seite „Anlegen“ („Schlüssel und Host-CA anlegen“) und ein Hinweis in der Vorprüfung, wenn sie fehlen. *[V, geprüft]* `viper setup` ist **nicht interaktiv** (keine Fragen): Es schreibt `.env.local`, die Beispiele in `groups/` und `machines/` in `--dir`, legt Schlüssel und Host-CA in `~/.ssh` an, **überschreibt `~/.ssh/known_hosts_hetzner_claude_ca`** und druckt `export`-Zeilen und den öffentlichen Schlüssel der CA. Es bleibt bei `--readonly` gesperrt; ob es mit `--edit` angeboten wird, ist zu entscheiden (es schreibt außerhalb des Einstellungsverzeichnisses).
* **Stufe 4: `image build`** (gebaut: eigene Seite „Image“ mit Name und k3d-Cluster, ein Bau zugleich, nicht mit `--readonly`; kein Knopf je Rechner). Für `compose` und `kubernetes`: ein langer Lauf mit Ausgabe (`docker build`, braucht den Docker-Socket); Felder `--tag=NAME:TAG` und `--k3d=CLUSTER` in der Form `--name=wert`, mit engen Mustern. Ein Knopf je Rechner mit solchem Anbieter und auf der Seite „Anlegen“.
* **Stufe 5: `ssh --cmd`** (gebaut nach §10: Knopf „VS Code“ und Seite „Befehl"; die Ausgabe steht auf der Seite „Läufe", nicht in einem eigenen Bereich). **Empfehlung der Prüfung: nicht bauen.** Ein freier Befehlstext widerspricht §2.1, die Oberfläche hält die Standardeingabe des Prozesses offen (ein Befehl, der von ihr liest, hängt), und der Nutzen ist klein. Wer es will: nur mit `--edit`, `--cmd=…` mit getipptem Wort und der Zeile im Protokoll.
* **Später/nie:** `vscode` (Knopf je Rechner, kleiner Nutzen), `dns` (besser als Spalte „Adresse“ in der Übersicht), Shell, Proxys und `mcp-binary` nie.

## 6 Tests

* **Server (Go):** Tabelle der Anfragen gegen die erwartete Befehlszeile; alle ungültigen (Grenzen, Muster, Optionen am falschen Befehl, `find` ohne Größe, Standort ohne Anbieter, `--` im Standort, NaN); `find` und Läufe ohne Rechner haben ein eigenes Budget und gehen neben vier Aktionen; `--readonly` lässt nur `check` und `find` zu.
* **Grenzen des Befehls (Fake-`ssh`, der jedes Argument einzeln ausgibt):** `-oProxyCommand=id`, ` -x`, `--x`, `-F` werden von Server **und** Programm abgelehnt; `echo "a b"; $X ü` und 4096 Byte kommen als **ein** Argument an; die Standardeingabe ist geschlossen (`timeout 1 cat` sieht EOF); `answer` hängt nicht. `runs_test.go:335` (ssh in der Tabelle der abgelehnten Aktionen) wird angepasst; `runRequest` bekommt `cmd` und `confirm` (`DisallowUnknownFields`).
* **Echtes Programm (§2.3):** Browser-Test mit dem echten `viper` und Fake-Docker: Up einer Gruppe mit `--parallel=2`, Down mit `--force` und `--keep-registry` (Fake-k3d fehlt: es genügt, dass die Option angenommen wird), `find` mit allen Feldern **und einer `.env.local`** im Verzeichnis (ohne sie bricht es mit „No .env.local“ ab; im Test mit Netz oder ohne endet es mit Preisen oder einer Meldung über die Preisquellen, nicht über eine Option; der Test macht keine Annahme über das Netz).
* **Server:** `beginEdit` und `hold` für `find` (blockiert das Ändern von Zugangsdaten nicht; eine zweite Suche wird mit 503 abgelehnt; vier NATS-Läufe daneben), die Regel für `--readonly`.
* **Browser:** Formular und Pflichtangaben, Bereich der Ausgabe, Abbrechen, Wiederaufnahme nach Neuladen, `--force` wird nach dem Start wieder ausgeschaltet, `--parallel` nur bei Gruppenknöpfen, axe (hell, dunkel, Englisch, Deutsch), Tastatur.
* **Handbuch:** Abschnitt „Passenden Server-Typ suchen“ nennt die Seite; die Optionen stehen im Kapitel der Oberfläche; die Zeile in Kapitel 11.

## 7 Meilensteine

1. **Server** (gemacht): `runRequest`, `findArgv`, `machineOptions`, `isSide`, Tests.
2. **Seite „Finden“** (gemacht, 156,1 KB der Seiten): Reiter, Formular, Bereich, Wiederaufnahme, Texte, Tests, Handbuch.
3. **Optionen für Läufe** (gemacht): Block in der Übersicht, `startRun` fügt sie an, `--force` einmalig, Tests mit dem echten Programm, Handbuch.
4. Nach jeder Stufe: unabhängige Prüfung, dann Release.
5. Danach Entscheidung über Stufe 3 bis 5.

## 8 Offen (zu entscheiden)

1. **`--force` ohne weitere Bestätigung** (Vorschlag: ja, die Rückfrage von `viper down` bleibt) oder mit einem getippten Wort in der Oberfläche? *Gebaut nach dem Vorschlag (ohne Wort); vom Benutzer nicht ausdrücklich bestätigt.*
2. **Ausgabe von `find` als Text** (Vorschlag) oder zuerst `viper find --json` und dann eine Tabelle der Seite (mehr Arbeit, aber „übernehmen“ möglich)? *Gebaut: Text.*
3. **Ort der Optionen:** ein Block in der Übersicht (Vorschlag) oder je Knopf ein kleines Menü? *Zuerst ein Block unter der Tabelle (zu weit unten, nicht zu finden); jetzt ein Menü-Knopf rechts in der Filterzeile (Benutzer, 16.11.2026).*
4. **`--parallel` bei „Prüfen“ der Gruppe:** gleicher Wert wie bei den anderen (Vorschlag). *Gebaut so.*
5. **Reihenfolge der Stufen 3 bis 5** nach Stufe 2: Stufe 4 gebaut, Stufe 3 zurückgestellt, Stufe 5 nicht gebaut (Empfehlung der Prüfung).

## 9 Prüfung

Ein unabhängiger Prüfer hat den Plan gegen den Code und das gebaute Programm gelesen. Die Funde und was daraus wurde:

| Fund | Folge |
|---|---|
| `--force` ist falsch beschrieben: es überspringt die Prüfung auf ungesicherte Arbeit nur, wenn SSH scheitert; es löscht auch NATS-Server mit Peers und trotz gescheitertem Widerruf | §4: neue Beschriftung und Hinweis, Reihenfolge der Fragen |
| `find` braucht eine `.env.local` und hat sonst keine Einstellung | §3, §6: der Test legt eine an |
| `find` schreibt Dateien in den Cache und nutzt Zugangsdaten der Cloud (Azure, GCP) | §3: gesagt, `--readonly` meint Einstellungen und Rechner |
| `beginEdit` und `hold` blockieren für `find` | §2.5, Code: `isSide`; Test |
| Ein Budget für `find` und `nats` ist falsch (vier lange Mitleser blockieren die Suche; mehrere Suchen teilen den Cache) | eigenes Budget: eine Suche zugleich |
| Exit 1 heißt „kein Treffer“, nicht Fehler; Ausgabe englisch, stdout und stderr gemischt | §3: neutraler Satz, Hinweis |
| Die Mehrwertsteuer ohne Angabe ist der Satz eines Anbieters (Hetzner), sonst 0, nicht „des Projekts“ | §3 berichtigt |
| Einmaliges `--force` nur per Zustand der Seite ist anfällig (zwei Reiter, Doppelklick) | §4: pro Anfrage, sofort zurückgesetzt |
| `setup` ist nicht interaktiv, überschreibt `known_hosts_hetzner_claude_ca` | §5: berichtigt |
| `ssh --cmd` widerspricht der Regel „nur geprüfte Felder“, hält die Standardeingabe offen | §5: nicht bauen |
| Die Seiten sind 156 KB groß bei 175 KB; Stufe 3 bis 5 passen nicht mehr hinein (gemildert: die Texte laden je Sprache, der Start lädt jetzt etwa 143 KB; weiter bei Bedarf: `app.js` nach Seiten teilen) | Stufe 1 und 2 brauchen etwa 10 bis 15 KB; die Grenze wird vor Stufe 3 bewusst entschieden |
| Die Abfrage alle zwei Sekunden läuft auch für `find` | §3: nur für Aktionsläufe |
| Wiederaufnahme nach dem Neuladen nicht beschrieben; doppelte Bereiche | §3: über `/api/runs`, ein Bereich |

**Antworten zu §8:** (1) die Rückfrage von `viper down` bleibt ohne weiteres Wort, die Beschriftung ist berichtigt; (2) zuerst Text, `--json` ist ein eigener Schritt am Befehl; (3) ein Block mit `--force` pro Anfrage; (4) gleicher Wert; (5) `setup` zuerst, dann `image build`; `ssh --cmd` nicht.

## 10 Stufe 5: `vscode` und `ssh --cmd` (Entwurf, vom Benutzer gewünscht, 16.11.2026)

Der Benutzer will beide Befehle in der Oberfläche. Die frühere Empfehlung (§5: `ssh --cmd` nicht bauen) bleibt als Warnung; dieser Abschnitt sagt, wie es gebaut würde, damit die Grenzen halten. *[V]* heißt im Code gelesen.

### 10.1 `viper vscode NAME [--in-group G]`

*[V]* Der Befehl lädt die Einstellungen des Rechners, verlangt die SSH-Konfiguration (`viper up` hat sie angelegt; sonst „No SSH config … run viper up first“), fragt den Rechner per `ssh` nach dem Arbeitsverzeichnis (`cat ~/.config/viper/workdir`, `ConnectTimeout=5`) und startet `code` mit Remote-SSH auf **diesem Computer** (`LaunchVSCode`). Er schreibt selbst nichts (auch nicht in `~/.ssh/config`: der Alias löst dank des `Include` auf, den `viper up` angelegt hat), öffnet aber ein Programm auf dem Desktop, und **VS Code installiert und startet auf dem Rechner seinen Server und Erweiterungen**. `LaunchVSCode` hat keine Zeitgrenze, und die Fehler von `code` außer „code not found“ bleiben stumm (nur „exit status N"); ob `Interrupt` ein wartendes `code` beendet, ist zu prüfen *[?]*.
* **Knopf „VS Code“ in der Zeile** eines Rechners, nur bei Zustand „gestartet“ (die Wörter von §Zustand), neben „Down“. Befehlszeile `vscode NAME [--in-group G]` (Namen aus der Übersicht, wie bei `up`).
* **Lauf der Seite (`isSide`)**, eigenes Budget „höchstens ein Start zugleich“ (der Befehl endet, sobald `code` gestartet ist); **hält seinen Rechner** (ein `down` desselben Rechners bekommt währenddessen 409; so gebaut, weil der Lauf den Namen aus der Übersicht trägt). `maxFinds` heißt dafür allgemein `maxOne`. Die Ausgabe steht im Lauf („No SSH config …“, „code not found“ …).
* **`--readonly`: nicht angeboten** (Knopf aus, Server 403): Es startet ein Programm, auch wenn es nichts ändert. *[?]* Zu entscheiden.
* Läuft `viper ui` auf einem Computer ohne Desktop (etwa per SSH), scheitert der Lauf mit der Meldung des Befehls; die Seite sagt es im Handbuch, prüft es nicht vorher.

### 10.2 `viper ssh NAME --cmd=BEFEHL` und `viper ssh --group G --cmd=BEFEHL`

*[V]* Ein Rechner: `ssh [-t] -F CFG ALIAS BEFEHL` **ersetzt** `viper` (`ExecTool`, nur Unix; unter Windows läuft `ssh` als Kind), **ohne** `BatchMode` und ohne Zeitgrenze (ein Rechner, der nicht antwortet, hält den Lauf bis zum TCP-Timeout); Exit-Status ist der von `ssh` (255 bei Verbindungsfehler, sonst der des Befehls). Die Oberfläche hält die Standardeingabe offen (ein Befehl, der liest, hängt). Eine Gruppe: `sshOnce` je Rechner mit `-n -o BatchMode=yes`, Ausgabe mit `[name]`-Zeilen, Tabelle am Ende, `--parallel` gilt; der Exit-Status ist nur 0, 1 (mindestens ein Rechner fehlgeschlagen) oder 130, der des Befehls geht verloren. `--tmux` und die Shell bleiben **nie** (brauchen ein Terminal).
* **Freier Befehlstext widerspricht §2.1** (nur geprüfte Felder). Der Ausweg, der die Regel nicht aufweicht: eine eigene, **ausdrückliche Ausnahme** mit diesen Grenzen:
  1. **Nur mit `--edit`** (dem Schalter für „darf etwas ändern“); mit `--readonly` und ohne `--edit` lehnt der Server mit 403 ab. Der Befehl läuft als der Benutzer auf dem Rechner, mit allem, was er dort darf (wie `VIPER_POST_CLONE`, das die Seite „Ändern“ schon schreibt).
  2. **Das Wort `run` tippen** (wie bei den gefährlichen Werten), zusammen mit dem Namen des Rechners oder der Gruppe; die Seite zeigt vorher den Befehl wörtlich.
  3. Der Befehl wird **nie** Teil einer Shell-Zeile von `viper ui`: Er geht als ein einzelnes Argument `--cmd=BEFEHL` an `viper`, das ihn an `ssh` weitergibt. Grenzen: höchstens 4096 Byte, gültiges UTF-8, **eine Zeile** (kein Zeilenumbruch, kein Steuerzeichen, kein NUL). **Kein führender Bindestrich** (auch nicht nach Leerraum): `ssh` liest Optionen auch **hinter** dem Host, `-oProxyCommand=…` oder `-oLocalCommand=…` liefe lokal als Benutzer von `viper ui` (belegt mit dem echten `ssh`). Server **und** Programm lehnen so einen Wert ab; das Programm setzt zusätzlich `--` vor den Befehl (`ssh … ALIAS -- BEFEHL`; ob `--` mit dem Remote-Befehl überall wirkt, ist zu prüfen *[?]*, darum bleibt die Ablehnung die eigentliche Grenze). Ein Befehl, der mit `-` beginnen soll, wird mit einem Wort davor geschrieben (`exec -- …` oder `sh -c '…'`).
  4. Die erste Zeile des Laufs zeigt den Befehl (`$ viper ssh web --cmd=…`). Die Schwärzung kennt nur Werte aus den Einstellungen dieses Computers: **ein getipptes Geheimnis und Werte, die nur auf dem Rechner liegen (`env`, `cat .env.local`), werden nicht erkannt**; der Befehl steht auch in `ps` und `/proc/*/cmdline` von `viper` und `ssh`, und der Lauf bleibt bis zu 1 MiB im Speicher. Die Seite sagt es („Keine Geheimnisse in den Befehl schreiben“). Das Ereignisprotokoll bekommt einen **Spureintrag** mit Rechner und **Länge**, nicht mit dem Text; `viper ssh` schreibt heute nichts in `events.log`, darum ein neuer Haken `Trace func(string)` in `web.Config` (gebunden an `uiEditor.trace`), den `startRun` vor `Spawn` ruft.
  5. **Standardeingabe des Laufs wird nach dem Start geschlossen** (der Prozess sieht sofort EOF, belegt mit einem Fake-`ssh` auch nach dem Austausch durch `ExecTool`): kein Hängen. Dafür braucht `web.Proc` eine Methode `CloseInput()` (alle Test-Procs mit); `answer` bietet für diesen Lauf **keine Rückfrage** an (kein `claimQuestion`), sonst stünde nach einer offenen letzten Zeile (`printf x; sleep 2`, 600 ms) ein Antwortfeld, dessen Antwort scheitert. Gilt nur für diesen Befehl.
  6. Kein Zeitlimit: ein langer Befehl (`sleep 3600`, `tail -f`) hält einen der vier Plätze und sperrt das Ändern der Zugangsdaten, bis er abgebrochen ist; die Seite sagt es und rät zu `timeout 60 …` auf dem Rechner.
  7. Aktionslauf der Rechner: **hält den Rechner** (ein Lauf je Rechner, vier zugleich; bei einer Gruppe alle Rechner der Gruppe), damit kein `down` darunter läuft; gesperrt, während Einstellungen geändert werden (`beginEdit`).
* **Das Wort `run` und `--edit` sind eine Bremse gegen Fehlklicks, keine Sicherheitsgrenze:** Die Grenze ist der Schlüssel (Cookie `SameSite=Strict`, POST-Wache, CSP). Der Server prüft das Wort trotzdem (wie `confirm` im Editor). `--edit` heißt heute „Einstellungsdateien anlegen und ändern“; die Bindung des Befehls daran wird im Handbuch bei `--edit` genannt („und Befehle auf Rechnern ausführen“). Auch ein Rechner mit `compose` oder `kubernetes` ist erreichbar (über den Docker- oder Kubernetes-Zugang des Betreibers).
* **Seite „Befehl“** (Reiter nur mit `--edit`; Beschriftungen in zwei Sprachen, `role=status`, Ausgabe `aria-live="off"`, das Feld für das Wort beschriftet): Auswahl Rechner oder Gruppe, Textfeld einzeilig, Feld für das Wort, Knopf „Ausführen“, Bereich der Ausgabe wie bei „Finden“ (gemeinsame Funktion `sideArea`), Abbrechen. Es gibt **keinen Verlauf** und keine gespeicherten Befehle.
* **Nicht gebaut:** `--tmux`, die Shell, Eingaben während des Laufs, mehrzeilige Befehle.

### 10.3 Tests und Größe

* **Grenzen des Befehls (Fake-`ssh`, der jedes Argument einzeln ausgibt):** `-oProxyCommand=id`, ` -x`, `--x`, `-F` werden von Server **und** Programm abgelehnt; `echo "a b"; $X ü` und 4096 Byte kommen als **ein** Argument an; die Standardeingabe ist geschlossen (`timeout 1 cat` sieht EOF); `answer` hängt nicht. `runs_test.go:335` (ssh in der Tabelle der abgelehnten Aktionen) wird angepasst; `runRequest` bekommt `cmd` und `confirm` (`DisallowUnknownFields`).
* **Echtes Programm (§2.3):** `viper vscode web` mit Fake-`ssh` und Fake-`code` im `PATH` (der Fixture hat schon ein Fake-Docker); `viper ssh web --cmd=…` und `--group … --cmd=…` mit Fake-`ssh`, die angenommene Befehlszeile und die EOF-Eigenschaft. Ein Befehl mit Anführungszeichen, `$`, `;`, `--x`, Umlauten und 4096 Byte kommt unverändert als ein Argument an.
* **Server (Go):** Tabelle gültiger und ungültiger Anfragen (Zeilenumbruch, NUL, zu lang, ohne Wort, falsches Wort, ohne `--edit`, `--readonly`, unbekannter Rechner); Haltung der Rechner; Ablehnung bei Änderung der Einstellungen; Protokollzeile ohne Befehlstext.
* **Browser:** Knopf nur im Zustand „gestartet“, Sperre ohne Wort, Wiederaufnahme, axe, Tastatur.
* **Größe:** Start etwa 148 KB bei 175 KB; die Seite „Befehl“ und der Knopf brauchen etwa 6 bis 8 KB.

### 10.4 Prüfung des Entwurfs (16.11.2026)

Ein unabhängiger Prüfer hat §10 gegen den Code und ein gebautes Programm gelesen. Fund und Folge: **`--cmd=-oProxyCommand=…` führt lokal Code aus** (ssh liest Optionen hinter dem Host) → §10.2 Nr. 3, Tests §10.3. Kein Weg für das Ereignisprotokoll vom Server aus → `Trace`-Haken (Nr. 4). `web.Proc` hat kein `CloseInput` → Nr. 5. Beschreibung von `ssh` falsch (kein `BatchMode` beim einen Rechner, Exit-Status der Gruppe, kein Zeitlimit) → §10.2. Seiteneffekte von VS Code (Server und Erweiterungen auf dem Rechner) → §10.1. `ssh --cmd` ist kein `isSide`: es zählt zu den vier Aktionen und sperrt `beginEdit` → Nr. 6. Das Wort `run` ist keine Sicherheitsgrenze → eigener Punkt. Windows: `ssh` ersetzt `viper` nur unter Unix.

### 10.5 Offen für den Benutzer

1. `vscode` bei `--readonly` aus (Vorschlag) oder erlaubt?
2. `ssh --cmd` nur mit `--edit` und dem Wort `run` (Vorschlag), oder auch ohne Wort?
3. Befehl im Ereignisprotokoll: nur die Länge (Vorschlag) oder der Text?

## 11 Knopf „Terminal“ (Entwurf, vom Benutzer gewünscht, 16.11.2026)

Die Oberfläche ist eine Seite im Browser und hat kein Terminal; `viper ssh NAME --tmux` geht nur am Terminal. Der Knopf öffnet auf **dem Computer, auf dem `viper ui` läuft**, ein Terminalfenster, das `viper ssh NAME [--in-group G] --tmux` ausführt (dieselbe tmux-Sitzung `claude` wie die Terminals von VS Code). Wie bei „VS Code“ (§10.1) ist die Oberfläche nur der Starter; das Terminal ist ein Programm des Desktops.

### 11.1 Aufbau

* **Neuer Unterbefehl `viper terminal NAME [--in-group G]`** („open a terminal window on this computer with the tmux session of the machine“). Er prüft wie `vscode` die SSH-Konfiguration (`No SSH config … run viper up first`), baut die Befehlszeile des Fensters und startet das Terminalprogramm über einen Dienst des Hosts (wie `LaunchVSCode`: `host.go`, `app.go`, Fake in den Tests). Er **wartet nicht** auf das Fenster: Er startet das Programm, wartet kurz (etwa 1 s), ob es sofort mit einem Fehler endet (dann meldet er dessen Ausgabe und Exit 1), und endet mit 0. Das Fenster überlebt `viper` und die Oberfläche.
* **Aktion `terminal` der Oberfläche** wie `vscode` (§10.1): Knopf „Terminal“ neben „VS Code“ nur bei Zustand „gestartet“ und nicht mit `--readonly`; Lauf der Seite (`isSide`, eigenes Budget „einer zugleich“, hält den Rechner, solange er startet); `argv` = `terminal NAME [--in-group G]` (Namen aus der Übersicht, kein Freitext). Es braucht **kein `--edit` und kein Wort**: Die Seite schickt keinen Befehl; die Shell tippt der Benutzer selbst im eigenen Fenster. *(Zu prüfen: gilt das auch für die Rechner mit `compose`/`kubernetes`, deren SSH-Konfiguration einen ProxyCommand hat? Der Zugang ist derselbe wie bei `viper ssh` am Terminal.)*
* **Den Befehl des Fensters** baut `viper terminal` aus dem **absoluten Pfad der eigenen Datei** (`os.Executable`) und festen Wörtern: `<viper> ssh NAME [--in-group G] --tmux --dir DIR`. Der Name kommt aus der Übersicht (Muster der Dateinamen, kein Bindestrich am Anfang).

### 11.2 Terminalprogramme je System (Annahmen, vor dem Bauen zu prüfen *[?]*)

* **Nie eine Shell-Zeile zusammenbauen**, wo es vermeidbar ist: Das Programm bekommt die Wörter als einzelne Argumente.
* **macOS:** Das Terminal nimmt keine Argumente für ein Programm; Weg: eine Datei `viper-ssh-NAME.command` (Rechte `0700`, in einem privaten Verzeichnis des Zustandsverzeichnisses, Inhalt `exec '<viper>' ssh 'NAME' --tmux --dir '<DIR>'` mit einfachen Anführungszeichen und Maskierung, nicht ändern lassbar), geöffnet mit `open -a Terminal DATEI`; die Datei löscht `viper terminal` beim nächsten Start nach 1 Tag (das Fenster liest sie sofort). Alternative `osascript` mit `do script` verlangt Maskierung in AppleScript: nicht nehmen.
* **Linux:** Eine feste Liste in dieser Reihenfolge, die erste, die im `PATH` steht: `x-terminal-emulator -e`, `gnome-terminal --`, `konsole -e`, `xfce4-terminal -x`, `kitty`, `alacritty -e`, `wezterm start --`, `xterm -e`. Die Umgebungsvariable `VIPER_TERMINAL` (im Prozess von `viper ui`, **nicht** in einer Einstellungsdatei: die Seite „Ändern“ könnte sonst lokal Code einschleusen) wählt **einen Namen aus dieser Liste**, nie ein freies Programm. Ohne `DISPLAY` und `WAYLAND_DISPLAY`: Fehler „no desktop“.
* **Windows:** `wt.exe new-tab` (Windows Terminal), sonst `cmd /c start "" cmd /k` mit dem Befehl als Argumente; verlangt den OpenSSH-Client im `PATH`. Anführungszeichen für `cmd` sind heikel: Der Entwurf nimmt `wt`, ohne `wt` den Fehler „Windows Terminal not found“ mit dem Hinweis auf `viper ssh --tmux` am Terminal.
* Ob ein bestimmtes Programm bei einem Snap- oder Flatpak-Fenster oder in WSL richtig startet, ist nicht geprüft; dann hilft `VIPER_TERMINAL`.

### 11.3 Grenzen und Sicherheit

* Die Seite öffnet **keine Shell im Browser** und schickt keinen Text an sie. Das Fenster startet ein Programm mit festen Wörtern; die Bedrohung ist die der Seite „VS Code“: Ein Klick (mit Schlüssel, POST-Wache) startet ein lokales Programm und eine Verbindung zu einem eigenen Rechner.
* `VIPER_TERMINAL` kommt nur aus der Umgebung und nur als Name aus der Liste. Der Pfad zu `viper` ist der der laufenden Datei; die Datei des macOS-Weges liegt in einem Verzeichnis mit Rechten `0700` und hat feste Wörter plus den geprüften Namen und das Verzeichnis der Einstellungen (Pfad, in einfache Anführungszeichen gesetzt; ein `'` im Pfad wird maskiert, oder der Befehl lehnt den Pfad ab).
* Läuft `viper ui` auf einem Rechner ohne Desktop, scheitert der Lauf mit der Meldung des Befehls („no desktop“, „no terminal program found“).
* Das Fenster gehört dem Benutzer: Die Oberfläche kann es nicht schließen, nicht lesen und nicht abbrechen. „Abbrechen“ des Laufs beendet nur den Start.

### 11.4 Tests und Größe

* **Echtes Programm (§2.3):** `viper terminal web` mit Fake-Terminalprogramm im `PATH` (ein Skript, das seine Argumente in eine Datei schreibt; Linux: `x-terminal-emulator` und `DISPLAY` gesetzt, macOS: Fake-`open`, Windows: Fake-`wt.cmd`), mit angelegter SSH-Konfiguration: Die Argumente sind genau `<viper> ssh web --tmux --dir …` als einzelne Wörter; ein Name mit Sonderzeichen kommt nicht vor (die Namen der Dateien lassen es nicht zu), der **Pfad** mit Leerzeichen und `'` kommt unverändert an. Ohne Konfiguration: „No SSH config“. Mit Fake-Programm, das sofort mit Fehler endet: Exit 1 und dessen Meldung. Ohne `DISPLAY`: „no desktop“.
* **Reine Funktion** (Go-Test) je System: Eingabe (System, Umgebung, vorhandene Programme, Wörter) → Programm und Argumente; die Tabelle der Linux-Liste; `VIPER_TERMINAL` mit unbekanntem Namen wird abgelehnt; die `.command`-Datei mit Pfaden voller Sonderzeichen.
* **Server (Go):** `terminal` ist `isSide`, hält den Rechner, ist mit `--readonly` 403, mit Gruppe 400, mit `cmd` 400; Tabelle der Befehlszeile.
* **Browser:** Knopf nur bei „gestartet“ und nicht readonly (wie `VS Code`), axe, Tastatur.
* **Größe:** etwa 0,5 KB in `app.js` und den Texten; Start etwa 154 KB bei 175 KB.
* **Handbuch:** Abschnitt bei der Übersicht („Terminal“ öffnet ein Fenster **dieses Computers**), die Liste der Programme je System, `VIPER_TERMINAL`; Hilfe `viper terminal --help`; die Zeile in Kapitel 11.

### 11.5 Offen für den Benutzer

1. Nur die tmux-Sitzung `claude` (Vorschlag) oder auch eine einfache Shell als zweiter Knopf oder Auswahl?
2. Soll der Befehl `viper terminal` auch am Terminal zur Verfügung stehen (Vorschlag: ja, er ist ohnehin ein Unterbefehl) oder nur von der Oberfläche?
3. Windows im ersten Schritt nur mit Windows Terminal (`wt`)?
