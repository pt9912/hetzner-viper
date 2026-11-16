# Plan: Gestaltung der Oberfläche (`viper ui`)

**Stand (Wörter des Zustands seit v0.27.1: gestartet = `viper up` durchgelaufen, angelegt = Server da, nicht fertig):** Schritte 1 bis 3 und das Filtern und Sortieren der Übersicht sind umgesetzt (Commits 64004e3, daa95c9); einmal unabhängig geprüft (§8). Gehört zu [`ui.md`](ui.md) (Funktion, Sicherheit, Aufbau) und [`ui-bearbeiten.md`](ui-bearbeiten.md) (Formulare, §7 hier); dieses Dokument sagt nur, **wie die Oberfläche aussieht und sich anfühlt**. Die Funktion ändert sich nicht: Die Seiten, ihre `id`s, die Rollen und Namen für Tastatur und Screenreader bleiben, damit die Browser-Tests (`e2e/`) und die Zugänglichkeitsprüfung (axe) weiter gelten.

## 1 Ausgangslage

Die Oberfläche war funktional, aber optisch das Standardraster: Tabelle, Reiter oben, Systemschrift, blaue Knöpfe, alle Aktionen gleich laut. Was sie eigentlich zeigt, ging darin unter: **Zustand** (was läuft, was wartet, was ist fertig) und **der laufende Lauf** (Ausgabe, Rückfrage, Abbrechen).

* **Gegenstand:** ein Leitstand auf dem eigenen Rechner für Entwicklungsrechner (VMs, Container, Pods), ihre Läufe und die NATS-Gruppen, in denen Agenten miteinander sprechen.
* **Nutzer:** wer solche Rechner für Coding-Agenten betreibt, am Terminal zu Hause, aber hier lieber klickt als tippt. Sie kennen die Befehle; sie wollen sehen, was los ist, und nichts Falsches löschen.
* **Aufgabe der Oberfläche:** in einem Blick den Zustand jedes Rechners zeigen, die nächste sinnvolle Aktion nahelegen, das Löschen deutlich absetzen und einen Lauf wie ein Terminal lesbar machen, mit der Rückfrage dort, wo sie gestellt wird.

## 2 Idee: ein Logbuch für Maschinen

Leitbild ist der Maschinenraum: ruhig, dicht, ablesbar. **Eine** Sache ist das Auffällige, alles andere bleibt zurückhaltend:

* **Das Rack.** Jeder Rechner ist eine Rack-Einheit: links ein Streifen in der Farbe seines Zustands, eine **Lampe mit Wort** („gestartet“, „angelegt“, „nicht gestartet“; nie nur Farbe), der Name als Kennung in Festbreitenschrift, rechts die Aktionen. Der Streifen und die Lampe sind Information, kein Schmuck: Sie zeigen den Zustand, den die Marken dieses Rechners belegen. Eine Gruppe ist ein Rack mit einer Kopfzeile, die den Zustand der **ganzen** Gruppe trägt.
* **Die Rückfrage ist eine Zeile des Terminals.** Fragt `viper` etwas (`Type DELETE …`), steht die Eingabe als hervorgehobene Zeile am Fuß der Ausgabe, im Amber der Frage, und nicht als Formular irgendwo darunter.

Was von dem „Logbuch“ **sichtbar** ist, ist nur das Rack und das Terminal; Einträge mit Zeiten und Reihenfolge kämen erst mit einer Seite „Verlauf“ (die Protokolle sind sie heute). Der Name ist Leitbild, kein Versprechen.

## 3 Gestaltungsmittel

### Farben

Eine Markenfarbe, die zugleich die Farbe von „bereit“ ist; die übrigen Farben bedeuten etwas. Gemessen mit der Formel der WCAG 2.1 (Hellbezugswert), nicht geschätzt.

