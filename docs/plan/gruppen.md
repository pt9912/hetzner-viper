# Plan: Gruppen-Dateien (`groups/`) – mehrere Rechner gemeinsam starten und stoppen

Stand: 09.11.2026 · Status: Entwurf, nichts umgesetzt

## 1 Ziel

Heute startet und beendet man jeden Rechner einzeln (`viper up <name>`, `viper down <name>`). Eine NATS-Gruppe besteht nur aus Labels und dem privaten Netzwerk beim Anbieter. Neu: Neben `machines/` liegt ein Ordner `groups/`. Eine Datei dort beschreibt eine Gruppe und nennt ihre Rechner. Damit lässt sich die ganze Gruppe mit einem Befehl starten, prüfen und beenden.

## 2 Aufbau

```
.env.local                 gilt für alle
groups/entwicklung.env     die Gruppe: Rechner und gemeinsame Einstellungen
machines/alpha.env.local   ein Rechner (wie bisher)
machines/beta.env.local
```

`groups/entwicklung.env` (Vorschlag):

```
# Reihenfolge ist egal; der erste Rechner einer neuen Gruppe wird der NATS-Server
GROUP_MACHINES=alpha,beta
# Einstellungen, die für alle Rechner der Gruppe gelten (wie in machines/*.env.local)
NATS_ENABLED=true
NATS_MCP=true
```

* Der Dateiname ist der Gruppenname und wird zu `NATS_GROUP`. Die Regeln für Namen sind dieselben wie für `NATS_GROUP` (1 bis 31 Kleinbuchstaben, Ziffern, Bindestriche).
* Rangfolge der Einstellungen: `.env.local` → `groups/<g>.env` → `machines/<m>.env.local` (der Rechner gewinnt).
* Ein Rechner steht in höchstens einer Gruppendatei. Steht er in zwei, bricht `viper` ab.
* Eine `machines/<m>.env.local` darf `NATS_GROUP` nicht abweichend setzen, sonst bricht `viper` ab.
* Ein Rechner ohne Eintrag in einer Gruppendatei verhält sich wie bisher; `viper up alpha` funktioniert weiter einzeln, auch für einen Rechner einer Gruppe.
* Keine Geheimnisse in `groups/*.env`, wie bei `machines/`: Rechte 600 werden geprüft und gewarnt.

## 3 Befehle

| Befehl | Wirkung |
|---|---|
| `viper up --group g` | startet alle Rechner der Gruppe |
| `viper down --group g` | beendet alle Rechner der Gruppe |
| `viper check --group g` | prüft alle Rechner und die Gruppe |
| `viper ssh --group g --cmd …` | führt einen Befehl auf allen Rechnern aus (nur mit `--cmd`) |

`--group` und ein Rechnername zusammen sind ein Fehler. `--repo`, `--no-code` und `--force` gelten für alle Rechner der Gruppe.

### Reihenfolge

* **Start:** Besteht die Gruppe noch nicht, startet zuerst ein Rechner allein: der erste in `GROUP_MACHINES`. Er wird der NATS-Server (das Netzwerk entsteht, die Erststart-Sperre bleibt unverändert). Danach starten die übrigen. Besteht die Gruppe schon, starten alle fehlenden Rechner.
* **Stopp:** Zuerst die Clients, zuletzt der Server. Das Netzwerk räumt `viper down` auf, sobald die Gruppe leer ist (vorhandene Logik).
* **Parallelität:** Zunächst nacheinander (einfacher, lesbare Ausgabe, kein neues Fehlerbild). Parallel erst, wenn die Ausgabe je Rechner getrennt ist (Folgeschritt).

### Fehler

* Scheitert ein Rechner, macht der Befehl mit den übrigen weiter und meldet am Ende eine Tabelle (Rechner, Ergebnis); der Exit-Status ist ungleich 0, wenn einer scheiterte. Scheitert der Server beim Start, startet `viper` die Clients nicht (sie würden warten und aufgeben).
* Die Sicherheitsabfragen bleiben je Rechner (`DISCARD` bei ungesicherten Dateien, `CLEANUP`). Ein Gruppenbefehl umgeht sie nicht. Bei `down --group` fragt `viper` je Rechner; `--force` gilt nur dort, wo es für einen einzelnen Rechner gilt.
* Vor dem Start läuft `preflight` für alle Rechner (wie heute je Rechner), bevor etwas angelegt wird. So scheitert eine falsche Einstellung des dritten Rechners nicht erst nach dem Anlegen von zwei Servern.

## 4 Umsetzung

* `internal/adapter/driven/settingsfile`: Laden von `groups/<g>.env`, Rangfolge, Prüfungen (doppelte Zugehörigkeit, abweichendes `NATS_GROUP`, unbekannter Rechner ohne Datei).
* `internal/port/driving` und `internal/adapter/driving/cli`: Option `--group`, Hilfetext.
* Neuer Use Case `internal/usecase/groupops` (hexagonal: ruft `start`, `stop`, `check` je Rechner und kennt nur die Reihenfolge und die Sammlung der Ergebnisse).
* `viper setup` legt `groups/` an; `viper find`/`preflight` bleiben unverändert.
* Dokumentation: Handbuch (neues Kapitel, Einstellungen, Änderungsliste), `architektur.md`, `.env.local.example`, `groups/example.env.example`.

## 5 Meilensteine

| Nr. | Ergebnis |
|---|---|
| 1 | Datei laden, Rangfolge und Prüfungen, mit Tests (noch kein neuer Befehl) |
| 2 | `check --group` und `ssh --group` (lesend, ohne Risiko) |
| 3 | `up --group` (Server zuerst, dann Clients, Ergebnistabelle); echter Lauf mit drei Rechnern |
| 4 | `down --group` (Clients zuerst, Server zuletzt); echter Lauf mit Aufräumen |
| 5 | Handbuch, Abnahme; danach optional Parallelität |

## 6 Offene Fragen

* Soll `GROUP_MACHINES` auch Rechner nennen dürfen, die es noch nicht als Datei in `machines/` gibt (Standardwerte aus `.env.local` reichen oft)? Vorschlag: ja, ein Rechner ohne Datei nimmt die Werte aus `.env.local` und der Gruppe.
* Brauchen Gruppen außerhalb von NATS einen Sinn (zum Beispiel nur gemeinsam starten, ohne `NATS_ENABLED`)? Vorschlag: ja, `NATS_ENABLED` bleibt eine Einstellung der Gruppe und ist nicht Voraussetzung.
* Ein Rechner, der in der Datei fehlt, aber im Netzwerk der Gruppe ist (zum Beispiel von Hand gestartet): `down --group` beendet ihn nicht, `check --group` meldet ihn.
