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

**Befehl** *[V]*: `viper find [--cpu N] [--ram GB] [--disk GB] [--arch x86|arm] [--provider P] [--location L] [--vat PERCENT]`; mindestens eines von `--cpu`, `--ram`, `--disk`; `--location` braucht `--provider`; braucht keinen Rechner, aber **eine `.env.local`** im Einstellungsverzeichnis (ohne sie: „No .env.local: run viper setup“, Exit 1). *[V]* Es ist **nicht rein lesend**: Es legt Preislisten als Dateien im Cache-Verzeichnis des Benutzers ab (`os.UserCacheDir()/viper`: `aws-ec2-*.json`, `azure-*.json`, `gcp-*.json`, `hetzner-web.json`, `ecb-rates.json`), und Azure und GCP fragen mit den vorhandenen Zugangsdaten der Cloud ab (`AZURE_SUBSCRIPTION_ID`, `GCP_PROJECT` aus der `.env.local`); die Ausgabe hat kein Token gezeigt. Das erste Mal lädt es große Preislisten (Meldungen „reading the price list … a large file“ auf stderr). Exit-Status: 0 Treffer, **1 kein Treffer oder ein Anbieter übersprungen** (kein Fehler der Seite), 2 Fehler der Befehlszeile, 130 abgebrochen. Die Ausgabe ist eine Tabelle in Festbreite mit `€` und `*`, einer Zeile `PROVIDER=… SERVER_TYPE=…` und Hinweisen; **englisch auch in der deutschen Oberfläche**; stdout und stderr kommen in einem Strom.

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

* **Stufe 3: `setup`.** Legt `.env.local`, den Login-Schlüssel und die Host-CA an; ohne beides scheitert die Vorprüfung. Als Lauf mit Rückfragen (die Oberfläche kann Rückfragen); ein Knopf auf der Seite „Anlegen“ („Schlüssel und Host-CA anlegen“) und ein Hinweis in der Vorprüfung, wenn sie fehlen. *[V, geprüft]* `viper setup` ist **nicht interaktiv** (keine Fragen): Es schreibt `.env.local`, die Beispiele in `groups/` und `machines/` in `--dir`, legt Schlüssel und Host-CA in `~/.ssh` an, **überschreibt `~/.ssh/known_hosts_hetzner_claude_ca`** und druckt `export`-Zeilen und den öffentlichen Schlüssel der CA. Es bleibt bei `--readonly` gesperrt; ob es mit `--edit` angeboten wird, ist zu entscheiden (es schreibt außerhalb des Einstellungsverzeichnisses).
* **Stufe 4: `image build`.** Für `compose` und `kubernetes`: ein langer Lauf mit Ausgabe (`docker build`, braucht den Docker-Socket); Felder `--tag=NAME:TAG` und `--k3d=CLUSTER` in der Form `--name=wert`, mit engen Mustern. Ein Knopf je Rechner mit solchem Anbieter und auf der Seite „Anlegen“.
* **Stufe 5: `ssh --cmd`.** **Empfehlung der Prüfung: nicht bauen.** Ein freier Befehlstext widerspricht §2.1, die Oberfläche hält die Standardeingabe des Prozesses offen (ein Befehl, der von ihr liest, hängt), und der Nutzen ist klein. Wer es will: nur mit `--edit`, `--cmd=…` mit getipptem Wort und der Zeile im Protokoll.
* **Später/nie:** `vscode` (Knopf je Rechner, kleiner Nutzen), `dns` (besser als Spalte „Adresse“ in der Übersicht), Shell, Proxys und `mcp-binary` nie.

## 6 Tests

* **Server (Go):** Tabelle der Anfragen gegen die erwartete Befehlszeile; alle ungültigen (Grenzen, Muster, Optionen am falschen Befehl, `find` ohne Größe, Standort ohne Anbieter, `--` im Standort, NaN); `find` und Läufe ohne Rechner haben ein eigenes Budget und gehen neben vier Aktionen; `--readonly` lässt nur `check` und `find` zu.
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

1. **`--force` ohne weitere Bestätigung** (Vorschlag: ja, die Rückfrage von `viper down` bleibt) oder mit einem getippten Wort in der Oberfläche?
2. **Ausgabe von `find` als Text** (Vorschlag) oder zuerst `viper find --json` und dann eine Tabelle der Seite (mehr Arbeit, aber „übernehmen“ möglich)?
3. **Ort der Optionen:** ein Block in der Übersicht (Vorschlag) oder je Knopf ein kleines Menü?
4. **`--parallel` bei „Prüfen“ der Gruppe:** gleicher Wert wie bei den anderen (Vorschlag).
5. **Reihenfolge der Stufen 3 bis 5** nach Stufe 2.

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
| Die Seiten sind 143,9 KB groß bei 175 KB; Stufe 3 bis 5 passen nicht mehr hinein | Stufe 1 und 2 brauchen etwa 10 bis 15 KB; die Grenze wird vor Stufe 3 bewusst entschieden |
| Die Abfrage alle zwei Sekunden läuft auch für `find` | §3: nur für Aktionsläufe |
| Wiederaufnahme nach dem Neuladen nicht beschrieben; doppelte Bereiche | §3: über `/api/runs`, ein Bereich |

**Antworten zu §8:** (1) die Rückfrage von `viper down` bleibt ohne weiteres Wort, die Beschriftung ist berichtigt; (2) zuerst Text, `--json` ist ein eigener Schritt am Befehl; (3) ein Block mit `--force` pro Anfrage; (4) gleicher Wert; (5) `setup` zuerst, dann `image build`; `ssh --cmd` nicht.
