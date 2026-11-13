# Plan: Eine Compose-Datei je Gruppe (Docker Compose)

**Ziel:** Die Rechner einer Gruppe (`groups/<gruppe>.env`, `PROVIDER=compose`) liegen in **einem** Compose-Projekt mit **einer** `compose.yaml`, nicht je Rechner in einem eigenen Projekt. Starten, Stoppen und Löschen einzelner Rechner bleiben möglich: `docker compose up -d <dienst>`, `stop <dienst>` und `rm -sf <dienst>` wirken auf einen Dienst und lassen die anderen unberührt.

**Stand:** Entwurf (13.11.2026), noch nichts gebaut. Ausgangspunkt ist [`compose.md`](compose.md) (§3: „eine Maschine = ein Projekt“, D2). Dieser Plan ersetzt D2 für Rechner in einer Gruppe.

## 1 Warum

* Ein Blick in `~/.local/state/viper/compose/` zeigt heute ein Verzeichnis je Rechner mit je einer Datei. Eine Gruppe von drei Rechnern ist drei Projekte.
* Eine Datei je Gruppe lässt sich von Hand mit `docker compose -p viper-g-<gruppe> ps` und `logs` ansehen.
* Das Netz der NATS-Gruppe, die Rechner und der Docker-Zwilling (`<rechner>-docker`) stehen an einer Stelle.

## 2 Was sich nicht ändert

* Der Container und das Volume eines Rechners heißen weiter `<servername>`, `<servername>-home`, `<servername>-brew`, `<servername>-docker`. Docker-Befehle von `viper` (`exec`, `inspect`, `volume rm`) und der `docker-proxy` bleiben gleich.
* Rechner **ohne Gruppe** behalten ihr eigenes Projekt `viper-<servername>` und ihre eigene Datei.
* Der Dienstname in der Datei der Gruppe ist der Servername (`<präfix>-<rechner>`), nicht mehr `machine`.
* Die Schlüsselableitung (Host-Schlüssel, erlaubte Schlüssel) bleibt: Dateien, die per Compose-`secrets` in den Container kommen. Sie liegen nun je Rechner unter `compose/<projekt>/secret/<servername>/`.

## 3 Abbildung

| Heute | Neu (Rechner in einer Gruppe) |
|---|---|
| Projekt `viper-<servername>` | Projekt `viper-g-<gruppe>` |
| Verzeichnis `compose/<servername>/` | `compose/g-<gruppe>/` mit `compose.yaml` |
| Schlüssel `compose/<servername>/secret/` | `compose/g-<gruppe>/secret/<servername>/` |
| Sperre `compose/<servername>.lock` | `compose/g-<gruppe>.lock` (ein Rechner schreibt die gemeinsame Datei zur Zeit) |
| Marke `deleting` im Verzeichnis | `compose/g-<gruppe>/deleting-<servername>` |
| Dienst `machine` | Dienst `<servername>` (und `<servername>-docker`) |

Container und Volumes tragen zusätzlich das Label `viper-project=<projekt>` (`viper-g-<gruppe>` oder `viper-<servername>`). Aus dem Label findet `viper` zu einem Namen das Projekt, auch wenn die Datei fehlt.

## 4 Ablauf

* **`CreateServer`:** Sperre der Gruppe nehmen, die Datei der Gruppe lesen (oder neu beginnen), den Dienst des Rechners samt Docker-Zwilling, Volumes und Schlüsseln **ersetzen oder hinzufügen**, die Datei schreiben (atomar), dann `docker compose -p <projekt> up -d --no-recreate --wait <dienst> [<dienst>-docker]`. Die anderen Dienste bleiben unberührt.
* **`EnsureRunning`:** wie heute, aber `up -d --no-recreate --wait <dienst>`.
* **`DeleteServer`:** Marke `deleting-<servername>`, dann `docker compose -p <projekt> rm -sfv <dienst> <dienst>-docker` (nicht `down`: das hieße die ganze Gruppe), danach die Volumes des Rechners löschen, den Dienst aus der Datei nehmen und die Schlüssel des Rechners löschen. **Mit dem letzten Dienst** gehen Datei, Verzeichnis und, wenn `viper` es angelegt hat, die Marke weg. Das Netz der NATS-Gruppe räumt weiter `viper down` der Gruppe auf.
* **`Leftovers`/`DeleteLeftovers`:** wie heute je Rechner; ein Dienst, dessen Container fehlt, aber dessen Schlüssel und Volumes da sind, ist ein Rest.
* **Gruppenname:** `ServerSpec.Group` (neu, aus `Machine.Group`) trägt die Gruppe zum Anbieter. Rechner ohne Gruppe: leer, eigenes Projekt.

## 5 Vorhandene Rechner und Wechsel der Gruppe

* **Vorhandene Rechner** (Projekt `viper-<servername>`, Dienst `machine`) laufen weiter, `viper` erkennt sie am Label (ohne `viper-project`: altes Layout) und behandelt sie mit dem alten Code, bis sie mit `viper down` gelöscht und neu gestartet werden. Es gibt **keinen** automatischen Umzug: Er würde den Container neu anlegen.
* **Ein Rechner wechselt die Gruppe:** nicht möglich, solange er läuft (`viper` lehnt ab, wie bei [`gruppen.md`](gruppen.md): erst `viper down`, dann in der anderen Gruppe starten).

## 6 Meilensteine

1. **Gerüst:** `ServerSpec.Group`, das Label `viper-project`, der Projektschlüssel (`projectKey(name, group)`) und die Pfadfunktionen dahinter. Verhalten für Rechner ohne Gruppe bleibt Byte für Byte gleich, die vorhandenen Tests bleiben grün.
2. **Gemeinsame Datei:** Einlesen und Zusammenführen der Datei der Gruppe (`composeFile` aus YAML lesen), Schlüssel je Rechner, `CreateServer` und `EnsureRunning` für Gruppen.
3. **Löschen:** `DeleteServer` mit `rm -sfv`, Datei bereinigen, letzter Rechner räumt das Verzeichnis. `Leftovers` und `DeleteLeftovers`.
4. **NATS-Gruppe, Docker-Zwilling, `--parallel`:** mehrere Rechner starten gleichzeitig (Sperre), Server und Clients einer NATS-Gruppe in einer Datei.
5. **Handbuch und Probelauf:** Handbuch (Abschnitt Docker Compose), `developer.md`, Lauf mit drei Rechnern gegen das lokale Docker (`viper up --group`, `down` einzeln und als Gruppe, `viper check`).

## 7 Offene Fragen

* Soll der Projektname `viper-g-<gruppe>` heißen oder `viper-<gruppe>`? Der Vorschlag mit `g-` vermeidet eine Verwechslung mit dem Projekt eines Rechners, der so heißt wie eine Gruppe.
* Bei einer Gruppe mit Rechnern **verschiedener Präfixe** (Rechnerdatei überschreibt `SERVER_PREFIX`) bleiben die Servernamen verschieden; das Projekt ist trotzdem das der Gruppe.
* Gruppen, die den Docker-Kontext verschieden setzen (`SERVER_LOCATION` je Rechner), sind ein Fehler: Ein Projekt gehört zu **einem** Docker-Host.