| Name | Hell | Dunkel | Bedeutung |
|---|---|---|---|
| Papiergrau | `#eceeea` | `#0d1613` | Grund |
| Tafel | `#f7f9f6` | `#131f1b` | Fläche der Zeilen und Felder |
| Tinte | `#17231f` (13,9 / 15,3 : 1) | `#e2ebe6` (15,1 / 13,9) | Text |
| Gedämpft | `#46574f` (6,6 / 7,3) | `#9db1a8` (8,1 / 7,5) | Erklärungen |
| Viper-Grün | Text `#0e6b52` (5,5 / 6,1), Knopf `#0e6b52` mit Weiß (6,5) | Text `#52c79e` (8,8), Knopf `#1d7a5d` mit Weiß (5,3) | Marke, „gestartet“, Hauptaktion |
| Bernstein | `#8a5300` (5,4 / 6,0) | `#e2a640` (8,6) | „angelegt“, „wartet auf Antwort“ |
| Backstein | `#b3261e` (5,6 / 6,2) | `#ff8a80` (8,1) | Fehler, was löscht (Down, Abbrechen) |
| Rand von Feldern und Knöpfen | **`#7d8a83`** | **`#6c8279`** | der Umriss eines Eingabefeldes (mindestens 3 : 1) |
| Haarlinie | `#c3cbc5` | `#2b3d36` | nur Trennlinien, **nie** der Umriss eines Bedienelements |

Das Terminal ist in beiden Ansichten dunkel (`#0f1a16`, Text `#dbe6e0` 13,9, gedämpft `#8fa59b` 6,8, Frage `#f0b955` 10,0): Es ist der eine Ort, der wie ein Terminal aussehen soll.

**Was der erste Entwurf falsch hatte:** „Alle Paare erfüllen AA“ stimmte für **Text** (4,5 : 1), aber nicht für **Bedienelemente** (WCAG 1.4.11, mindestens 3 : 1). Der Rand der Felder und Knöpfe war die Haarlinie (`#c3cbc5` auf `#f7f9f6` = 1,6 : 1, dunkel 1,5 : 1; der Rand des Antwortfeldes im Terminal 2,4 : 1). axe prüft das nicht. Deshalb: eigener Rand-Ton für alle Eingabefelder, Auswahlen, Knöpfe mit Umriss und das Antwortfeld (im Terminal `#8a7a3f` auf `#1d1a10` oder heller); die Haarlinie bleibt Trennlinie. Die Lampen und Streifen (3,2 bis 6,1 : 1) und der Fokusring (6,6 bis 9,5 : 1) bestehen.

**Bedeutung ist doppelt belegt, deshalb nie nur Farbe.** Grün, Bernstein und Backstein haben fast denselben Hellbezugswert (0,112 / 0,116 / 0,111, Kontrast untereinander 1,01 bis 1,03): Wer die Farben nicht unterscheidet, sieht nur die Füllung der Lampe (leer / gefüllt) und das **Wort**. Darum steht das Wort immer da, und die Vorschau der Bearbeitung (§7) kennzeichnet Zeilen mit `+`, `~` und `−` und den Wörtern „neu“, „geändert“, „entfernt“. Bernstein trägt viel: „gestartet“, „wartet auf Antwort“, „Bearbeiten an“, „geändert“, die Konfliktzeile; jede dieser Stellen hat ein eigenes Wort, damit die Farbe keine Unterscheidung leisten muss. Im Modus für erzwungene Farben (`forced-colors`) fallen Füllung und Streifen weg; dort bekommen Lampe und Zeile einen Rand (`CanvasText`), und die Wörter tragen die Bedeutung.

### Schrift

Keine Webschriften: Die Content-Security-Policy erlaubt nur eigene Dateien, und die Seiten bleiben klein.

* **Überschriften:** Palatino-Stapel (`Iowan Old Style`, `Palatino`, `Georgia`). Das ist vor allem Stimmung; es trägt keine Information und kann entfallen, ohne dass etwas fehlt.
* **Text und Bedienung:** Systemschrift (`system-ui`).
* **Kennungen und Ausgaben:** Festbreitenschrift (`ui-monospace`, `Menlo`, `Consolas`): Namen von Rechnern, Gruppen, Einstellungen, IDs, die Ausgabe. Das ist Inhalt, kein Schmuck.
* **Maßstab:** 15 px Fließtext (Erklärungen in `rem`, also 16 px bei der Standardeinstellung: bewusst kleine Unterschiede, keine zweite Stufe); Überschriften 1,5 / 1,1 rem; Zeilenlänge der Erklärungen höchstens 62 Zeichen.

