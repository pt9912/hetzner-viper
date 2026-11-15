# Plan: Gestaltung der Oberfläche (`viper ui`)

**Stand:** Entwurf (15.11.2026). Gehört zu [`ui.md`](ui.md) (Funktion, Sicherheit, Aufbau); dieses Dokument sagt nur, **wie die Oberfläche aussieht und sich anfühlt**. Die Funktion ändert sich nicht: Die Seiten, ihre `id`s, die Rollen und Namen für Tastatur und Screenreader bleiben, damit die Browser-Tests (`e2e/`) und die Zugänglichkeitsprüfung (axe) weiter gelten.

## 1 Ausgangslage

Die Oberfläche ist funktional, aber optisch das Standardraster: Tabelle, Reiter oben, Systemschrift, blaue Knöpfe, alle Aktionen gleich laut. Was sie eigentlich zeigt, geht darin unter: **Zustand** (was läuft, was wartet, was ist fertig) und **der laufende Lauf** (Ausgabe, Rückfrage, Abbrechen).

* **Gegenstand:** ein Leitstand auf dem eigenen Rechner für Entwicklungsrechner (VMs, Container, Pods), ihre Läufe und die NATS-Gruppen, in denen Agenten miteinander sprechen.
* **Nutzer:** wer solche Rechner für Coding-Agenten betreibt, am Terminal zu Hause, aber hier lieber klickt als tippt. Sie kennen die Befehle; sie wollen sehen, was los ist, und nichts Falsches löschen.
* **Aufgabe der Oberfläche:** in einem Blick den Zustand jedes Rechners zeigen, die nächste sinnvolle Aktion nahelegen, das Löschen deutlich absetzen und einen Lauf wie ein Terminal lesbar machen, mit der Rückfrage dort, wo sie gestellt wird.

## 2 Idee: ein Logbuch für Maschinen

Leitbild ist der Maschinenraum: ruhig, dicht, ablesbar. **Eine** Sache ist das Auffällige, alles andere bleibt zurückhaltend:

* **Das Rack.** Jeder Rechner ist eine Rack-Einheit: links ein Streifen in der Farbe seines Zustands, eine **Lampe mit Wort** („eingerichtet“, „gestartet“, „nicht gestartet“; nie nur Farbe), der Name als Kennung in Festbreitenschrift, rechts die Aktionen. Der Streifen und die Lampe sind Information, kein Schmuck: Sie zeigen den Zustand, den die Marken dieses Rechners belegen.
* **Die Rückfrage ist eine Zeile des Terminals.** Fragt `viper` etwas (`Type DELETE …`), steht die Eingabe als hervorgehobene Zeile am Fuß der Ausgabe, im Amber der Frage, und nicht als Formular irgendwo darunter.

## 3 Gestaltungsmittel

### Farben

Eine Markenfarbe, die zugleich die Farbe von „bereit“ ist; die übrigen Farben bedeuten etwas.

| Name | Hell | Dunkel | Bedeutung |
|---|---|---|---|
| Papiergrau | `#eceeea` | `#0d1613` | Grund |
| Tafel | `#f7f9f6` | `#131f1b` | Fläche der Zeilen und Felder |
| Tinte | `#17231f` | `#e2ebe6` | Text |
| Viper-Grün | `#0e6b52` (Knopf `#0e6b52`) | Text `#52c79e`, Knopf `#1d7a5d` | Marke, „eingerichtet“, Hauptaktion |
| Bernstein | `#8a5300` | `#e2a640` | „gestartet“, „wartet auf Antwort“ |
| Backstein | `#b3261e` | `#ff8a80` | Fehler, was löscht (Down, Abbrechen) |

Das Terminal ist in beiden Ansichten dunkel (`#0f1a16`, Text `#dbe6e0`, Frage `#f0b955`): Es ist der eine Ort, der wie ein Terminal aussehen soll. Alle Paare erfüllen WCAG AA (Kontrast mindestens 4,5 : 1 für Text); das prüft axe in beiden Ansichten und beiden Sprachen.

### Schrift

Keine Webschriften: Die Content-Security-Policy erlaubt nur eigene Dateien, und die Seiten bleiben klein.

* **Überschriften:** Palatino-Stapel (`Iowan Old Style`, `Palatino`, `Georgia`): ein Logbuch, keine Konsole.
* **Text und Bedienung:** Systemschrift (`system-ui`).
* **Kennungen und Ausgaben:** Festbreitenschrift (`ui-monospace`, `Menlo`, `Consolas`): Namen von Rechnern, Gruppen, Einstellungen, IDs, die Ausgabe. Das ist Inhalt, kein Schmuck.
* **Maßstab:** 15 px Fließtext; Überschriften 1,5 / 1,1 rem; Zeilenlänge der Erklärungen höchstens 62 Zeichen; Serifenüberschriften etwas enger gesetzt.

### Aufbau

