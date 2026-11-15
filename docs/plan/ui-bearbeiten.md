# Plan: Einstellungen in der Oberfläche anlegen und bearbeiten (`viper ui`)

**Stand:** Entwurf (15.11.2026), noch nicht geprüft, noch nichts gebaut. Ergänzt [`ui.md`](ui.md) (dort ist das Bearbeiten in §8 ausdrücklich „nicht Teil“) und gehört zu [`ui-gestaltung.md`](ui-gestaltung.md) (§7: wie die Formulare aussehen). *[V]* heißt: im Code gelesen; *[?]* ist eine Vermutung, die in Meilenstein 0 zu prüfen ist.

**Ziel:** In der Oberfläche **neue Rechner und Gruppen anlegen** und **Einstellungen ändern**, ohne eine Datei von Hand zu öffnen, mit denselben Prüfungen wie am Terminal und ohne dass die Oberfläche Zugangsdaten zurückgibt. Das Bearbeiten von Dateien ist der gefährlichste Teil der Oberfläche (sie schreibt Dateien, die Zugangsdaten enthalten); deshalb in Stufen, jede einzeln nutzbar und prüfbar.

## 1 Ausgangslage

* Die Einstellungen liegen in `.env.local` (für alle), `groups/<gruppe>.env` und `machines/<rechner>.env.local`, dann kommt die Umgebung *[V]*. Eine Datei hat Vorrang vor der Umgebung, der Rechner vor der Gruppe vor `.env.local` *[V, Handbuch]*.
* Das Format ist das einer Shell-Datei mit `NAME=wert` (Anführungszeichen, `export`, Kommentare); ein Test (`TestSourceAgreesWithBash`) hält den eigenen Leser mit `bash` gleich *[V]*. Nicht in Anführungszeichen oder in doppelten werden `$VARIABLEN` ersetzt, in einfachen nicht *[V, Handbuch zu `VIPER_POST_CLONE`]*.
* `Settings.Load` lädt und prüft **einen** Rechner, `LoadGroup` eine Gruppe *[V]*. Rechnernamen: Kleinbuchstaben, Ziffern, Bindestriche, nicht am Rand (`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`); reservierte Namen: der Betreiber (`NATS_OPERATOR_NAME`) und `viper-admin` *[V]*.
* Die Oberfläche **liest** die Dateien schon: `cli/uisettings.go` kennt die Namen jeder Datei und zeigt Werte nur für eine Positivliste *[V]*. Geschrieben wird nichts.
* Vorlagen gibt es: `machines/example.env.local.example`, `groups/example.env.example`, `.env.local.example` *[V]*; die Anbieter haben Voreinstellungen (`model.ProviderInfo`: Größe, Region, Image, nötige Zugangsdaten) *[V]*.

## 2 Grundregeln

1. **Nur schreiben, was `viper` auch lesen würde.** Jede Änderung wird **vor dem Schreiben** mit dem Code von `viper up` geprüft (`Settings.Load`/`LoadGroup` auf dem Ergebnis). Was am Terminal abgelehnt würde, wird auch hier nicht geschrieben.
2. **Zugangsdaten gehen hinein, nie heraus.** Ein Feld für ein Token oder Passwort zeigt nie den Wert, auch nicht einem, der es eben getippt hat; es sagt „gesetzt“ und bietet „ersetzen“ und „entfernen“. Die Antworten des Servers und die Protokolle enthalten keinen Wert (die Schwärzung der Ausgaben gilt unverändert; zusätzlich kennt der Server den Wert nur für die Dauer der Anfrage).
3. **Kein Texteditor für die Rohdatei.** Die Oberfläche bietet **Felder für Einstellungen** (Name, Wert, Art), keine freie Textfläche. Das verhindert, dass ein eingefügter Text die Datei umbaut (zusätzliche Zeilen, Kommentare mit Befehlen).
4. **Eine Änderung ist ein Vorschlag, bevor sie gilt.** Der Server liefert die **Vorschau** (welche Zeilen sich ändern, ohne Werte von Zugangsdaten) und eine Kennung des Stands (Hash der Datei); „Anwenden“ schickt diese Kennung zurück. Hat sich die Datei inzwischen geändert (am Terminal, in einem anderen Tab), wird nichts geschrieben („Die Datei hat sich geändert: neu laden“).
5. **Erhalten, was der Benutzer geschrieben hat.** Kommentare, Leerzeilen, die Reihenfolge und die Schreibweise der anderen Zeilen bleiben; geändert wird nur die Zeile der Einstellung (zeilenweise, kein Neuschreiben aus einer Tabelle).
6. **Atomar und mit Sicherung.** Schreiben in eine Datei im selben Verzeichnis (`0600`), `fsync`, Umbenennen; die alte Datei bleibt als `<datei>.bak` (eine, die jüngste; `0600`). Eine neue Datei wird mit `O_EXCL` angelegt und überschreibt nie eine vorhandene.
7. **Nichts unter einem Lauf ändern.** Hält ein Lauf den Rechner (oder bei einer Gruppe ein Mitglied), wird seine Datei nicht geschrieben (dieselbe Tabelle wie die der Läufe, `ui.md` §3).
8. **Ausdrücklich einschalten (entschieden).** Die Oberfläche schreibt Einstellungen nur mit `--edit`; ohne die Option (und mit `--readonly`, das stärker ist) gibt es die Formulare nicht, und die Schreib-Endpunkte antworten 403. Die Kopfzeile zeigt bei `--edit` „Bearbeiten an“.
9. **Spur ohne Werte.** Jede Änderung steht im `events.log` („ui: machines/web.env.local: SERVER_TYPE geändert“, „… VIPER_GH_TOKEN gesetzt“), nie mit einem Wert.

