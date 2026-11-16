# Plan: fehlende Befehle in der Oberfläche (`viper ui`)

**Stand:** Entwurf (16.11.2026). Gehört zu [`ui.md`](ui.md) (Funktion, Sicherheit, Aufbau) und [`ui-gestaltung.md`](ui-gestaltung.md) (Gestalt). Stufe 1 und 2 sind entschieden (Benutzer, 16.11.2026); Stufe 3 bis 5 sind Vorschläge. *[V]* heißt: im Code gelesen; *[?]* ist eine Vermutung, die vor dem Bauen zu prüfen ist.

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

**Befehl** *[V]*: `viper find [--cpu N] [--ram GB] [--disk GB] [--arch x86|arm] [--provider P] [--location L] [--vat PERCENT]`; mindestens eines von `--cpu`, `--ram`, `--disk`; `--location` braucht `--provider`; liest nur, braucht keinen Rechner und nur öffentliche Preisquellen (Zugangsdaten machen die Angaben genauer).

**Seite:** ein neuer Reiter „Finden“ (immer sichtbar, auch mit `--readonly`):

* **Formular:** CPUs (ganze Zahl 1 bis 4096), RAM in GB (Zahl bis 65536, auch 0,5), Platte in GB (ganze Zahl bis 100000), Architektur (alle, x86, arm), Anbieter (alle, hetzner, aws, azure, gcp), Standort (Text nach `[a-z0-9-]`, höchstens 41 Zeichen; nur mit Anbieter), Mehrwertsteuer in Prozent (0 bis 100; leer: der Satz des Projekts, sonst keine). Die Seite meldet fehlende Pflichtangaben selbst; der Server prüft alles noch einmal.
* **Ablauf:** „Suchen“ startet `viper find …` als Lauf (`POST /api/run`, Aktion `find`). Die Ausgabe erscheint in einem **eigenen Bereich dieser Seite** (`pre`, `role=region`, Festbreite, die Tabelle von `viper find` bleibt lesbar), nicht im Terminal der Läufe; der Lauf steht zusätzlich in der Liste der Läufe. „Abbrechen“ schickt Strg-C. Die Seite bleibt auf „Finden“ und merkt sich die Eingaben (bis zum Neuladen).
* **Wiederaufnahme:** Läuft beim Öffnen der Seite ein `find`-Lauf, hängt sich der Bereich daran (wie das Mitlesen in NATS).
* **Wartezeit:** Die Preise kommen aus dem Netz (Sekunden, bei AWS länger). Die Seite sagt „Suche läuft …“ und zeigt die Ausgabe, sobald sie kommt.
* **Ausgabe:** Sie geht wie jede Ausgabe durch Reinigung und Schwärzung; die Seite setzt sie als Text (`textContent`).

**Server** *[V, entworfen]*: `runRequest` bekommt `cpu`, `ram`, `disk`, `arch`, `provider`, `location`, `vat`; `findArgv` baut die Befehlszeile (feste Wörter und Zahlen, §2.1); `repo` ist für `find` nicht erlaubt; `isAction` kennt `find`, `isSide` zählt es zur Nebenseite (§2.5); `startRun` lässt es bei `--readonly` zu.

**Nicht Teil der Stufe:** „Typ und Standort in ein Formular übernehmen“ (Anlegen). Es setzt voraus, dass die Ausgabe maschinenlesbar ist (`viper find --json` gibt es nicht): ein späterer Schritt, zuerst am Befehl.

## 4 Stufe 2: Optionen für Läufe

* **Ort in der Oberfläche:** unter dem Feld „Repository-URL“ der Übersicht ein aufklappbarer Block **„Optionen für Läufe“** (`<details>`, zu): 
  * **Gleichzeitig (Gruppen):** Zahl 1 bis 64, leer: der Standard von `viper` (8). Gilt für Gruppenläufe von Vorprüfung, Up, Down und Prüfen.
  * **Down ohne Prüfung auf ungesicherte Arbeit (`--force`):** Kontrollkästchen, mit einem Satz, was es bedeutet (nicht gesicherte Arbeit auf dem Server geht verloren). **Es gilt nur für den nächsten Down-Lauf und wird nach dem Start wieder ausgeschaltet** (nie dauerhaft an).
  * **Registry bei Down (k3d):** Auswahl „fragen“ (Standard), „löschen“ (`--delete-registry`), „behalten“ (`--keep-registry`).
* **Sichtbarkeit:** Der Befehl, den die Oberfläche startet, steht in der ersten Zeile des Laufs (`$ viper down web --force`): Was die Option bewirkt hat, ist im Protokoll zu sehen.
* **Bestätigung:** `viper down` fragt weiter selbst (`DELETE` tippen); `--force` ändert die Frage nicht, nur die Prüfung davor. Eine zusätzliche Bestätigung in der Oberfläche gibt es nicht: der Kasten warnt, die Zeile im Lauf zeigt es, und die Rückfrage von `viper` bleibt.
* **Server** *[V, entworfen]*: `machineOptions` baut `--parallel=N`, `--force`, `--delete-registry` oder `--keep-registry` und lehnt sie an anderer Stelle ab (§2.2).
* **Nicht Teil der Stufe:** `--cmd`, `--tmux` (Stufe 5 und nie).