### Aufbau

```
┌ ▮ viper    v0.25.0    [Bearbeiten an]                       Sprache ▾ ┐
├───────────────┬────────────────────────────────────────────────────────┤
│ ▌Übersicht    │ Filter [        ] Zustand [Alle ▾]            2 von 3 Rechnern │
│  Läufe  ●     │ Rechner▲  Anbieter  Typ  Ort  NATS  Zustand        Aktionen    │
│  NATS         │ dev  (Gruppe)                    ● teilweise (1 gestartet, 1 angelegt) [Vorprüfung][Up][Down] │
│  Einstellungen│ ┃● a     compose  …   dev   gestartet      [Prüfen] Vorprüfung Up Down Protokoll │
│  Protokolle   │ ┃○ b     compose  …   dev   nicht gestartet Prüfen [Up] …       │
│               │ Ohne Gruppe                                              │
│               │ ┃● web   hetzner  cpx22 fsn1 gestartet      [Prüfen] …          │
└───────────────┴────────────────────────────────────────────────────────┘

 Läufe:  ┌ Liste der Läufe ┐ ┌ Terminal ───────────────────────────────┐
         │ up web  läuft   │ │ Läuft …                       [Abbrechen]│
         │ down a  fertig  │ │ $ viper down web                         │
         └─────────────────┘ │ Type DELETE to continue:                 │
                             │ ▌Antwort  [ DELETE        ] [Senden]     │
                             └──────────────────────────────────────────┘
```

* **Die Übersicht ist nach Gruppen gegliedert, Gruppen zuerst.** Eine Gruppe ist ein Rack: eine Kopfzeile mit dem Namen, dem **Zustand der Gruppe** (Lampe und Wort; „teilweise“ nennt die Zahlen) und den Aktionen der Gruppe, darunter ihre Rechner als Einheiten. Rechner ohne Gruppe stehen zuletzt unter „Ohne Gruppe“ (die Kopfzeile erscheint nur, wenn es Gruppen gibt; **ohne Gruppen** gibt es nur die Einheiten, ohne Kopfzeile). Ein Rechner in mehreren Gruppen steht unter jeder. Fehlerhafte Gruppen (unlesbare Einstellungen) stehen mit ihrer Meldung in der Kopfzeile. Es bleibt **eine** Tabelle (`#machines`) mit **einem `tbody` je Gruppe**; die Einheiten haben die Klasse `unit`, die Kopfzeilen `grouphead`. Die Namen der Knöpfe („Up web“, „Up group dev“) bleiben.
* **Filtern und Sortieren:** ein Feld „Filter“ (Wörter, alle müssen passen) und eine Auswahl „Zustand“ über der Tabelle; die Überschriften der Spalten sind Knöpfe mit `aria-sort` (ein Klick aufsteigend, zwei absteigend, drei aus; der Pfeil ist Zierde mit leerem Alternativtext). Sortiert wird **innerhalb jeder Gruppe**, die Gruppen bleiben nach Namen. Der Zustand einer Gruppe zählt immer alle ihre Rechner.
* **Linke Leiste** statt Reiter oben: fünf Seiten, linksbündig, die gewählte mit Rand und Fläche. Die Leiste ist eine **senkrechte** Reiterleiste (`aria-orientation="vertical"`, Auf/Ab und weiterhin Links/Rechts); **unter 52 rem** wird sie waagerecht, und dann wird das Attribut per Skript auf `horizontal` gesetzt (`matchMedia`). Ein **Lauf in Arbeit** zeigt einen atmenden Punkt hinter „Läufe“; er hat einen **Textersatz** („läuft“, nur für Screenreader) und bleibt bei „weniger Bewegung“ ein ruhiger Punkt. Er ist die einzige Bewegung der Seite, die ohne Zutun des Nutzers läuft.
* **Knöpfe nach Gewicht:** Die wahrscheinlich nächste Aktion ist gefüllt (nicht gestartet: **Up**; gestartet oder angelegt: **Prüfen**), die übrigen sind ruhig, **Down** und **Abbrechen** sind backsteinfarben. Die Namen für Screenreader bleiben („Up web“).
* **Linksbündig**, kein Zentrieren; Tabellen und Formulare auf einer Spalte von höchstens 76 rem.