```
┌ ▮ viper                       v0.25.0                    Sprache ▾ ┐
├───────────────┬────────────────────────────────────────────────────┤
│ ▌Übersicht    │  Rechner                                            │
│  Läufe  ●     │  ┃●  web       hetzner  cpx22  fsn1  eingerichtet  [Prüfen] Vorprüfung Up  Down │
│  NATS         │  ┃○  a  dev    compose  …            nicht gestartet [Up] … │
│  Einstellungen│  Gruppen …                                           │
│  Protokolle   │  Prüfung  (Ausgabe)                                  │
└───────────────┴────────────────────────────────────────────────────┘

 Läufe:  ┌ Liste der Läufe ┐ ┌ Terminal ───────────────────────────────┐
         │ up web  läuft   │ │ Läuft …                       [Abbrechen]│
         │ down a  fertig  │ │ $ viper down web                         │
         └─────────────────┘ │ Type DELETE to continue:                 │
                             │ ▌Antwort  [ DELETE        ] [Senden]     │
                             └──────────────────────────────────────────┘
```

* **Linke Leiste** statt Reiter oben: fünf Seiten, linksbündig, die gewählte mit Rand und Fläche. Ein **Lauf in Arbeit** zeigt einen atmenden Punkt hinter „Läufe“: die einzige Bewegung der Seite, die ohne Zutun des Nutzers läuft.
* **Unter 52 rem Breite:** die Leiste wird zur Leiste oben (waagerecht, scrollbar), die Läufe stehen untereinander.
* **Knöpfe nach Gewicht:** Die wahrscheinlich nächste Aktion ist gefüllt (nicht gestartet: **Up**; eingerichtet: **Prüfen**), die übrigen sind ruhig, **Down** und **Abbrechen** sind backsteinfarben. Die Namen für Screenreader bleiben („Up web“).
* **Linksbündig**, kein Zentrieren; Tabellen und Formulare auf einer Spalte von höchstens 76 rem.

## 4 Gegen die Standards geprüft

Der erste Entwurf wurde gegen die Muster geprüft, die jede erzeugte Oberfläche hat. Geändert wurde:

* **Blau als Akzent** (der Standard von Werkzeugen) → die Marke ist Viper-Grün und ist selbst die Zustandsfarbe von „bereit“.
* **Nahezu schwarz mit giftgrünem Akzent** (Terminalwerkzeug-Klischee) → helle Hauptansicht in Papiergrau; Dunkel ist ein gedämpftes Grün-Grau, das Terminal der einzige tiefschwarze Ort.
* **Kachelkarten mit gleichem Schatten** → keine Karten: Zeilen mit Rändern, eine einzige Fläche (die Tafel).
* **Großbuchstaben-Etiketten, Nummern, Pfeile in Knöpfen, Mittelpunkt-Ketten** → keine. Etiketten stehen in normaler Schreibung.
* **Einblend- und Schwebeeffekte** → keine; die einzige Bewegung ist der atmende Punkt (bei „weniger Bewegung“ aus).

## 5 Grenzen und Verträge

* **Keine neue Bibliothek, keine Webschrift, keine Bilddatei außer dem Favicon** (`style-src 'self'`, `img-src 'self'`, kein Inline-Stil: Zustände sind Klassen).
* **Zugänglichkeit bleibt Vertrag:** sichtbarer Fokus, Tastaturbedienung (die senkrechte Leiste nimmt Auf/Ab und weiterhin Links/Rechts), Zustand nie nur als Farbe, `prefers-color-scheme`, `prefers-reduced-motion`.
* **Seitengröße:** alle Dateien zusammen unter 120 KB (Test `TestThePagesStaySmall`).
* **Tests:** Die `id`s und die Rollen bleiben; die Browser-Tests in Chromium, Firefox und WebKit und die axe-Prüfung (hell und dunkel, Englisch und Deutsch) laufen unverändert gegen die neue Gestalt. Neu: Bildschirmfotos der fünf Seiten als Prüfung von Hand (nicht als Test).

## 6 Schritte

1. **Rahmen:** linke Leiste, Kopfzeile, neue Farben und Schrift (`style.css`, `index.html`). *(begonnen)*
2. **Übersicht:** Rack-Zeilen mit Streifen, Lampe und Wort; Knöpfe nach Gewicht (`app.js`: Klassen je Zustand).
3. **Läufe:** Liste und Terminal nebeneinander, Rückfrage als Zeile des Terminals, Zähler/Punkt für laufende Läufe.
4. **NATS, Einstellungen, Protokolle:** Formulare und Tabellen in der neuen Gestalt; Ablegefläche für Dateien.
5. **Prüfen:** axe hell und dunkel, Englisch und Deutsch; Tastatur; schmale Breite; Bildschirmfotos ansehen und eine Sache streichen, die nicht dient.
6. **Handbuch:** Beschreibung der Seiten anpassen, wo sich Namen oder Orte ändern (die Leiste liegt links).