## 5 Stufen 3 bis 5 (Vorschläge, einzeln zu entscheiden)

* **Stufe 3: `setup`.** Legt `.env.local`, den Login-Schlüssel und die Host-CA an; ohne beides scheitert die Vorprüfung. Als Lauf mit Rückfragen (die Oberfläche kann Rückfragen); ein Knopf auf der Seite „Anlegen“ („Schlüssel und Host-CA anlegen“) und ein Hinweis in der Vorprüfung, wenn sie fehlen. *[?]* zu prüfen: was `viper setup` an Eingaben verlangt (Passphrase des Schlüssels? Domain?), und ob es mit `--readonly` gesperrt bleibt (ja: es schreibt in `~/.ssh`).
* **Stufe 4: `image build`.** Für `compose` und `kubernetes`: ein langer Lauf mit Ausgabe; Felder `--tag NAME:TAG` (Muster) und `--k3d CLUSTER` (Name nach Muster). Ein Knopf je Rechner mit solchem Anbieter und auf der Seite „Anlegen“.
* **Stufe 5: `ssh --cmd`.** Beliebige Befehle auf dem Server: nur mit `--edit`, mit der Rückfrage „Befehl tippen“ und der Zeile im Protokoll; für eine Gruppe nur mit `--parallel`. Heikel (Befehlsausführung auf dem Server aus dem Browser): erst nach der Entscheidung, ob das nötig ist; die Einstellung `VIPER_POST_CLONE` ist ein Vorbild für die Bestätigung.
* **Später/nie:** `vscode` (Knopf je Rechner, kleiner Nutzen), `dns` (besser als Spalte „Adresse“ in der Übersicht), Shell, Proxys und `mcp-binary` nie.

## 6 Tests

* **Server (Go):** Tabelle der Anfragen gegen die erwartete Befehlszeile; alle ungültigen (Grenzen, Muster, Optionen am falschen Befehl, `find` ohne Größe, Standort ohne Anbieter, `--` im Standort, NaN); `find` und Läufe ohne Rechner haben ein eigenes Budget und gehen neben vier Aktionen; `--readonly` lässt nur `check` und `find` zu.
* **Echtes Programm (§2.3):** Browser-Test mit dem echten `viper` und Fake-Docker: Up einer Gruppe mit `--parallel=2`, Down mit `--force` und `--keep-registry` (Fake-k3d fehlt: es genügt, dass die Option angenommen wird), `find` mit allen Feldern (ohne Netz endet es mit einer Meldung über die Preisquellen, nicht über eine Option).
* **Browser:** Formular und Pflichtangaben, Bereich der Ausgabe, Abbrechen, Wiederaufnahme nach Neuladen, `--force` wird nach dem Start wieder ausgeschaltet, `--parallel` nur bei Gruppenknöpfen, axe (hell, dunkel, Englisch, Deutsch), Tastatur.
* **Handbuch:** Abschnitt „Passenden Server-Typ suchen“ nennt die Seite; die Optionen stehen im Kapitel der Oberfläche; die Zeile in Kapitel 11.

## 7 Meilensteine

1. **Server** (gemacht im Entwurf): `runRequest`, `findArgv`, `machineOptions`, `isSide`, Tests.
2. **Seite „Finden“:** Reiter, Formular, Bereich, Wiederaufnahme, Texte, Tests, Handbuch.
3. **Optionen für Läufe:** Block in der Übersicht, `startRun` fügt sie an, `--force` einmalig, Tests mit dem echten Programm, Handbuch.
4. Nach jeder Stufe: unabhängige Prüfung, dann Release.
5. Danach Entscheidung über Stufe 3 bis 5.

## 8 Offen (zu entscheiden)

1. **`--force` ohne weitere Bestätigung** (Vorschlag: ja, die Rückfrage von `viper down` bleibt) oder mit einem getippten Wort in der Oberfläche?
2. **Ausgabe von `find` als Text** (Vorschlag) oder zuerst `viper find --json` und dann eine Tabelle der Seite (mehr Arbeit, aber „übernehmen“ möglich)?
3. **Ort der Optionen:** ein Block in der Übersicht (Vorschlag) oder je Knopf ein kleines Menü?
4. **`--parallel` bei „Prüfen“ der Gruppe:** gleicher Wert wie bei den anderen (Vorschlag).
5. **Reihenfolge der Stufen 3 bis 5** nach Stufe 2.

## 9 Prüfung

*(wird nach der Prüfung des Plans ausgefüllt)*