## 3 Sicherheit (zusätzlich zu `ui.md` §3)

| Maßnahme | gegen |
|---|---|
| Dieselbe Torwache wie für Aktionen (Host, Cookie, `Origin`, Kopf, Inhaltstyp); die Anfragen sind `POST`, es gibt **keinen** `GET`, der schreibt oder eine Vorschau mit Werten von Zugangsdaten liefert | fremde Seiten, CSRF |
| Der Pfad entsteht nie aus dem Text des Browsers: Rechner- und Gruppennamen werden gegen das Muster geprüft und auf `machines/<name>.env.local` oder `groups/<name>.env` abgebildet; geschrieben wird nur dort und in `.env.local` | Pfadumgehung |
| Das Zielverzeichnis wird mit `Lstat` geprüft: ein Symlink, ein Verzeichnis oder eine Datei außerhalb des Einstellungsverzeichnisses wird nicht beschrieben (kein Folgen von Links) | Umlenken des Schreibens |
| Werte werden vor dem Schreiben auf **Zeilenumbruch, NUL und andere Steuerzeichen** geprüft und in **einfachen Anführungszeichen** geschrieben (dort ersetzt nichts `$` oder `` ` ``); ein Wert mit `'` wird nach den Regeln des Lesers geschrieben oder abgelehnt (Meilenstein 0) | Einschleusen von Zeilen oder Befehlen (die Dateien sind Shell-Syntax) |
| Namen von Einstellungen kommen aus einer **Liste** (die der Anbieter und die bekannten), ein freier Name nur mit dem Muster `[A-Z][A-Z0-9_]*` | Einschleusen anderer Zeilen |
| Größe begrenzt (Wert höchstens 8 KiB, Datei höchstens 256 KiB), Rate begrenzt | Erschöpfung |
| Rechte der geschriebenen Dateien `0600`, der Verzeichnisse `0700`; eine vorhandene Datei behält ihre Rechte | Lesen durch andere Benutzer |
| Die Vorschau zeigt Werte nur für die Positivliste (wie die Einstellungsseite); bei einem Zugangsdatum steht „gesetzt → ersetzt“ | Zugangsdaten auf dem Bildschirm |
| Eine Einstellung, die auch in der **Umgebung** steht, wird als „überschrieben durch die Umgebung“ markiert: Die Datei zu ändern wirkt dann nicht | Verwirrung, falsches Vertrauen |

## 4 Stufen

### Stufe A: anlegen

* **Neuer Rechner:** Formular mit Name, Anbieter (Liste), Typ und Ort (mit den Voreinstellungen des Anbieters als Vorschlag, bei `compose` und `kubernetes` die Felder dieser Anbieter), optional Gruppe und Repository. Ergebnis: **eine neue Datei** `machines/<name>.env.local`, nur mit den Zeilen, die vom Standard abweichen, sonst mit kurzen Kommentaren nach der Vorlage.
* **Neue Gruppe:** Name, Mitglieder (Auswahl aus den vorhandenen Rechnern; ein neuer Rechner kann gleich angelegt werden), optional NATS (Gruppenname, Objektspeicher und seine Grenzen). Ergebnis: `groups/<name>.env`.
* Beide schreiben **nur neue Dateien** (`O_EXCL`): Gibt es den Namen schon, steht dort „gibt es schon“ und ein Link zur Bearbeitung (Stufe B).
* Vorher: die Vorschau (der Text der Datei, ohne Zugangsdaten) und die Prüfung mit `Settings.Load`.

### Stufe B: ändern (ohne Zugangsdaten)

* Eine Seite je Datei: Liste der Einstellungen mit Wert (nur Positivliste), je Zeile „ändern“ und „entfernen“, unten „Einstellung hinzufügen“ (Name aus der Liste, Art und Hilfe je Name).
* Eingabearten je Einstellung: Auswahl (Anbieter, `true/false`, `none/dind/remote`), Zahl mit Grenzen (`NATS_OBJECTS_*`), Text mit Muster (Namen, Listen mit Komma). Die Grenzen sind die der Prüfung am Terminal.
* **Wirkung sichtbar machen:** Eine Änderung in `.env.local` betrifft alle Rechner (Warnung mit der Zahl der Rechner), eine in der Gruppendatei die Mitglieder; ein Rechner, der **läuft**, ändert sich erst mit dem nächsten `viper up` (Hinweis, kein Neustart).

### Stufe C: Zugangsdaten setzen

* Nur in der Positivliste der **Namen von Zugangsdaten** (`HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `VIPER_GH_TOKEN`, `VIPER_CLAUDE_TOKEN`, Passwörter, die der Anbieter), als Feld, das **nur schreibt** (Eingabeart Passwort, `autocomplete="new-password"`, kein Wert im DOM nach dem Senden).
* Gesetzt wird nur in der Datei, in der die Zeile schon steht, oder, wenn es keine gibt, in der Datei, die der Benutzer wählt (Standard: `.env.local`); die Wirkung („gilt für alle Rechner“) steht daneben.
* Eine Probe ist möglich, aber nicht Teil der Stufe (die Vorprüfung am Rechner prüft die Zugangsdaten gegen den Anbieter).

### Stufe D: entfernen *(erst nach A bis C, vielleicht nie)*

* Eine Rechnerdatei entfernen nur, wenn der Rechner **nicht gestartet** ist (keine Marken, kein Lauf) und mit getipptem Namen als Bestätigung; die Datei wird zu `<datei>.removed` umbenannt, nicht gelöscht.

## 5 Aufbau

* **Port:** ein eigener treibender Port `internal/port/driving` (`SettingsEditor`: `Preview(change) (Preview, error)`, `Apply(change, etag) error`) und ein treibender Teil in `settingsfile` (`Edit`: zeilenweises Ändern, `O_EXCL`, atomares Schreiben, Sicherung). Die Oberfläche ruft nur den Port; die Prüfung (`Settings.Load` auf dem Ergebnis) liegt hinter ihm *[?: wie liest `settingsfile` die Dateien? Eine Prüfung auf einer **Überlagerung** (die Datei im Speicher, die übrigen von der Platte) ist besser als eine Kopie des Verzeichnisses, weil nichts mit Zugangsdaten in ein Wegwerf-Verzeichnis geschrieben wird]*.
* **Web:** `POST /api/edit/preview` und `POST /api/edit/apply` (JSON: Art der Änderung, Ziel, Felder, `etag`), `GET /api/templates` (die Vorlagen und Voreinstellungen der Anbieter; ohne Werte). Alle hinter der Torwache, alle mit `--edit` sonst 403.
* **Schreibweise der Zeile:** `NAME='wert'` (einfache Anführungszeichen), der Kopf mit `export` und die Schreibweise einer vorhandenen Zeile bleiben, wenn sie nur ihren Wert ändert.
* **Sperre:** eine Sperrdatei (`settings.lock` im Zustandsverzeichnis) für die Dauer des Schreibens, damit zwei Tabs oder ein Terminal nicht gleichzeitig schreiben; die Läufe der Oberfläche bleiben davon unberührt.
* **Zustand der Oberfläche:** Nach `apply` lädt die Seite die Übersicht neu (der Zwischenspeicher wird verworfen, `forgetOverview`).

## 6 Gestaltung

Siehe `ui-gestaltung.md` §7 (Formulare, Vorschau, Zugangsdatenfeld, Bestätigungen).

## 7 Tests

* **Go:** Tabelle der Eingaben gegen die Regeln (Pfade mit `..`, Symlinks, Namen, Zeilenumbruch und NUL in Werten, `'` in Werten, zu große Werte, unbekannte Namen, vorhandene Datei bei „anlegen“, geänderte Datei bei „anwenden“, laufender Lauf, `--readonly` ohne `--edit`); Kommentare und Reihenfolge bleiben (Vorher/Nachher-Vergleich byte für byte außer der einen Zeile); Rechte `0600`; atomar (eine Unterbrechung lässt die alte Datei); `.bak` ist die alte Fassung; ein Zugangsdatum erscheint in **keiner** Antwort, keinem Protokoll, keiner Vorschau (Suche in allen Antworten, wie beim Test der Schwärzung); jede so geschriebene Datei besteht `Settings.Load` und `TestSourceAgreesWithBash` (der Leser und `bash` lesen dasselbe).
* **Browser (Playwright):** anlegen, ändern, Vorschau, Konflikt („Datei hat sich geändert“), Zugangsdatenfeld (nach dem Senden leer, kein Wert im DOM), `--edit` fehlt, axe (hell, dunkel, Englisch, Deutsch), Tastatur.
* **Echt:** ein Rechner, der in der Oberfläche angelegt wurde, besteht `viper preflight` und (mit dem gefälschten `docker`) einen Lauf; ein Durchlauf gegen echtes Docker (Compose) wie bei den anderen Seiten.

## 8 Meilensteine

0. **Prüfen (nichts bauen):** (a) wie liest `settingsfile` (Zeilen, Anführungszeichen, `export`, `\r\n`, Mehrzeiliges, `'` im Wert), und was schreibt eine Zeile so, dass Leser und `bash` dasselbe sehen; (b) eine Prüfung auf einer Überlagerung ohne Kopie; (c) welche Namen eine Positivliste der **Zugangsdaten** braucht (aus `model.ProviderInfo.Secrets` und den bekannten); (d) wie sich das Schreiben mit der Sperre der Läufe verträgt; (e) Windows (Rechte, Umbenennen über eine vorhandene Datei, Zeilenenden).
1. **Stufe A:** anlegen (Rechner, Gruppe), mit Vorschau, `--edit`, Tests, Handbuch („Einen Rechner in der Oberfläche anlegen“).
2. **Stufe B:** ändern ohne Zugangsdaten.
3. **Stufe C:** Zugangsdaten setzen.
4. *(Stufe D, wenn gewünscht.)* Nach jeder Stufe: Code-Review durch einen unabhängigen Prüfer und Prüfung der Dokumentation, dann Release.

## 9 Offene Fragen und Risiken

* **`--edit` (entschieden: ja):** Bearbeiten ist aus, bis man `--edit` angibt; ein Hinweis in der Kopfzeile zeigt, dass es an ist. Offen bleibt, ob `--edit` zusätzlich eine Bestätigung beim ersten Schreiben einer Sitzung braucht (Vorschlag: nein, die Vorschau ist die Bestätigung).
* **Kommentare erhalten, mehrzeilige Werte, `export`:** Ein Wert über mehrere Zeilen (Anführungszeichen über Zeilenenden, etwa `VIPER_POST_CLONE`) wird in Stufe B **nicht** bearbeitet (nur angezeigt als „mehrzeilig, am Terminal ändern“), bis Meilenstein 0 sagt, dass es sicher geht.
* **Umgebung schlägt Datei nicht, Datei schlägt Umgebung:** Wer eine Einstellung zugleich in der Umgebung hat, merkt es erst an dem Hinweis; die Oberfläche sieht nur die Umgebung ihres eigenen Prozesses.
* **Datei in einem Git-Repository:** Liegt das Einstellungsverzeichnis in einem Repository (Standard dieses Projekts: `.env.local` steht in `.gitignore`), schreibt die Oberfläche trotzdem; sie prüft nicht, ob eine Datei eingecheckt würde. Die Warnung gegen Zugangsdaten in Git steht im Handbuch.
* **Sicherung `.bak` enthält alte Zugangsdaten:** Sie hat dieselben Rechte (`0600`) und steht in derselben `.gitignore`-Regel (`*.bak` ergänzen).
* **Mehrere Benutzer des Rechners:** unverändert: Die Oberfläche ist nur für den, der sie startet (Cookie).