## 4 Gegen die Standards geprüft (ehrlich)

Der erste Entwurf wurde gegen die Muster geprüft, die jede erzeugte Oberfläche hat. Geändert wurde:

* **Blau als Akzent** → die Marke ist Viper-Grün und ist selbst die Zustandsfarbe von „bereit“.
* **Nahezu schwarz mit giftgrünem Akzent** → helle Hauptansicht in Papiergrau; Dunkel ist ein gedämpftes Grün-Grau, das Terminal der einzige tiefschwarze Ort.
* **Kachelkarten mit gleichem Schatten** → keine Karten: Zeilen mit Rändern.
* **Großbuchstaben-Etiketten, Nummern, Mittelpunkt-Ketten** → keine; der Pfeil der Sortierung ist Information, kein Schmuck an Knöpfen.
* **Einblend- und Schwebeeffekte** → keine; die einzige Bewegung ist der atmende Punkt.

**Was die Prüfung dazu sagte und was bleibt:** Grün, Bernstein und Backstein als Zustandspalette, ein grün getönter dunkler Modus, Serifenüberschriften mit linker Leiste auf grauem Papier sind jeweils **bekannte Muster**; neu ist an dieser Oberfläche weniger die Farbe als die **Struktur**: die Einheit mit Streifen, Lampe und Wort, die Gruppe als Rack mit eigenem Zustand und die Rückfrage als Zeile des Terminals. Die Serifenüberschrift ist Stimmung und kann entfallen; der Rest trägt Information.

## 5 Grenzen und Verträge

* **Keine neue Bibliothek, keine Webschrift, keine Bilddatei außer dem Favicon** (`style-src 'self'`, `img-src 'self'`, kein Inline-Stil: Zustände sind Klassen). Neuere CSS-Funktionen (`color-mix`, `:focus-visible`, `content: … / ""`) sind in den Browsern der Prüfmatrix (Chromium, Firefox, WebKit, Safari) vorhanden; für ältere steht jeweils eine Vorgabe davor.
* **Zugänglichkeit bleibt Vertrag:** sichtbarer Fokus, Tastaturbedienung, Zustand nie nur als Farbe, `prefers-color-scheme`, `prefers-reduced-motion`, `forced-colors`, Kontrast von Text (4,5 : 1) **und von Bedienelementen (3 : 1, WCAG 1.4.11; axe prüft das nicht: Rechnung in einem Test oder Prüfung von Hand)**.
* **Seitengröße:** was eine Seite beim Öffnen lädt, bleibt unter 175 KB, alle Dateien zusammen unter 200 KB (Test `TestThePagesStaySmall`). Die Texte stehen je Sprache in `i18n.en.js` und `i18n.de.js`; die Seite lädt nur die Datei ihrer Sprache (`i18n.js` lädt sie, die Seite startet danach), die andere erst beim Wechsel. Frühere Grenze: alle Dateien zusammen unter 175 KB ( zuerst 120 KB, mit der Bearbeitung auf 140 und dann auf 175 KB angehoben; Stand der ersten Gestalt rund 64 KB). Die Formulare der Bearbeitung (§7) mit Hilfe je Einstellung und zwei Sprachen brauchen mehr: Die **Hilfetexte kommen vom Server** (`GET /api/templates`), nicht aus `i18n.js`, sonst wird die Grenze mit Absicht angehoben. Jeder neue `data-i18n`-Schlüssel steht in beiden Sprachen (Test).
* **Tests:** Die `id`s und die Rollen bleiben. Die Tests zählen die Rechner mit `#machines tr.unit` und die Gruppen mit `#machines tr.grouphead.real` (nicht mehr `tbody tr`/`#groups`). Die Browser-Tests (Chromium, Firefox, WebKit, Safari, Windows) und die axe-Prüfung (hell und dunkel, Englisch und Deutsch) laufen gegen die neue Gestalt. Bildschirmfotos der Seiten sind eine Prüfung von Hand, kein Test.

## 6 Schritte

1. **Rahmen:** linke Leiste, Kopfzeile, neue Farben und Schrift. *(gemacht)*
2. **Übersicht:** nach Gruppen gegliedert, Zustand der Gruppe, Rack-Zeilen mit Streifen, Lampe und Wort, Knöpfe nach Gewicht; Filtern und Sortieren. *(gemacht)*
3. **Läufe:** Liste und Terminal nebeneinander, Rückfrage als Zeile des Terminals, Punkt für laufende Läufe. *(gemacht; der Textersatz des Punktes und `aria-orientation` je Breite noch)*
4. **Nachziehen nach dem Review:** Rand-Ton für Bedienelemente (1.4.11), Textersatz des Punktes, `aria-orientation` per `matchMedia`, `forced-colors`; **NATS, Einstellungen, Protokolle** in der neuen Gestalt; Ablegefläche für Dateien.
5. **Prüfen:** axe, Tastatur, schmale Breite, Bildschirmfotos ansehen und eine Sache streichen, die nicht dient; Firefox, WebKit, Safari.
6. **Handbuch:** Beschreibung der Seiten (die Leiste liegt links, die Übersicht nach Gruppen, Filter und Sortierung). *(Übersicht gemacht)*

## 7 Bearbeiten (zu [`ui-bearbeiten.md`](ui-bearbeiten.md))

Formulare für Einstellungen sind der sensibelste Teil der Oberfläche: Sie müssen **ruhig und vorhersehbar** wirken und das Gefährliche deutlich absetzen.

* **Nur mit `--edit`.** Ohne die Option gibt es keine Formulare; mit ihr zeigt die Kopfzeile das Wort „Bearbeiten an“ (Bernstein mit Wort), damit man es nicht vergisst.
* **Ein Feld je Einstellung**, nicht eine Textfläche. Name in Festbreite links, Eingabe rechts, darunter eine Zeile Hilfe (vom Server: was es tut, was der Standard ist). Die Art der Eingabe folgt dem Inhalt (Auswahl, Zahl mit Grenzen, Text mit Muster). Eine Einstellung, die auch in der **Umgebung** steht, trägt **beim Entfernen** den Hinweis „gibt die Einstellung an die Umgebung ab“ (die Datei gewinnt, sie wird nicht überschrieben). Mehrzeilige Werte sind nur angezeigt („am Terminal ändern“).
* **Vorschau vor dem Anwenden:** zwei Spalten „bisher“ und „neu“ in Festbreite, nur die Zeile, die sich ändert, mit **`+`/`~`/`−` und dem Wort** (neu, geändert, entfernt) und zusätzlich der Zustandsfarbe; Zugangsdaten als „gesetzt → ersetzt“ ohne Wert. „Anwenden“ ist der gefüllte Knopf, „Verwerfen“ ruhig. Über der Vorschau: wie viele Rechner die Änderung trifft und welche Rechner dadurch von gut auf Fehler wechseln. Die Vorschau ist **kein** Live-Bereich: Sie wird mit einem Knopf („Vorschau“) oder beim Verlassen des Feldes aktualisiert, **nicht** mit jedem Tastendruck (das würde ansagen und bei Zugangsdaten den Wert wiederholt senden).
* **Zugangsdatenfeld:** Eingabeart Passwort mit `autocomplete="new-password"`; daneben „gesetzt“ oder „nicht gesetzt“ als Lampe mit Wort; „ersetzen“ öffnet das Feld und **setzt den Fokus hinein**; „entfernen“ ist backsteinfarben mit **Nachfrage an Ort und Stelle** (der Name des Eintrags steht in der Frage). Nach dem Senden wird der Wert aus dem Feld gelöscht (auch gegen „Zurück“ und den Zwischenspeicher des Browsers), und der Wert steht nirgends auf der Seite. Passwortmanager können anbieten, es zu speichern: ein Hinweis.
* **Gefährliches:** Einstellungen der Tabelle „gefährlich“ (`ui-bearbeiten.md` §2.9) verlangen das Tippen des Rechnernamens und nennen die Wirkung in einem Satz.
* **Anlegen** als **sechste Seite** (Ziel „Anlegen“ in der Leiste, nur mit `--edit`), nicht als Fenster: links das Formular, rechts die **Vorschau der Datei** (Festbreite, wird mit „Vorschau“ aktualisiert); unten „Anlegen“ (gefüllt) und „Abbrechen“ (zurück zur Übersicht, Fokus auf den Knopf, der die Seite öffnete). Ein Rechner, der es schon gibt, zeigt „gibt es schon“ mit einem Link.
* **Sicherungen:** Auf der Seite einer Datei steht „Frühere Fassungen“ (Zeit, Aktion, Dateien; höchstens fünf), je Zeile „Herunterladen“ (das ZIP) und „Wiederherstellen“ (mit Vorschau wie beim Ändern; leere Zugangsdaten werden genannt und neu abgefragt) und „Alle löschen“. Ein Satz sagt, dass die Sicherungen **keine Zugangsdaten** enthalten.
* **Konflikt:** „Die Datei hat sich geändert“ als Hinweiszeile mit „Neu laden“ (Bernstein mit Wort); **der Fokus geht auf die Meldung, die Eingaben des Benutzers bleiben**; nichts wird still überschrieben.
* **Fokus und Ansagen:** Nach „Anwenden“ geht der Fokus auf den Statusbereich oder die geänderte Zeile („Gespeichert: machines/web.env.local“); jedes Feld hat eine Beschriftung, die Vorschau ist ein Bereich mit Namen (`role="region"`).

## 8 Prüfung

Ein unabhängiger Prüfer hat den ersten Entwurf gelesen und die Kontraste berechnet. Die Funde und was daraus wurde:

| Fund | Folge im Plan |
|---|---|
| „Alle Paare erfüllen AA“ stimmt für Text, nicht für Bedienelemente (1.4.11: Ränder 1,4 bis 1,6 : 1; Antwortfeld im Terminal 2,4 : 1) | §3: eigener Rand-Ton, Haarlinie nur als Trennlinie, die Rechnung in der Tabelle; §5: 3 : 1 als Vertrag |
| Die gruppierte Übersicht bricht die Selektoren der Tests (`#machines tbody tr`, `#groups`) | §3, §5: eine Tabelle, ein `tbody` je Gruppe, Klassen `unit`/`grouphead`; die Tests sind umgestellt |
| Der Punkt „Lauf in Arbeit“ hat keinen Textersatz; `aria-orientation="vertical"` stimmt unter 52 rem nicht | §3: Textersatz, Umschalten per `matchMedia` |
| Bernstein und Backstein tragen zu viel; Farben haben fast gleiche Helligkeit; die Vorschau ist nur farbig | §3: Wort und `+`/`~`/`−`, `forced-colors`, eigene Wörter je Stelle |
| Fehlende Regeln für die Formulare: Fokus, Live-Vorschau, die sechste Seite, Passwortfeld und Zurück-Knopf, Nachfrage an Ort und Stelle | §7 |
| Größe: die Hilfetexte in zwei Sprachen sprengen die Grenze | §5, §7: Hilfetexte vom Server |
| §4 war teils Selbstlob (Grün/Bernstein/Backstein, grün getönter dunkler Modus, Serif plus Leiste sind bekannte Muster) | §4: ehrlich; das Unterscheidende ist die Struktur, die Serifenüberschrift kann entfallen |
| Das „Logbuch“ ist nicht sichtbar (keine Einträge, Zeiten) | §2: Leitbild, kein Versprechen |
| Null Gruppen und mehrere Gruppen je Rechner nicht beschrieben | §3 |
